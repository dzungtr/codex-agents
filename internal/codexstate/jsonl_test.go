package codexstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanSessionsJSONL_ParsesMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeRolloutFile(t, filepath.Join(dir, "2026", "07", "08", "rollout-a.jsonl"), sessionMetaPayload{
		ID: "a", Title: "Thread A", CWD: "/repo/a", Model: "m1", GitBranch: "main",
	}, 100, true)
	writeRolloutFile(t, filepath.Join(dir, "2026", "07", "09", "rollout-b.jsonl"), sessionMetaPayload{
		ID: "b", Title: "Thread B", CWD: "/repo/b", Model: "m2", GitBranch: "dev",
	}, 0, false)

	threads, err := scanSessionsJSONL(dir)
	if err != nil {
		t.Fatalf("scanSessionsJSONL: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads, got %d: %+v", len(threads), threads)
	}
	byID := map[string]Thread{}
	for _, th := range threads {
		byID[th.ID] = th
	}
	if byID["a"].TokenCount != 100 {
		t.Errorf("thread a TokenCount = %d, want 100", byID["a"].TokenCount)
	}
	if byID["b"].TokenCount != -1 {
		t.Errorf("thread b TokenCount = %d, want -1 (no token_count record)", byID["b"].TokenCount)
	}
	for _, th := range threads {
		if th.Source != SourceJSONL {
			t.Errorf("thread %s Source = %v, want SourceJSONL", th.ID, th.Source)
		}
	}
}

func TestScanSessionsJSONL_SkipsUnparseableFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "garbage.jsonl"), []byte("not json at all\n{\"type\":\"session_meta\"\n"), 0o644); err != nil {
		t.Fatalf("write garbage file: %v", err)
	}
	writeRolloutFile(t, filepath.Join(dir, "good.jsonl"), sessionMetaPayload{ID: "good", Title: "Good", CWD: "/repo"}, 0, false)

	threads, err := scanSessionsJSONL(dir)
	if err != nil {
		t.Fatalf("scanSessionsJSONL: %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "good" {
		t.Fatalf("expected only the parseable thread, got %+v", threads)
	}
}

func TestScanSessionsJSONL_IgnoresNonJSONLFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	threads, err := scanSessionsJSONL(dir)
	if err != nil {
		t.Fatalf("scanSessionsJSONL: %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("expected 0 threads, got %+v", threads)
	}
}

func TestScanSessionsJSONL_MissingDirReturnsEmptyNotError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	threads, err := scanSessionsJSONL(dir)
	if err != nil {
		t.Fatalf("expected no error for missing sessions dir, got %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("expected 0 threads, got %+v", threads)
	}
}

func TestThreadFromRolloutFile_CapturesFirstUserMessage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeRolloutFile(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"}, 0, false)
	appendLines(t, path, userMessageLine(t, "create new agent"))

	th, ok := threadFromRolloutFile(path)
	if !ok {
		t.Fatalf("threadFromRolloutFile: expected ok, got false")
	}
	if th.FirstMessage != "create new agent" {
		t.Errorf("FirstMessage = %q, want %q", th.FirstMessage, "create new agent")
	}
}

func TestThreadFromRolloutFile_NoUserMessageLeavesFirstMessageEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeRolloutFile(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"}, 0, false)

	th, ok := threadFromRolloutFile(path)
	if !ok {
		t.Fatalf("threadFromRolloutFile: expected ok, got false")
	}
	if th.FirstMessage != "" {
		t.Errorf("FirstMessage = %q, want empty", th.FirstMessage)
	}
}

func TestThreadFromRolloutFile_MultipleUserMessagesFirstNonEmptyWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeRolloutFile(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"}, 0, false)
	appendLines(t, path,
		userMessageLine(t, "first message"),
		userMessageLine(t, "second message"),
	)

	th, ok := threadFromRolloutFile(path)
	if !ok {
		t.Fatalf("threadFromRolloutFile: expected ok, got false")
	}
	if th.FirstMessage != "first message" {
		t.Errorf("FirstMessage = %q, want %q", th.FirstMessage, "first message")
	}
}

func TestThreadFromRolloutFile_WhitespaceOnlyFirstMessageSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeRolloutFile(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"}, 0, false)
	appendLines(t, path,
		userMessageLine(t, "   \n\t  "),
		userMessageLine(t, "real message"),
	)

	th, ok := threadFromRolloutFile(path)
	if !ok {
		t.Fatalf("threadFromRolloutFile: expected ok, got false")
	}
	if th.FirstMessage != "real message" {
		t.Errorf("FirstMessage = %q, want %q", th.FirstMessage, "real message")
	}
}

func TestThreadFromRolloutFile_TruncatesLongMessageRuneSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeRolloutFile(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"}, 0, false)

	// Use a multibyte rune (3-byte "€") repeated well past the cap, so a
	// byte-slice truncation would split a rune and produce invalid UTF-8 or
	// a different rune count.
	long := strings.Repeat("€", firstMessageMaxRunes+50)
	appendLines(t, path, userMessageLine(t, long))

	th, ok := threadFromRolloutFile(path)
	if !ok {
		t.Fatalf("threadFromRolloutFile: expected ok, got false")
	}
	gotRunes := []rune(th.FirstMessage)
	if len(gotRunes) != firstMessageMaxRunes {
		t.Fatalf("FirstMessage rune count = %d, want %d", len(gotRunes), firstMessageMaxRunes)
	}
	want := strings.Repeat("€", firstMessageMaxRunes)
	if th.FirstMessage != want {
		t.Errorf("FirstMessage = %q, want %d copies of €", th.FirstMessage, firstMessageMaxRunes)
	}
}

func TestThreadFromRolloutFile_MalformedEventMsgPayloadSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	writeRolloutFile(t, path, sessionMetaPayload{ID: "t1", Title: "T1", CWD: "/repo"}, 0, false)
	appendLines(t, path,
		`{"type":"event_msg","payload":42}`,
		userMessageLine(t, "recovered message"),
	)

	th, ok := threadFromRolloutFile(path)
	if !ok {
		t.Fatalf("threadFromRolloutFile: expected ok, got false")
	}
	if th.FirstMessage != "recovered message" {
		t.Errorf("FirstMessage = %q, want %q", th.FirstMessage, "recovered message")
	}
}
