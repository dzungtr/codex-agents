// turns.go extends the jsonl parsing with per-turn reading: given a thread
// id, locate its rollout file, count completed turns, and return the last
// assistant message of the latest completed turn. This is the "rollout is
// the sole source of truth" layer described in ADR 0003 decision 3 — all
// rollout-format knowledge stays in this package.

package codexstate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// turnEventMsgPayload extends eventMsgPayload with the fields carried by
// turn-boundary event_msg records. The existing eventMsgPayload (in jsonl.go)
// only understands user_message; turn reading needs task_started and
// task_complete, whose payloads carry a turn_id and (for task_complete) a
// last_agent_message.
//
// event_msg records codex writes to the rollout file, by payload.Type:
//
//	task_started    — turn begin marker; carries turn_id
//	task_complete   — turn end marker; carries turn_id and last_agent_message
//	                  (the assistant message codex itself collected for that
//	                  turn; subthread output uses it directly rather than
//	                  re-deriving from agent_message records)
//	agent_message   — an assistant conversational reply within a turn
//	user_message    — a user message within a turn
type turnEventMsgPayload struct {
	Type             string `json:"type"`
	Message          string `json:"message"`
	TurnID           string `json:"turn_id"`
	LastAgentMessage string `json:"last_agent_message"`
}

// Turn is one completed unit of assistant work within a thread, bounded by
// codex's own task_started/task_complete markers in the rollout file (see
// CONTEXT.md: "Turn"). Only completed turns appear in Turns.Completed.
type Turn struct {
	// Number is the 1-based monotonic index of this completed turn within
	// the rollout file, derived from turn-marker order on every call. There
	// is no cursor token: a poll returning an already-seen Number means
	// nothing new (ADR 0003 decision 5).
	Number int
	// TurnID is codex's own turn_id from the task_complete marker, when
	// present. Empty when the marker carried none (older rollout format).
	TurnID string
	// Message is the last assistant message of this turn, taken from the
	// task_complete record's last_agent_message field. Empty when codex
	// collected no message for the turn (e.g. interrupted). This is the
	// field cdxa output prints as "message".
	Message string
}

// Turns is the result of scanning a rollout file for turn boundaries. The
// rollout is the sole source of truth for completion detection (ADR 0003
// decision 3): Completed holds every turn that has both a task_started and a
// matching task_complete marker; InProgress reports whether the file ends
// with a task_started that has no matching task_complete (the thread is mid-
// turn when the file was last flushed).
type Turns struct {
	// Completed lists the completed turns in the order they appear. Empty
	// when no turn has completed yet.
	Completed []Turn
	// InProgress is true when the rollout's final turn boundary is a
	// task_started with no following task_complete — i.e. a turn is in
	// flight. This is the signal that distinguishes cdxa's exit 2 (still
	// working) from exit 0 (done): a thread whose latest turn has ended
	// reads as done even if its tmux session is still alive (it's waiting
	// for input, which is a completed-turn-available state for the parent).
	InProgress bool
}

// ErrThreadNotFound is returned by FindThread when no thread with the given
// id exists in codex's state (neither sqlite nor the jsonl fallback). The
// cdxa output contract maps this to exit code 3 ("thread unknown or gone").
var ErrThreadNotFound = errors.New("codexstate: thread not found")

// FindThread resolves a thread id to its Thread record. It first tries the
// sqlite threads table (which carries the authoritative rollout_path); if
// that fails (no db, schema drift) it falls back to the jsonl session scan,
// which recovers the thread from its rollout file's session_meta record.
// Archived threads return ErrThreadNotFound: they're gone from the cockpit's
// view, and cdxa's "unknown/gone" exit code (3) is the right answer for
// them too.
func FindThread(codexHome, threadID string) (Thread, error) {
	dbPath, findErr := newestStateDB(codexHome)
	if findErr == nil {
		threads, qErr := queryThreads(dbPath)
		if qErr == nil {
			for _, th := range threads {
				if th.ID == threadID {
					return th, nil
				}
			}
			// sqlite is usable but the id isn't there. Fall through to the
			// jsonl scan: a thread registered after the sqlite snapshot was
			// taken (or one codex wrote only as a rollout file) may still be
			// recoverable. If the jsonl scan also misses it, ErrThreadNotFound
			// is returned below.
		}
	}

	threads, err := scanSessionsJSONL(filepath.Join(codexHome, sessionsDirName))
	if err != nil {
		return Thread{}, fmt.Errorf("codexstate: find thread %q: %w", threadID, err)
	}
	for _, th := range threads {
		if th.ID == threadID {
			return th, nil
		}
	}
	return Thread{}, ErrThreadNotFound
}

// ReadTurns parses a rollout jsonl file and returns the completed turns it
// contains, plus whether the file ends mid-turn. A turn is completed when
// codex has written both a task_started and a matching task_complete marker
// for it. Malformed jsonl lines are skipped rather than fatal — the same
// best-effort posture as the rest of this package (ADR 0001).
func ReadTurns(rolloutPath string) (Turns, error) {
	f, err := os.Open(rolloutPath)
	if err != nil {
		return Turns{}, fmt.Errorf("codexstate: open rollout %s: %w", rolloutPath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		completed  []Turn
		nextNumber int
		inProgress bool
	)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "event_msg" {
			continue
		}
		var p turnEventMsgPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			continue
		}
		switch p.Type {
		case "task_started":
			// A new turn has begun. If a previous task_started had no
			// matching task_complete, it's superseded (codex doesn't nest
			// turns, but a crashed/interrupted turn may lack a
			// task_complete); the latest task_started is the one in flight.
			inProgress = true
		case "task_complete":
			nextNumber++
			completed = append(completed, Turn{
				Number:  nextNumber,
				TurnID:  p.TurnID,
				Message: p.LastAgentMessage,
			})
			inProgress = false
		default:
			// agent_message, user_message, token_count and other event_msg
			// subtypes don't change turn boundaries.
		}
	}
	if err := scanner.Err(); err != nil {
		return Turns{}, fmt.Errorf("codexstate: scan rollout %s: %w", rolloutPath, err)
	}
	return Turns{Completed: completed, InProgress: inProgress}, nil
}
