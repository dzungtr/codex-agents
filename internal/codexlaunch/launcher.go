package codexlaunch

import (
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/notifyhook"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
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
	profile := req.Profile
	if profile == "" {
		profile = DefaultProfile
	}

	ws, err := ResolveWorkspace(l.Git, req.StartDir, req.Task)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: resolve workspace: %w", err)
	}

	threadID := l.newThreadID()
	session := tmuxstatus.SessionName(threadID)
	notifyArgs := l.notifyArgsFor(threadID, profile)
	codexArgs := NewThreadArgs(NewThreadSpec{Profile: profile, Model: req.Model, Task: req.Task, Notify: notifyArgs})
	tmuxArgs := tmuxstatus.NewSessionArgs(session, ws.WorkDir, codexArgs)

	if err := l.Tmux.Run(tmuxArgs); err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: start tmux session: %w", err)
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

// Resume starts a managed tmux session running `codex resume <threadID>`
// for a thread that codex already knows about but has no live session
// (i.e. StatusClosed, including a thread whose history predates the
// cockpit entirely). cwd is the thread's own working directory, as
// recorded by codex (codexstate.Thread.CWD) — resuming must happen in the
// same directory the original conversation ran in.
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
func (l *Launcher) Resume(threadID, cwd string) (LaunchResult, error) {
	session := tmuxstatus.SessionName(threadID)
	tmuxArgs := tmuxstatus.NewSessionArgs(session, cwd, ResumeArgs(threadID))
	if err := l.Tmux.Run(tmuxArgs); err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: start resume tmux session: %w", err)
	}

	st, err := agentstate.Load(l.StatePath)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("codexlaunch: load state: %w", err)
	}
	entry := st.Threads[threadID]
	entry.TmuxSession = session
	if entry.WorktreePath == "" {
		entry.WorktreePath = cwd
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
