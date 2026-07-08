package main

import (
	"path/filepath"
	"testing"

	"github.com/dzungtr/codex-agents/internal/agentstate"
)

func TestLoadTurnEndedByThread_ReflectsAgentStateEntries(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := agentstate.Upsert(statePath, "waiting-thread", agentstate.Entry{
		TmuxSession:   "cxa-waiting-thread",
		LastTurnEvent: "turn-ended@2026-07-08T12:00:00Z",
	}); err != nil {
		t.Fatalf("seed waiting-thread: %v", err)
	}
	if err := agentstate.Upsert(statePath, "working-thread", agentstate.Entry{
		TmuxSession: "cxa-working-thread",
	}); err != nil {
		t.Fatalf("seed working-thread: %v", err)
	}

	got := loadTurnEndedByThread(statePath)
	if !got["waiting-thread"] {
		t.Errorf("expected waiting-thread to report turnEnded=true, got %v", got)
	}
	if got["working-thread"] {
		t.Errorf("expected working-thread (empty LastTurnEvent) to report turnEnded=false, got %v", got)
	}
}

// TestLoadTurnEndedByThread_MissingStateDegradesToEmpty exercises the PRD's
// "hook unavailable -> degrade to open/closed" contract at the read side:
// a state.json that doesn't exist yet (e.g. first run on a machine, or the
// notify hook has never fired) must not error the whole list — every
// thread simply reports turnEnded=false, matching plain tmux-liveness
// status derivation.
func TestLoadTurnEndedByThread_MissingStateDegradesToEmpty(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "does-not-exist", "state.json")
	got := loadTurnEndedByThread(statePath)
	if len(got) != 0 {
		t.Errorf("expected an empty map for a missing state file, got %v", got)
	}
}
