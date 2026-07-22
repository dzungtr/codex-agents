package codexlaunch

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/notifyhook"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// DefaultPollInterval is the delay between registration polls when a
// Launcher PollInterval is zero. Mirrors internal/subthread
// DefaultPollInterval (ADR 0003 decision 4) so the interactive launch
// path and the headless spawn path share the same registration-poll
// cadence.
const DefaultPollInterval = 500 * time.Millisecond

// DefaultRegistrationWait bounds how long Launch blocks waiting for
// codex to register the freshly started thread (i.e. write its row to
// codex own sqlite). Mirrors internal/subthread DefaultRegistrationWait.
// On timeout Launch kills the tmux session and returns a clear error
// - no optimistic row is emitted, so no duplicate to clean up (PRD #48).
const DefaultRegistrationWait = 30 * time.Second

// ErrRegistrationTimeout is returned by Launch when the freshly started
// thread did not appear in codex state within the Launcher
// RegistrationWait. The tmux session is killed before returning.
var ErrRegistrationTimeout = fmt.Errorf("codexlaunch: thread did not register in codex state before timeout")

// Registrar discovers the thread id codex assigned to a freshly
// launched thread by matching the worktree cwd against codex own
// records. The production adapter is cwdRegistrar (over
// codexstate.ThreadsByCWD); tests inject a fake that scripts
// no-row-then-row responses to model codex startup-to-registration
// latency. This mirrors internal/subthread Registrar seam (ADR 0003
// decision 4) so the interactive launch path and the headless spawn
// path block on the same registration signal.
type Registrar interface {
	// KnownByCWD returns the set of codex thread ids already known
	// for cwd, in most-recent-first order. The launcher calls it
	// once before starting its tmux session to snapshot the
	// pre-launch set, then again during the registration poll to
	// pick a freshly-registered id that is not in that snapshot
	// (issue #67: a single-id resolver collapsed consecutive
	// in-place launches on the same cwd into the same id). An
	// empty slice with a nil error means "not registered yet, keep
	// polling".
	KnownByCWD(cwd string) ([]string, error)
}

// cwdRegistrar adapts codexstate.ThreadsByCWD to the Launcher.Registrar
// interface. It captures codexHome so each poll queries codex newest
// state_*.sqlite fresh - the id is discovered the moment codex writes
// the thread row, not from a cached read.
type cwdRegistrar struct {
	codexHome string
}

func (r cwdRegistrar) KnownByCWD(cwd string) ([]string, error) {
	return codexstate.ThreadsByCWD(r.codexHome, cwd)
}

// defaultLivenessAttempts/defaultLivenessInterval bound verifyAlive's
// total polling window (5 x 60ms = 300ms) added to every Launch/Resume: a
// missing binary or bad flag reliably kills the pane's command within a
// handful of milliseconds, well inside this window, without adding a
// noticeable delay to a healthy launch.
const (
	defaultLivenessAttempts = 5
	defaultLivenessInterval = 60 * time.Millisecond
)

// Launcher ties together workspace resolution (worktree-per-thread),
// codex CLI invocation, the detached tmux session, and this tool's own
// state.json bookkeeping. It is the package's single orchestration entry
// point: everything else in codexlaunch is a pure helper Launcher composes.
type Launcher struct {
	// Git runs git subcommands for worktree resolution. Required.
	Git GitRunner
	// Tmux starts the detached tmux session. Required.
	Tmux tmuxstatus.Runner
	// StatePath is the state.json path to read/write. Required.
	StatePath string
	// NewThreadID generates the cockpit-side thread ID used to name the
	// tmux session (cxa-<prefix>) and key state.json. Defaults to a
	// random UUID; tests override for determinism.
	NewThreadID func() string
	// CodexHome is $CODEX_HOME (or its ~/.codex default), used to look up
	// an existing notify command from the chosen profile's config.toml so
	// the notify wrapper can forward to it (PRD #1 / issue #4). Empty
	// skips the lookup — the wrapper then only records turn-ended events.
	CodexHome string
	// ExecutablePath resolves the codex-agents binary's own path, used to
	// configure it as codex's notify hook (re-invoked in hook mode; see
	// internal/notifyhook). Defaults to os.Executable; tests override for
	// determinism. If resolution fails, Launch degrades gracefully by
	// omitting the notify hook entirely rather than failing the launch.
	ExecutablePath func() (string, error)
	// InspectPane checks a freshly created session's pane liveness right
	// after Launch/Resume starts it (see verifyAlive) — catching a `tmux
	// new-session -d` that reported success only because tmux itself
	// accepted the command, while the pane's own command (codex) had
	// already died. Defaults to tmuxstatus.InspectPane; tests override to
	// avoid depending on a real tmux server and to exercise the dead-pane
	// path deterministically.
	InspectPane func(session string) (tmuxstatus.PaneState, error)
	// Sleep is the delay verifyAlive uses between poll attempts. Defaults
	// to time.Sleep; tests override to a no-op (or a recording stub) so
	// the suite doesn't actually wait out the polling window.
	Sleep func(time.Duration)
	// LivenessAttempts and LivenessInterval configure verifyAlive's poll
	// budget. Zero (the default) means "use defaultLivenessAttempts /
	// defaultLivenessInterval"; tests override for a tighter/deterministic
	// budget.
	LivenessAttempts int
	LivenessInterval time.Duration
	// Registrar discovers the thread id codex assigned to a freshly
	// launched thread by matching the worktree cwd (PRD #48: Launch
	// blocks until codex registers, then returns codex id as
	// LaunchResult.ThreadID). Defaults to a cwdRegistrar over
	// codexstate.ThreadsByCWD; tests inject a fake that scripts
	// no-row-then-row responses.
	Registrar Registrar
	// PollInterval is the delay between registration polls. Zero means
	// DefaultPollInterval; tests override for a tighter/deterministic
	// cadence.
	PollInterval time.Duration
	// RegistrationWait bounds how long Launch blocks waiting for codex
	// to register the freshly started thread. Zero means
	// DefaultRegistrationWait; tests override for a tighter budget.
	RegistrationWait time.Duration
	// RegSleep is the delay the registration poll loop uses between
	// attempts. Defaults to time.Sleep; tests override to a no-op so
	// the suite does not actually wait out the polling window.
	RegSleep func(time.Duration)
}

func (l *Launcher) newThreadID() string {
	if l.NewThreadID != nil {
		return l.NewThreadID()
	}
	return uuid.NewString()
}

func (l *Launcher) executablePath() (string, error) {
	if l.ExecutablePath != nil {
		return l.ExecutablePath()
	}
	return os.Executable()
}

func (l *Launcher) inspectPane() func(string) (tmuxstatus.PaneState, error) {
	if l.InspectPane != nil {
		return l.InspectPane
	}
	return tmuxstatus.InspectPane
}

func (l *Launcher) sleep() func(time.Duration) {
	if l.Sleep != nil {
		return l.Sleep
	}
	return time.Sleep
}

func (l *Launcher) livenessAttempts() int {
	if l.LivenessAttempts > 0 {
		return l.LivenessAttempts
	}
	return defaultLivenessAttempts
}

func (l *Launcher) livenessInterval() time.Duration {
	if l.LivenessInterval > 0 {
		return l.LivenessInterval
	}
	return defaultLivenessInterval
}

func (l *Launcher) registrar() Registrar {
	if l.Registrar != nil {
		return l.Registrar
	}
	return cwdRegistrar{codexHome: l.CodexHome}
}

func (l *Launcher) pollInterval() time.Duration {
	if l.PollInterval > 0 {
		return l.PollInterval
	}
	return DefaultPollInterval
}

func (l *Launcher) registrationWait() time.Duration {
	if l.RegistrationWait > 0 {
		return l.RegistrationWait
	}
	return DefaultRegistrationWait
}

func (l *Launcher) regSleep() func(time.Duration) {
	if l.RegSleep != nil {
		return l.RegSleep
	}
	return time.Sleep
}

// waitForRegistration polls the Registrar until codex has written the
// freshly launched thread row (discovered by matching the worktree
// cwd and not present in exclude), returning codex own thread id.
// Bounded by RegistrationWait; on timeout returns
// ErrRegistrationTimeout. The shape mirrors
// internal/subthread.Spawner.Spawn poll loop (check first, then sleep)
// so the common case - codex registered during Launch liveness poll -
// is one check, zero sleeps.
//
// exclude is the snapshot of pre-launch ids captured by Launch before
// starting the tmux session (issue #67). Without it, two consecutive
// in-place launches on the same cwd both latch onto the first
// launch's id because the registrar immediately returns any matching
// row. Filtering against the snapshot ensures Launch only returns an
// id that codex registered during the current launch.
func (l *Launcher) waitForRegistration(cwd string, exclude []string) (string, error) {
	deadline := time.Now().Add(l.registrationWait())
	sleep := l.regSleep()
	interval := l.pollInterval()
	reg := l.registrar()
	for {
		known, err := reg.KnownByCWD(cwd)
		if err != nil {
			return "", fmt.Errorf("codexlaunch: discover codex thread id for %s: %w", cwd, err)
		}
		if id, ok := pickNewID(known, exclude); ok {
			return id, nil
		}
		if !time.Now().Before(deadline) {
			return "", ErrRegistrationTimeout
		}
		sleep(interval)
	}
}

// pickNewID returns the first id in known that is not in exclude and
// whether one was found. Both slices are expected to be short (the
// pre-launch snapshot plus one or two freshly-registered ids), so a
// linear scan is fine and keeps the result stable for the common
// "one matching id" case.
func pickNewID(known, exclude []string) (string, bool) {
	excluded := make(map[string]struct{}, len(exclude))
	for _, id := range exclude {
		excluded[id] = struct{}{}
	}
	for _, id := range known {
		if _, skip := excluded[id]; skip {
			continue
		}
		return id, true
	}
	return "", false
}

// verifyAlive polls session's pane a few times right after new-session/
// resume returns, closing the race where `tmux new-session -d` reports
// success the instant tmux itself accepts the command line — before the
// pane's own process (codex) has had any chance to prove it didn't just
// immediately exit (missing binary, bad profile/model flags, etc). The
// session must already have remain-on-exit set (see
// tmuxstatus.RemainOnExitArgs) or a dead pane tears its session down
// before this can observe it.
//
// Returns an error carrying whatever diagnostic pane output is available
// the moment the pane is ever observed dead (or the moment InspectPane
// itself errors, e.g. because the session doesn't exist at all — also
// evidence of death). Returns nil once the poll budget is exhausted
// without ever seeing that, treating the pane as alive: a healthy launch
// keeps running well past this window, so its absence within the window
// isn't proof of anything by itself.
func (l *Launcher) verifyAlive(session string) error {
	inspect := l.inspectPane()
	sleep := l.sleep()
	attempts := l.livenessAttempts()
	interval := l.livenessInterval()

	for i := 0; i < attempts; i++ {
		if i > 0 {
			sleep(interval)
		}
		state, err := inspect(session)
		if err != nil {
			return fmt.Errorf("codexlaunch: launched session died immediately: %w", err)
		}
		if state.Dead {
			return fmt.Errorf("codexlaunch: launched command exited immediately (exit %d): %s", state.ExitCode, state.Output)
		}
	}
	return nil
}

// notifyArgsFor builds the `-c notify=[...]` argv for a freshly launched
// thread, or nil if the wrapper can't be configured (e.g. the cockpit's own
// executable path can't be resolved). This is the "hook unavailable ->
// degrade to open/closed" contract from the PRD: a launch never fails just
// because notify-hook setup couldn't be completed.
func (l *Launcher) notifyArgsFor(threadID, profile string) []string {
	exePath, err := l.executablePath()
	if err != nil {
		return nil
	}
	forward := ExistingNotifyCommand(l.CodexHome, profile)
	eventsPath := notifyhook.DefaultEventsPath(l.StatePath)
	return notifyhook.WrapperArgs(exePath, threadID, eventsPath, forward)
}

// LaunchRequest is a composer submission: a task description plus the
// chosen profile/model (both optional; see NewThreadSpec for defaulting).
type LaunchRequest struct {
	StartDir string // cockpit's own working directory; where a non-git launch runs in place
	Task     string
	Profile  string
	Model    string
	// WorkspaceMode selects where the launched thread runs (issue #30,
	// ADR 0003 decision 6). The zero value (WorkspaceWorktree) preserves
	// the pre-#30 behaviour exactly: a fresh worktree per thread.
	WorkspaceMode WorkspaceMode
}

// LaunchResult is what the caller (the composer's submit handler) needs to
// know about a freshly launched thread: enough to render a new list row
// and, later, to attach to it.
type LaunchResult struct {
	ThreadID     string
	SessionName  string
	WorktreePath string
	Branch       string
	InPlace      bool
	Profile      string
	Model        string
}

// Launch resolves a worktree (or runs in place for a non-git start dir),
// starts a detached tmux session running a brand-new codex thread in it,
// polls codex own state until the thread registers, and records the
// result in state.json keyed by codex thread id (PRD #48: codex id is
// the single source of truth for thread identity, not a cockpit-minted
// UUID). If the tmux start fails, no state.json entry is written.
func (l *Launcher) Launch(req LaunchRequest) (LaunchResult, error) {
	// req.Profile passes through unchanged: an empty string is a
	// legitimate signal from the composer ("no profile files on disk,
	// launch with codex's own default") rather than a missing value to
	// be replaced with a hard-coded name. NewThreadArgs omits the -p
	// flag for an empty Profile, and ExistingNotifyCommand treats an
	// empty profile as "no forwarding target" — the no-profile launch
	// path is safe end-to-end.
	profile := req.Profile

	ws, err := ResolveWorkspace(l.Git, req.StartDir, req.Task, req.WorkspaceMode)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: resolve workspace: %w", err)
	}

	// Snapshot the set of codex thread ids already known for the
	// resolved cwd *before* starting the tmux session. The
	// registration poll below filters against this snapshot so two
	// consecutive in-place launches on the same cwd wait for a
	// freshly-registered id rather than latching onto the prior
	// launch's id (issue #67). Taking the snapshot here — before
	// codex has had a chance to write the new thread row — also
	// means a stale thread freshly started by some other process
	// cannot poison the snapshot.
	known, err := l.registrar().KnownByCWD(ws.WorkDir)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: snapshot known codex ids for %s: %w", ws.WorkDir, err)
	}

	// The cockpit handle mints the tmux session name (cxa-<prefix>) and
	// serves as the notify-hook wrapper identity positional - a stable
	// handle from launch time. It is NOT thread identity: codex own
	// thread id (discovered below once codex registers) is.
	handle := l.newThreadID()
	session := tmuxstatus.SessionName(handle)
	notifyArgs := l.notifyArgsFor(session, profile)
	codexArgs := NewThreadArgs(NewThreadSpec{Profile: profile, Model: req.Model, Task: req.Task, Notify: notifyArgs})
	tmuxArgs := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.ModifierKeysArgs(), tmuxstatus.NewSessionArgs(session, ws.WorkDir, codexArgs))

	if err := l.Tmux.Run(tmuxArgs); err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: start tmux session: %w", err)
	}
	if err := l.verifyAlive(session); err != nil {
		_ = l.Tmux.Run(tmuxstatus.KillSessionArgs(session))
		return LaunchResult{}, err
	}

	// Block until codex has registered the thread (written its row to
	// codex own sqlite), discovering codex thread id by matching the
	// worktree cwd. This is the PRD #48 contract: LaunchResult.ThreadID
	// is codex id, so the optimistic row, agentstate, and the
	// notify-hook event feed all key by codex id and the
	// RowsRefreshedMsg merge collapses the duplicate by construction.
	threadID, err := l.waitForRegistration(ws.WorkDir, known)
	if err != nil {
		_ = l.Tmux.Run(tmuxstatus.KillSessionArgs(session))
		return LaunchResult{}, err
	}

	// Rename the tmux session from the cockpit-derived name to one
	// derived from codex id (cxa-<codexID>) so every downstream consumer
	// that derives the session name from the thread id via
	// tmuxstatus.SessionName (attach, liveness, interrupt, quick-reply,
	// archive) keeps resolving to the actual session. The cockpit handle
	// was needed only to name the session at creation time (codex id is
	// not known until codex registers, which is after the tmux session
	// starts). The notify-hook wrapper identity positional stays the
	// original session name (baked into the launch command); runNotifyHook
	// resolves it back to codex id via agentstate (the entry below stores
	// the original session name as TmuxSession for that resolution).
	renamed := tmuxstatus.SessionName(threadID)
	if renamed != session {
		if err := l.Tmux.Run(tmuxstatus.RenameSessionArgs(session, renamed)); err != nil {
			_ = l.Tmux.Run(tmuxstatus.KillSessionArgs(session))
			return LaunchResult{}, fmt.Errorf("codexlaunch: rename session to codex id: %w", err)
		}
	}

	entry := agentstate.Entry{
		TmuxSession:  session,
		Profile:      profile,
		WorktreePath: ws.WorkDir,
	}
	if err := agentstate.Upsert(l.StatePath, threadID, entry); err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: persist state: %w", err)
	}

	return LaunchResult{
		ThreadID:     threadID,
		SessionName:  renamed,
		WorktreePath: ws.WorkDir,
		Branch:       ws.Branch,
		InPlace:      ws.InPlace,
		Profile:      profile,
		Model:        req.Model,
	}, nil
}

// Resume starts a managed tmux session running `codex -p <profile> resume
// <threadID>` (or plain `codex resume <threadID>` if no profile is known)
// for a thread that codex already knows about but has no live session (i.e.
// StatusClosed, including a thread whose history predates the cockpit
// entirely). cwd is the thread's own working directory, as recorded by
// codex (codexstate.Thread.CWD) — resuming must happen in the same
// directory the original conversation ran in.
//
// profile should be the thread's own recorded profile (codexstate.Thread.
// Profile, sourced from the thread's rollout JSONL) so the resumed session
// picks up the same profile — and therefore the same model — the thread
// originally ran under, rather than silently falling back to the base
// config.toml's model. If profile is empty, Resume falls back to any
// profile already recorded in this cockpit's own state.json for threadID
// (e.g. a prior cockpit-managed launch of the same thread).
//
// If threadID already has a state.json entry (e.g. a prior cockpit-managed
// launch that has since closed), its profile is preserved; otherwise
// Profile is left empty ("unknown", consistent with codexstate's own
// best-effort semantics).
//
// Resume does not chain the notify-hook wrapper (issue #4's Status hook is
// scoped to "launched threads" per the PRD): a resumed thread's status
// derivation degrades to plain tmux-liveness (StatusWorking while alive)
// unless an earlier Launch of the same thread ID already left a recorded
// event behind.
func (l *Launcher) Resume(threadID, cwd, profile string) (LaunchResult, error) {
	st, err := agentstate.Load(l.StatePath)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: load state: %w", err)
	}
	entry := st.Threads[threadID]

	resumeProfile := profile
	if resumeProfile == "" {
		resumeProfile = entry.Profile
	}

	session := tmuxstatus.SessionName(threadID)
	tmuxArgs := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.ModifierKeysArgs(), tmuxstatus.NewSessionArgs(session, cwd, ResumeArgs(threadID, resumeProfile)))
	if err := l.Tmux.Run(tmuxArgs); err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: start resume tmux session: %w", err)
	}
	if err := l.verifyAlive(session); err != nil {
		_ = l.Tmux.Run(tmuxstatus.KillSessionArgs(session))
		return LaunchResult{}, err
	}

	entry.TmuxSession = session
	if entry.WorktreePath == "" {
		entry.WorktreePath = cwd
	}
	if entry.Profile == "" {
		entry.Profile = resumeProfile
	}
	if err := agentstate.Upsert(l.StatePath, threadID, entry); err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: persist state: %w", err)
	}

	return LaunchResult{
		ThreadID:     threadID,
		SessionName:  session,
		WorktreePath: entry.WorktreePath,
		Profile:      entry.Profile,
	}, nil
}
