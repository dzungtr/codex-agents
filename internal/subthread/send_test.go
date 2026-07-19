package subthread

import (
	"errors"
	"testing"

	"github.com/dzungtr/codex-agents/internal/codexstate"
)

// fakeReplier scripts QuickReply calls: records every (threadID, msg) pair
// it received and returns the canned err (nil by default). Used to exercise
// Send's delivery + turn-derivation flow without a real tmux server.
type fakeReplier struct {
	calls []fakeReplierCall
	err   error
}

type fakeReplierCall struct {
	ThreadID string
	Msg      string
}

func (f *fakeReplier) QuickReply(threadID, msg string) error {
	f.calls = append(f.calls, fakeReplierCall{ThreadID: threadID, Msg: msg})
	return f.err
}

// startedTurnCases exercises Send's turn-number derivation against multi-turn
// fixture rollouts (issue #31 acceptance: "Tests cover turn-number
// derivation at send time against multi-turn fixture rollouts"). The started
// turn is len(Completed)+1 — the next turn to appear after the follow-up is
// delivered (ADR 0003 decision 5).
func TestSend_TurnDerivationAgainstMultiTurnRollouts(t *testing.T) {
	cases := []struct {
		name        string
		turns       codexstate.Turns
		wantTurn    int
		wantDeliver bool
	}{
		{
			name:        "two completed turns → started turn 3",
			turns:       codexstate.Turns{Completed: []codexstate.Turn{{Number: 1, Message: "first"}, {Number: 2, Message: "second"}}},
			wantTurn:    3,
			wantDeliver: true,
		},
		{
			name:        "single completed turn → started turn 2",
			turns:       codexstate.Turns{Completed: []codexstate.Turn{{Number: 1, Message: "only"}}},
			wantTurn:    2,
			wantDeliver: true,
		},
		{
			name:        "zero completed turns → started turn 1 (first turn in flight)",
			turns:       codexstate.Turns{Completed: nil, InProgress: true},
			wantTurn:    1,
			wantDeliver: true,
		},
		{
			name:        "one completed, one in progress → started turn 2 (in-flight turn)",
			turns:       codexstate.Turns{Completed: []codexstate.Turn{{Number: 1, Message: "done"}}, InProgress: true},
			wantTurn:    2,
			wantDeliver: true,
		},
		{
			name:        "three completed turns → started turn 4",
			turns:       codexstate.Turns{Completed: []codexstate.Turn{{Number: 1}, {Number: 2}, {Number: 3, Message: "third"}}},
			wantTurn:    4,
			wantDeliver: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := fakeState{
				threads: map[string]codexstate.Thread{
					"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
				},
				turns: map[string]codexstate.Turns{"/r/t1.jsonl": tc.turns},
			}
			replier := &fakeReplier{}
			live := func(string) bool { return true }

			turn, err := Send(state, live, replier, "/codex", "t1", "follow up")
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if turn != tc.wantTurn {
				t.Errorf("turn = %d, want %d", turn, tc.wantTurn)
			}
			if tc.wantDeliver && len(replier.calls) != 1 {
				t.Errorf("expected exactly one QuickReply call, got %d", len(replier.calls))
			}
			if tc.wantDeliver {
				if replier.calls[0].ThreadID != "t1" {
					t.Errorf("QuickReply threadID = %q, want t1", replier.calls[0].ThreadID)
				}
				if replier.calls[0].Msg != "follow up" {
					t.Errorf("QuickReply msg = %q, want %q", replier.calls[0].Msg, "follow up")
				}
			}
		})
	}
}

func TestSend_UnknownThread_ReturnsErrGone(t *testing.T) {
	state := fakeState{} // no threads → ErrThreadNotFound
	replier := &fakeReplier{}
	live := func(string) bool { return true }

	_, err := Send(state, live, replier, "/codex", "nope", "msg")
	if !errors.Is(err, ErrGone) {
		t.Fatalf("expected ErrGone for unknown thread, got %v", err)
	}
	if len(replier.calls) != 0 {
		t.Errorf("expected zero QuickReply calls for unknown thread, got %d", len(replier.calls))
	}
}

func TestSend_DeadSession_ReturnsErrGone(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "done"}}}},
	}
	replier := &fakeReplier{}
	live := func(string) bool { return false } // session dead

	_, err := Send(state, live, replier, "/codex", "t1", "msg")
	if !errors.Is(err, ErrGone) {
		t.Fatalf("expected ErrGone for dead session, got %v", err)
	}
	if len(replier.calls) != 0 {
		t.Errorf("expected zero QuickReply calls for dead session, got %d", len(replier.calls))
	}
}

func TestSend_NilLiveness_ReturnsErrGone(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
	}
	replier := &fakeReplier{}

	_, err := Send(state, nil, replier, "/codex", "t1", "msg")
	if !errors.Is(err, ErrGone) {
		t.Fatalf("expected ErrGone when liveness is nil, got %v", err)
	}
}

func TestSend_FindThreadOperationalError_ReturnsErrOperational(t *testing.T) {
	state := fakeState{findErr: errors.New("sqlite: disk I/O error")}
	replier := &fakeReplier{}
	live := func(string) bool { return true }

	_, err := Send(state, live, replier, "/codex", "t1", "msg")
	if !errors.Is(err, ErrOperational) {
		t.Fatalf("expected ErrOperational, got %v", err)
	}
	if len(replier.calls) != 0 {
		t.Errorf("expected zero QuickReply calls on find error, got %d", len(replier.calls))
	}
}

func TestSend_ReadTurnsError_ReturnsErrOperational(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		readErr: errors.New("open /r/t1.jsonl: no such file"),
	}
	replier := &fakeReplier{}
	live := func(string) bool { return true }

	_, err := Send(state, live, replier, "/codex", "t1", "msg")
	if !errors.Is(err, ErrOperational) {
		t.Fatalf("expected ErrOperational, got %v", err)
	}
	if len(replier.calls) != 0 {
		t.Errorf("expected zero QuickReply calls on read error, got %d", len(replier.calls))
	}
}

func TestSend_ReplierFailure_ReturnsErrOperational(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "done"}}}},
	}
	replier := &fakeReplier{err: errors.New("tmux boom")}
	live := func(string) bool { return true }

	_, err := Send(state, live, replier, "/codex", "t1", "msg")
	if !errors.Is(err, ErrOperational) {
		t.Fatalf("expected ErrOperational for replier failure, got %v", err)
	}
	if len(replier.calls) != 1 {
		t.Errorf("expected QuickReply attempted once, got %d", len(replier.calls))
	}
}
