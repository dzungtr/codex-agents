// Package codexstate is the sole owner of codex-specific knowledge: the
// layout of ~/.codex (state sqlite databases and session jsonl files), the
// "threads" table schema, and how to degrade gracefully when that schema
// doesn't match what we expect (e.g. after a codex upgrade).
//
// Nothing outside this package should know that codex stores its state in
// sqlite, what the threads table looks like, or where session files live.
package codexstate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

// Source identifies which backend produced a Thread record.
type Source int

const (
	// SourceSQLite means the thread was read via the narrow SELECT against
	// codex's state_*.sqlite threads table.
	SourceSQLite Source = iota
	// SourceJSONL means the sqlite schema probe failed and the thread was
	// recovered from a best-effort scan of ~/.codex/sessions/*.jsonl.
	SourceJSONL
)

func (s Source) String() string {
	switch s {
	case SourceSQLite:
		return "sqlite"
	case SourceJSONL:
		return "jsonl"
	default:
		return "unknown"
	}
}

// Thread is a single codex conversation, as seen by the cockpit.
//
// ID, Title, CWD, Model, GitBranch, Archived and Recency come directly from
// codex's own records (sqlite threads table, or best-effort jsonl scan).
// Profile, TokenCount, MessageCount and FirstMessage are best-effort
// enrichment parsed from the rollout session file when available; an empty
// Profile, a negative TokenCount/MessageCount, or an empty FirstMessage
// means "unknown", not "empty"/"zero".
type Thread struct {
	ID           string
	Title        string
	CWD          string
	Model        string
	GitBranch    string
	Archived     bool
	Recency      time.Time
	RolloutPath  string
	Profile      string
	FirstMessage string
	TokenCount   int
	MessageCount int
	Source       Source
}

// Repo derives a short repo label from CWD (its last path component), since
// codex's threads table does not store a repo name directly.
func (t Thread) Repo() string {
	if t.CWD == "" {
		return ""
	}
	base := filepath.Base(filepath.Clean(t.CWD))
	if base == "." || base == string(filepath.Separator) {
		return t.CWD
	}
	return base
}

// stateDBGlob is the filename pattern used to find codex's state database
// under $CODEX_HOME. Per PRD #1 / ADR 0001, the newest match (by mtime) wins.
const stateDBGlob = "state_*.sqlite"

// sessionsDirName is the subdirectory under $CODEX_HOME holding per-thread
// rollout jsonl files, used both for the schema-probe-failure fallback and
// for best-effort detail-line enrichment (profile, token count) of threads
// read from sqlite.
const sessionsDirName = "sessions"

// DefaultCodexHome returns the default codex state directory (~/.codex).
func DefaultCodexHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("codexstate: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

// LoadResult is the outcome of LoadThreads: the threads found, and which
// backend produced them (useful for surfacing a degraded-mode notice).
type LoadResult struct {
	Threads []Thread
	Source  Source
}

// LoadThreads is the package's single entry point: it finds the newest
// state_*.sqlite under codexHome and runs the narrow threads SELECT against
// it. If no database is found, it can't be opened, or the schema probe
// fails (missing table/columns from a codex upgrade), it degrades to a
// best-effort scan of codexHome/sessions/*.jsonl rather than erroring out.
//
// Archived threads are never returned. Results are ordered most-recent
// first (by Recency), which is the only ordering slice #2 owns; status
// based re-ordering (waiting/working/closed groups) is a later slice.
func LoadThreads(codexHome string) (LoadResult, error) {
	dbPath, findErr := newestStateDB(codexHome)
	if findErr == nil {
		threads, err := queryThreads(dbPath)
		if err == nil {
			enrichFromSessionFiles(threads, filepath.Join(codexHome, sessionsDirName))
			sortByRecency(threads)
			return LoadResult{Threads: threads, Source: SourceSQLite}, nil
		}
	}

	threads, err := scanSessionsJSONL(filepath.Join(codexHome, sessionsDirName))
	if err != nil {
		return LoadResult{}, fmt.Errorf("codexstate: sqlite unavailable and jsonl fallback failed: %w", err)
	}
	sortByRecency(threads)
	return LoadResult{Threads: threads, Source: SourceJSONL}, nil
}

func sortByRecency(threads []Thread) {
	sort.SliceStable(threads, func(i, j int) bool {
		return threads[i].Recency.After(threads[j].Recency)
	})
}

// newestStateDB returns the most-recently-modified file matching
// state_*.sqlite under codexHome. Ties break on lexicographically-greatest
// filename so results are deterministic in tests.
func newestStateDB(codexHome string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(codexHome, stateDBGlob))
	if err != nil {
		return "", fmt.Errorf("codexstate: glob %s: %w", stateDBGlob, err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("codexstate: no state database matching %s under %s", stateDBGlob, codexHome)
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	candidates := make([]candidate, 0, len(matches))
	for _, m := range matches {
		info, statErr := os.Stat(m)
		if statErr != nil {
			continue
		}
		candidates = append(candidates, candidate{path: m, modTime: info.ModTime()})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("codexstate: no readable state database under %s", codexHome)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].path > candidates[j].path
		}
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	return candidates[0].path, nil
}

// threadsQuery is the narrow, read-only SELECT this package relies on. It is
// intentionally minimal: only the columns the cockpit needs. Pinned against
// fixtures for schema "state_5" (see testdata).
const threadsQuery = `
SELECT id, title, cwd, model, git_branch, archived, recency, rollout_path
FROM threads
WHERE archived = 0
`

// queryThreads opens dbPath read-only and runs threadsQuery. Any failure
// (missing file, missing table, missing/renamed column) is returned as an
// error so the caller can treat it as a schema-probe failure and degrade to
// the jsonl fallback; codex's data is never written to.
func queryThreads(dbPath string) ([]Thread, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("codexstate: open %s: %w", dbPath, err)
	}
	defer db.Close()

	rows, err := db.Query(threadsQuery)
	if err != nil {
		return nil, fmt.Errorf("codexstate: schema probe query failed: %w", err)
	}
	defer rows.Close()

	var threads []Thread
	for rows.Next() {
		var (
			t        Thread
			archived int64
			recency  int64
		)
		if err := rows.Scan(&t.ID, &t.Title, &t.CWD, &t.Model, &t.GitBranch, &archived, &recency, &t.RolloutPath); err != nil {
			return nil, fmt.Errorf("codexstate: scan threads row: %w", err)
		}
		t.Archived = archived != 0
		t.Recency = time.Unix(recency, 0).UTC()
		t.Source = SourceSQLite
		t.TokenCount = -1
		t.MessageCount = -1
		threads = append(threads, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("codexstate: iterate threads rows: %w", err)
	}
	return threads, nil
}
