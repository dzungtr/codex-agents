package codexlaunch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fakeGitRunner is a scripted GitRunner: it records every call and returns
// pre-programmed responses keyed by the joined args, so workspace-resolution
// logic can be tested without a real git binary.
type fakeGitRunner struct {
	calls     []fakeGitCall
	responses map[string]fakeGitResponse
}

type fakeGitCall struct {
	dir  string
	args []string
}

type fakeGitResponse struct {
	out string
	err error
}

func (f *fakeGitRunner) Run(dir string, args ...string) (string, error) {
	f.calls = append(f.calls, fakeGitCall{dir: dir, args: append([]string(nil), args...)})
	key := fmt.Sprintf("%v", args)
	if resp, ok := f.responses[key]; ok {
		return resp.out, resp.err
	}
	// An unprogrammed `rev-parse --verify` (branch-existence check)
	// defaults to "does not exist" (BranchExists treats err==nil as
	// "exists"). Without this, a collision-suffix test that only
	// programs the base slug's response would spin forever: every
	// un-mapped candidate (fix-auth-hook-2, -3, ...) would otherwise
	// default to "exists" too, and UniqueSlug would never terminate.
	if len(args) > 1 && args[0] == "rev-parse" && args[1] == "--verify" {
		return "", fmt.Errorf("not found (unprogrammed fake response)")
	}
	return "", nil
}

func TestRepoRoot_ReturnsToplevel(t *testing.T) {
	f := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {out: "/repo\n"},
	}}
	root, ok := RepoRoot(f, "/repo/sub")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if root != "/repo" {
		t.Fatalf("RepoRoot() = %q, want /repo", root)
	}
}

func TestRepoRoot_NonGitDirReturnsNotOK(t *testing.T) {
	f := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a git repository")},
	}}
	_, ok := RepoRoot(f, "/tmp/plain-dir")
	if ok {
		t.Fatalf("expected ok=false for a non-git dir")
	}
}

func TestBranchExists(t *testing.T) {
	f := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --verify --quiet refs/heads/fix-auth-hook]": {out: "abc123\n"},
		"[rev-parse --verify --quiet refs/heads/other]":         {err: fmt.Errorf("exit 1")},
	}}
	if !BranchExists(f, "/repo", "fix-auth-hook") {
		t.Fatalf("expected fix-auth-hook to exist")
	}
	if BranchExists(f, "/repo", "other") {
		t.Fatalf("expected other to not exist")
	}
}

func TestResolveWorkspace_NonGitDirRunsInPlace(t *testing.T) {
	f := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]": {err: fmt.Errorf("not a git repository")},
	}}
	ws, err := ResolveWorkspace(f, "/tmp/plain-dir", "Fix auth hook")
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if !ws.InPlace {
		t.Fatalf("expected InPlace=true for a non-git start dir")
	}
	if ws.WorkDir != "/tmp/plain-dir" {
		t.Fatalf("WorkDir = %q, want /tmp/plain-dir", ws.WorkDir)
	}
	if ws.Branch != "" {
		t.Fatalf("Branch = %q, want empty for in-place run", ws.Branch)
	}
	for _, c := range f.calls {
		if len(c.args) > 0 && c.args[0] == "worktree" {
			t.Fatalf("did not expect a worktree add call for a non-git dir, got %v", c.args)
		}
	}
}

func TestResolveWorkspace_GitDirCreatesWorktree(t *testing.T) {
	f := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]":                          {out: "/repo\n"},
		"[rev-parse --verify --quiet refs/heads/fix-auth-hook]": {err: fmt.Errorf("exit 1")},
	}}
	ws, err := ResolveWorkspace(f, "/repo/sub", "Fix auth hook")
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if ws.InPlace {
		t.Fatalf("expected InPlace=false for a git dir")
	}
	wantDir := filepath.Join("/repo", ".worktrees", "fix-auth-hook")
	if ws.WorkDir != wantDir {
		t.Fatalf("WorkDir = %q, want %q", ws.WorkDir, wantDir)
	}
	if ws.Branch != "fix-auth-hook" {
		t.Fatalf("Branch = %q, want fix-auth-hook", ws.Branch)
	}

	found := false
	for _, c := range f.calls {
		if len(c.args) >= 2 && c.args[0] == "worktree" && c.args[1] == "add" {
			found = true
			if c.dir != "/repo" {
				t.Fatalf("expected worktree add to run in repo root, ran in %q", c.dir)
			}
		}
	}
	if !found {
		t.Fatalf("expected a 'git worktree add' call, calls: %+v", f.calls)
	}
}

func TestResolveWorkspace_CollisionAppendsSuffix(t *testing.T) {
	f := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]":                          {out: "/repo\n"},
		"[rev-parse --verify --quiet refs/heads/fix-auth-hook]": {out: "abc123\n"}, // branch already exists
	}}
	ws, err := ResolveWorkspace(f, "/repo", "Fix auth hook")
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if ws.Branch != "fix-auth-hook-2" {
		t.Fatalf("Branch = %q, want fix-auth-hook-2", ws.Branch)
	}
}

func TestResolveWorkspace_CollisionOnExistingWorktreeDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".worktrees", "fix-auth-hook"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	f := &fakeGitRunner{responses: map[string]fakeGitResponse{
		"[rev-parse --show-toplevel]":                          {out: dir + "\n"},
		"[rev-parse --verify --quiet refs/heads/fix-auth-hook]": {err: fmt.Errorf("exit 1")}, // no branch, but dir exists on disk
	}}
	ws, err := ResolveWorkspace(f, dir, "Fix auth hook")
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if ws.Branch != "fix-auth-hook-2" {
		t.Fatalf("Branch = %q, want fix-auth-hook-2 (dir collision should also suffix)", ws.Branch)
	}
}

// TestResolveWorkspace_RealGit exercises the full path against a real git
// binary and a real temp repo, per the PRD's testing decisions (this
// sandbox has no real codex/tmux, but git is available).
func TestResolveWorkspace_RealGit(t *testing.T) {
	dir := t.TempDir()
	run := func(d string, args ...string) (string, error) {
		return runGit(t, d, args...)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", "init")

	ws, err := ResolveWorkspace(GitRunnerFunc(run), dir, "Fix auth hook")
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if ws.InPlace {
		t.Fatalf("expected a real worktree, not in-place")
	}
	if _, err := os.Stat(filepath.Join(ws.WorkDir, "README.md")); err != nil {
		t.Fatalf("expected worktree to contain checked-out files: %v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	out, err := ExecGitRunner{}.Run(dir, args...)
	if err != nil {
		t.Logf("git %v (dir=%s) failed: %v\n%s", args, dir, err, out)
	}
	return out, err
}
