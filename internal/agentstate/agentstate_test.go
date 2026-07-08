package agentstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFileReturnsEmptyState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing file returned error: %v", err)
	}
	if st.Threads == nil {
		t.Fatalf("expected non-nil Threads map on a fresh state")
	}
	if len(st.Threads) != 0 {
		t.Fatalf("expected empty Threads map, got %v", st.Threads)
	}
}

func TestSaveThenLoad_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	st := State{Threads: map[string]Entry{
		"thread-1": {
			TmuxSession:  "cxa-thread-1",
			Profile:      "general-agentic",
			WorktreePath: "/repo/.worktrees/fix-auth-hook",
		},
	}}

	if err := Save(path, st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry, ok := got.Threads["thread-1"]
	if !ok {
		t.Fatalf("expected thread-1 entry, got %v", got.Threads)
	}
	if entry.TmuxSession != "cxa-thread-1" || entry.Profile != "general-agentic" || entry.WorktreePath != "/repo/.worktrees/fix-auth-hook" {
		t.Fatalf("round-tripped entry mismatch: %+v", entry)
	}
}

func TestSave_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "state.json")

	if err := Save(path, State{Threads: map[string]Entry{}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected state file to exist: %v", err)
	}
}

func TestSave_NoLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := Save(path, State{Threads: map[string]Entry{}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("expected only state.json in dir, got %v", entries)
	}
}

func TestSave_OverwritesExistingAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := Save(path, State{Threads: map[string]Entry{"a": {TmuxSession: "cxa-a"}}}); err != nil {
		t.Fatalf("Save 1: %v", err)
	}
	if err := Save(path, State{Threads: map[string]Entry{"b": {TmuxSession: "cxa-b"}}}); err != nil {
		t.Fatalf("Save 2: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := got.Threads["a"]; ok {
		t.Fatalf("expected first save to be fully replaced, still found 'a': %v", got.Threads)
	}
	if _, ok := got.Threads["b"]; !ok {
		t.Fatalf("expected 'b' entry after second save, got %v", got.Threads)
	}
}

func TestUpsert_LoadsModifiesAndSaves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := Upsert(path, "thread-1", Entry{TmuxSession: "cxa-thread-1", Profile: "review"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := Upsert(path, "thread-2", Entry{TmuxSession: "cxa-thread-2", Profile: "general-agentic"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Threads) != 2 {
		t.Fatalf("expected 2 entries after two upserts, got %v", got.Threads)
	}
	if got.Threads["thread-1"].Profile != "review" {
		t.Fatalf("expected thread-1 profile 'review', got %+v", got.Threads["thread-1"])
	}
}

func TestDefaultPath_UnderCodexAgentsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(home, ".codex-agents", "state.json")
	if path != want {
		t.Fatalf("DefaultPath() = %q, want %q", path, want)
	}
}
