package subthread

import (
	"errors"
	"testing"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// recordingSessionStopper is a SessionStopper test double that captures the
// thread id it was called with and returns the canned err. Used by every
// Shutdown branch test below to assert Stop was called (or not) and to
// script kill failures without a real tmux server.
type recordingSessionStopper struct {
	calls []string
	err   error
}

func (r *recordingSessionStopper) Stop(threadID string) error {
	r.calls = append(r.calls, threadID)
	return r.err
}

// recordingCodexArchiver is a CodexArchiver test double that captures the
// thread id it was asked to archive and returns the canned err. Mirrors
// recordingSessionStopper so the kill/archive ordering and the failure
// branches are exercisable without a real codex binary.
type recordingCodexArchiver struct {
	calls []string
	err   error
}

func (r *recordingCodexArchiver) Archive(threadID string) error {
	r.calls = append(r.calls, threadID)
	return r.err
}

// shutdownFixedLister returns a Lister that reports the supplied session
// names as the live set, used to exercise the production SessionStopper's
// "skip kill when not in live set" path without a real tmux server.
func shutdownFixedLister(names ...string) tmuxstatus.Lister {
	return func() ([]string, error) { return names, nil }
}

func TestShutdownStatusString(t *testing.T) {
	tests := []struct {
		s    ShutdownStatus
		want string
	}{
		{ShutdownArchived, "archived"},
		{ShutdownWorking, "working"},
		{ShutdownGone, "gone"},
		{ShutdownStatus(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("ShutdownStatus(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestShutdown_CompletedAndSessionLive_StopsAndArchives(t *testing.T) {
	// The happy path: thread has a completed turn, no in-progress turn,
	// tmux session alive. Shutdown stops the session then archives.
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "ok"}}},
		},
	}
	live := func(string) bool { return true }
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{}

	got, err := Shutdown(state, live, sessions, archiver, "/codex", "", "t1")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got.Status != ShutdownArchived {
		t.Errorf("Status = %s, want archived", got.Status)
	}
	if got.ThreadID != "t1" {
		t.Errorf("ThreadID = %q, want t1", got.ThreadID)
	}
	if len(sessions.calls) != 1 || sessions.calls[0] != "t1" {
		t.Errorf("sessions.Stop calls = %v, want [t1]", sessions.calls)
	}
	if len(archiver.calls) != 1 || archiver.calls[0] != "t1" {
		t.Errorf("archiver.Archive calls = %v, want [t1]", archiver.calls)
	}
}

func TestShutdown_CompletedAndSessionAlreadyGone_StillArchives(t *testing.T) {
	// Issue #103 clarification: at least one completed turn + no turn in
	// progress is eligible even when the tmux session is already absent.
	// Shutdown still calls the SessionStopper (which treats absent as
	// clean), then archives the codex record. This is the retry path
	// after a partial-failure earlier teardown.
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "ok"}}},
		},
	}
	live := func(string) bool { return false } // tmux already gone
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{}

	got, err := Shutdown(state, live, sessions, archiver, "/codex", "", "t1")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got.Status != ShutdownArchived {
		t.Errorf("Status = %s, want archived", got.Status)
	}
	if len(sessions.calls) != 1 {
		t.Errorf("sessions.Stop calls = %d, want 1 (Stop is still called; the lister-routed stopper treats absent as clean)", len(sessions.calls))
	}
	if len(archiver.calls) != 1 {
		t.Errorf("archiver.Archive calls = %d, want 1", len(archiver.calls))
	}
}

func TestShutdown_ListerSeesSessionMissing_SkipKillButArchive(t *testing.T) {
	// The production SessionStopper must skip kill-session when the
	// lister reports the session absent (ADR 0007 decision 5: already
	// missing is clean). Exercise that branch via the real default
	// SessionStopper wired with a fixed Lister that reports the
	// session not present.
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "ok"}}},
		},
	}
	live := func(string) bool { return false }
	// Lister that reports a different session, not ours — production
	// SessionStopper will see ours as absent and return nil without
	// touching the runner.
	stopper := &listerRunnerSessionStopper{
		lister: shutdownFixedLister("cxa-someoneelse"),
		runner: &recordingRunner{},
	}
	archiver := &recordingCodexArchiver{}

	got, err := Shutdown(state, live, stopper, archiver, "/codex", "", "t1")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got.Status != ShutdownArchived {
		t.Errorf("Status = %s, want archived", got.Status)
	}
	if len(archiver.calls) != 1 {
		t.Errorf("archiver.Archive calls = %d, want 1", len(archiver.calls))
	}
}

// recordingRunner captures tmux kill-session calls without shelling out.
type recordingRunner struct {
	calls [][]string
	err   error
}

func (r *recordingRunner) Run(args []string) error {
	r.calls = append(r.calls, append([]string(nil), args...))
	return r.err
}

func TestShutdown_TurnInProgress_ReturnsWorkingNoMutation(t *testing.T) {
	// Latest turn is in flight: shutdown is refused. Neither the tmux
	// session nor the codex archive is touched — shutting down an active
	// turn would be data loss.
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {
				Completed:  []codexstate.Turn{{Number: 1, Message: "first"}},
				InProgress: true,
			},
		},
	}
	live := func(string) bool { return true }
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{}

	got, err := Shutdown(state, live, sessions, archiver, "/codex", "", "t1")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got.Status != ShutdownWorking {
		t.Errorf("Status = %s, want working (turn in progress)", got.Status)
	}
	if len(sessions.calls) != 0 {
		t.Errorf("sessions.Stop calls = %d, want 0 (no mutation on working)", len(sessions.calls))
	}
	if len(archiver.calls) != 0 {
		t.Errorf("archiver.Archive calls = %d, want 0 (no mutation on working)", len(archiver.calls))
	}
}

func TestShutdown_NoTurnLiveSession_ReturnsWorkingNoMutation(t *testing.T) {
	// Issue #103 clarification: no completed turn + live session = working.
	// The thread just registered and its first turn is still in flight.
	// This matches cdxa output's distinction (see subthread.go).
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: nil, InProgress: true},
		},
	}
	live := func(string) bool { return true }
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{}

	got, err := Shutdown(state, live, sessions, archiver, "/codex", "", "t1")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got.Status != ShutdownWorking {
		t.Errorf("Status = %s, want working (no turn, session live)", got.Status)
	}
	if len(sessions.calls) != 0 {
		t.Errorf("sessions.Stop calls = %d, want 0", len(sessions.calls))
	}
	if len(archiver.calls) != 0 {
		t.Errorf("archiver.Archive calls = %d, want 0", len(archiver.calls))
	}
}

func TestShutdown_NoTurnDeadSession_ReturnsGoneNoMutation(t *testing.T) {
	// Issue #103 clarification: no completed turn + no live session = gone.
	// Matches cdxa output's distinction (see subthread.go).
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: nil, InProgress: true},
		},
	}
	live := func(string) bool { return false }
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{}

	got, err := Shutdown(state, live, sessions, archiver, "/codex", "", "t1")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got.Status != ShutdownGone {
		t.Errorf("Status = %s, want gone (no turn, session dead)", got.Status)
	}
	if len(sessions.calls) != 0 {
		t.Errorf("sessions.Stop calls = %d, want 0", len(sessions.calls))
	}
	if len(archiver.calls) != 0 {
		t.Errorf("archiver.Archive calls = %d, want 0", len(archiver.calls))
	}
}

func TestShutdown_UnknownThread_ReturnsGoneNoMutation(t *testing.T) {
	// codex doesn't know the id — neither sqlite nor jsonl fallback has
	// it. The parent should stop its cleanup loop; no mutation attempted.
	state := fakeState{} // no threads
	live := func(string) bool { return true }
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{}

	got, err := Shutdown(state, live, sessions, archiver, "/codex", "", "missing")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got.Status != ShutdownGone {
		t.Errorf("Status = %s, want gone (unknown thread)", got.Status)
	}
	if len(sessions.calls) != 0 {
		t.Errorf("sessions.Stop calls = %d, want 0", len(sessions.calls))
	}
	if len(archiver.calls) != 0 {
		t.Errorf("archiver.Archive calls = %d, want 0", len(archiver.calls))
	}
}

func TestShutdown_HiddenThread_ReturnsGoneNoMutation(t *testing.T) {
	// The cockpit already archived the thread via its Hidden bookkeeping
	// (issue #60 equivalent). codex still resolves it through the jsonl
	// fallback, but shutdown must treat it as gone rather than calling
	// codex archive again on an already-cockpit-archived thread.
	state := fakeState{
		threads: map[string]codexstate.Thread{"t-hidden": {ID: "t-hidden", RolloutPath: "/r/h.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/h.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "old result"}}},
		},
	}
	live := func(string) bool { return false }
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{}
	statePath := writeHiddenState(t, "t-hidden")

	got, err := Shutdown(state, live, sessions, archiver, "/codex", statePath, "t-hidden")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got.Status != ShutdownGone {
		t.Errorf("Status = %s, want gone (hidden thread)", got.Status)
	}
	if len(sessions.calls) != 0 {
		t.Errorf("sessions.Stop calls = %d, want 0 (no mutation on hidden)", len(sessions.calls))
	}
	if len(archiver.calls) != 0 {
		t.Errorf("archiver.Archive calls = %d, want 0 (no mutation on hidden)", len(archiver.calls))
	}
}

func TestShutdown_TmuxFailure_PreventsArchiveReturnsOperational(t *testing.T) {
	// ADR 0007 decision 3: a real tmux failure must prevent the codex
	// archive so the record still resolves to a live process and the
	// parent can retry. The session Stopper's error is wrapped as
	// ErrOperational (exit 1); the archiver must NOT be called.
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "ok"}}},
		},
	}
	live := func(string) bool { return true }
	sessions := &recordingSessionStopper{err: errors.New("tmux: server unreachable")}
	archiver := &recordingCodexArchiver{}

	_, err := Shutdown(state, live, sessions, archiver, "/codex", "", "t1")
	if err == nil {
		t.Fatalf("expected error from tmux failure, got nil")
	}
	if !errors.Is(err, ErrOperational) {
		t.Errorf("error = %v, want ErrOperational", err)
	}
	if len(archiver.calls) != 0 {
		t.Errorf("archiver.Archive calls = %d, want 0 (archive skipped after tmux failure)", len(archiver.calls))
	}
}

func TestShutdown_CodexArchiveFailure_AfterTmuxStopReturnsOperational(t *testing.T) {
	// ADR 0007 decision 3: a codex archive failure surfaces AFTER the tmux
	// session has been stopped (so the next call can finish the record
	// transition without re-killing an already-stopped session). Both the
	// Stop call and the Archive call must have happened, and the error
	// must be ErrOperational.
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "ok"}}},
		},
	}
	live := func(string) bool { return true }
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{err: errors.New("codex archive failed: no such thread")}

	_, err := Shutdown(state, live, sessions, archiver, "/codex", "", "t1")
	if err == nil {
		t.Fatalf("expected error from codex archive failure, got nil")
	}
	if !errors.Is(err, ErrOperational) {
		t.Errorf("error = %v, want ErrOperational", err)
	}
	if len(sessions.calls) != 1 {
		t.Errorf("sessions.Stop calls = %d, want 1 (tmux kill must precede codex archive)", len(sessions.calls))
	}
	if len(archiver.calls) != 1 {
		t.Errorf("archiver.Archive calls = %d, want 1", len(archiver.calls))
	}
}

func TestShutdown_RetryAfterCodexFailure_FinishesArchive(t *testing.T) {
	// The retry scenario from ADR 0007 decision 5: tmux already gone after
	// the first attempt failed at the archive step. A second call sees
	// the session gone (live=false) and a completed rollout, so the
	// production SessionStopper short-circuits and the archiver is called
	// again. This proves the retry path is real, not just a best-effort
	// promise.
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "ok"}}},
		},
	}
	live := func(string) bool { return false } // tmux gone from earlier teardown
	stopper := &listerRunnerSessionStopper{
		lister: shutdownFixedLister(), // empty live set
		runner: &recordingRunner{},
	}
	// First call: archiver fails.
	archiver := &recordingCodexArchiver{err: errors.New("transient")}
	if _, err := Shutdown(state, live, stopper, archiver, "/codex", "", "t1"); err == nil {
		t.Fatalf("first call: expected archive error, got nil")
	}
	// Second call: archiver succeeds — the same in-process state should
	// let the retry finish without re-killing (already empty live set).
	archiver.err = nil
	got, err := Shutdown(state, live, stopper, archiver, "/codex", "", "t1")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got.Status != ShutdownArchived {
		t.Errorf("retry Status = %s, want archived", got.Status)
	}
	if len(archiver.calls) != 2 {
		t.Errorf("archiver.Archive calls = %d, want 2 (failed then succeeded)", len(archiver.calls))
	}
}

func TestShutdown_SqliteUnreadableReturnsErrOperational(t *testing.T) {
	// A non-ErrThreadNotFound error from FindThread is an operational
	// failure — the parent should treat it as exit 1, not exit 3.
	state := fakeState{findErr: errors.New("sqlite: disk I/O error")}
	live := func(string) bool { return false }
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{}

	_, err := Shutdown(state, live, sessions, archiver, "/codex", "", "t1")
	if err == nil {
		t.Fatalf("expected error for unreadable sqlite, got nil")
	}
	if !errors.Is(err, ErrOperational) {
		t.Errorf("error = %v, want ErrOperational", err)
	}
}

func TestShutdown_RolloutMissingReturnsErrOperational(t *testing.T) {
	// Rollout file unreadable after FindThread succeeded is also an
	// operational failure (exit 1), not gone (exit 3): the parent might
	// be holding the rollout elsewhere and should retry.
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/missing.jsonl"}},
		readErr: errors.New("open /r/missing.jsonl: no such file"),
	}
	live := func(string) bool { return false }
	sessions := &recordingSessionStopper{}
	archiver := &recordingCodexArchiver{}

	_, err := Shutdown(state, live, sessions, archiver, "/codex", "", "t1")
	if err == nil {
		t.Fatalf("expected error for missing rollout, got nil")
	}
	if !errors.Is(err, ErrOperational) {
		t.Errorf("error = %v, want ErrOperational", err)
	}
}

func TestDefaultSessionStopper_ListerSeesSession_PassesThroughToRunner(t *testing.T) {
	// The lister reports the session present; the default SessionStopper
	// must invoke the runner. Use a recording runner to capture the
	// exact kill-session args.
	runner := &recordingRunner{}
	stopper := &listerRunnerSessionStopper{
		lister: shutdownFixedLister(tmuxstatus.SessionName("t1")),
		runner: runner,
	}
	if err := stopper.Stop("t1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner.Run calls = %d, want 1", len(runner.calls))
	}
	want := tmuxstatus.KillSessionArgs(tmuxstatus.SessionName("t1"))
	if got := runner.calls[0]; len(got) != len(want) {
		t.Errorf("kill args = %v, want %v", got, want)
	} else {
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("kill args[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	}
}

func TestDefaultSessionStopper_ListerErrors_FallsThroughToRunner(t *testing.T) {
	// Lister failure (no PATH, broken tmux) must fall through to the
	// runner — which itself surfaces the failure as ErrOperational via
	// its returned error. Recording runner returns nil here, so the
	// stopper should also return nil.
	runner := &recordingRunner{}
	stopper := &listerRunnerSessionStopper{
		lister: func() ([]string, error) { return nil, errors.New("lister boom") },
		runner: runner,
	}
	if err := stopper.Stop("t1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Errorf("runner.Run calls = %d, want 1 (fall through after lister error)", len(runner.calls))
	}
}

func TestDefaultSessionStopper_RunnerFailureSurfacesError(t *testing.T) {
	// When the runner itself fails (real tmux error, not just absent),
	// the stopper must surface that error so Shutdown can return
	// ErrOperational. This is the ADR 0007 decision 3 hook.
	runner := &recordingRunner{err: errors.New("tmux: server unreachable")}
	stopper := &listerRunnerSessionStopper{
		lister: shutdownFixedLister(tmuxstatus.SessionName("t1")),
		runner: runner,
	}
	if err := stopper.Stop("t1"); err == nil {
		t.Fatalf("expected runner error to surface, got nil")
	}
}
