// Package worktreesafety implements the uncommitted/unpushed detection that
// gates worktree removal during the Archive (`a`) list action (PRD #1's
// List behavior -> Archive row: "offers worktree removal — refuses if
// uncommitted/unpushed work"). It knows nothing about codex, tmux, or
// agentstate — only how to ask git whether a worktree is safe to remove,
// and how to remove it once it is.
package worktreesafety

import (
	"fmt"
	"strconv"
	"strings"
)

// GitRunner runs a git subcommand with its working directory set to dir,
// returning combined output. This is structurally identical to
// internal/codexlaunch.GitRunner (and satisfied by the same concrete
// implementations, e.g. codexlaunch.ExecGitRunner, since Go interfaces are
// structural) — declared separately here so this package doesn't need to
// import codexlaunch just for a two-method interface.
type GitRunner interface {
	Run(dir string, args ...string) (string, error)
}

// Result is the outcome of Check.
type Result struct {
	// Safe is true when worktreePath has no uncommitted changes and no
	// commits that removal would lose.
	Safe bool
	// Reason is a human-readable refusal explanation. Empty when Safe.
	Reason string
}

// Check reports whether worktreePath is safe to remove per PRD #1's
// Archive row: it must have
//
//  1. no uncommitted changes (`git status --porcelain`), and
//  2. no commits that removal would lose — measured against its upstream
//     tracking branch if one is configured (the "unpushed" case), or
//     against baseBranch otherwise (the "unmerged" case for a branch that
//     was never pushed at all).
//
// baseBranch is typically the repo's main branch (e.g. "main"); callers
// resolve it once per repo rather than this package guessing.
func Check(run GitRunner, worktreePath, baseBranch string) (Result, error) {
	porcelain, err := run.Run(worktreePath, "status", "--porcelain")
	if err != nil {
		return Result{}, fmt.Errorf("worktreesafety: git status --porcelain: %w", err)
	}
	if strings.TrimSpace(porcelain) != "" {
		return Result{Safe: false, Reason: "worktree has uncommitted changes"}, nil
	}

	if upstream, ok := upstreamRef(run, worktreePath); ok {
		ahead, err := commitsAhead(run, worktreePath, upstream)
		if err != nil {
			return Result{}, err
		}
		if ahead > 0 {
			return Result{Safe: false, Reason: fmt.Sprintf("%d commit(s) not pushed to %s", ahead, upstream)}, nil
		}
		return Result{Safe: true}, nil
	}

	if _, err := run.Run(worktreePath, "rev-parse", "--verify", "--quiet", "refs/heads/"+baseBranch); err != nil {
		return Result{Safe: false, Reason: fmt.Sprintf("no upstream configured and base branch %q not found; refusing to remove", baseBranch)}, nil
	}
	ahead, err := commitsAhead(run, worktreePath, baseBranch)
	if err != nil {
		return Result{}, err
	}
	if ahead > 0 {
		return Result{Safe: false, Reason: fmt.Sprintf("%d commit(s) not merged into %s", ahead, baseBranch)}, nil
	}
	return Result{Safe: true}, nil
}

// upstreamRef returns worktreePath's configured upstream tracking ref
// (e.g. "origin/feature-x"), if any.
func upstreamRef(run GitRunner, worktreePath string) (string, bool) {
	out, err := run.Run(worktreePath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		return "", false
	}
	ref := strings.TrimSpace(out)
	if ref == "" {
		return "", false
	}
	return ref, true
}

// commitsAhead reports how many commits HEAD has that ref does not.
func commitsAhead(run GitRunner, worktreePath, ref string) (int, error) {
	out, err := run.Run(worktreePath, "rev-list", "--count", ref+"..HEAD")
	if err != nil {
		return 0, fmt.Errorf("worktreesafety: git rev-list --count %s..HEAD: %w", ref, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("worktreesafety: parse rev-list count %q: %w", out, err)
	}
	return n, nil
}

// RemoveWorktree runs `git worktree remove <worktreePath>` from repoRoot.
// Callers must only call this after Check reports Safe=true: Remove itself
// applies no unpushed/unmerged gating (only git's own native
// uncommitted-changes refusal, since no --force is passed).
func RemoveWorktree(run GitRunner, repoRoot, worktreePath string) error {
	if _, err := run.Run(repoRoot, "worktree", "remove", worktreePath); err != nil {
		return fmt.Errorf("worktreesafety: git worktree remove: %w", err)
	}
	return nil
}
