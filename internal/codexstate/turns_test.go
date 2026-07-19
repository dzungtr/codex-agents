package codexstate

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// taskStartedLine returns a jsonl line for an event_msg/task_started record
// carrying turnID, the turn-begin marker codex writes to the rollout file.
func taskStartedLine(t *testing.T, turnID string) string {
	t.Helper()
	return mustMarshalString(t, jsonlRecord{
		Type: "event_msg",
		Payload: mustMarshalRaw(t, turnEventMsgPayload{
			Type:   "task_started",
			TurnID: turnID,
		}),
	})
}

// taskCompleteLine returns a jsonl line for an event_msg/task_complete record
// carrying turnID and lastAgentMessage, the turn-end marker. lastAgentMessage
// is the assistant message codex itself collected for that turn — we use it
// directly rather than re-deriving from agent_message records.
func taskCompleteLine(t *testing.T, turnID, lastAgentMessage string) string {
	t.Helper()
	return mustMarshalString(t, jsonlRecord{
		Type: "event_msg",
		Payload: mustMarshalRaw(t, turnEventMsgPayload{
			Type:             "task_complete",
			TurnID:           turnID,
			LastAgentMessage: lastAgentMessage,
		}),
	})
}

// agentMessageLine returns a jsonl line for an event_msg/agent_message record
// (an assistant conversational reply within a turn). Tests use it to verify
// the last assistant message of a completed turn is the one from
// task_complete, not the last agent_message record.
func agentMessageLine(t *testing.T, message string) string {
	t.Helper()
	return mustMarshalString(t, jsonlRecord{
		Type: "event_msg",
		Payload: mustMarshalRaw(t, turnEventMsgPayload{
			Type:    "agent_message",
			Message: message,
		}),
	})
}

func mustMarshalString(t *testing.T, v any) string {
	t.Helper()
	return string(mustMarshal(t, v))
}

func mustMarshalRaw(t *testing.T, v any) []byte {
	t.Helper()
	// json.RawMessage needs raw bytes; round-trip through Marshal.
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	return b
}

// writeFullRollout writes a session_meta record then the given raw jsonl
// lines, creating the rollout file fresh. Used by turn-reading tests.
func writeFullRollout(t *testing.T, path string, meta sessionMetaPayload, lines ...string) {
	t.Helper()
	writeRolloutFile(t, path, meta, 0, false)
	if len(lines) > 0 {
		appendLines(t, path, lines...)
	}
}

func TestReadTurns_NoTurnMarkersReturnsZeroTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeFullRollout(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"},
		userMessageLine(t, "hello"))

	turns, err := ReadTurns(path)
	if err != nil {
		t.Fatalf("ReadTurns: %v", err)
	}
	if len(turns.Completed) != 0 {
		t.Fatalf("expected 0 turns (no task_started/task_complete), got %d: %+v", len(turns.Completed), turns)
	}
}

func TestReadTurns_SingleCompletedTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeFullRollout(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"},
		taskStartedLine(t, "turn-1"),
		agentMessageLine(t, "intermediate reply"),
		agentMessageLine(t, "final reply"),
		taskCompleteLine(t, "turn-1", "collected last message"))

	turns, err := ReadTurns(path)
	if err != nil {
		t.Fatalf("ReadTurns: %v", err)
	}
	if len(turns.Completed) != 1 {
		t.Fatalf("expected 1 completed turn, got %d: %+v", len(turns.Completed), turns)
	}
	got := turns.Completed[0]
	if got.Number != 1 {
		t.Errorf("Number = %d, want 1", got.Number)
	}
	if got.Message != "collected last message" {
		t.Errorf("Message = %q, want %q", got.Message, "collected last message")
	}
	if got.TurnID != "turn-1" {
		t.Errorf("TurnID = %q, want turn-1", got.TurnID)
	}
}

func TestReadTurns_MultipleCompletedTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeFullRollout(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"},
		taskStartedLine(t, "turn-1"),
		taskCompleteLine(t, "turn-1", "first turn result"),
		taskStartedLine(t, "turn-2"),
		taskCompleteLine(t, "turn-2", "second turn result"),
		taskStartedLine(t, "turn-3"),
		taskCompleteLine(t, "turn-3", "third turn result"))

	turns, err := ReadTurns(path)
	if err != nil {
		t.Fatalf("ReadTurns: %v", err)
	}
	if len(turns.Completed) != 3 {
		t.Fatalf("expected 3 completed turns, got %d: %+v", len(turns.Completed), turns)
	}
	if turns.Completed[0].Number != 1 || turns.Completed[0].Message != "first turn result" {
		t.Errorf("turn 0 = %+v, want Number=1 Message=first turn result", turns.Completed[0])
	}
	if turns.Completed[1].Number != 2 || turns.Completed[1].Message != "second turn result" {
		t.Errorf("turn 1 = %+v, want Number=2 Message=second turn result", turns.Completed[1])
	}
	if turns.Completed[2].Number != 3 || turns.Completed[2].Message != "third turn result" {
		t.Errorf("turn 2 = %+v, want Number=3 Message=third turn result", turns.Completed[2])
	}
}

func TestReadTurns_InProgressFinalTurnNotCounted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeFullRollout(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"},
		taskStartedLine(t, "turn-1"),
		taskCompleteLine(t, "turn-1", "done result"),
		taskStartedLine(t, "turn-2"),
		agentMessageLine(t, "still working on this"))

	turns, err := ReadTurns(path)
	if err != nil {
		t.Fatalf("ReadTurns: %v", err)
	}
	if len(turns.Completed) != 1 {
		t.Fatalf("expected 1 completed turn (turn-2 in progress, not counted), got %d: %+v", len(turns.Completed), turns)
	}
	if turns.Completed[0].Number != 1 || turns.Completed[0].Message != "done result" {
		t.Errorf("turn 0 = %+v, want Number=1 Message=done result", turns.Completed[0])
	}
	if !turns.InProgress {
		t.Errorf("InProgress = false, want true (turn-2 has no task_complete)")
	}
}

func TestReadTurns_MalformedLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeFullRollout(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"},
		"not json at all",
		taskStartedLine(t, "turn-1"),
		`{"type":"event_msg","payload":42}`,
		taskCompleteLine(t, "turn-1", "survived garbage"))

	turns, err := ReadTurns(path)
	if err != nil {
		t.Fatalf("ReadTurns: %v", err)
	}
	if len(turns.Completed) != 1 {
		t.Fatalf("expected 1 turn (malformed lines skipped), got %d: %+v", len(turns.Completed), turns)
	}
	if turns.Completed[0].Message != "survived garbage" {
		t.Errorf("Message = %q, want survived garbage", turns.Completed[0].Message)
	}
}

func TestReadTurns_OnlyTaskStartedNoTaskComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeFullRollout(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"},
		taskStartedLine(t, "turn-1"),
		agentMessageLine(t, "no completion yet"))

	turns, err := ReadTurns(path)
	if err != nil {
		t.Fatalf("ReadTurns: %v", err)
	}
	if len(turns.Completed) != 0 {
		t.Fatalf("expected 0 completed turns (turn in progress), got %d: %+v", len(turns.Completed), turns)
	}
	if !turns.InProgress {
		t.Errorf("InProgress = false, want true (task_started with no task_complete)")
	}
}

func TestReadTurns_MissingRolloutFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.jsonl")

	_, err := ReadTurns(missing)
	if err == nil {
		t.Fatalf("expected error for missing rollout file, got nil")
	}
}

func TestReadTurns_EmptyMessageWhenTaskCompleteHasNone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	// task_complete with empty last_agent_message — a turn that ended
	// without the assistant producing a message (e.g. interrupted).
	writeFullRollout(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"},
		taskStartedLine(t, "turn-1"),
		taskCompleteLine(t, "turn-1", ""))

	turns, err := ReadTurns(path)
	if err != nil {
		t.Fatalf("ReadTurns: %v", err)
	}
	if len(turns.Completed) != 1 {
		t.Fatalf("expected 1 completed turn, got %d", len(turns.Completed))
	}
	if turns.Completed[0].Message != "" {
		t.Errorf("Message = %q, want empty", turns.Completed[0].Message)
	}
}

func TestFindThread_SQLiteSource(t *testing.T) {
	dir := t.TempDir()
	rolloutPath := filepath.Join(dir, "sessions", "rollout.jsonl")
	writeFullRollout(t, rolloutPath, sessionMetaPayload{ID: "t-sql", Title: "SQL thread", CWD: "/repo"},
		taskStartedLine(t, "turn-1"),
		taskCompleteLine(t, "turn-1", "sql result"))
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "t-sql", Title: "SQL thread", CWD: "/repo", Model: "m", GitBranch: "main", RecencyAgo: 0, RolloutPath: rolloutPath},
	})

	th, err := FindThread(dir, "t-sql")
	if err != nil {
		t.Fatalf("FindThread: %v", err)
	}
	if th.ID != "t-sql" {
		t.Errorf("ID = %q, want t-sql", th.ID)
	}
	if th.RolloutPath != rolloutPath {
		t.Errorf("RolloutPath = %q, want %q", th.RolloutPath, rolloutPath)
	}
	if th.Source != SourceSQLite {
		t.Errorf("Source = %v, want SourceSQLite", th.Source)
	}
}

func TestFindThread_JSONLFallbackSource(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	rolloutPath := filepath.Join(sessionsDir, "rollout.jsonl")
	writeRolloutFile(t, rolloutPath, sessionMetaPayload{
		ID: "t-jsonl", Title: "JSONL thread", CWD: "/repo", Model: "m",
	}, 0, false)

	// No state_*.sqlite — forces the jsonl fallback.
	th, err := FindThread(dir, "t-jsonl")
	if err != nil {
		t.Fatalf("FindThread: %v", err)
	}
	if th.ID != "t-jsonl" {
		t.Errorf("ID = %q, want t-jsonl", th.ID)
	}
	if th.Source != SourceJSONL {
		t.Errorf("Source = %v, want SourceJSONL", th.Source)
	}
}

func TestFindThread_UnknownThreadIDReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "t-known", Title: "Known", CWD: "/repo", Model: "m", GitBranch: "main", RecencyAgo: 0},
	})

	_, err := FindThread(dir, "t-missing")
	if err != ErrThreadNotFound {
		t.Fatalf("expected ErrThreadNotFound, got %v", err)
	}
}

func TestFindThread_ArchivedThreadReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "t-archived", Title: "Archived", CWD: "/repo", Model: "m", GitBranch: "main", RecencyAgo: 0, Archived: true},
	})

	_, err := FindThread(dir, "t-archived")
	if err != ErrThreadNotFound {
		t.Fatalf("expected ErrThreadNotFound for archived thread, got %v", err)
	}
}
