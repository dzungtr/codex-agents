package subthread

import (
	"errors"
	"testing"
	"time"

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

	got, err := Output(state, live, "/codex", "t1", 0)
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

	got, err := Output(state, live, "/codex", "t1", 0)
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

	got, err := Output(state, live, "/codex", "t1", 0)
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

	got, err := Output(state, live, "/codex", "t1", 0)
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

	got, err := Output(state, live, "/codex", "no-such-thread", 0)
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

	_, err := Output(state, live, "/codex", "t1", 0)
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

	_, err := Output(state, live, "/codex", "t1", 0)
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

	first, err := Output(state, live, "/codex", "t1", 0)
	if err != nil {
		t.Fatalf("first Output: %v", err)
	}
	second, err := Output(state, live, "/codex", "t1", 0)
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

	got, err := Output(state, live, "/codex", "t1", 0)
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

// waitTestState is a StateProvider whose ReadTurns result changes after a
// fixed number of calls. It scripts the "turn completes mid-wait" fixture
// (issue #32): the first N ReadTurns calls return an in-progress turn
// (StatusWorking), and every call after that returns a completed turn
// (StatusDone). FindThread is constant. This is the fixture the wait-loop
// tests drive — a real rollout file would require the test to swap it
// mid-flight, but the StateProvider seam already abstracts the read, so the
// fixture swaps the canned result instead.
type waitTestState struct {
	thread  codexstate.Thread
	calls   int
	working codexstate.Turns
	done    codexstate.Turns
	// flipAfter is the number of ReadTurns calls that return `working`
	// before subsequent calls return `done`.
	flipAfter int
}

func (w *waitTestState) FindThread(_ string, _ string) (codexstate.Thread, error) {
	return w.thread, nil
}

func (w *waitTestState) ReadTurns(_ string) (codexstate.Turns, error) {
	w.calls++
	if w.calls > w.flipAfter {
		return w.done, nil
	}
	return w.working, nil
}

// withFastPoll shrinks the wait loop's cadence and stubs out real sleeping so
// the wait tests run in microseconds, not hundreds of milliseconds. It
// restores the package vars on cleanup.
func withFastPoll(t *testing.T) {
	t.Helper()
	origSleep, origInterval := sleep, pollInterval
	sleep = func(time.Duration) {}
	pollInterval = time.Millisecond
	t.Cleanup(func() { sleep, pollInterval = origSleep, origInterval })
}

func TestOutput_WaitZeroIdenticalToNoWait(t *testing.T) {
	// --wait 0 must behave exactly like the omitted flag (#28). The same
	// in-progress fixture returns StatusWorking immediately with no polling.
	state := &waitTestState{
		thread:    codexstate.Thread{ID: "t1", RolloutPath: "/r/t1.jsonl"},
		working:   codexstate.Turns{Completed: []codexstate.Turn{{Number: 1, Message: "wip"}}, InProgress: true},
		flipAfter: 100, // never flips within a single zero-wait call
	}
	live := func(string) bool { return true }

	got, err := Output(state, live, "/codex", "t1", 0)
	if err != nil {
		t.Fatalf("Output wait=0: %v", err)
	}
	if got.Status != StatusWorking {
		t.Errorf("Status = %s, want working", got.Status)
	}
	if state.calls != 1 {
		t.Errorf("ReadTurns called %d times, want 1 (no polling for wait=0)", state.calls)
	}
}

func TestOutput_WaitNegativeIdenticalToNoWait(t *testing.T) {
	state := &waitTestState{
		thread:    codexstate.Thread{ID: "t1", RolloutPath: "/r/t1.jsonl"},
		working:   codexstate.Turns{Completed: []codexstate.Turn{{Number: 1, Message: "wip"}}, InProgress: true},
		flipAfter: 100,
	}
	live := func(string) bool { return true }

	got, err := Output(state, live, "/codex", "t1", -1*time.Second)
	if err != nil {
		t.Fatalf("Output wait<0: %v", err)
	}
	if got.Status != StatusWorking {
		t.Errorf("Status = %s, want working", got.Status)
	}
	if state.calls != 1 {
		t.Errorf("ReadTurns called %d times, want 1 (no polling for wait<0)", state.calls)
	}
}

func TestOutput_WaitImmediateDone(t *testing.T) {
	// The very first read already sees a completed turn: Output returns
	// StatusDone without entering the poll loop, even with wait > 0.
	state := &waitTestState{
		thread:    codexstate.Thread{ID: "t1", RolloutPath: "/r/t1.jsonl"},
		done:      codexstate.Turns{Completed: []codexstate.Turn{{Number: 1, Message: "done result"}}, InProgress: false},
		flipAfter: 0,
	}
	live := func(string) bool { return false }

	got, err := Output(state, live, "/codex", "t1", 5*time.Second)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got.Status != StatusDone {
		t.Errorf("Status = %s, want done", got.Status)
	}
	if got.Turn != 1 || got.Message != "done result" {
		t.Errorf("Result = %+v, want turn 1 / done result", got)
	}
	if state.calls != 1 {
		t.Errorf("ReadTurns called %d times, want 1 (immediate done, no polling)", state.calls)
	}
}

func TestOutput_WaitTurnCompletesMidWait(t *testing.T) {
	// The fixture returns "working" for the first two reads and "done" on
	// the third. With wait > 0 the loop polls until the done result appears,
	// then returns promptly — it must NOT wait out the full N seconds.
	withFastPoll(t)
	state := &waitTestState{
		thread: codexstate.Thread{ID: "t1", RolloutPath: "/r/t1.jsonl"},
		working: codexstate.Turns{
			Completed:  []codexstate.Turn{{Number: 1, Message: "wip"}},
			InProgress: true,
		},
		done: codexstate.Turns{
			Completed: []codexstate.Turn{
				{Number: 1, Message: "wip"},
				{Number: 2, Message: "completed result"},
			},
			InProgress: false,
		},
		flipAfter: 2,
	}
	live := func(string) bool { return true }

	got, err := Output(state, live, "/codex", "t1", 10*time.Second)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got.Status != StatusDone {
		t.Errorf("Status = %s, want done (turn completed mid-wait)", got.Status)
	}
	if got.Turn != 2 || got.Message != "completed result" {
		t.Errorf("Result = %+v, want turn 2 / completed result", got)
	}
	if state.calls != 3 {
		t.Errorf("ReadTurns called %d times, want 3 (initial + 2 working + flip)", state.calls)
	}
}

func TestOutput_WaitTimeoutStillWorking(t *testing.T) {
	// The fixture never flips: every read is StatusWorking. The loop polls
	// until the deadline elapses, then returns the last StatusWorking result
	// (caller exits 2). It must not return done, gone, or an error.
	withFastPoll(t)
	state := &waitTestState{
		thread: codexstate.Thread{ID: "t1", RolloutPath: "/r/t1.jsonl"},
		working: codexstate.Turns{
			Completed:  []codexstate.Turn{{Number: 1, Message: "still wip"}},
			InProgress: true,
		},
		flipAfter: 1 << 30, // effectively never flips
	}
	live := func(string) bool { return true }

	start := time.Now()
	got, err := Output(state, live, "/codex", "t1", 50*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got.Status != StatusWorking {
		t.Errorf("Status = %s, want working (timed out)", got.Status)
	}
	if got.Turn != 1 || got.Message != "still wip" {
		t.Errorf("Result = %+v, want last working result (turn 1)", got)
	}
	// Must respect the timeout precisely: not return early, not overshoot by
	// a full poll interval. With a 1ms poll cadence allow generous slack.
	if elapsed < 40*time.Millisecond {
		t.Errorf("returned after %v, want ≈50ms (timeout honoured)", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("returned after %v, overshoots the 50ms timeout", elapsed)
	}
}

func TestOutput_WaitThreadGoneMidWait(t *testing.T) {
	// A non-working terminal status (gone) appearing mid-wait returns
	// immediately, same as done — the loop only keeps polling while the
	// status is StatusWorking.
	withFastPoll(t)
	flipState := &fakeFlipState{
		thread:    codexstate.Thread{ID: "t1", RolloutPath: "/r/t1.jsonl"},
		flipAfter: 2,
		working: codexstate.Turns{
			Completed:  []codexstate.Turn{{Number: 1, Message: "wip"}},
			InProgress: true,
		},
		gone: true,
	}
	live := func(string) bool { return false } // dead session so gone path is reachable

	got, err := Output(flipState, live, "/codex", "t1", 10*time.Second)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got.Status != StatusGone {
		t.Errorf("Status = %s, want gone (thread went away mid-wait)", got.Status)
	}
	if flipState.calls != 3 {
		t.Errorf("ReadTurns called %d times, want 3", flipState.calls)
	}
}

// fakeFlipState scripts a working→gone transition: ReadTurns returns the
// `working` fixture for the first flipAfter calls, then returns
// ErrThreadNotFound (so FindThread-style gone semantics would apply). Because
// Output's gone path is reached when ReadTurns yields no completed turn and
// the session is dead, fakeFlipState models gone by returning an empty
// completed slice with InProgress=false and a dead session — exercised via
// the `gone` flag switching the live provider's answer. To keep the fixture
// self-contained here we instead make ReadTurns return a completed-empty,
// not-in-progress result, which with a dead live() yields StatusGone.
type fakeFlipState struct {
	thread    codexstate.Thread
	calls     int
	flipAfter int
	working   codexstate.Turns
	gone      bool
}

func (f *fakeFlipState) FindThread(_ string, _ string) (codexstate.Thread, error) {
	if f.gone && f.calls > f.flipAfter {
		return codexstate.Thread{}, codexstate.ErrThreadNotFound
	}
	return f.thread, nil
}

func (f *fakeFlipState) ReadTurns(_ string) (codexstate.Turns, error) {
	f.calls++
	if f.calls > f.flipAfter {
		// No completed turns, not in progress, and FindThread will report
		// gone — together that yields StatusGone.
		return codexstate.Turns{Completed: nil, InProgress: false}, nil
	}
	return f.working, nil
}
