package codexstate

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// fixtureThread is the input shape for buildFixtureDB, expressed with a
// human-friendly recency offset instead of a raw unix timestamp.
type fixtureThread struct {
	ID          string
	Title       string
	CWD         string
	Model       string
	GitBranch   string
	Archived    bool
	RecencyAgo  time.Duration
	RolloutPath string
}

// buildFixtureDB creates a state_5-schema sqlite file at dir/name populated
// with rows, and returns its path. This stands in for a real
// ~/.codex/state_5.sqlite, which is not available in this environment.
func buildFixtureDB(t *testing.T, dir, name string, rows []fixtureThread) string {
	t.Helper()
	path := filepath.Join(dir, name)

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()

	const schema = `
CREATE TABLE threads (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	cwd TEXT NOT NULL,
	model TEXT NOT NULL,
	git_branch TEXT NOT NULL,
	archived INTEGER NOT NULL DEFAULT 0,
	recency INTEGER NOT NULL,
	rollout_path TEXT NOT NULL DEFAULT ''
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}

	now := time.Now()
	stmt, err := db.Prepare(`INSERT INTO threads (id, title, cwd, model, git_branch, archived, recency, rollout_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare insert: %v", err)
	}
	defer stmt.Close()

	for _, r := range rows {
		archived := 0
		if r.Archived {
			archived = 1
		}
		recency := now.Add(-r.RecencyAgo).Unix()
		if _, err := stmt.Exec(r.ID, r.Title, r.CWD, r.Model, r.GitBranch, archived, recency, r.RolloutPath); err != nil {
			t.Fatalf("insert fixture row %s: %v", r.ID, err)
		}
	}
	return path
}

// buildLegacySchemaDB creates a sqlite file whose threads table is missing
// columns the cockpit expects (simulating a codex upgrade that drifted the
// schema), to exercise the jsonl fallback path.
func buildLegacySchemaDB(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open legacy fixture db: %v", err)
	}
	defer db.Close()

	// Old schema: no git_branch, no rollout_path columns.
	const schema = `
CREATE TABLE threads (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	cwd TEXT NOT NULL
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create legacy fixture schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO threads (id, title, cwd) VALUES ('legacy-1', 'Legacy thread', '/tmp/legacy')`); err != nil {
		t.Fatalf("insert legacy fixture row: %v", err)
	}
	return path
}

// writeRolloutFile writes a minimal rollout jsonl file with a session_meta
// record and, optionally, a trailing token_count record.
func writeRolloutFile(t *testing.T, path string, meta sessionMetaPayload, totalTokens int, hasTokenCount bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for rollout file: %v", err)
	}

	metaJSON := mustMarshal(t, meta)
	content := `{"type":"session_meta","payload":` + string(metaJSON) + "}\n"
	if hasTokenCount {
		tokenJSON := mustMarshal(t, tokenCountPayload{TotalTokens: totalTokens})
		content += `{"type":"token_count","payload":` + string(tokenJSON) + "}\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write rollout file: %v", err)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// userMessageLine returns a single jsonl line for an event_msg/user_message
// record carrying message, for tests that need first-user-message records
// beyond what writeRolloutFile covers.
func userMessageLine(t *testing.T, message string) string {
	t.Helper()
	payload := mustMarshal(t, eventMsgPayload{Type: "user_message", Message: message})
	return `{"type":"event_msg","payload":` + string(payload) + `}`
}

// appendLines appends raw jsonl lines (each already a complete JSON object,
// no trailing newline) to the file at path, which must already exist (e.g.
// created by writeRolloutFile).
func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatalf("append line to %s: %v", path, err)
		}
	}
}
