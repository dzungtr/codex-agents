package codexstate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadThreads_OrdersByRecencyAndHidesArchived(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "t-old", Title: "Old thread", CWD: "/repo/a", Model: "gpt-5-codex", GitBranch: "main", RecencyAgo: 3 * time.Hour},
		{ID: "t-new", Title: "New thread", CWD: "/repo/b", Model: "gpt-5-codex", GitBranch: "feature-x", RecencyAgo: 1 * time.Minute},
		{ID: "t-archived", Title: "Archived thread", CWD: "/repo/c", Model: "gpt-5-codex", GitBranch: "main", Archived: true, RecencyAgo: 30 * time.Second},
	})

	result, err := LoadThreads(dir)
	if err != nil {
		t.Fatalf("LoadThreads: %v", err)
	}
	if result.Source != SourceSQLite {
		t.Fatalf("expected SourceSQLite, got %v", result.Source)
	}
	if len(result.Threads) != 2 {
		t.Fatalf("expected 2 non-archived threads, got %d: %+v", len(result.Threads), result.Threads)
	}
	if result.Threads[0].ID != "t-new" || result.Threads[1].ID != "t-old" {
		t.Fatalf("expected [t-new, t-old] most-recent-first, got [%s, %s]", result.Threads[0].ID, result.Threads[1].ID)
	}
	for _, th := range result.Threads {
		if th.ID == "t-archived" {
			t.Fatalf("archived thread leaked into results: %+v", th)
		}
	}
}

func TestLoadThreads_PicksNewestStateDBByGlob(t *testing.T) {
	dir := t.TempDir()
	older := buildFixtureDB(t, dir, "state_4.sqlite", []fixtureThread{
		{ID: "old-schema-thread", Title: "From old db", CWD: "/repo/a", Model: "m", GitBranch: "main", RecencyAgo: time.Minute},
	})
	newer := buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "new-schema-thread", Title: "From new db", CWD: "/repo/b", Model: "m", GitBranch: "main", RecencyAgo: time.Minute},
	})

	// Make mtimes unambiguous regardless of filesystem timestamp resolution.
	now := time.Now()
	if err := os.Chtimes(older, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("chtimes older: %v", err)
	}
	if err := os.Chtimes(newer, now, now); err != nil {
		t.Fatalf("chtimes newer: %v", err)
	}

	result, err := LoadThreads(dir)
	if err != nil {
		t.Fatalf("LoadThreads: %v", err)
	}
	if len(result.Threads) != 1 || result.Threads[0].ID != "new-schema-thread" {
		t.Fatalf("expected only the newest db's thread, got %+v", result.Threads)
	}
}

func TestLoadThreads_SchemaProbeFailureFallsBackToJSONL(t *testing.T) {
	dir := t.TempDir()
	buildLegacySchemaDB(t, dir, "state_5.sqlite")

	sessionsDir := filepath.Join(dir, "sessions", "2026", "07", "08")
	writeRolloutFile(t, filepath.Join(sessionsDir, "rollout-fallback.jsonl"), sessionMetaPayload{
		ID:        "jsonl-thread",
		Title:     "Recovered via jsonl",
		CWD:       "/repo/fallback",
		Model:     "gpt-5-codex",
		GitBranch: "main",
		Profile:   "general-agentic",
	}, 500, true)

	result, err := LoadThreads(dir)
	if err != nil {
		t.Fatalf("LoadThreads: %v", err)
	}
	if result.Source != SourceJSONL {
		t.Fatalf("expected fallback to SourceJSONL, got %v", result.Source)
	}
	if len(result.Threads) != 1 || result.Threads[0].ID != "jsonl-thread" {
		t.Fatalf("expected the jsonl-recovered thread, got %+v", result.Threads)
	}
}

func TestLoadThreads_NoStateDBFallsBackToJSONL(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	writeRolloutFile(t, filepath.Join(sessionsDir, "rollout-only.jsonl"), sessionMetaPayload{
		ID:    "only-jsonl-thread",
		Title: "No sqlite at all",
		CWD:   "/repo/x",
		Model: "m",
	}, 0, false)

	result, err := LoadThreads(dir)
	if err != nil {
		t.Fatalf("LoadThreads: %v", err)
	}
	if result.Source != SourceJSONL {
		t.Fatalf("expected SourceJSONL, got %v", result.Source)
	}
	if len(result.Threads) != 1 || result.Threads[0].ID != "only-jsonl-thread" {
		t.Fatalf("unexpected threads: %+v", result.Threads)
	}
}

func TestLoadThreads_EnrichesFromRolloutFile(t *testing.T) {
	dir := t.TempDir()
	rolloutPath := filepath.Join(dir, "sessions", "2026", "07", "08", "rollout-enrich.jsonl")
	writeRolloutFile(t, rolloutPath, sessionMetaPayload{
		ID:      "t-enrich",
		Profile: "general-agentic",
	}, 4210, true)

	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "t-enrich", Title: "Enrich me", CWD: "/repo/a", Model: "gpt-5-codex", GitBranch: "main", RecencyAgo: time.Minute, RolloutPath: rolloutPath},
	})

	result, err := LoadThreads(dir)
	if err != nil {
		t.Fatalf("LoadThreads: %v", err)
	}
	if len(result.Threads) != 1 {
		t.Fatalf("expected 1 thread, got %d", len(result.Threads))
	}
	got := result.Threads[0]
	if got.Profile != "general-agentic" {
		t.Errorf("Profile = %q, want general-agentic", got.Profile)
	}
	if got.TokenCount != 4210 {
		t.Errorf("TokenCount = %d, want 4210", got.TokenCount)
	}
}

func TestLoadThreads_EnrichesFirstMessageFromRolloutFile(t *testing.T) {
	dir := t.TempDir()
	rolloutPath := filepath.Join(dir, "sessions", "2026", "07", "08", "rollout-enrich.jsonl")
	writeRolloutFile(t, rolloutPath, sessionMetaPayload{
		ID:      "t-enrich",
		Profile: "general-agentic",
	}, 4210, true)
	appendLines(t, rolloutPath, userMessageLine(t, "first user message"))

	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "t-enrich", Title: "Enrich me", CWD: "/repo/a", Model: "gpt-5-codex", GitBranch: "main", RecencyAgo: time.Minute, RolloutPath: rolloutPath},
	})

	result, err := LoadThreads(dir)
	if err != nil {
		t.Fatalf("LoadThreads: %v", err)
	}
	if len(result.Threads) != 1 {
		t.Fatalf("expected 1 thread, got %d", len(result.Threads))
	}
	got := result.Threads[0]
	if got.FirstMessage != "first user message" {
		t.Errorf("FirstMessage = %q, want %q", got.FirstMessage, "first user message")
	}
}

func TestLoadThreads_MissingRolloutFileLeavesEnrichmentUnknown(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "t-no-rollout", Title: "No rollout on disk", CWD: "/repo/a", Model: "gpt-5-codex", GitBranch: "main", RecencyAgo: time.Minute, RolloutPath: filepath.Join(dir, "sessions", "missing.jsonl")},
	})

	result, err := LoadThreads(dir)
	if err != nil {
		t.Fatalf("LoadThreads: %v", err)
	}
	got := result.Threads[0]
	if got.Profile != "" {
		t.Errorf("Profile = %q, want empty (unknown)", got.Profile)
	}
	if got.TokenCount != -1 {
		t.Errorf("TokenCount = %d, want -1 (unknown)", got.TokenCount)
	}
}

func TestQueryThreads_OpensReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "t1", Title: "T1", CWD: "/repo", Model: "m", GitBranch: "main", RecencyAgo: time.Minute},
	})

	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open ro: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO threads (id, title, cwd, model, git_branch, archived, recency, rollout_path) VALUES ('x','x','x','x','x',0,0,'')`); err == nil {
		t.Fatalf("expected write against mode=ro connection to fail, but it succeeded")
	}

	// Confirm reads still work over the same read-only connection.
	rows, err := db.Query(threadsQuery)
	if err != nil {
		t.Fatalf("read-only query failed: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

func TestThread_RepoDerivesFromCWD(t *testing.T) {
	tests := []struct {
		cwd  string
		want string
	}{
		{"/Users/tony/project/web-app", "web-app"},
		{"/Users/tony/project/web-app/", "web-app"},
		{"", ""},
	}
	for _, tt := range tests {
		th := Thread{CWD: tt.cwd}
		if got := th.Repo(); got != tt.want {
			t.Errorf("Thread{CWD: %q}.Repo() = %q, want %q", tt.cwd, got, tt.want)
		}
	}
}
