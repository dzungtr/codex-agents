// send.go implements `cdxa send <thread-id> "msg"` (ADR 0003 decision 2,
// issue #31): deliver a one-line follow-up into a living subthread's codex
// composer via the cockpit's QuickReply mechanism, then return the turn
// number the follow-up started — derived from the rollout file's completed
// turn count at send time. The returned turn is what the parent's next
// `cdxa output` poll targets (ADR 0003 decision 5: "send-then-poll
// unambiguous — the follow-up's output is the next turn").
//
// Send is the headless counterpart to the cockpit's QuickReply action (PRD
// #1). It wraps QuickReply rather than reimplementing it: the delivery
// mechanism (two tmux send-keys calls, literal text then a separate Enter
// keypress, followed by clearing the recorded last-turn-event) is owned by
// internal/codexlaunch and is unchanged here. What Send adds over QuickReply
// alone is (a) the gone-check that QuickReply deliberately omits (a dead
// session has nothing to send keys to) and (b) the started-turn derivation
// that gives the parent a concrete turn number to poll for.
//
// This is a sibling function to Output (#28) and Spawn (#29) in the same
// package. The three share no state: each resolves the thread fresh from
// codex's sqlite on every call (ADR 0003 decision 4).
package subthread

import (
	"errors"

	"fmt"
	"github.com/dzungtr/codex-agents/internal/codexstate"
)

// ErrGone is returned by Send when the thread is unknown or its tmux
// session is dead — either way there is nothing to deliver to. Maps to exit
// code 3 (ADR 0003 decision 2). Mirrors the Output command's StatusGone
// outcome, but as a typed error because Send's only return values are a
// turn number and an error (there is no Result struct to carry a Status).
var ErrGone = errors.New("subthread: thread unknown or gone")

// Replier delivers a one-line message into a thread's codex composer. It is
// the narrow surface Send needs over the cockpit's QuickReply mechanism;
// production code wires *codexlaunch.Launcher (whose QuickReply method
// satisfies this interface), tests inject a fake so the delivery + turn
// derivation flow is exercisable without a real tmux server.
type Replier interface {
	QuickReply(threadID, msg string) error
}

// Send delivers msg into threadID's living subthread and returns the turn
// number the follow-up started — the 1-based index of the next turn to
// complete after delivery (ADR 0003 decision 5).
//
// The started turn is derived at send time from the rollout file's
// completed-turn count: if the rollout has N completed turns, the follow-up
// starts turn N+1. When a turn is already in progress (the rollout ends
// with a task_started and no matching task_complete), the in-flight turn is
// the one the follow-up lands in, so the started turn is the in-flight
// turn's number (len(Completed)+1) — the same value, derived the same way.
// This is the contract the parent relies on: send returns turn N, and the
// next `cdxa output` that observes a new completed turn reports turn N.
//
// Failure modes (ADR 0003 decision 2):
//   - thread not found (ErrThreadNotFound) → ErrGone; caller exits 3.
//   - thread found but tmux session dead → ErrGone; caller exits 3. This is
//     the gone-check QuickReply itself omits (callers were expected to
//     exclude closed threads first); Send performs it because a headless
//     parent has no UI to filter against and the send-keys call against a
//     dead session would surface as an opaque operational error.
//   - sqlite unreadable / rollout missing → ErrOperational; caller exits 1.
//   - QuickReply delivery failure (tmux refused the send-keys) →
//     ErrOperational; caller exits 1.
//
// codexHome is $CODEX_HOME (or its ~/.codex default), passed in by the
// caller so this package has no filesystem-root knowledge of its own.
func Send(state StateProvider, live LivenessProvider, replier Replier, codexHome, threadID, msg string) (int, error) {
	thread, err := state.FindThread(codexHome, threadID)
	if err != nil {
		if errors.Is(err, codexstate.ErrThreadNotFound) {
			return 0, ErrGone
		}
		return 0, fmt.Errorf("%w: find thread %q: %v", ErrOperational, threadID, err)
	}

	turns, err := state.ReadTurns(thread.RolloutPath)
	if err != nil {
		return 0, fmt.Errorf("%w: read turns for %q: %v", ErrOperational, threadID, err)
	}

	// A dead session has nothing to send keys to. QuickReply itself omits
	// this check (the cockpit excludes closed threads before offering the
	// reply row); Send performs it because a headless parent has no such
	// pre-filter, and an opaque send-keys failure against a dead session
	// would read as exit 1 when exit 3 is the right answer. A nil live
	// provider is treated as "not alive", matching Output's posture so the
	// gone path is exercisable in tests without a liveness provider.
	if live == nil || !live(threadID) {
		return 0, ErrGone
	}

	// The started turn is the next turn to complete after delivery: one
	// beyond the last completed turn. When a turn is already in flight,
	// that in-flight turn IS the one the follow-up lands in, so the count
	// is the same (len(Completed)+1) — the derivation does not branch on
	// InProgress. This is the value the parent's next `cdxa output` poll
	// will observe as the new completed turn.
	startedTurn := len(turns.Completed) + 1

	if err := replier.QuickReply(threadID, msg); err != nil {
		return 0, fmt.Errorf("%w: deliver reply to %q: %v", ErrOperational, threadID, err)
	}
	return startedTurn, nil
}
