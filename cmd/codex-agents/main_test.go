package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/codexlaunch"
	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
	"github.com/dzungtr/codex-agents/internal/ui"
)

func TestTurnEndedByThread_ReflectsAgentStateEntries(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := agentstate.Upsert(statePath, "waiting-thread", agentstate.Entry{
		TmuxSession:   "cxa-waiting-thread",
		LastTurnEvent: "turn-ended@2026-07-08T12:00:00Z",
	}); err != nil {
		t.Fatalf("seed waiting-thread: %v", err)
	}
	if err := agentstate.Upsert(statePath, "working-thread", agentstate.Entry{
		TmuxSession: "cxa-working-thread",
	}); err != nil {
		t.Fatalf("seed working-thread: %v", err)
	}

	got := turnEndedByThread(loadAgentState(statePath))
	if !got["waiting-thread"] {
		t.Errorf("expected waiting-thread to report turnEnded=true, got %v", got)
	}
	if got["working-thread"] {
		t.Errorf("expected working-thread (empty LastTurnEvent) to report turnEnded=false, got %v", got)
	}
}

// TestLoadTurnEndedByThread_MissingStateDegradesToEmpty exercises the PRD's
// "hook unavailable -> degrade to open/closed" contract at the read side:
// a state.json that doesn't exist yet (e.g. first run on a machine, or the
// notify hook has never fired) must not error the whole list — every
// thread simply reports turnEnded=false, matching plain tmux-liveness
// status derivation.
func TestTurnEndedByThread_MissingStateDegradesToEmpty(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "does-not-exist", "state.json")
	got := turnEndedByThread(loadAgentState(statePath))
	if len(got) != 0 {
		t.Errorf("expected an empty map for a missing state file, got %v", got)
	}
}

func TestHiddenByThread_ReflectsAgentStateEntries(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := agentstate.Upsert(statePath, "archived-thread", agentstate.Entry{
		TmuxSession: "cxa-archived-thread",
		Hidden:      true,
	}); err != nil {
		t.Fatalf("seed archived-thread: %v", err)
	}
	if err := agentstate.Upsert(statePath, "live-thread", agentstate.Entry{
		TmuxSession: "cxa-live-thread",
	}); err != nil {
		t.Fatalf("seed live-thread: %v", err)
	}

	got := hiddenByThread(loadAgentState(statePath))
	if !got["archived-thread"] {
		t.Errorf("expected archived-thread to report hidden=true, got %v", got)
	}
	if got["live-thread"] {
		t.Errorf("expected live-thread to report hidden=false, got %v", got)
	}
}

func TestHiddenByThread_MissingStateDegradesToEmpty(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "does-not-exist", "state.json")
	got := hiddenByThread(loadAgentState(statePath))
	if len(got) != 0 {
		t.Errorf("expected an empty map for a missing state file, got %v", got)
	}
}

func TestLoadRows_FiltersHiddenThreads(t *testing.T) {
	codexHome := t.TempDir()
	writeJSONLThread(t, codexHome, "hidden-1", "Archived thread")
	writeJSONLThread(t, codexHome, "visible-1", "Visible thread")

	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := agentstate.MarkHidden(statePath, "hidden-1"); err != nil {
		t.Fatalf("MarkHidden: %v", err)
	}

	rows, err := loadRows(codexHome, statePath)
	if err != nil {
		t.Fatalf("loadRows: %v", err)
	}
	for _, r := range rows {
		if r.Thread.ID == "hidden-1" {
			t.Fatalf("expected hidden-1 to be filtered out, got rows: %+v", rows)
		}
	}
	found := false
	for _, r := range rows {
		if r.Thread.ID == "visible-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected visible-1 to remain, got rows: %+v", rows)
	}
}

// writeJSONLThread seeds a minimal codexstate-recoverable session jsonl file
// (no sqlite database present under codexHome forces codexstate.LoadThreads
// to use its jsonl fallback), so loadRows has real thread records to filter
// hidden entries out of.
func writeJSONLThread(t *testing.T, codexHome, id, title string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	line := `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/tmp/` + id + `","title":"` + title + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
}

// fakeTmuxRunner records tmux argument lists instead of shelling out.
type fakeTmuxRunner struct {
	calls [][]string
	err   error
}

func (f *fakeTmuxRunner) Run(args []string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	return f.err
}

func TestNeedsResume_ClosedRow_AlwaysTrue(t *testing.T) {
	row := ui.Row{Thread: rowThread("t1", "x"), Status: tmuxstatus.StatusClosed}
	// Even if the live set somehow contains the session, a row the cockpit
	// already believes is closed always resumes rather than attaching
	// directly.
	live := tmuxstatus.NewLiveSet([]string{tmuxstatus.SessionName("t1")})
	if !needsResume(row, tmuxstatus.SessionName("t1"), live) {
		t.Errorf("expected needsResume=true for a StatusClosed row")
	}
}

func TestNeedsResume_AliveRowWithLiveSession_False(t *testing.T) {
	row := ui.Row{Thread: rowThread("t1", "x"), Status: tmuxstatus.StatusWorking}
	session := tmuxstatus.SessionName("t1")
	live := tmuxstatus.NewLiveSet([]string{session})
	if needsResume(row, session, live) {
		t.Errorf("expected needsResume=false when the row's session is actually alive")
	}
}

// TestNeedsResume_AliveRowButSessionMissing_True is the self-heal case
// this fix adds: a row cached as Working/Waiting whose tmux session has
// actually already died (e.g. a race the launch-time and delayed
// liveness checks both missed) should route through Resume instead of
// attaching straight into a dead session.
func TestNeedsResume_AliveRowButSessionMissing_True(t *testing.T) {
	row := ui.Row{Thread: rowThread("t1", "x"), Status: tmuxstatus.StatusWaiting}
	session := tmuxstatus.SessionName("t1")
	live := tmuxstatus.NewLiveSet([]string{"cxa-someoneelse"})
	if !needsResume(row, session, live) {
		t.Errorf("expected needsResume=true when the row's status is stale and its session is actually gone")
	}
}

func TestCheckLivenessAction_RealTmux_ReportsClosedForANeverCreatedSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in this environment")
	}
	origDelay := livenessRecheckDelay
	livenessRecheckDelay = 10 * time.Millisecond
	t.Cleanup(func() { livenessRecheckDelay = origDelay })

	statePath := filepath.Join(t.TempDir(), "state.json")
	action := checkLivenessAction(statePath)

	msg := action("thread-that-never-launched")()
	liveness, ok := msg.(ui.ThreadLivenessMsg)
	if !ok {
		t.Fatalf("expected ui.ThreadLivenessMsg, got %#v", msg)
	}
	if liveness.ThreadID != "thread-that-never-launched" {
		t.Errorf("ThreadID = %q, want thread-that-never-launched", liveness.ThreadID)
	}
	if liveness.Status != tmuxstatus.StatusClosed {
		t.Errorf("Status = %v, want StatusClosed for a session that was never created", liveness.Status)
	}
}

func TestCheckLivenessAction_RealTmux_ReportsWorkingWhileSessionAlive(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in this environment")
	}
	origDelay := livenessRecheckDelay
	livenessRecheckDelay = 10 * time.Millisecond
	t.Cleanup(func() { livenessRecheckDelay = origDelay })

	const threadID = "fedcba9876543210"
	session := tmuxstatus.SessionName(threadID)
	runner := tmuxstatus.ExecRunner{}
	_ = runner.Run(tmuxstatus.KillSessionArgs(session))
	if err := runner.Run(tmuxstatus.NewSessionArgs(session, ".", []string{"sleep", "5"})); err != nil {
		t.Fatalf("start detached session: %v", err)
	}
	t.Cleanup(func() { runner.Run(tmuxstatus.KillSessionArgs(session)) })

	statePath := filepath.Join(t.TempDir(), "state.json")
	action := checkLivenessAction(statePath)

	msg := action(threadID)()
	liveness, ok := msg.(ui.ThreadLivenessMsg)
	if !ok {
		t.Fatalf("expected ui.ThreadLivenessMsg, got %#v", msg)
	}
	if liveness.Status != tmuxstatus.StatusWorking {
		t.Errorf("Status = %v, want StatusWorking for a still-alive session", liveness.Status)
	}
}

func TestInterruptAction_AliveThread_SendsCtrlCAndRecordsTurnEnded(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := agentstate.Upsert(statePath, "t1", agentstate.Entry{TmuxSession: "cxa-t1"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	tmux := &fakeTmuxRunner{}
	action := interruptAction(tmux, statePath)

	row := ui.Row{Thread: rowThread("t1", "Rabbit hole thread"), Status: tmuxstatus.StatusWorking}
	msg := action(row)()

	done, ok := msg.(ui.InterruptDoneMsg)
	if !ok {
		t.Fatalf("expected InterruptDoneMsg, got %#v", msg)
	}
	if done.ThreadID != "t1" {
		t.Errorf("ThreadID = %q, want t1", done.ThreadID)
	}

	wantSession := tmuxstatus.SessionName("t1")
	if len(tmux.calls) != 1 || strings.Join(tmux.calls[0], " ") != strings.Join(tmuxstatus.InterruptArgs(wantSession), " ") {
		t.Fatalf("tmux calls = %v, want a single InterruptArgs(%s) call", tmux.calls, wantSession)
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if !strings.HasPrefix(st.Threads["t1"].LastTurnEvent, "turn-ended@") {
		t.Errorf("expected LastTurnEvent to record a turn-ended event, got %q", st.Threads["t1"].LastTurnEvent)
	}
}

func TestInterruptAction_ClosedThread_ReturnsErrorWithoutCallingTmux(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	tmux := &fakeTmuxRunner{}
	action := interruptAction(tmux, statePath)

	row := ui.Row{Thread: rowThread("t1", "Closed thread"), Status: tmuxstatus.StatusClosed}
	msg := action(row)()

	if _, ok := msg.(ui.ThreadLaunchErrorMsg); !ok {
		t.Fatalf("expected ThreadLaunchErrorMsg for a closed thread, got %#v", msg)
	}
	if len(tmux.calls) != 0 {
		t.Fatalf("expected no tmux calls for a closed thread, got %v", tmux.calls)
	}
}

func TestInterruptAction_TmuxFailure_ReturnsErrorAndDoesNotUpdateState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	tmux := &fakeTmuxRunner{err: errors.New("tmux boom")}
	action := interruptAction(tmux, statePath)

	row := ui.Row{Thread: rowThread("t1", "Working thread"), Status: tmuxstatus.StatusWorking}
	msg := action(row)()

	if _, ok := msg.(ui.ThreadLaunchErrorMsg); !ok {
		t.Fatalf("expected ThreadLaunchErrorMsg on tmux failure, got %#v", msg)
	}
	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if st.Threads["t1"].LastTurnEvent != "" {
		t.Errorf("expected no LastTurnEvent recorded on tmux failure, got %q", st.Threads["t1"].LastTurnEvent)
	}
}

func TestArchiveAction_ClosedThreadWithNoWorktree_HidesWithoutKillingSession(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := agentstate.Upsert(statePath, "t1", agentstate.Entry{TmuxSession: "cxa-t1"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	tmux := &fakeTmuxRunner{}
	launcher := &codexlaunch.Launcher{Git: codexlaunch.ExecGitRunner{}, Tmux: tmux, StatePath: statePath}
	action := archiveAction(launcher, statePath)

	row := ui.Row{Thread: rowThread("t1", "Done thread"), Status: tmuxstatus.StatusClosed}
	msg := action(row)()

	done, ok := msg.(ui.ArchiveDoneMsg)
	if !ok {
		t.Fatalf("expected ArchiveDoneMsg, got %#v", msg)
	}
	if done.ThreadID != "t1" {
		t.Errorf("ThreadID = %q, want t1", done.ThreadID)
	}
	if done.Note != "archived Done thread" {
		t.Errorf("Note = %q, want %q (no worktree note)", done.Note, "archived Done thread")
	}
	if len(tmux.calls) != 0 {
		t.Fatalf("expected no kill-session call for an already-closed thread, got %v", tmux.calls)
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if !st.Threads["t1"].Hidden {
		t.Errorf("expected thread hidden after archive, got %+v", st.Threads["t1"])
	}
}

func TestArchiveAction_AliveThread_KillsSession(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := agentstate.Upsert(statePath, "t1", agentstate.Entry{TmuxSession: "cxa-t1"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	tmux := &fakeTmuxRunner{}
	launcher := &codexlaunch.Launcher{Git: codexlaunch.ExecGitRunner{}, Tmux: tmux, StatePath: statePath}
	action := archiveAction(launcher, statePath)

	row := ui.Row{Thread: rowThread("t1", "Alive thread"), Status: tmuxstatus.StatusWorking}
	if _, ok := action(row)().(ui.ArchiveDoneMsg); !ok {
		t.Fatalf("expected ArchiveDoneMsg")
	}

	wantSession := tmuxstatus.SessionName("t1")
	if len(tmux.calls) != 1 || strings.Join(tmux.calls[0], " ") != strings.Join(tmuxstatus.KillSessionArgs(wantSession), " ") {
		t.Fatalf("tmux calls = %v, want a single KillSessionArgs(%s) call", tmux.calls, wantSession)
	}
}

// setupArchiveRepo creates a real git repo with an initial commit on branch
// "main" and a worktree for "feature" branched off it, per the PRD's
// testing decisions ("worktree-safety checks... against real git repos in
// temp dirs"). Returns the worktree path.
func setupArchiveRepo(t *testing.T) (repoDir, worktreeDir string) {
	t.Helper()
	run := codexlaunch.ExecGitRunner{}
	g := func(dir string, args ...string) {
		t.Helper()
		if out, err := run.Run(dir, args...); err != nil {
			t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
		}
	}

	repoDir = t.TempDir()
	g(repoDir, "init", "-q", "-b", "main")
	g(repoDir, "config", "user.email", "test@example.com")
	g(repoDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	g(repoDir, "add", "README.md")
	g(repoDir, "commit", "-q", "-m", "init")

	worktreeDir = filepath.Join(t.TempDir(), "feature")
	g(repoDir, "worktree", "add", "-b", "feature", worktreeDir)
	return repoDir, worktreeDir
}

func TestArchiveAction_CleanWorktree_IsRemoved(t *testing.T) {
	_, worktreeDir := setupArchiveRepo(t)

	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := agentstate.Upsert(statePath, "t1", agentstate.Entry{TmuxSession: "cxa-t1", WorktreePath: worktreeDir}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	tmux := &fakeTmuxRunner{}
	launcher := &codexlaunch.Launcher{Git: codexlaunch.ExecGitRunner{}, Tmux: tmux, StatePath: statePath}
	action := archiveAction(launcher, statePath)

	row := ui.Row{Thread: rowThread("t1", "Merged thread"), Status: tmuxstatus.StatusClosed}
	msg := action(row)()

	done, ok := msg.(ui.ArchiveDoneMsg)
	if !ok {
		t.Fatalf("expected ArchiveDoneMsg, got %#v", msg)
	}
	if !strings.Contains(done.Note, "worktree removed") {
		t.Errorf("Note = %q, want it to mention worktree removed", done.Note)
	}
	if _, err := os.Stat(worktreeDir); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir removed, stat err = %v", err)
	}
}

func TestArchiveAction_DirtyWorktree_IsKeptWithReason(t *testing.T) {
	_, worktreeDir := setupArchiveRepo(t)
	if err := os.WriteFile(filepath.Join(worktreeDir, "scratch.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := agentstate.Upsert(statePath, "t1", agentstate.Entry{TmuxSession: "cxa-t1", WorktreePath: worktreeDir}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	tmux := &fakeTmuxRunner{}
	launcher := &codexlaunch.Launcher{Git: codexlaunch.ExecGitRunner{}, Tmux: tmux, StatePath: statePath}
	action := archiveAction(launcher, statePath)

	row := ui.Row{Thread: rowThread("t1", "Dirty thread"), Status: tmuxstatus.StatusClosed}
	msg := action(row)()

	done, ok := msg.(ui.ArchiveDoneMsg)
	if !ok {
		t.Fatalf("expected ArchiveDoneMsg, got %#v", msg)
	}
	if !strings.Contains(done.Note, "worktree kept") || !strings.Contains(done.Note, "uncommitted") {
		t.Errorf("Note = %q, want it to explain the worktree was kept due to uncommitted changes", done.Note)
	}
	if _, err := os.Stat(worktreeDir); err != nil {
		t.Fatalf("expected worktree dir to still exist, stat err = %v", err)
	}

	// Archiving (kill session + hide) still proceeds even when the
	// worktree is refused: only removal is gated by safety, not the rest
	// of the archive action.
	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if !st.Threads["t1"].Hidden {
		t.Errorf("expected thread hidden even though worktree removal was refused, got %+v", st.Threads["t1"])
	}
}

// rowThread builds a minimal codexstate.Thread for action tests that only
// need an ID and Title.
func rowThread(id, title string) codexstate.Thread {
	return codexstate.Thread{ID: id, Title: title}
}

// TestApplyProfileFallback_FillsEmptyProfileFromState is the Bug 2
// regression test (issue #25): a cockpit-launched thread whose rollout
// jsonl doesn't record a profile (or predates session_meta parsing) comes
// back from codexstate with Profile == "". attachAction passes
// row.Thread.Profile straight to Launcher.Resume, so an empty profile here
// means `codex resume <id>` without `-p` — the resumed session runs on the
// base config.toml model instead of the launch profile's. The fallback
// must fill it from the cockpit's own state.json entry.
func TestApplyProfileFallback_FillsEmptyProfileFromState(t *testing.T) {
	rows := []ui.Row{
		{Thread: codexstate.Thread{ID: "no-rollout-profile"}},
		{Thread: codexstate.Thread{ID: "rollout-profile", Profile: "from-rollout"}},
	}
	st := agentstate.State{Threads: map[string]agentstate.Entry{
		"no-rollout-profile": {Profile: "general-agentic"},
		"rollout-profile":    {Profile: "from-state"},
	}}

	applyProfileFallback(rows, st)

	if rows[0].Thread.Profile != "general-agentic" {
		t.Errorf("expected empty profile filled from state.json, got %q", rows[0].Thread.Profile)
	}
	if rows[1].Thread.Profile != "from-rollout" {
		t.Errorf("fallback must not clobber a non-empty codexstate profile, got %q", rows[1].Thread.Profile)
	}
}

// TestApplyProfileFallback_MissingEntryLeavesProfileEmpty pins the other
// side of the degrade posture: a thread with no state.json entry at all
// (not cockpit-launched) keeps its empty profile — Resume's own
// entry.Profile fallback then remains the only safety net, and a plain
// `codex resume <id>` is still the correct behavior for foreign threads.
func TestApplyProfileFallback_MissingEntryLeavesProfileEmpty(t *testing.T) {
	rows := []ui.Row{{Thread: codexstate.Thread{ID: "foreign-thread"}}}
	applyProfileFallback(rows, agentstate.State{Threads: map[string]agentstate.Entry{}})
	if rows[0].Thread.Profile != "" {
		t.Errorf("expected empty profile for thread with no state entry, got %q", rows[0].Thread.Profile)
	}
}
