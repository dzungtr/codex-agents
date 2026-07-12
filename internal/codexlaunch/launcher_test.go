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

func newTestLauncher(t *testing.T, git GitRunner, tmux tmuxstatus.Runner, ids []string) (*Launcher, string) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	i := 0
	next := func() string {
		if i >= len(ids) {
			t.Fatalf("ran out of scripted thread IDs")
		}
		id := ids[i]
		i++
		return id
	}
	return &Launcher{
		Git:         git,
		Tmux:        tmux,
		StatePath:   statePath,
		NewThreadID: next,
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
	}, statePath
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

	if res.ThreadID != "0123456789abcdef" {
		t.Errorf("ThreadID = %q, want scripted id", res.ThreadID)
	}
	wantSession := tmuxstatus.SessionName(res.ThreadID)
	if res.SessionName != wantSession {
		t.Errorf("SessionName = %q, want %q", res.SessionName, wantSession)
	}
	wantDir := filepath.Join("/repo", ".worktrees", "fix-auth-hook")
	if res.WorktreePath != wantDir {
		t.Errorf("WorktreePath = %q, want %q", res.WorktreePath, wantDir)
	}
	if res.Branch != "fix-auth-hook" {
		t.Errorf("Branch = %q, want fix-auth-hook", res.Branch)
	}

	if len(tmux.calls) != 1 {
		t.Fatalf("expected exactly one tmux call, got %v", tmux.calls)
	}
	got := tmux.calls[0]
	wantNotify := notifyhook.WrapperArgs("/opt/codex-agents/codex-agents", res.ThreadID, notifyhook.DefaultEventsPath(statePath), nil)
	wantCodexArgs := NewThreadArgs(NewThreadSpec{Profile: "general-agentic", Task: "Fix auth hook", Notify: wantNotify})
	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.NewSessionArgs(wantSession, wantDir, wantCodexArgs))
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("tmux call = %v, want %v", got, want)
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	entry, ok := st.Threads[res.ThreadID]
	if !ok {
		t.Fatalf("expected state entry for %s, got %v", res.ThreadID, st.Threads)
	}
	if entry.TmuxSession != wantSession || entry.Profile != "general-agentic" || entry.WorktreePath != wantDir {
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

	wantNotify := notifyhook.WrapperArgs("/opt/codex-agents/codex-agents", res.ThreadID, notifyhook.DefaultEventsPath(statePath), []string{"/usr/bin/terminal-notifier", "-title", "codex"})
	wantCodexArgs := NewThreadArgs(NewThreadSpec{Profile: "general-agentic", Task: "do a thing", Notify: wantNotify})
	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.NewSessionArgs(tmuxstatus.SessionName(res.ThreadID), "/plain", wantCodexArgs))
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
	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.NewSessionArgs(wantSession, "/repo/.worktrees/fix-auth-hook", ResumeArgs("existing-thread-id", "general-agentic")))
	if fmt.Sprint(tmux.calls[0]) != fmt.Sprint(want) {
		t.Errorf("tmux call = %v, want %v", tmux.calls[0], want)
	}

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
	wantSession := tmuxstatus.SessionName("threadid1")
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

	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.NewSessionArgs(tmuxstatus.SessionName("t1"), "/repo/.worktrees/t1", ResumeArgs("t1", "review")))
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

	want := tmuxstatus.ChainArgs(tmuxstatus.RemainOnExitArgs(), tmuxstatus.MouseOnArgs(), tmuxstatus.WheelUpArgs(), tmuxstatus.WheelDownArgs(), tmuxstatus.NewSessionArgs(tmuxstatus.SessionName("t1"), "/repo/.worktrees/t1", ResumeArgs("t1", "general-agentic")))
	if fmt.Sprint(tmux.calls[0]) != fmt.Sprint(want) {
		t.Errorf("tmux call = %v, want %v (caller-supplied profile used for -p)", tmux.calls[0], want)
	}
}
