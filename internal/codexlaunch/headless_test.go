package codexlaunch

import (
	"testing"

	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// fakeGitForHeadless resolves every start dir as non-git (in-place launch),
// keeping the headless test free of filesystem/git dependencies — it only
// asserts that HeadlessLaunch delegates to Launch and returns the identity
// fields a headless caller (cdxa spawn) needs.
func fakeGitForHeadless() GitRunner {
	return GitRunnerFunc(func(dir string, args ...string) (string, error) {
		return "", errHeadlessFakeGitNotARepo
	})
}

var errHeadlessFakeGitNotARepo = strErr("not a git repo")

type strErr string

func (e strErr) Error() string { return string(e) }

func TestHeadlessLaunch_ReturnsThreadIdentity(t *testing.T) {
	tmux := &fakeTmuxRunner{}
	l, _ := newTestLauncher(t, fakeGitForHeadless(), tmux, []string{"abcd1234efgh5678"})

	res, err := l.HeadlessLaunch(LaunchRequest{
		StartDir: "/plain",
		Task:     "explore the module graph",
		Profile:  "general-agentic",
	})
	if err != nil {
		t.Fatalf("HeadlessLaunch: %v", err)
	}
	// PRD #48: ThreadID is codex id (from the registrar); the session is
	// renamed to derive from codex id after registration.
	if res.ThreadID != "abcd1234efgh5678" {
		t.Errorf("ThreadID = %q, want codex id abcd1234efgh5678", res.ThreadID)
	}
	if res.SessionName != tmuxstatus.SessionName("abcd1234efgh5678") {
		t.Errorf("SessionName = %q, want %q (renamed to codex id)", res.SessionName, tmuxstatus.SessionName("abcd1234efgh5678"))
	}
	if res.Profile != "general-agentic" {
		t.Errorf("Profile = %q, want general-agentic", res.Profile)
	}
	if res.InPlace != true {
		t.Errorf("InPlace = %v, want true (non-git start dir)", res.InPlace)
	}
}

func TestHeadlessLaunch_TmuxFailurePropagates(t *testing.T) {
	tmux := &fakeTmuxRunner{err: errHeadlessTmuxFail}
	l, _ := newTestLauncher(t, fakeGitForHeadless(), tmux, []string{"abcd1234efgh5678"})

	_, err := l.HeadlessLaunch(LaunchRequest{StartDir: "/plain", Task: "x"})
	if err == nil {
		t.Fatalf("expected tmux failure to propagate, got nil")
	}
}

var errHeadlessTmuxFail = strErr("tmux boom")

func TestHeadlessLaunch_InPlaceModeOnGitDirRunsInCallerCwd(t *testing.T) {
	// A git fake: rev-parse reports a real toplevel, so in-place mode
	// must override the default worktree-per-thread path.
	git := GitRunnerFunc(func(dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "rev-parse" && len(args) > 1 && args[1] == "--show-toplevel" {
			return "/repo\n", nil
		}
		return "", errHeadlessFakeGitNotARepo
	})
	tmux := &fakeTmuxRunner{}
	l, _ := newTestLauncher(t, git, tmux, []string{"abcd1234efgh5678"})

	res, err := l.HeadlessLaunch(LaunchRequest{
		StartDir:      "/repo/sub",
		Task:          "explore the graph",
		Profile:       "general-agentic",
		WorkspaceMode: WorkspaceInPlace,
	})
	if err != nil {
		t.Fatalf("HeadlessLaunch: %v", err)
	}
	if !res.InPlace {
		t.Errorf("InPlace = %v, want true (in-place mode on a git dir)", res.InPlace)
	}
	if res.WorktreePath != "/repo/sub" {
		t.Errorf("WorktreePath = %q, want /repo/sub", res.WorktreePath)
	}
	if res.Branch != "" {
		t.Errorf("Branch = %q, want empty for in-place run", res.Branch)
	}
}
