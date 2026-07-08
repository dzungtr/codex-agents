package codexlaunch

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dzungtr/codex-agents/internal/agentstate"
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
	}, statePath
}

func TestLaunch_GitRepo_CreatesWorktreeAndTmuxSessionAndState(t *testing.T) {
	git := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]":                          {out: "/repo\n"},
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
	want := tmuxstatus.NewSessionArgs(wantSession, wantDir, []string{"codex", "-p", "general-agentic", "Fix auth hook"})
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
	if res.Profile != DefaultProfile {
		t.Errorf("Profile = %q, want default %q", res.Profile, DefaultProfile)
	}
	if !res.InPlace {
		t.Errorf("expected InPlace=true for a non-git start dir")
	}
	if res.WorktreePath != "/plain" {
		t.Errorf("WorktreePath = %q, want /plain (run in place)", res.WorktreePath)
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

	res, err := l.Resume("existing-thread-id", "/repo/.worktrees/fix-auth-hook")
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
	want := tmuxstatus.NewSessionArgs(wantSession, "/repo/.worktrees/fix-auth-hook", ResumeArgs("existing-thread-id"))
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

func TestResume_PreservesKnownProfileFromPriorState(t *testing.T) {
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, nil, tmux, nil)
	if err := agentstate.Upsert(statePath, "t1", agentstate.Entry{Profile: "review", WorktreePath: "/repo/.worktrees/t1"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	res, err := l.Resume("t1", "/repo/.worktrees/t1")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.Profile != "review" {
		t.Errorf("Profile = %q, want preserved 'review'", res.Profile)
	}
}
