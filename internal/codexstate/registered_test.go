package codexstate

import (
	"testing"
	"time"
)

// TestThreadRegistered_FoundInNewestSQLite asserts that ThreadRegistered
// sees a thread living in the newest state_*.sqlite, which is the
// registration-detection contract ADR 0003 decision 4 rests on: spawn
// returns only once codex's own records know about the thread.
func TestThreadRegistered_FoundInNewestSQLite(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "spawned-1", Title: "Spawned", CWD: "/repo", Model: "m", GitBranch: "main", RecencyAgo: time.Second},
	})

	got, err := ThreadRegistered(dir, "spawned-1")
	if err != nil {
		t.Fatalf("ThreadRegistered: %v", err)
	}
	if !got {
		t.Errorf("expected spawned-1 to be registered, got false")
	}
}

func TestThreadRegistered_NotYetPresent(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "other-1", Title: "Other", CWD: "/repo", Model: "m", GitBranch: "main", RecencyAgo: time.Second},
	})

	got, err := ThreadRegistered(dir, "spawned-1")
	if err != nil {
		t.Fatalf("ThreadRegistered: %v", err)
	}
	if got {
		t.Errorf("expected spawned-1 to be NOT registered, got true")
	}
}

func TestThreadRegistered_ArchivedStillCountsAsRegistered(t *testing.T) {
	// A thread codex has archived is still one codex knows about —
	// registration detection must not depend on the archived filter.
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "archived-spawn", Title: "Archived", CWD: "/repo", Model: "m", GitBranch: "main", Archived: true, RecencyAgo: time.Second},
	})

	got, err := ThreadRegistered(dir, "archived-spawn")
	if err != nil {
		t.Fatalf("ThreadRegistered: %v", err)
	}
	if !got {
		t.Errorf("expected archived-spawn to still count as registered, got false")
	}
}

func TestThreadRegistered_NoStateDBFallsBackToJSONL(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := dir + "/sessions"
	writeRolloutFile(t, sessionsDir+"/rollout.jsonl", sessionMetaPayload{
		ID:    "jsonl-spawn",
		Title: "JSONL",
		CWD:   "/repo",
		Model: "m",
	}, 0, false)

	got, err := ThreadRegistered(dir, "jsonl-spawn")
	if err != nil {
		t.Fatalf("ThreadRegistered: %v", err)
	}
	if !got {
		t.Errorf("expected jsonl-spawn to be registered via jsonl fallback, got false")
	}
}

func TestThreadRegistered_NoDataAtAllIsNotRegistered(t *testing.T) {
	dir := t.TempDir()
	got, err := ThreadRegistered(dir, "anything")
	if err != nil {
		t.Fatalf("ThreadRegistered on empty codexHome: %v", err)
	}
	if got {
		t.Errorf("expected false when no codex data exists at all, got true")
	}
}
