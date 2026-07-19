package codexlaunch

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/notifyhook"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// fakeTmuxRunner records tmux argument lists instead of shelling out.
type fakeTmuxRunner struct {
	calls [][]string
	err   error
}

func (f *fakeTmuxRunner) Run(args []string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	return f.err
}

// fakeRegistrar scripts the codex thread id a Launch discovers for a given
// worktree cwd. It returns the scripted ids in order, one per call, modeling
// the post-launch registration (PRD #48: Launch blocks until the registrar
// reports a registered id). A test that wants to exercise the
// not-yet-registered-then-registered transition can prepend a "" to ids.
type fakeRegistrar struct {
	ids []string
	i   int
	t   *testing.T
}

func (f *fakeRegistrar) ThreadIDByCWD(string) (string, bool, error) {
	if f.i >= len(f.ids) {
		f.t.Fatalf("ran out of scripted registrar ids")
	}
	id := f.ids[f.i]
	f.i++
	if id == "" {
		return "", false, nil
	}
	return id, true, nil
}

// alwaysNotRegistered is a Registrar stub that never reports a registered
// thread, used to exercise the Launch registration-timeout path.
type alwaysNotRegistered struct{}

func (alwaysNotRegistered) ThreadIDByCWD(string) (string, bool, error) {
	return "", false, nil
}

func newTestLauncher(t *testing.T, git GitRunner, tmux tmuxstatus.Runner, ids []string) (*Launcher, string) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	// ids scripts the codex thread ids the fake Registrar returns (one per
	// Launch). The cockpit handle (NewThreadID) is a separate, distinct
	// value: it mints the tmux session name (cxa-<prefix>) and the
	// notify-hook wrapper identity positional, but is NOT thread identity
	// (PRD #48). Keeping them distinct lets tests assert that LaunchResult.
	// ThreadID is codex id while the tmux session name still derives from
	// the cockpit handle.
	handleI := 0
	nextHandle := func() string {
		handleI++
		return fmt.Sprintf("cockpit-handle-%d", handleI)
	}
	reg := &fakeRegistrar{ids: ids, t: t}
	return &Launcher{
		Git:         git,
		Tmux:        tmux,
		StatePath:   statePath,
		NewThreadID: nextHandle,
		// A deterministic, no-lookup executable path keeps tmux call
		// assertions stable across machines/CI (os.Executable() would
		// otherwise vary); CodexHome is left empty, meaning
		// ExistingNotifyCommand always returns nil, so notify wrapper args
		// never include a forward command unless a test opts in.
		ExecutablePath: func() (string, error) { return "/opt/codex-agents/codex-agents", nil },
		// A fake InspectPane reporting "always alive" plus a no-op Sleep
		// decouples the rest of this suite from the post-launch liveness
		// poll (and from needing a real tmux server) unless a test opts
		// into exercising that behavior directly.
		InspectPane: func(string) (tmuxstatus.PaneState, error) { return tmuxstatus.PaneState{Dead: false}, nil },
		Sleep:       func(time.Duration) {},
		// The fake Registrar returns the scripted codex id immediately, so
		// Launch registration poll exits on the first check. RegSleep is a
		// no-op so a test that scripts a not-yet-registered transition does
		// not actually sleep.
		Registrar:    reg,
		RegSleep:     func(time.Duration) {},
		PollInterval: time.Millisecond,
	}, statePath
}

// assertModifierKeysChainedBeforeNewSession is the Bug-3 regression check:
// the modifier-key decode options (xterm-keys, and the version-guarded
// extended-keys) must appear in the session-creation invocation, before
// new-session — without them tmux drops Shift+Enter and other modified
// keys before they ever reach codex's pane.
func assertModifierKeysChainedBeforeNewSession(t *testing.T, got []string) {
	t.Helper()
	joined := " " + strings.Join(got, " ") + " "
	for _, opt := range []string{"xterm-keys on", "extended-keys on", "terminal-features"} {
		if !strings.Contains(joined, " "+opt) {
			t.Errorf("tmux call missing modifier-key setup %q, got %v", opt, got)
		}
	}
	xtermIdx := -1
	newSessionIdx := -1
	for i, a := range got {
		if a == "xterm-keys" && xtermIdx == -1 {
			xtermIdx = i
		}
		if a == "new-session" && newSessionIdx == -1 {
			newSessionIdx = i
		}
	}
	if xtermIdx == -1 || newSessionIdx == -1 {
		t.Fatalf("tmux call missing xterm-keys or new-session: %v", got)
	}
	if xtermIdx >= newSessionIdx {
		t.Errorf("xterm-keys (arg %d) must be chained before new-session (arg %d): %v", xtermIdx, newSessionIdx, got)
	}
}

func TestLaunch_GitRepo_CreatesWorktreeAndTmuxSessionAndState(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]":                           {out: "/repo\n"},
		"[rev-parse --verify --quiet refs/heads/fix-auth-hook]": {err: fmt.Errorf("exit 1")},
	}}
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, git, tmux, []string{"0123456789abcdef"})

	res, err := l.Launch(LaunchRequest{StartDir: "/repo/sub", Task: "Fix auth hook", Profile: "general-agentic"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	// PRD #48: LaunchResult.ThreadID is codex id (what the registrar
	// returned), NOT the cockpit handle.
	if res.ThreadID != "0123456789abcdef" {
		t.Errorf("ThreadID = %q, want codex id 0123456789abcdef", res.ThreadID)
	}
	// The tmux session name derives from the cockpit handle, not codex id.
	// The session is renamed to derive from codex id after registration,
	// so SessionName(codexID) resolves to the actual session everywhere.
	wantSession := tmuxstatus.SessionName(res.ThreadID)
	if res.SessionName != wantSession {
		t.Errorf("SessionName = %q, want %q (renamed to codex id)", res.SessionName, wantSession)
	}
	wantDir := filepath.Join("/repo", ".worktrees", "fix-auth-hook")
	if res.WorktreePath != wantDir {
		t.Errorf("WorktreePath = %q, want %q", res.WorktreePath, wantDir)
	}
	if res.Branch != "fix-auth-hook" {
		t.Errorf("Branch = %q, want fix-auth-hook", res.Branch)
	}

	if len(tmux.calls) != 2 {
		t.Fatalf("expected new-session + rename-session, got %v", tmux.calls)
	}
	got := tmux.calls[0]
	// The notify-hook wrapper identity positional is the original
	// (cockpit-handle-derived) session name baked into the launch command;
	// runNotifyHook resolves it back to codex id at hook-fire time.
	wantOriginalSession := tmuxstatus.SessionName("cockpit-handle-1")
	wantNotify := notifyhook.WrapperArgs("/opt/codex-agents/codex-agents", wantOriginalSession, notifyhook.DefaultEventsPath(statePath), nil)
	wantCodexArgs := NewThreadArgs(NewThreadSpec{Profile: "general-agentic", Task: "Fix auth hook", Notify: wantNotify})
	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.ModifierKeysArgs(), tmuxstatus.NewSessionArgs(wantOriginalSession, wantDir, wantCodexArgs))
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("tmux call = %v, want %v", got, want)
	}
	assertModifierKeysChainedBeforeNewSession(t, got)
	// calls[1] renames the session to the codex-id-derived name.
	wantRename := tmuxstatus.RenameSessionArgs(wantOriginalSession, wantSession)
	if fmt.Sprint(tmux.calls[1]) != fmt.Sprint(wantRename) {
		t.Errorf("rename call = %v, want %v", tmux.calls[1], wantRename)
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	// agentstate is keyed by codex id. TmuxSession stores the ORIGINAL
	// (pre-rename) session name: it is the notify-hook resolution handle
	// (runNotifyHook resolves it back to codex id), not the actual tmux
	// session name (which was renamed to SessionName(codexID)).
	entry, ok := st.Threads[res.ThreadID]
	if !ok {
		t.Fatalf("expected state entry keyed by codex id %s, got %v", res.ThreadID, st.Threads)
	}
	if entry.TmuxSession != wantOriginalSession || entry.Profile != "general-agentic" || entry.WorktreePath != wantDir {
		t.Errorf("state entry = %+v, unexpected", entry)
	}
}

func TestLaunch_DefaultsProfileWhenUnset(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{}
	l, _ := newTestLauncher(t, git, tmux, []string{"threadid1"})

	res, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// Empty Profile is now a legitimate signal: the launcher records
	// the empty string and NewThreadArgs omits the -p flag, so the
	// launch goes ahead with codex's own default profile.
	if res.Profile != "" {
		t.Errorf("Profile = %q, want %q (empty: no -p flag at launch time)", res.Profile, "")
	}
	if !res.InPlace {
		t.Errorf("expected InPlace=true for a non-git start dir")
	}
	if res.WorktreePath != "/plain" {
		t.Errorf("WorktreePath = %q, want /plain (run in place)", res.WorktreePath)
	}
}

func TestLaunch_ChainsExistingNotifyCommandFromProfileConfig(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, git, tmux, []string{"threadid1"})
	l.CodexHome = t.TempDir()
	writeConfig(t, l.CodexHome, "general-agentic", `notify = ["/usr/bin/terminal-notifier", "-title", "codex"]`+"\n")

	res, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing", Profile: "general-agentic"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// PRD #48: ThreadID is codex id, distinct from the cockpit handle.
	if res.ThreadID != "threadid1" {
		t.Errorf("ThreadID = %q, want codex id threadid1", res.ThreadID)
	}

	wantSession := tmuxstatus.SessionName("cockpit-handle-1")
	wantNotify := notifyhook.WrapperArgs("/opt/codex-agents/codex-agents", wantSession, notifyhook.DefaultEventsPath(statePath), []string{"/usr/bin/terminal-notifier", "-title", "codex"})
	wantCodexArgs := NewThreadArgs(NewThreadSpec{Profile: "general-agentic", Task: "do a thing", Notify: wantNotify})
	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.ModifierKeysArgs(), tmuxstatus.NewSessionArgs(wantSession, "/plain", wantCodexArgs))
	if fmt.Sprint(tmux.calls[0]) != fmt.Sprint(want) {
		t.Errorf("tmux call = %v, want %v (expected the profile's existing notify command chained in)", tmux.calls[0], want)
	}
}

func TestLaunch_ExecutablePathFailure_OmitsNotifyHookInsteadOfFailing(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{}
	l, _ := newTestLauncher(t, git, tmux, []string{"threadid1"})
	l.ExecutablePath = func() (string, error) { return "", fmt.Errorf("boom: can't resolve self") }

	if _, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing"}); err != nil {
		t.Fatalf("expected Launch to degrade gracefully rather than fail, got: %v", err)
	}

	got := tmux.calls[0]
	for i, a := range got {
		if a == "-c" && i+1 < len(got) && strings.HasPrefix(got[i+1], "notify=") {
			t.Fatalf("expected no notify flag when executable path resolution fails, got %v", got)
		}
	}
}

func TestLaunch_ModelLayersOnTopOfProfile(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{}
	l, _ := newTestLauncher(t, git, tmux, []string{"threadid1"})

	if _, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing", Profile: "review", Model: "o3"}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	got := tmux.calls[0]
	foundModel := false
	for i, a := range got {
		if a == "-c" && i+1 < len(got) && got[i+1] == "model=o3" {
			foundModel = true
		}
	}
	if !foundModel {
		t.Errorf("expected -c model=o3 in tmux call, got %v", got)
	}
}

func TestLaunch_TmuxFailurePropagatesAndDoesNotWriteState(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{err: fmt.Errorf("tmux boom")}
	l, statePath := newTestLauncher(t, git, tmux, []string{"threadid1"})

	if _, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing"}); err == nil {
		t.Fatalf("expected an error when tmux fails")
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if len(st.Threads) != 0 {
		t.Fatalf("expected no state entries after a failed launch, got %v", st.Threads)
	}
}

func TestResume_ReusesThreadIDAndUpdatesState(t *testing.T) {
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, nil, tmux, nil)

	res, err := l.Resume("existing-thread-id", "/repo/.worktrees/fix-auth-hook", "general-agentic")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.ThreadID != "existing-thread-id" {
		t.Errorf("ThreadID = %q, want existing-thread-id", res.ThreadID)
	}
	wantSession := tmuxstatus.SessionName("existing-thread-id")
	if res.SessionName != wantSession {
		t.Errorf("SessionName = %q, want %q", res.SessionName, wantSession)
	}

	if len(tmux.calls) != 1 {
		t.Fatalf("expected one tmux call, got %v", tmux.calls)
	}
	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.ModifierKeysArgs(), tmuxstatus.NewSessionArgs(wantSession, "/repo/.worktrees/fix-auth-hook", ResumeArgs("existing-thread-id", "general-agentic")))
	if fmt.Sprint(tmux.calls[0]) != fmt.Sprint(want) {
		t.Errorf("tmux call = %v, want %v", tmux.calls[0], want)
	}
	assertModifierKeysChainedBeforeNewSession(t, tmux.calls[0])

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if st.Threads["existing-thread-id"].TmuxSession != wantSession {
		t.Errorf("expected state updated with resumed session, got %+v", st.Threads["existing-thread-id"])
	}
}

func TestLaunch_ReturnsErrorAndDoesNotWriteState_WhenPaneDiesImmediately(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, git, tmux, []string{"threadid1"})
	l.InspectPane = func(session string) (tmuxstatus.PaneState, error) {
		return tmuxstatus.PaneState{Dead: true, ExitCode: 127, Output: "codex: command not found"}, nil
	}

	_, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing"})
	if err == nil {
		t.Fatalf("expected Launch to fail when the pane died immediately")
	}
	if !strings.Contains(err.Error(), "127") || !strings.Contains(err.Error(), "command not found") {
		t.Errorf("error = %q, want it to carry the exit code and captured output", err.Error())
	}

	st, loadErr := agentstate.Load(statePath)
	if loadErr != nil {
		t.Fatalf("Load state: %v", loadErr)
	}
	if len(st.Threads) != 0 {
		t.Fatalf("expected no state entries after a dead-pane launch, got %v", st.Threads)
	}

	if len(tmux.calls) != 2 {
		t.Fatalf("expected new-session + a cleanup kill-session call, got %v", tmux.calls)
	}
	// The session name derives from the cockpit handle, not codex id (the
	// pane dies before the registration poll discovers codex id).
	wantSession := tmuxstatus.SessionName("cockpit-handle-1")
	wantCleanup := tmuxstatus.KillSessionArgs(wantSession)
	if fmt.Sprint(tmux.calls[1]) != fmt.Sprint(wantCleanup) {
		t.Errorf("cleanup call = %v, want %v", tmux.calls[1], wantCleanup)
	}
}

func TestLaunch_TreatsInspectPaneErrorAsDeath(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{}
	l, _ := newTestLauncher(t, git, tmux, []string{"threadid1"})
	l.InspectPane = func(session string) (tmuxstatus.PaneState, error) {
		return tmuxstatus.PaneState{}, fmt.Errorf("no such session")
	}

	if _, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing"}); err == nil {
		t.Fatalf("expected Launch to fail when InspectPane itself errors")
	}
}

func TestLaunch_PollsInspectPaneUpToBudgetBeforeSucceeding(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{}
	l, _ := newTestLauncher(t, git, tmux, []string{"threadid1"})
	l.LivenessAttempts = 3
	l.LivenessInterval = time.Millisecond

	var inspectCalls, sleepCalls int
	l.InspectPane = func(string) (tmuxstatus.PaneState, error) {
		inspectCalls++
		return tmuxstatus.PaneState{Dead: false}, nil
	}
	l.Sleep = func(time.Duration) { sleepCalls++ }

	if _, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing"}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if inspectCalls != 3 {
		t.Errorf("InspectPane called %d times, want 3 (LivenessAttempts)", inspectCalls)
	}
	if sleepCalls != 2 {
		t.Errorf("Sleep called %d times, want 2 (attempts-1)", sleepCalls)
	}
}

func TestResume_ReturnsErrorAndDoesNotUpdateState_WhenPaneDiesImmediately(t *testing.T) {
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, nil, tmux, nil)
	if err := agentstate.Upsert(statePath, "t1", agentstate.Entry{Profile: "review", WorktreePath: "/repo/.worktrees/t1"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	l.InspectPane = func(string) (tmuxstatus.PaneState, error) {
		return tmuxstatus.PaneState{Dead: true, ExitCode: 1, Output: "codex: no such thread"}, nil
	}

	if _, err := l.Resume("t1", "/repo/.worktrees/t1", "review"); err == nil {
		t.Fatalf("expected Resume to fail when the pane died immediately")
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if st.Threads["t1"].TmuxSession != "" {
		t.Errorf("expected state left untouched (no TmuxSession recorded) after a dead-pane resume, got %+v", st.Threads["t1"])
	}
}

func TestResume_PreservesKnownProfileFromPriorState(t *testing.T) {
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, nil, tmux, nil)
	if err := agentstate.Upsert(statePath, "t1", agentstate.Entry{Profile: "review", WorktreePath: "/repo/.worktrees/t1"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	res, err := l.Resume("t1", "/repo/.worktrees/t1", "")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.Profile != "review" {
		t.Errorf("Profile = %q, want preserved 'review'", res.Profile)
	}

	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.ModifierKeysArgs(), tmuxstatus.NewSessionArgs(tmuxstatus.SessionName("t1"), "/repo/.worktrees/t1", ResumeArgs("t1", "review")))
	if fmt.Sprint(tmux.calls[0]) != fmt.Sprint(want) {
		t.Errorf("tmux call = %v, want %v (fell back to prior state's profile)", tmux.calls[0], want)
	}
}

func TestResume_CallerProfileOverridesPriorState(t *testing.T) {
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, nil, tmux, nil)
	if err := agentstate.Upsert(statePath, "t1", agentstate.Entry{Profile: "review", WorktreePath: "/repo/.worktrees/t1"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	res, err := l.Resume("t1", "/repo/.worktrees/t1", "general-agentic")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.Profile != "review" {
		t.Errorf("Profile = %q, want state's existing 'review' preserved", res.Profile)
	}

	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.ModifierKeysArgs(), tmuxstatus.NewSessionArgs(tmuxstatus.SessionName("t1"), "/repo/.worktrees/t1", ResumeArgs("t1", "general-agentic")))
	if fmt.Sprint(tmux.calls[0]) != fmt.Sprint(want) {
		t.Errorf("tmux call = %v, want %v (caller-supplied profile used for -p)", tmux.calls[0], want)
	}
}

func TestLaunch_InPlaceModeOnGitDirRunsInCallerCwd(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {out: "/repo\n"},
	}}
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, git, tmux, []string{"0123456789abcdef"})

	res, err := l.Launch(LaunchRequest{
		StartDir:      "/repo/sub",
		Task:          "explore the graph",
		Profile:       "general-agentic",
		WorkspaceMode: WorkspaceInPlace,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !res.InPlace {
		t.Errorf("expected InPlace=true for in-place mode")
	}
	if res.WorktreePath != "/repo/sub" {
		t.Errorf("WorktreePath = %q, want /repo/sub (caller's cwd)", res.WorktreePath)
	}
	if res.Branch != "" {
		t.Errorf("Branch = %q, want empty for in-place run", res.Branch)
	}
	if len(tmux.calls) != 2 {
		t.Fatalf("expected new-session + rename-session, got %v", tmux.calls)
	}
	// The new-session call uses the original (cockpit-handle-derived) name.
	wantSession := tmuxstatus.SessionName("cockpit-handle-1")
	wantNotify := notifyhook.WrapperArgs("/opt/codex-agents/codex-agents", wantSession, notifyhook.DefaultEventsPath(statePath), nil)
	wantCodexArgs := NewThreadArgs(NewThreadSpec{Profile: "general-agentic", Task: "explore the graph", Notify: wantNotify})
	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.ModifierKeysArgs(), tmuxstatus.NewSessionArgs(wantSession, "/repo/sub", wantCodexArgs))
	if fmt.Sprint(tmux.calls[0]) != fmt.Sprint(want) {
		t.Errorf("tmux call = %v, want %v", tmux.calls[0], want)
	}
	// The rename-session call moves it to the codex-id-derived name.
	wantRename := tmuxstatus.RenameSessionArgs(wantSession, tmuxstatus.SessionName(res.ThreadID))
	if fmt.Sprint(tmux.calls[1]) != fmt.Sprint(wantRename) {
		t.Errorf("rename call = %v, want %v", tmux.calls[1], wantRename)
	}
}

func TestLaunch_WorktreeModeOnGitDirCreatesWorktree(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]":                               {out: "/repo\n"},
		"[rev-parse --verify --quiet refs/heads/explore-the-graph]": {err: fmt.Errorf("exit 1")},
	}}
	tmux := &fakeTmuxRunner{}
	l, _ := newTestLauncher(t, git, tmux, []string{"0123456789abcdef"})

	res, err := l.Launch(LaunchRequest{
		StartDir:      "/repo/sub",
		Task:          "explore the graph",
		Profile:       "general-agentic",
		WorkspaceMode: WorkspaceWorktree,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.InPlace {
		t.Errorf("expected InPlace=false for worktree mode on a git dir")
	}
	if res.Branch != "explore-the-graph" {
		t.Errorf("Branch = %q, want explore-the-graph", res.Branch)
	}
}

// TestLaunch_RegistersAfterPoll_ReturnsCodexID exercises the PRD #48
// registration poll: the registrar reports not-registered on the first check
// (codex hasn't written its row yet) then registered on the second, and
// Launch returns codex id (not the cockpit handle). This pins the poll loop
// shape (check first, then sleep) and the id-source-of-truth contract.
func TestLaunch_RegistersAfterPoll_ReturnsCodexID(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, git, tmux, []string{"", "codex-id-xyz"})
	l.RegistrationWait = time.Second

	res, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.ThreadID != "codex-id-xyz" {
		t.Errorf("ThreadID = %q, want codex-id-xyz (discovered after poll)", res.ThreadID)
	}
	if res.SessionName != tmuxstatus.SessionName("codex-id-xyz") {
		t.Errorf("SessionName = %q, want codex-id-derived session (renamed after registration)", res.SessionName)
	}
	// agentstate is keyed by codex id, not the cockpit handle.
	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if _, ok := st.Threads["codex-id-xyz"]; !ok {
		t.Errorf("expected state entry keyed by codex id codex-id-xyz, got %v", st.Threads)
	}
	if _, ok := st.Threads["cockpit-handle-1"]; ok {
		t.Errorf("expected NO state entry keyed by the cockpit handle, got %v", st.Threads)
	}
}

// TestLaunch_RegistrationTimeout_KillsSessionAndReturnsError pins the PRD
// #48 timeout path: when codex never registers within RegistrationWait,
// Launch kills the tmux session and returns ErrRegistrationTimeout, writing
// no state.json entry (so no orphaned optimistic row).
func TestLaunch_RegistrationTimeout_KillsSessionAndReturnsError(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a repo")},
	}}
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, git, tmux, nil)
	// A registrar that always reports not-registered, so Launch exhausts
	// its RegistrationWait and times out.
	l.Registrar = alwaysNotRegistered{}
	l.RegistrationWait = 5 * time.Millisecond

	_, err := l.Launch(LaunchRequest{StartDir: "/plain", Task: "do a thing"})
	if err == nil || !strings.Contains(err.Error(), "register") {
		t.Fatalf("expected a registration timeout error, got %v", err)
	}

	// The tmux session is killed on timeout (new-session + kill-session).
	if len(tmux.calls) != 2 {
		t.Fatalf("expected new-session + a cleanup kill-session call, got %v", tmux.calls)
	}
	wantSession := tmuxstatus.SessionName("cockpit-handle-1")
	wantCleanup := tmuxstatus.KillSessionArgs(wantSession)
	if fmt.Sprint(tmux.calls[1]) != fmt.Sprint(wantCleanup) {
		t.Errorf("cleanup call = %v, want %v", tmux.calls[1], wantCleanup)
	}

	// No state.json entry is written on timeout.
	st, loadErr := agentstate.Load(statePath)
	if loadErr != nil {
		t.Fatalf("Load state: %v", loadErr)
	}
	if len(st.Threads) != 0 {
		t.Fatalf("expected no state entries after a registration timeout, got %v", st.Threads)
	}
}
