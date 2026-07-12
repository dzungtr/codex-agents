package codexlaunch

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/notifyhook"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

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
// and records the result in state.json. If the tmux start fails, no
// state.json entry is written.
func (l *Launcher) Launch(req LaunchRequest) (LaunchResult, error) {
	// req.Profile passes through unchanged: an empty string is a
	// legitimate signal from the composer ("no profile files on disk,
	// launch with codex's own default") rather than a missing value to
	// be replaced with a hard-coded name. NewThreadArgs omits the -p
	// flag for an empty Profile, and ExistingNotifyCommand treats an
	// empty profile as "no forwarding target" — the no-profile launch
	// path is safe end-to-end.
	profile := req.Profile

	ws, err := ResolveWorkspace(l.Git, req.StartDir, req.Task)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: resolve workspace: %w", err)
	}

	threadID := l.newThreadID()
	session := tmuxstatus.SessionName(threadID)
	notifyArgs := l.notifyArgsFor(threadID, profile)
	codexArgs := NewThreadArgs(NewThreadSpec{Profile: profile, Model: req.Model, Task: req.Task, Notify: notifyArgs})
	tmuxArgs := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.NewSessionArgs(session, ws.WorkDir, codexArgs))

	if err := l.Tmux.Run(tmuxArgs); err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: start tmux session: %w", err)
	}
	if err := l.verifyAlive(session); err != nil {
		_ = l.Tmux.Run(tmuxstatus.KillSessionArgs(session))
		return LaunchResult{}, err
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
		SessionName:  session,
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
	tmuxArgs := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.NewSessionArgs(session, cwd, ResumeArgs(threadID, resumeProfile)))
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
