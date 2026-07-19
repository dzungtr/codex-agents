package subthread

import (
	"errors"
	"testing"

	"github.com/dzungtr/codex-agents/internal/codexstate"
)

// fakeState is a test double for StateProvider. It returns canned results
// per thread id, so the subthread module can be exercised without sqlite or
// rollout files on disk. The zero value returns ErrThreadNotFound for every
// id; populate the maps to script behaviour.
type fakeState struct {
	threads map[string]codexstate.Thread
	turns   map[string]codexstate.Turns
	// findErr, when set, is returned from FindThread for every id (used to
	// exercise the operational-error path — a non-ErrThreadNotFound error
	// from codexstate surfaces as ErrOperational).
	findErr error
	// readErr, when set, is returned from ReadTurns for every rollout path
	// (used to exercise the rollout-missing/unreadable path).
	readErr error
}

func (f fakeState) FindThread(_ string, threadID string) (codexstate.Thread, error) {
	if f.findErr != nil {
		return codexstate.Thread{}, f.findErr
	}
	if th, ok := f.threads[threadID]; ok {
		return th, nil
	}
	return codexstate.Thread{}, codexstate.ErrThreadNotFound
}

func (f fakeState) ReadTurns(rolloutPath string) (codexstate.Turns, error) {
	if f.readErr != nil {
		return codexstate.Turns{}, f.readErr
	}
	if turns, ok := f.turns[rolloutPath]; ok {
		return turns, nil
	}
	return codexstate.Turns{}, nil
}

func TestOutput_CompletedTurnStatusDone(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: []codexstate.Turn{
				{Number: 1, TurnID: "turn-1", Message: "first result"},
				{Number: 2, TurnID: "turn-2", Message: "second result"},
			}},
		},
	}
	live := func(string) bool { return false } // session alive or not doesn't matter when latest turn ended

	got, err := Output(state, live, "/codex", "t1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got.Status != StatusDone {
		t.Errorf("Status = %s, want done", got.Status)
	}
	if got.Turn != 2 {
		t.Errorf("Turn = %d, want 2", got.Turn)
	}
	if got.Message != "second result" {
		t.Errorf("Message = %q, want second result", got.Message)
	}
}

func TestOutput_LastTurnInProgressStatusWorking(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {
				Completed:  []codexstate.Turn{{Number: 1, Message: "first result"}},
				InProgress: true,
			},
		},
	}
	live := func(string) bool { return false }

	got, err := Output(state, live, "/codex", "t1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got.Status != StatusWorking {
		t.Errorf("Status = %s, want working", got.Status)
	}
	// Turn/Message reflect the last completed turn for idempotency.
	if got.Turn != 1 {
		t.Errorf("Turn = %d, want 1 (last completed turn)", got.Turn)
	}
	if got.Message != "first result" {
		t.Errorf("Message = %q, want first result", got.Message)
	}
}

func TestOutput_NoCompletedTurnSessionAliveStatusWorking(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: nil, InProgress: true},
		},
	}
	live := func(string) bool { return true }

	got, err := Output(state, live, "/codex", "t1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got.Status != StatusWorking {
		t.Errorf("Status = %s, want working (first turn in flight)", got.Status)
	}
	if got.Turn != 0 {
		t.Errorf("Turn = %d, want 0 (no completed turn)", got.Turn)
	}
	if got.Message != "" {
		t.Errorf("Message = %q, want empty", got.Message)
	}
}

func TestOutput_NoCompletedTurnSessionDeadStatusGone(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: nil, InProgress: true},
		},
	}
	live := func(string) bool { return false } // session died, no output collected

	got, err := Output(state, live, "/codex", "t1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got.Status != StatusGone {
		t.Errorf("Status = %s, want gone (dead session, no output)", got.Status)
	}
}

func TestOutput_UnknownThreadIDStatusGone(t *testing.T) {
	state := fakeState{}                      // no threads
	live := func(string) bool { return true } // alive status irrelevant when thread unknown

	got, err := Output(state, live, "/codex", "no-such-thread")
	if err != nil {
		t.Fatalf("Output: expected no error for unknown thread, got %v", err)
	}
	if got.Status != StatusGone {
		t.Errorf("Status = %s, want gone (unknown thread id)", got.Status)
	}
}

func TestOutput_SqliteUnreadableReturnsErrOperational(t *testing.T) {
	state := fakeState{findErr: errors.New("sqlite: disk I/O error")}
	live := func(string) bool { return false }

	_, err := Output(state, live, "/codex", "t1")
	if err == nil {
		t.Fatalf("expected error for unreadable sqlite, got nil")
	}
	if !errors.Is(err, ErrOperational) {
		t.Errorf("error = %v, want ErrOperational", err)
	}
}

func TestOutput_RolloutMissingReturnsErrOperational(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/missing.jsonl"},
		},
		readErr: errors.New("open /r/missing.jsonl: no such file"),
	}
	live := func(string) bool { return false }

	_, err := Output(state, live, "/codex", "t1")
	if err == nil {
		t.Fatalf("expected error for missing rollout, got nil")
	}
	if !errors.Is(err, ErrOperational) {
		t.Errorf("error = %v, want ErrOperational", err)
	}
}

func TestOutput_IdempotentRepeatPolls(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: []codexstate.Turn{
				{Number: 1, Message: "stable result"},
			}},
		},
	}
	live := func(string) bool { return false }

	first, err := Output(state, live, "/codex", "t1")
	if err != nil {
		t.Fatalf("first Output: %v", err)
	}
	second, err := Output(state, live, "/codex", "t1")
	if err != nil {
		t.Fatalf("second Output: %v", err)
	}
	if first != second {
		t.Errorf("repeat polls not idempotent: first=%+v second=%+v", first, second)
	}
}

func TestOutput_EmptyRolloutNoTurnsSessionAlive(t *testing.T) {
	// A thread whose rollout exists but has no turn markers at all, with a
	// live session: codex just registered, nothing has happened yet. This
	// is "working" (the session is alive), not "gone".
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: nil, InProgress: false},
		},
	}
	live := func(string) bool { return true }

	got, err := Output(state, live, "/codex", "t1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got.Status != StatusWorking {
		t.Errorf("Status = %s, want working (live session, no turns yet)", got.Status)
	}
}

func TestStatus_String(t *testing.T) {
	tests := []struct {
		s    Status
		want string
	}{
		{StatusDone, "done"},
		{StatusWorking, "working"},
		{StatusGone, "gone"},
		{Status(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Status(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestDefaultLiveness_NilListerReturnsFalse(t *testing.T) {
	live := DefaultLiveness(nil)
	if live("any-thread") {
		t.Errorf("nil lister should report not-alive, got alive")
	}
}

func TestDefaultLiveness_SessionInLiveSet(t *testing.T) {
	// tmuxstatus.SessionName takes the first 8 chars of the id and prefixes
	// "cxa-". For thread "abcdefgh-more" the session is "cxa-abcdefgh".
	live := DefaultLiveness(func() ([]string, error) {
		return []string{"cxa-abcdefgh", "other"}, nil
	})
	if !live("abcdefgh-more-here") {
		t.Errorf("expected thread with live tmux session to be alive")
	}
	if live("zzzzzzzz-more-here") {
		t.Errorf("expected thread with no live tmux session to not be alive")
	}
}

func TestDefaultLiveness_ListerErrorReturnsFalse(t *testing.T) {
	live := DefaultLiveness(func() ([]string, error) {
		return nil, errors.New("tmux: no server running")
	})
	if live("abcdefgh") {
		t.Errorf("lister error should report not-alive, got alive")
	}
}
