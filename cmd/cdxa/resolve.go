package main

import (
	"github.com/dzungtr/codex-agents/internal/agentstate"
)

// resolveStatePath returns the cockpit's own state.json location
// (~/.codex-agents/state.json), the same file the cockpit writes launch
// bookkeeping to. cdxa spawn writes to it via codexlaunch.Launcher for the
// same reason the cockpit does: so the spawned thread's tmux session and
// worktree are resolvable by the cockpit later (ADR 0001 decision 2).
func resolveStatePath() (string, error) {
	return agentstate.DefaultPath()
}
