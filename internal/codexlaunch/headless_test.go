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
	if res.ThreadID != "abcd1234efgh5678" {
		t.Errorf("ThreadID = %q, want abcd1234efgh5678", res.ThreadID)
	}
	if res.SessionName != tmuxstatus.SessionName("abcd1234efgh5678") {
		t.Errorf("SessionName = %q, want %q", res.SessionName, tmuxstatus.SessionName("abcd1234efgh5678"))
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
