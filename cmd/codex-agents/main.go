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
	"github.com/dzungtr/codex-agents/internal/codexserver"
	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/notifyhook"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
	"github.com/dzungtr/codex-agents/internal/ui"
	"github.com/dzungtr/codex-agents/internal/worktreesafety"
)

// defaultBaseBranch is the branch worktreesafety.Check compares an
// unpushed thread's worktree against when it has no upstream tracking
// branch configured (PRD #1's Archive row: "unpushed/unmerged commits
// relative to its upstream or main"). If a repo's default branch isn't
// actually named "main", Check's own missing-base-branch handling refuses
// removal with a clear reason rather than guessing wrong and deleting
// unmerged work.
const defaultBaseBranch = "main"

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

	profiles, err := codexlaunch.DiscoverProfiles(codexHome)
	if err != nil {
		// A profile-discovery failure must never abort startup: the
		// composer falls back to the "no profiles on disk" state and
		// launches with no -p flag, so a permissions error on
		// $CODEX_HOME can't take the whole cockpit down.
		fmt.Fprintf(os.Stderr, "codex-agents: discover profiles: %v\n", err)
		profiles = nil
	}

	rows, err := loadRows(codexHome, statePath)
	if err != nil {
		return err
	}

	// Wire the codex App Server manager (ADR 0002). A failed Start
	// is logged and the cockpit continues in degraded mode — every
	// Subscribe call is a no-op, every Events() read returns
	// nothing, and the UI behaves exactly like the pre-0002
	// cockpit (codexstate MessageCount/TokenCount on first load,
	// refreshed on user-driven Refresh). The same applies when
	// the codex binary is missing or the App Server refuses the
	// initialize handshake: the cockpit must always render its
	// row list, never crash on a peripheral.
	mgr := codexserver.NewManager(codexHome)
	if err := mgr.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "codex-agents: codex App Server unavailable (%v); live updates disabled\n", err)
	} else {
		// Subscribe to every alive thread on startup so the
		// existing rows light up immediately. We pass over
		// closed rows — ADR 0002 decision 4 says only alive
		// threads get subscriptions, and the App Server's
		// thread/resume would either fail or come back
		// empty for a dead session anyway.
		for _, r := range rows {
			if r.Status == tmuxstatus.StatusClosed {
				continue
			}
			_ = mgr.Subscribe(r.Thread.ID)
		}
	}

	// Action hooks for the ADR 0002 subscribe lifecycle. Each one
	// returns a tea.Cmd that runs in a goroutine — the actual
	// JSON-RPC round-trip is network latency and must never stall
	// bubbletea. The hooks ignore the manager's nil error from a
	// degraded Start (Subscribe/Unsubscribe are documented as
	// no-ops in that state) so the UI doesn't surface a fake
	// error message.
	silentLiveCmd := func(fn func(string) error) func(string) tea.Cmd {
		return func(threadID string) tea.Cmd {
			return func() tea.Msg {
				_ = fn(threadID)
				return nil
			}
		}
	}

	actions := ui.Actions{
		Launch:          launchAction(launcher, startDir),
		Attach:          attachAction(launcher),
		Refresh:         refreshAction(codexHome, statePath),
		QuickReply:      quickReplyAction(launcher),
		Interrupt:       interruptAction(launcher.Tmux, statePath),
		Archive:         archiveAction(launcher, statePath),
		CheckLiveness:   checkLivenessAction(statePath),
		LiveSubscribe:   silentLiveCmd(mgr.Subscribe),
		LiveUnsubscribe: silentLiveCmd(mgr.Unsubscribe),
	}

	program := tea.NewProgram(
		ui.New(rows).
			WithActions(actions).
			WithLaunchDir(startDir).
			WithProfiles(profiles),
		tea.WithAltScreen(),
	)

	// Forward codex App Server events into the bubbletea program
	// until it exits. The manager closes its Events() channel on
	// Stop, so this loop drains cleanly when mgr.Stop runs below
	// (after program.Run returns). We do NOT call mgr.Stop in a
	// defer because the program may have been torn down by
	// SIGINT already; the explicit teardown ordering here is the
	// "kill the producer, then drain the consumer" rule from
	// ADR 0002 decision 1.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range mgr.Events() {
			program.Send(ui.ThreadLiveUpdateMsg{
				ThreadID:     ev.ThreadID,
				MessageCount: ev.MessageCount,
				TokenCount:   ev.TokenCount,
			})
		}
	}()

	_, err = program.Run()
	mgr.Stop()
	<-done
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
	hidden := loadHiddenByThread(statePath)

	rows := make([]ui.Row, 0, len(result.Threads))
	for _, t := range result.Threads {
		// Archived (issue #5) threads are hidden in the cockpit's own
		// state rather than codex's (codexstate opens codex's sqlite
		// read-only and exposes no archive write path); filtering them
		// out here is what makes "archived rows disappear from the list"
		// hold after a refresh, not just immediately after ArchiveDoneMsg.
		if hidden[t.ID] {
			continue
		}
		rows = append(rows, ui.Row{
			Thread: t,
			Status: tmuxstatus.StatusFor(t.ID, liveSet, turnEnded[t.ID]),
		})
	}
	return rows, nil
}

// loadHiddenByThread reads agentstate's state.json and reports, per thread
// ID, whether it has been archived from the cockpit's own bookkeeping
// (agentstate.Entry.Hidden, set by the Archive (`a`) action — see
// archiveAction). A load failure degrades to "nothing hidden" rather than
// erroring the whole list, matching loadTurnEndedByThread's degrade
// posture.
func loadHiddenByThread(statePath string) map[string]bool {
	st, err := agentstate.Load(statePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents: load state (degrading to no hidden threads):", err)
		return map[string]bool{}
	}
	hidden := make(map[string]bool, len(st.Threads))
	for id, entry := range st.Threads {
		hidden[id] = entry.Hidden
	}
	return hidden
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

// needsResume reports whether attaching to row should first go through
// Launcher.Resume rather than attaching directly: either the row is
// already known to be closed, or — self-healing a stale row — its tmux
// session has actually died despite the row's cached Status saying
// otherwise (e.g. a race the launch-time liveness check and the delayed
// ui.Actions.CheckLiveness recheck both missed). Attaching straight to a
// dead session via tea.ExecProcess hands the terminal to tmux, which
// prints its own "can't find session" error directly to the inherited
// terminal and exits — bubbletea then redraws over it, so the error
// flashes and vanishes (the reported bug's "looks like nothing happened"
// symptom). Routing a stale row through Resume instead means the user
// gets a working session (or, if Resume itself fails, a real persisted
// error) rather than a flash.
func needsResume(row ui.Row, session string, live tmuxstatus.LiveSet) bool {
	if row.Status == tmuxstatus.StatusClosed {
		return true
	}
	_, alive := live[session]
	return !alive
}

// attachAction adapts tmux attach/resume into a ui.Actions.Attach hook.
// For a closed (or unexpectedly dead, see needsResume) thread it first
// spawns `codex resume <id>` into a managed tmux session (Launcher.Resume),
// then attaches to whichever session now exists — alive or freshly
// resumed — via tea.ExecProcess, which suspends the bubbletea program for
// the duration of the interactive tmux client.
func attachAction(launcher *codexlaunch.Launcher) func(row ui.Row) tea.Cmd {
	return func(row ui.Row) tea.Cmd {
		session := tmuxstatus.SessionName(row.Thread.ID)

		// Best-effort: a ListLiveSessions failure just skips the self-heal
		// check below (needsResume then falls back to row.Status alone,
		// same as before this existed).
		live, _ := tmuxstatus.ListLiveSessions()
		if needsResume(row, session, tmuxstatus.NewLiveSet(live)) {
			if _, err := launcher.Resume(row.Thread.ID, row.Thread.CWD, row.Thread.Profile); err != nil {
				return func() tea.Msg { return ui.ThreadLaunchErrorMsg{Err: err} }
			}
		}

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

// quickReplyAction adapts codexlaunch.Launcher.QuickReply into a
// ui.Actions.QuickReply hook (PRD #1's List behavior -> Quick reply row /
// issue #6): it runs synchronously inside the returned tea.Cmd, same
// pattern as launchAction, and turns the result into ui.QuickReplySentMsg
// or ui.ThreadLaunchErrorMsg.
func quickReplyAction(launcher *codexlaunch.Launcher) func(threadID, text string) tea.Cmd {
	return func(threadID, text string) tea.Cmd {
		return func() tea.Msg {
			if err := launcher.QuickReply(threadID, text); err != nil {
				return ui.ThreadLaunchErrorMsg{Err: err}
			}
			return ui.QuickReplySentMsg{ThreadID: threadID}
		}
	}
}

// livenessRecheckDelay is how long checkLivenessAction waits before
// re-deriving a just-launched thread's tmux-liveness status. Long enough
// to catch a codex process that starts fine but dies shortly after (e.g.
// an auth failure surfaced only once it reaches the model) — slower than
// the sub-second window codexlaunch.Launcher.verifyAlive already polls
// during Launch itself, which only catches near-instant deaths (missing
// binary, bad flags). Short enough that a genuinely dead thread doesn't
// sit showing "working" for long. A var (not a const) so tests can shrink
// it rather than actually waiting out the production delay.
var livenessRecheckDelay = 2 * time.Second

// checkLivenessAction adapts a delayed, single-thread tmux-liveness
// recheck into a ui.Actions.CheckLiveness hook (see that field's doc
// comment for why this is narrower than the Refresh action other
// post-action messages use: codex may not have written this thread's own
// record yet, so a full reload could drop the freshly-launched row
// outright instead of correcting its status).
func checkLivenessAction(statePath string) func(threadID string) tea.Cmd {
	return func(threadID string) tea.Cmd {
		return func() tea.Msg {
			time.Sleep(livenessRecheckDelay)
			live, err := tmuxstatus.ListLiveSessions()
			if err != nil {
				// Best-effort: a transient tmux query failure shouldn't
				// disrupt the freshly-launched row's optimistic status.
				return nil
			}
			turnEnded := loadTurnEndedByThread(statePath)
			status := tmuxstatus.StatusFor(threadID, tmuxstatus.NewLiveSet(live), turnEnded[threadID])
			return ui.ThreadLivenessMsg{ThreadID: threadID, Status: status}
		}
	}
}

// interruptAction adapts a tmux interrupt (send-keys C-c) into a
// ui.Actions.Interrupt hook (PRD #1's List behavior -> Interrupt row): it
// stops the selected thread's current turn without killing its session,
// then records the turn as ended in agentstate itself — using the exact
// same "<kind>@<RFC3339>" format issue #4's notify-hook wrapper writes
// (notifyhook.LastTurnEventValue) — so tmuxstatus.StatusFor reads the
// thread back as StatusWaiting immediately, rather than depending on
// whether an interrupted codex process reliably invokes its own notify
// hook (unverifiable in this sandbox, and not documented upstream).
func interruptAction(tmux tmuxstatus.Runner, statePath string) func(row ui.Row) tea.Cmd {
	return func(row ui.Row) tea.Cmd {
		return func() tea.Msg {
			if row.Status == tmuxstatus.StatusClosed {
				return ui.ThreadLaunchErrorMsg{Err: fmt.Errorf("cannot interrupt %q: no live session", row.Thread.Title)}
			}

			session := tmuxstatus.SessionName(row.Thread.ID)
			if err := tmux.Run(tmuxstatus.InterruptArgs(session)); err != nil {
				return ui.ThreadLaunchErrorMsg{Err: fmt.Errorf("interrupt %s: %w", row.Thread.Title, err)}
			}

			event := notifyhook.LastTurnEventValue(notifyhook.Event{Kind: notifyhook.KindTurnEnded, At: time.Now().UTC()})
			if err := agentstate.UpdateLastTurnEvent(statePath, row.Thread.ID, event); err != nil {
				return ui.ThreadLaunchErrorMsg{Err: fmt.Errorf("interrupt %s: stopped turn but failed to update state: %w", row.Thread.Title, err)}
			}
			return ui.InterruptDoneMsg{ThreadID: row.Thread.ID}
		}
	}
}

// archiveAction adapts kill-session + agentstate hiding + worktree cleanup
// into a ui.Actions.Archive hook (PRD #1's List behavior -> Archive row):
// kill the tmux session if alive, mark the thread hidden in the cockpit's
// own state (codexstate has no codex-sanctioned archive write path — see
// agentstate.Entry.Hidden), then offer worktree removal via
// archiveWorktree. Archiving never touches codex's own sqlite/jsonl
// records, preserving the read-only guarantee.
func archiveAction(launcher *codexlaunch.Launcher, statePath string) func(row ui.Row) tea.Cmd {
	return func(row ui.Row) tea.Cmd {
		return func() tea.Msg {
			if row.Status != tmuxstatus.StatusClosed {
				session := tmuxstatus.SessionName(row.Thread.ID)
				if err := launcher.Tmux.Run(tmuxstatus.KillSessionArgs(session)); err != nil {
					return ui.ThreadLaunchErrorMsg{Err: fmt.Errorf("archive %s: kill session: %w", row.Thread.Title, err)}
				}
			}

			if err := agentstate.MarkHidden(statePath, row.Thread.ID); err != nil {
				return ui.ThreadLaunchErrorMsg{Err: fmt.Errorf("archive %s: %w", row.Thread.Title, err)}
			}

			note := fmt.Sprintf("archived %s", row.Thread.Title)
			if wtNote := archiveWorktree(launcher, statePath, row.Thread.ID); wtNote != "" {
				note += "; " + wtNote
			}
			return ui.ArchiveDoneMsg{ThreadID: row.Thread.ID, Note: note}
		}
	}
}

// archiveWorktree looks up threadID's recorded worktree path (agentstate's
// bookkeeping from Launch, if any) and, when worktreesafety.Check reports
// it safe, removes it. It refuses — returning a human-readable reason
// instead — when there's uncommitted or unpushed/unmerged work, per PRD
// #1's Archive row ("refuses if uncommitted/unpushed work"). Returns "" for
// a thread with no recorded worktree (e.g. resumed from a plain-terminal
// session, or launched in-place in a non-git start dir), since there is
// nothing to offer removal of.
func archiveWorktree(launcher *codexlaunch.Launcher, statePath, threadID string) string {
	st, err := agentstate.Load(statePath)
	if err != nil {
		return fmt.Sprintf("worktree check skipped: %v", err)
	}
	entry, ok := st.Threads[threadID]
	if !ok || entry.WorktreePath == "" {
		return ""
	}

	result, err := worktreesafety.Check(launcher.Git, entry.WorktreePath, defaultBaseBranch)
	if err != nil {
		return fmt.Sprintf("worktree check failed: %v", err)
	}
	if !result.Safe {
		return fmt.Sprintf("worktree kept (%s)", result.Reason)
	}

	repoRoot, ok := codexlaunch.RepoRoot(launcher.Git, entry.WorktreePath)
	if !ok {
		return "worktree kept (could not resolve repo root)"
	}
	if err := worktreesafety.RemoveWorktree(launcher.Git, repoRoot, entry.WorktreePath); err != nil {
		return fmt.Sprintf("worktree removal failed: %v", err)
	}
	return "worktree removed"
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
