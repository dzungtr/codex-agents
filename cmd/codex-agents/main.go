// Command codex-agents is the cockpit's entry point: a terminal list of
// every codex thread, sourced from codexstate (thread data) and tmuxstatus
// (working/waiting/closed status), rendered by the ui package. This slice
// (#3) wires the composer (launch a new thread into a worktree + detached
// tmux session via internal/codexlaunch) and the Enter/Detach handoff
// (attach an alive thread, or `codex resume` a closed one). Slice #4 adds
// the notify-hook subcommand (see runNotifyHook) that launched threads
// invoke via `-c notify=[...]` to report turn-ended events, and status
// derivation now also consults those events (loadTurnEndedByThread) rather
// than tmux liveness alone.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/codexlaunch"
	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/notifyhook"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
	"github.com/dzungtr/codex-agents/internal/ui"
)

func main() {
	// Launched threads invoke this binary as their notify hook via `-c
	// notify=[...]` (internal/notifyhook.WrapperArgs); dispatch to that
	// mode before anything else tries to start the bubbletea program.
	if len(os.Args) > 1 && os.Args[1] == notifyhook.Subcommand {
		runNotifyHook(os.Args[2:])
		return
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents:", err)
		os.Exit(1)
	}
}

// runNotifyHook implements the hidden `codex-agents notify-hook ...`
// subcommand codex invokes when a launched thread's turn ends. Per the PRD
// #1 / issue #4 contract, this must never block or fail codex's own
// turn-completion flow: failures go to stderr and the process still exits
// 0, so a broken hook degrades the cockpit's status derivation (that
// thread simply reads as StatusWorking whenever it's alive) instead of
// disrupting the user's codex session.
func runNotifyHook(args []string) {
	threadID, eventsPath, forward, payload, err := notifyhook.ParseWrapperArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents notify-hook:", err)
		return
	}
	statePath, err := agentstate.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents notify-hook: resolve state path:", err)
		statePath = ""
	}
	notifyhook.Run(os.Stderr, notifyhook.ExecForwardRunner{}, statePath, threadID, eventsPath, forward, payload, time.Now().UTC())
}

func run() error {
	codexHome, err := resolveCodexHome()
	if err != nil {
		return err
	}

	statePath, err := agentstate.DefaultPath()
	if err != nil {
		return err
	}

	startDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("codex-agents: resolve working directory: %w", err)
	}

	launcher := &codexlaunch.Launcher{
		Git:       codexlaunch.ExecGitRunner{},
		Tmux:      tmuxstatus.ExecRunner{},
		StatePath: statePath,
		CodexHome: codexHome,
	}

	rows, err := loadRows(codexHome, statePath)
	if err != nil {
		return err
	}

	actions := ui.Actions{
		Launch:  launchAction(launcher, startDir),
		Attach:  attachAction(launcher),
		Refresh: refreshAction(codexHome, statePath),
	}

	_, err = tea.NewProgram(ui.New(rows).WithActions(actions), tea.WithAltScreen()).Run()
	return err
}

// loadRows loads codex's own thread records and merges in tmux-liveness
// plus turn-event status (tmuxstatus.StatusFor's working/waiting/closed
// derivation, PRD #1 / issue #4), in the shape the ui package renders. It's
// the single place both the initial load and the post-attach Refresh
// action go through, so the two can never drift out of sync.
func loadRows(codexHome, statePath string) ([]ui.Row, error) {
	result, err := codexstate.LoadThreads(codexHome)
	if err != nil {
		return nil, err
	}

	live, err := tmuxstatus.ListLiveSessions()
	if err != nil {
		return nil, err
	}
	liveSet := tmuxstatus.NewLiveSet(live)

	turnEnded := loadTurnEndedByThread(statePath)

	rows := make([]ui.Row, 0, len(result.Threads))
	for _, t := range result.Threads {
		rows = append(rows, ui.Row{
			Thread: t,
			Status: tmuxstatus.StatusFor(t.ID, liveSet, turnEnded[t.ID]),
		})
	}
	return rows, nil
}

// loadTurnEndedByThread reads agentstate's state.json and reports, per
// thread ID, whether its LastTurnEvent field is populated (i.e. the
// notify-hook wrapper has recorded at least one turn-ended event for it —
// internal/notifyhook is the producer). A load failure degrades to "no
// events known" (every entry false) rather than erroring the whole list:
// per the PRD's "hook unavailable -> degrade to open/closed" contract, a
// corrupt or unreadable state.json must not stop the cockpit from showing
// plain tmux-liveness status.
func loadTurnEndedByThread(statePath string) map[string]bool {
	st, err := agentstate.Load(statePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents: load state (degrading to tmux-liveness-only status):", err)
		return map[string]bool{}
	}
	turnEnded := make(map[string]bool, len(st.Threads))
	for id, entry := range st.Threads {
		turnEnded[id] = entry.LastTurnEvent != ""
	}
	return turnEnded
}

// launchAction adapts codexlaunch.Launcher.Launch into a ui.Actions.Launch
// hook: it runs synchronously inside the returned tea.Cmd (bubbletea's
// standard pattern for a blocking call that reports back via a Msg), and
// turns the result into ui.ThreadLaunchedMsg or ui.ThreadLaunchErrorMsg.
func launchAction(launcher *codexlaunch.Launcher, startDir string) func(task, profile string) tea.Cmd {
	return func(task, profile string) tea.Cmd {
		return func() tea.Msg {
			res, err := launcher.Launch(codexlaunch.LaunchRequest{StartDir: startDir, Task: task, Profile: profile})
			if err != nil {
				return ui.ThreadLaunchErrorMsg{Err: err}
			}
			return ui.ThreadLaunchedMsg{Row: ui.Row{
				Thread: codexstate.Thread{
					ID:        res.ThreadID,
					Title:     task,
					CWD:       res.WorktreePath,
					GitBranch: res.Branch,
					Profile:   res.Profile,
					Model:     res.Model,
					// TokenCount unknown until codex writes its own thread
					// record; -1 is codexstate's "unknown" sentinel.
					TokenCount: -1,
				},
				// A freshly launched thread's turn is by definition still
				// in progress: no turn-ended event exists for it yet.
				Status: tmuxstatus.StatusWorking,
			}}
		}
	}
}

// attachAction adapts tmux attach/resume into a ui.Actions.Attach hook.
// For a closed thread it first spawns `codex resume <id>` into a managed
// tmux session (Launcher.Resume), then attaches to whichever session now
// exists — alive or freshly resumed — via tea.ExecProcess, which suspends
// the bubbletea program for the duration of the interactive tmux client.
func attachAction(launcher *codexlaunch.Launcher) func(row ui.Row) tea.Cmd {
	return func(row ui.Row) tea.Cmd {
		if row.Status == tmuxstatus.StatusClosed {
			if _, err := launcher.Resume(row.Thread.ID, row.Thread.CWD); err != nil {
				return func() tea.Msg { return ui.ThreadLaunchErrorMsg{Err: err} }
			}
		}

		session := tmuxstatus.SessionName(row.Thread.ID)
		var args []string
		if tmuxstatus.InsideTmux() {
			args = tmuxstatus.SwitchClientArgs(session)
		} else {
			args = tmuxstatus.AttachArgs(session)
		}
		cmd := exec.Command("tmux", args...)
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				return ui.ThreadLaunchErrorMsg{Err: err}
			}
			return ui.AttachDoneMsg{}
		})
	}
}

// refreshAction reloads the thread list, used after tmux detach returns
// control to the cockpit ("a refreshed list", per PRD #1's List behavior
// table).
func refreshAction(codexHome, statePath string) func() tea.Cmd {
	return func() tea.Cmd {
		return func() tea.Msg {
			rows, err := loadRows(codexHome, statePath)
			if err != nil {
				return ui.ThreadLaunchErrorMsg{Err: err}
			}
			return ui.RowsRefreshedMsg{Rows: rows}
		}
	}
}

// resolveCodexHome honors $CODEX_HOME (as codex's own CLI does) before
// falling back to the default ~/.codex.
func resolveCodexHome() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home, nil
	}
	return codexstate.DefaultCodexHome()
}
