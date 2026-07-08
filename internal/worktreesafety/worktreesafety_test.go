package worktreesafety

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dzungtr/codex-agents/internal/codexlaunch"
)

// These tests exercise Check and RemoveWorktree against a real git binary
// and real temp repos, per the PRD's testing decisions ("worktree-safety
// checks (uncommitted/unpushed detection)" against real git repos) — this
// sandbox has no real codex/tmux, but git is available.

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := codexlaunch.ExecGitRunner{}.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v (dir=%s) failed: %v\n%s", args, dir, err, out)
	}
	return out
}

// setupRepo creates a repo with a single commit on branch "main", returning
// its path.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func addWorktree(t *testing.T, repoDir, branch string) string {
	t.Helper()
	wtPath := filepath.Join(t.TempDir(), branch)
	runGit(t, repoDir, "worktree", "add", "-b", branch, wtPath)
	return wtPath
}

func TestCheck_CleanNoUpstreamNoAheadOfMain_Safe(t *testing.T) {
	repo := setupRepo(t)
	wt := addWorktree(t, repo, "feature-clean")

	got, err := Check(codexlaunch.ExecGitRunner{}, wt, "main")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !got.Safe {
		t.Fatalf("expected safe, got refusal: %s", got.Reason)
	}
}

func TestCheck_UncommittedChanges_Unsafe(t *testing.T) {
	repo := setupRepo(t)
	wt := addWorktree(t, repo, "feature-dirty")
	if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	got, err := Check(codexlaunch.ExecGitRunner{}, wt, "main")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Safe {
		t.Fatalf("expected unsafe for uncommitted changes")
	}
	if !strings.Contains(got.Reason, "uncommitted") {
		t.Fatalf("expected reason to mention uncommitted changes, got %q", got.Reason)
	}
}

func TestCheck_CommitsNotMergedIntoBaseBranch_Unsafe(t *testing.T) {
	repo := setupRepo(t)
	wt := addWorktree(t, repo, "feature-unmerged")
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, wt, "add", "new.txt")
	runGit(t, wt, "commit", "-q", "-m", "add new file")

	got, err := Check(codexlaunch.ExecGitRunner{}, wt, "main")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Safe {
		t.Fatalf("expected unsafe for commits not merged into main")
	}
	if !strings.Contains(got.Reason, "not merged") {
		t.Fatalf("expected reason to mention unmerged commits, got %q", got.Reason)
	}
}

func TestCheck_UnpushedRelativeToUpstream_Unsafe(t *testing.T) {
	repo := setupRepo(t)
	origin := t.TempDir()
	runGit(t, origin, "init", "-q", "--bare")
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "-q", "-u", "origin", "main")

	wt := addWorktree(t, repo, "feature-pushed")
	runGit(t, wt, "push", "-q", "-u", "origin", "feature-pushed")

	// One more local commit after the push: now ahead of its upstream.
	if err := os.WriteFile(filepath.Join(wt, "more.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, wt, "add", "more.txt")
	runGit(t, wt, "commit", "-q", "-m", "more work")

	got, err := Check(codexlaunch.ExecGitRunner{}, wt, "main")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Safe {
		t.Fatalf("expected unsafe for commits not pushed to upstream")
	}
	if !strings.Contains(got.Reason, "not pushed") {
		t.Fatalf("expected reason to mention unpushed commits, got %q", got.Reason)
	}
}

func TestCheck_PushedAndMatchingUpstream_Safe(t *testing.T) {
	repo := setupRepo(t)
	origin := t.TempDir()
	runGit(t, origin, "init", "-q", "--bare")
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "-q", "-u", "origin", "main")

	wt := addWorktree(t, repo, "feature-safe")
	if err := os.WriteFile(filepath.Join(wt, "more.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, wt, "add", "more.txt")
	runGit(t, wt, "commit", "-q", "-m", "more work")
	runGit(t, wt, "push", "-q", "-u", "origin", "feature-safe")

	got, err := Check(codexlaunch.ExecGitRunner{}, wt, "main")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !got.Safe {
		t.Fatalf("expected safe once pushed to upstream, got refusal: %s", got.Reason)
	}
}

func TestCheck_NoUpstreamAndBaseBranchMissing_Unsafe(t *testing.T) {
	repo := setupRepo(t)
	wt := addWorktree(t, repo, "feature-nobase")

	got, err := Check(codexlaunch.ExecGitRunner{}, wt, "does-not-exist")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Safe {
		t.Fatalf("expected unsafe when base branch is missing and no upstream configured")
	}
	if !strings.Contains(got.Reason, "does-not-exist") {
		t.Fatalf("expected reason to mention the missing base branch, got %q", got.Reason)
	}
}

func TestRemoveWorktree_RemovesCleanWorktree(t *testing.T) {
	repo := setupRepo(t)
	wt := addWorktree(t, repo, "feature-removeme")

	if err := RemoveWorktree(codexlaunch.ExecGitRunner{}, repo, wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir removed, stat err = %v", err)
	}
}
