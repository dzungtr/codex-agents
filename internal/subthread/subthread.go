// Package subthread is the deep module behind the `cdxa output` command. It
// resolves a thread id to its latest completed turn — the status, the
// monotonic turn number, and the last assistant message of that turn — and
// is the single place that composes codexstate (rollout-format knowledge)
// with tmuxstatus (live-session liveness). Callers (cmd/cdxa, tests) never
// touch either of those packages directly.
//
// The Output contract is frozen by ADR 0003 decision 2: a Result carries
// status/turn/message; the caller maps Status to the exit-code contract
// (0 = a completed turn is available, 2 = still working, 3 = thread unknown
// or gone without collectable output, 1 = operational error). The exit-code
// mapping itself lives in cmd/cdxa, not here — this package returns a typed
// Status the caller switches on, so the mapping is testable without a
// subprocess.
package subthread

import (
	"errors"
	"fmt"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// Status is the outcome of an Output call, mapped by cmd/cdxa to the
// ADR 0003 exit-code contract.
type Status int

const (
	// StatusDone means a completed turn is available and the thread's latest
	// turn is not in progress. Result.Turn and Result.Message carry the
	// latest completed turn. Maps to exit 0.
	StatusDone Status = iota
	// StatusWorking means the thread's latest turn is in progress (the
	// rollout's final turn boundary is a task_started with no matching
	// task_complete). Result.Turn and Result.Message reflect the last
	// completed turn, if any, so repeat polls against unchanged state return
	// identical values (idempotency). Maps to exit 2.
	StatusWorking
	// StatusGone means the thread is unknown (no record in codex's state)
	// or its rollout has no completed turn to collect. Maps to exit 3.
	StatusGone
)

// String returns the lowercase status name used in the JSON output object
// ("done", "working", "gone").
func (s Status) String() string {
	switch s {
	case StatusDone:
		return "done"
	case StatusWorking:
		return "working"
	case StatusGone:
		return "gone"
	default:
		return "unknown"
	}
}

// Result is the value Output returns. The JSON shape cdxa prints is exactly
// {"status","turn","message"}: Status serialises to its String form, Turn is
// the 1-based monotonic completed-turn counter (0 when no turn has completed
// yet), and Message is the last assistant message of that turn.
type Result struct {
	Status  Status
	Turn    int
	Message string
}

// ErrOperational is returned by Output when something went wrong that the
// caller should treat as an operational error (exit 1): the sqlite database
// is unreadable, or a thread's rollout_path resolves to a file that can't
// be read. It carries a descriptive message; cmd/cdxa wraps it in a JSON
// error object on stdout.
var ErrOperational = errors.New("subthread: operational error")

// StateProvider is the codexstate surface Output composes over. Production
// code uses the real codexstate package (DefaultStateProvider); tests inject
// a fake so the subthread module can be exercised without sqlite or rollout
// files on disk.
type StateProvider interface {
	// FindThread resolves a thread id to its codexstate.Thread (carrying
	// RolloutPath). Returns codexstate.ErrThreadNotFound when the thread is
	// unknown or archived.
	FindThread(codexHome, threadID string) (codexstate.Thread, error)
	// ReadTurns returns the completed turns in a thread's rollout file plus
	// whether the file ends mid-turn. An empty Completed slice with
	// InProgress=false means no turn has ever completed (the thread may be
	// gone or freshly launched).
	ReadTurns(rolloutPath string) (codexstate.Turns, error)
}

// DefaultStateProvider is the production StateProvider, backed by the real
// codexstate package. It has no mutable state and is safe for concurrent use.
type DefaultStateProvider struct{}

// FindThread delegates to codexstate.FindThread.
func (DefaultStateProvider) FindThread(codexHome, threadID string) (codexstate.Thread, error) {
	return codexstate.FindThread(codexHome, threadID)
}

// ReadTurns delegates to codexstate.ReadTurns.
func (DefaultStateProvider) ReadTurns(rolloutPath string) (codexstate.Turns, error) {
	return codexstate.ReadTurns(rolloutPath)
}

// LivenessProvider reports whether a thread's tmux session is currently
// alive. This disambiguates the "no completed turn" case: a thread with no
// completed turn that is still alive reads as StatusWorking (just started,
// first turn in flight); one whose session has died reads as StatusGone
// (gone without collectable output). Production code wraps tmuxstatus; tests
// inject a fake.
type LivenessProvider func(threadID string) bool

// Output resolves a thread id to its latest completed turn. The contract it
// implements (ADR 0003 decision 2):
//
//   - thread found, ≥1 completed turn, latest turn ended → StatusDone,
//     Turn/Message set; caller exits 0.
//   - thread found, latest turn in progress → StatusWorking; Turn/Message
//     reflect the last completed turn (0/"" if none) so repeat polls are
//     idempotent; caller exits 2.
//   - thread found, no completed turn, session alive → StatusWorking (first
//     turn in flight); caller exits 2.
//   - thread found, no completed turn, session dead → StatusGone; caller
//     exits 3.
//   - thread not found (ErrThreadNotFound) → StatusGone; caller exits 3.
//   - sqlite unreadable / rollout missing → ErrOperational; caller exits 1.
//
// codexHome is $CODEX_HOME (or its ~/.codex default), passed in by the caller
// so this package has no filesystem-root knowledge of its own.
func Output(state StateProvider, live LivenessProvider, codexHome, threadID string) (Result, error) {
	thread, err := state.FindThread(codexHome, threadID)
	if err != nil {
		if errors.Is(err, codexstate.ErrThreadNotFound) {
			return Result{Status: StatusGone}, nil
		}
		return Result{}, fmt.Errorf("%w: find thread %q: %v", ErrOperational, threadID, err)
	}

	turns, err := state.ReadTurns(thread.RolloutPath)
	if err != nil {
		return Result{}, fmt.Errorf("%w: read turns for %q: %v", ErrOperational, threadID, err)
	}

	if len(turns.Completed) == 0 {
		// No completed turn yet: working if the session is alive (first
		// turn in flight), gone if it died before producing any output.
		// live may be nil (tests that don't care about liveness); treat
		// nil as "not alive" so the gone path is exercisable without a
		// liveness provider.
		if live != nil && live(threadID) {
			return Result{Status: StatusWorking}, nil
		}
		return Result{Status: StatusGone}, nil
	}

	latest := turns.Completed[len(turns.Completed)-1]
	if turns.InProgress {
		// A newer turn is in flight; the last completed turn's output is
		// still the collectable value, so Turn/Message are set for
		// idempotency, but the status is "working" so the parent keeps
		// polling for the next turn.
		return Result{Status: StatusWorking, Turn: latest.Number, Message: latest.Message}, nil
	}
	return Result{Status: StatusDone, Turn: latest.Number, Message: latest.Message}, nil
}

// DefaultLiveness is the production LivenessProvider: a thread is alive iff
// its tmux session name appears in the live session set returned by lister.
// A nil lister (or one that errors) means "no sessions alive", so a thread
// with no completed turn reads as StatusGone — the same degraded posture as
// the cockpit's "tmux not installed / no server running" case.
func DefaultLiveness(lister tmuxstatus.Lister) LivenessProvider {
	return func(threadID string) bool {
		if lister == nil {
			return false
		}
		names, err := lister()
		if err != nil || len(names) == 0 {
			return false
		}
		live := tmuxstatus.NewLiveSet(names)
		_, ok := live[tmuxstatus.SessionName(threadID)]
		return ok
	}
}
