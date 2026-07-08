package codexlaunch

import (
	"fmt"
	"testing"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

func TestQuickReply_SendsTextThenEnterToTheThreadsSession(t *testing.T) {
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, nil, tmux, nil)
	if err := agentstate.Upsert(statePath, "thread1", agentstate.Entry{TmuxSession: tmuxstatus.SessionName("thread1"), LastTurnEvent: "turn-ended@2026-07-08T00:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := l.QuickReply("thread1", "proceed with option B"); err != nil {
		t.Fatalf("QuickReply: %v", err)
	}

	wantSession := tmuxstatus.SessionName("thread1")
	if len(tmux.calls) != 2 {
		t.Fatalf("expected exactly two tmux calls (text, then enter), got %v", tmux.calls)
	}
	wantText := tmuxstatus.SendKeysArgs(wantSession, "proceed with option B")
	wantEnter := tmuxstatus.SendEnterArgs(wantSession)
	if fmt.Sprint(tmux.calls[0]) != fmt.Sprint(wantText) {
		t.Errorf("first tmux call = %v, want %v", tmux.calls[0], wantText)
	}
	if fmt.Sprint(tmux.calls[1]) != fmt.Sprint(wantEnter) {
		t.Errorf("second tmux call = %v, want %v", tmux.calls[1], wantEnter)
	}
}

// TestQuickReply_ClearsLastTurnEvent closes the gap #4's reviewer flagged in
// PRD #1's Handoffs table: last_turn_event was never cleared, so a thread
// replied to would keep reading as "waiting" until its tmux session died.
func TestQuickReply_ClearsLastTurnEvent(t *testing.T) {
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, nil, tmux, nil)
	if err := agentstate.Upsert(statePath, "thread1", agentstate.Entry{
		TmuxSession:   tmuxstatus.SessionName("thread1"),
		Profile:       "general-agentic",
		WorktreePath:  "/repo/.worktrees/thread1",
		LastTurnEvent: "turn-ended@2026-07-08T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := l.QuickReply("thread1", "go ahead"); err != nil {
		t.Fatalf("QuickReply: %v", err)
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	entry := st.Threads["thread1"]
	if entry.LastTurnEvent != "" {
		t.Errorf("LastTurnEvent = %q, want cleared", entry.LastTurnEvent)
	}
	// Other fields must survive the clear.
	if entry.Profile != "general-agentic" || entry.WorktreePath != "/repo/.worktrees/thread1" {
		t.Errorf("expected other entry fields preserved, got %+v", entry)
	}
}

func TestQuickReply_TmuxTextFailure_DoesNotSendEnterOrClearState(t *testing.T) {
	tmux := &fakeTmuxRunner{err: fmt.Errorf("tmux boom")}
	l, statePath := newTestLauncher(t, nil, tmux, nil)
	if err := agentstate.Upsert(statePath, "thread1", agentstate.Entry{LastTurnEvent: "turn-ended@2026-07-08T00:00:00Z"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := l.QuickReply("thread1", "go ahead"); err == nil {
		t.Fatalf("expected an error when tmux fails")
	}

	if len(tmux.calls) != 1 {
		t.Fatalf("expected send-keys to be attempted once and enter never sent, got %v", tmux.calls)
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if st.Threads["thread1"].LastTurnEvent == "" {
		t.Errorf("expected LastTurnEvent left untouched after a failed send, got cleared")
	}
}

func TestQuickReply_UnknownThreadID_StillClearsAFreshEntry(t *testing.T) {
	// QuickReply is only meant to be called against a thread the caller
	// already knows is alive, but it shouldn't error out just because
	// state.json has no prior entry for it (mirrors
	// agentstate.UpdateLastTurnEvent's own no-prior-entry tolerance).
	tmux := &fakeTmuxRunner{}
	l, statePath := newTestLauncher(t, nil, tmux, nil)

	if err := l.QuickReply("unknown-thread", "go ahead"); err != nil {
		t.Fatalf("QuickReply: %v", err)
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if _, ok := st.Threads["unknown-thread"]; !ok {
		t.Errorf("expected a state entry to exist for unknown-thread after QuickReply")
	}
}
