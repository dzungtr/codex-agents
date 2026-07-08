// Package agentstate owns the cockpit's own small state file
// (~/.codex-agents/state.json by default): a map from codex thread ID to
// the cockpit-launched bookkeeping for that thread (tmux session name,
// profile, worktree path, last turn event).
//
// This is deliberately separate from codex's own data: codexstate reads
// codex's sqlite/jsonl records read-only, and nothing in this package ever
// touches $CODEX_HOME. Per PRD #1 ("codex's data is never written to"),
// agentstate is the only file this tool writes.
package agentstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Entry is one cockpit-launched thread's bookkeeping.
//
// LastTurnEvent is a placeholder owned by the Statuses slice (PRD #1 / issue
// #4): it stays empty here since this slice does no notify-hook work, but
// the field exists now so #4 can start writing to it without a schema
// migration.
// Hidden marks a thread as archived from the cockpit's own bookkeeping.
// Issue #5's Archive (`a`) action sets this when no codex-sanctioned
// archive mechanism is available (codexstate opens codex's sqlite
// read-only and exposes no write path) — hiding here is the fallback the
// PRD calls for: "or mark hidden in the cockpit's own agentstate and
// filter it from the list". A Hidden thread is filtered out of
// cmd/codex-agents' row list regardless of what codex's own `archived`
// column later says.
type Entry struct {
	TmuxSession   string `json:"tmux_session"`
	Profile       string `json:"profile"`
	WorktreePath  string `json:"worktree_path"`
	LastTurnEvent string `json:"last_turn_event,omitempty"`
	Hidden        bool   `json:"hidden,omitempty"`
}

// State is the full contents of state.json: every cockpit-launched thread,
// keyed by its thread ID.
type State struct {
	Threads map[string]Entry `json:"threads"`
}

// DefaultPath returns the default state file location: ~/.codex-agents/state.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agentstate: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".codex-agents", "state.json"), nil
}

// Load reads state.json at path. A missing file is not an error: it
// returns a fresh, empty State, since the first launch on a machine won't
// have one yet.
func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{Threads: map[string]Entry{}}, nil
		}
		return State{}, fmt.Errorf("agentstate: read %s: %w", path, err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("agentstate: parse %s: %w", path, err)
	}
	if st.Threads == nil {
		st.Threads = map[string]Entry{}
	}
	return st, nil
}

// Save writes state.json atomically: it writes to a temp file in the same
// directory, then renames over the destination, so a crash mid-write never
// leaves a corrupt state.json. The parent directory is created if needed.
func Save(path string, st State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("agentstate: create dir %s: %w", dir, err)
	}

	if st.Threads == nil {
		st.Threads = map[string]Entry{}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("agentstate: marshal state: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("agentstate: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("agentstate: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agentstate: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("agentstate: rename into place: %w", err)
	}
	return nil
}

// Upsert loads the state at path, sets threadID's entry, and saves it back.
// It's a convenience for the common launch/resume case of updating a single
// thread's bookkeeping without the caller juggling Load/Save itself.
func Upsert(path, threadID string, entry Entry) error {
	st, err := Load(path)
	if err != nil {
		return err
	}
	if st.Threads == nil {
		st.Threads = map[string]Entry{}
	}
	st.Threads[threadID] = entry
	return Save(path, st)
}

// UpdateLastTurnEvent loads path, sets threadID's LastTurnEvent to event
// (preserving its other fields — TmuxSession/Profile/WorktreePath — since
// the notify hook fires long after Launch/Resume already populated them),
// and saves it back. This is the write side of the Statuses slice (PRD #1 /
// issue #4): internal/notifyhook's wrapper calls this on every turn-ended
// event so the cockpit's status derivation can read it back without
// re-deriving anything from codex's own data.
//
// A threadID with no prior entry gets a fresh one with only LastTurnEvent
// set, rather than erroring: a hook firing for a thread state.json doesn't
// know about yet (e.g. a plain-terminal session using a stray inherited
// notify config) shouldn't be treated as a failure.
func UpdateLastTurnEvent(path, threadID, event string) error {
	st, err := Load(path)
	if err != nil {
		return err
	}
	if st.Threads == nil {
		st.Threads = map[string]Entry{}
	}
	entry := st.Threads[threadID]
	entry.LastTurnEvent = event
	st.Threads[threadID] = entry
	return Save(path, st)
}

// MarkHidden loads path, sets threadID's Hidden flag to true (preserving
// its other fields, same pattern as UpdateLastTurnEvent), and saves it
// back. This is the Archive (`a`) action's fallback write when no
// codex-sanctioned archive mechanism exists: see Entry.Hidden's doc
// comment.
func MarkHidden(path, threadID string) error {
	st, err := Load(path)
	if err != nil {
		return err
	}
	if st.Threads == nil {
		st.Threads = map[string]Entry{}
	}
	entry := st.Threads[threadID]
	entry.Hidden = true
	st.Threads[threadID] = entry
	return Save(path, st)
}
