// Package notifyhook owns the notify-wrapper contract chained onto a
// launched thread's codex invocation via `-c notify=[...]` (PRD #1's Launch
// semantics -> Status hook row, issue #4). codex invokes the configured
// notify program whenever a turn ends, appending a single JSON payload
// argument describing the event. The cockpit configures itself (re-invoked
// in hook mode, see Subcommand) as that program so it can:
//
//  1. record a turn-ended event for its own status derivation
//     (internal/tmuxstatus.StatusFor's turnEnded input), and
//  2. forward the original invocation, unchanged, to the user's own
//     pre-existing notify command if one was configured before the cockpit
//     chained this wrapper in.
//
// Event file contract (the producer/consumer handoff for issues #5 and #6,
// per PRD #1's Handoffs table): a JSONL file, one Event per line, appended
// to on every hook invocation. Each line is a JSON object:
//
//	{"thread_id":"<id>","kind":"turn-ended","at":"<RFC3339 UTC timestamp>"}
//
// The default location is "events.jsonl" next to agentstate's state.json
// (~/.codex-agents/events.jsonl) — "the cockpit's own state", never
// codex's data. Only the most recent line per thread_id matters for status
// derivation; older lines are left in place (no compaction in v1). Readers
// should skip lines that fail to parse rather than fail outright, since a
// torn append (this file is not written atomically, unlike state.json) is
// expected to occasionally happen and must not corrupt the whole read.
//
// Everything in this package is designed to never block or fail codex's own
// turn-completion flow: Run reports failures to an io.Writer (production
// code wires stderr) rather than returning an error, so a broken/missing
// events file degrades the cockpit to plain open/closed status (via
// tmuxstatus.StatusFor's turnEnded=false default) instead of erroring.
package notifyhook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dzungtr/codex-agents/internal/agentstate"
)

// Subcommand is the hidden main.go dispatch key. WrapperArgs configures
// codex to invoke exactly:
//
//	<exePath> notify-hook <threadID> <eventsPath> <forwardJoined> <payload>
//
// where <payload> is appended by codex itself (the notify hook's single
// JSON argument) — everything before it is what WrapperArgs configured.
const Subcommand = "notify-hook"

// KindTurnEnded is the only event kind this slice produces: codex invokes
// the notify hook when a turn ends and the thread needs user input.
const KindTurnEnded = "turn-ended"

// forwardSep joins/splits a forward command's argv into the single argv
// slot WrapperArgs has available for it (it sits between two fixed
// positional args and the payload codex appends, so it can't itself be a
// variable number of argv entries).
const forwardSep = "\x1f"

// EventsFileName is the event log's filename, placed alongside
// agentstate's state.json.
const EventsFileName = "events.jsonl"

// DefaultEventsPath returns the events file path that lives next to
// statePath (agentstate.DefaultPath()'s result, typically
// ~/.codex-agents/state.json), i.e. ~/.codex-agents/events.jsonl.
func DefaultEventsPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), EventsFileName)
}

// Event is one line of the events.jsonl file.
type Event struct {
	ThreadID string    `json:"thread_id"`
	Kind     string    `json:"kind"`
	At       time.Time `json:"at"`
}

// LastTurnEventValue formats ev the way agentstate.Entry.LastTurnEvent
// stores it: "<kind>@<RFC3339 timestamp>". A non-empty LastTurnEvent means
// "the last known turn for this thread ended" — the only kind this slice
// produces — which is exactly the turnEnded bool tmuxstatus.StatusFor
// wants.
func LastTurnEventValue(ev Event) string {
	return fmt.Sprintf("%s@%s", ev.Kind, ev.At.Format(time.RFC3339))
}

// WrapperArgs builds the `-c notify=[...]` argv for a launched thread's
// codex invocation: exePath re-invoked in hook mode. forward is the user's
// pre-existing notify command read from their profile config before the
// cockpit overrode it (nil/empty means none was configured — the wrapper
// then only records the event).
func WrapperArgs(exePath, threadID, eventsPath string, forward []string) []string {
	return []string{exePath, Subcommand, threadID, eventsPath, strings.Join(forward, forwardSep)}
}

// ParseWrapperArgs splits a hook invocation's args — os.Args[2:], i.e.
// everything after the "notify-hook" subcommand word — back into
// WrapperArgs' pieces plus the trailing JSON payload codex appended.
func ParseWrapperArgs(args []string) (threadID, eventsPath string, forward []string, payload string, err error) {
	if len(args) != 4 {
		return "", "", nil, "", fmt.Errorf("notifyhook: expected 4 args (thread id, events path, forward, payload), got %d", len(args))
	}
	threadID, eventsPath, joined, payload := args[0], args[1], args[2], args[3]
	if joined != "" {
		forward = strings.Split(joined, forwardSep)
	}
	return threadID, eventsPath, forward, payload, nil
}

// AppendEvent appends ev as one JSON line to path, creating the parent
// directory and file as needed. Unlike agentstate.Save, this is a plain
// append rather than an atomic rename: a torn write here loses at most one
// event line, and the hook must never risk blocking on a more elaborate
// write strategy.
func AppendEvent(path string, ev Event) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("notifyhook: create events dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("notifyhook: open events file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("notifyhook: marshal event: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("notifyhook: write event: %w", err)
	}
	return nil
}

// LatestByThread reads path and returns the most recent Event per thread
// ID (later lines win). A missing file is not an error: it returns an
// empty map, since no thread has ever had a turn-ended event recorded —
// exactly the "hook unavailable" degraded state that leaves every alive
// thread reading as StatusWorking. Malformed lines (a torn append, or a
// stray non-JSON line) are skipped rather than failing the whole read.
func LatestByThread(path string) (map[string]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Event{}, nil
		}
		return nil, fmt.Errorf("notifyhook: read %s: %w", path, err)
	}

	out := map[string]Event{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		out[ev.ThreadID] = ev
	}
	return out, nil
}

// ForwardRunner executes the user's pre-existing notify command. Production
// code uses ExecForwardRunner; tests inject a fake so Run can be exercised
// without shelling out.
type ForwardRunner interface {
	Run(argv []string) error
}

// ExecForwardRunner shells out to the forward command's own argv[0].
type ExecForwardRunner struct{}

func (ExecForwardRunner) Run(argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Run performs one hook invocation's work: append the turn-ended event,
// best-effort-update agentstate's last_turn_event for threadID, and forward
// to the user's own notify command if one was configured. now is the
// event's timestamp (production code uses time.Now().UTC(); tests pass a
// fixed time for determinism).
//
// Run deliberately never returns an error: any failure (can't write the
// events file, can't update state.json, the forward command exits
// non-zero) is written to stderr instead, per the PRD's "hook unavailable
// -> degrade to open/closed" contract. codex's own turn-completion flow
// must not be blocked or fail because the cockpit's bookkeeping had a
// problem.
func Run(stderr io.Writer, forward ForwardRunner, statePath, threadID, eventsPath string, forwardCmd []string, payload string, now time.Time) {
	ev := Event{ThreadID: threadID, Kind: KindTurnEnded, At: now}

	if err := AppendEvent(eventsPath, ev); err != nil {
		fmt.Fprintln(stderr, "notify-hook: record event:", err)
	}

	if statePath != "" {
		if err := agentstate.UpdateLastTurnEvent(statePath, threadID, LastTurnEventValue(ev)); err != nil {
			fmt.Fprintln(stderr, "notify-hook: update state:", err)
		}
	}

	if len(forwardCmd) > 0 && forward != nil {
		argv := append(append([]string(nil), forwardCmd...), payload)
		if err := forward.Run(argv); err != nil {
			fmt.Fprintln(stderr, "notify-hook: forward notify command:", err)
		}
	}
}
