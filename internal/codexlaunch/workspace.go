package codexlaunch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// worktreesDirName is the subdirectory under a repo's root where
// cockpit-launched worktrees live, per PRD #1's Launch semantics table.
const worktreesDirName = ".worktrees"

// GitRunner runs a git subcommand with its working directory set to dir,
// returning combined output. Production code uses ExecGitRunner; tests
// inject a fake so workspace resolution can be exercised without a real
// git binary (and without touching the filesystem for the non-worktree
// cases).
type GitRunner interface {
	Run(dir string, args ...string) (string, error)
}

// GitRunnerFunc adapts a plain function to a GitRunner.
type GitRunnerFunc func(dir string, args ...string) (string, error)

func (f GitRunnerFunc) Run(dir string, args ...string) (string, error) {
	return f(dir, args...)
}

// ExecGitRunner shells out to the real git binary.
type ExecGitRunner struct{}

func (ExecGitRunner) Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Workspace is the resolved launch location for a new thread: either a
// fresh git worktree on its own branch, or the start directory itself when
// it isn't part of a git repo ("run in place", per PRD #1).
type Workspace struct {
	WorkDir string // directory to launch codex in (tmux -c)
	Branch  string // branch slug; empty when InPlace
	InPlace bool
}

// RepoRoot runs `git rev-parse --show-toplevel` in startDir. ok is false
// when startDir isn't inside a git working tree (or git isn't available),
// which the caller treats as "run in place" rather than an error.
func RepoRoot(run GitRunner, startDir string) (root string, ok bool) {
	out, err := run.Run(startDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	root = strings.TrimSpace(out)
	if root == "" {
		return "", false
	}
	return root, true
}

// BranchExists reports whether branch already exists in the repo rooted at
// repoRoot.
func BranchExists(run GitRunner, repoRoot, branch string) bool {
	_, err := run.Run(repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// worktreeDirExists reports whether repoRoot/.worktrees/slug already exists
// on disk. This is checked in addition to BranchExists because a prior
// worktree could have been removed as a branch but left its directory (or
// vice versa); either signals the slug is taken.
func worktreeDirExists(repoRoot, slug string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, worktreesDirName, slug))
	return err == nil
}

// CreateWorktree runs `git worktree add -b <branch> <path>` for a repo
// rooted at repoRoot, returning the new worktree's path.
func CreateWorktree(run GitRunner, repoRoot, branch string) (string, error) {
	path := filepath.Join(repoRoot, worktreesDirName, branch)
	if _, err := run.Run(repoRoot, "worktree", "add", "-b", branch, path); err != nil {
		return "", err
	}
	return path, nil
}

// ResolveWorkspace decides where a newly launched thread should run:
//
//   - If startDir is inside a git repo, a new worktree is created at
//     <repo-root>/.worktrees/<slug>, where slug is derived from title and
//     suffixed on collision (an existing branch of that name, or an
//     existing directory at that path).
//   - Otherwise ("non-git startup dir"), the thread runs in place: WorkDir
//     is startDir itself, InPlace is true, Branch is empty.
func ResolveWorkspace(run GitRunner, startDir, title string) (Workspace, error) {
	root, ok := RepoRoot(run, startDir)
	if !ok {
		return Workspace{WorkDir: startDir, InPlace: true}, nil
	}

	taken := func(slug string) bool {
		return BranchExists(run, root, slug) || worktreeDirExists(root, slug)
	}
	branch := BranchSlug(title, taken)

	path, err := CreateWorktree(run, root, branch)
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{WorkDir: path, Branch: branch, InPlace: false}, nil
}
