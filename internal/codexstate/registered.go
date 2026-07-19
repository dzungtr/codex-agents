package codexstate

import (
	"database/sql"
	"fmt"
	"path/filepath"
)

// ThreadRegistered reports whether codex already knows about threadID —
// i.e. the thread has appeared in codex's own records (the newest
// state_*.sqlite threads table, or the jsonl fallback when the schema
// probe fails). This is the registration-detection seam ADR 0003 decision 4
// rests on: cdxa spawn blocks until the freshly launched thread is
// resolvable via codex's own data, so the id it returns works immediately
// with cdxa output (issue #28) and the cockpit's list.
//
// Unlike LoadThreads, registration detection deliberately counts an
// archived thread as registered: codex knows about a thread the moment it
// writes any row, archived or not, and spawn's contract is "appears in
// codex's sqlite", not "appears in the cockpit's non-archived list".
//
// Read-only: codex's data is never written (ADR 0001 decision 2).
func ThreadRegistered(codexHome, threadID string) (bool, error) {
	// SQLite path: a direct existence check is cheaper than LoadThreads
	// (no enrichment, no recency sort, no full table scan into memory)
	// and survives the archived distinction by not filtering on it.
	dbPath, findErr := newestStateDB(codexHome)
	if findErr == nil {
		found, err := threadExists(dbPath, threadID)
		if err == nil {
			return found, nil
		}
		// Fall through to jsonl on a schema-probe failure, matching
		// LoadThreads' degradation posture (ADR 0001 decision 2).
	}

	threads, err := scanSessionsJSONL(filepath.Join(codexHome, sessionsDirName))
	if err != nil {
		return false, err
	}
	for _, th := range threads {
		if th.ID == threadID {
			return true, nil
		}
	}
	return false, nil
}

// threadExists runs a read-only existence check for one thread id against
// dbPath's threads table. It does not filter on archived, so an archived
// thread still counts as registered (see ThreadRegistered's doc comment).
// Any schema-probe failure (missing table/columns after a codex upgrade) is
// returned as an error so the caller can degrade to the jsonl fallback.
func threadExists(dbPath, threadID string) (bool, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return false, fmt.Errorf("codexstate: open %s: %w", dbPath, err)
	}
	defer db.Close()

	// The threads table's existence and the id column's presence are the
	// schema probe: a missing table or renamed column errors here, which
	// ThreadRegistered treats as "fall back to jsonl". A row that exists
	// but has an archived=1 flag is still a hit.
	var found int
	err = db.QueryRow(`SELECT 1 FROM threads WHERE id = ? LIMIT 1`, threadID).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("codexstate: registration probe failed: %w", err)
	}
	return true, nil
}

// ThreadByCWD returns the id of codex's thread whose cwd matches cwd, plus
// whether one was found. It mirrors ThreadRegistered's discovery posture —
// the newest state_*.sqlite first, the jsonl session scan as the
// schema-probe-failure fallback — but resolves by cwd rather than by id,
// so a launcher that has just created a worktree can discover the thread
// id codex assigned to it without already knowing that id.
//
// The cwd match is exact (after filepath.Clean on both sides). The
// worktree path a launcher just created is unique per launch (ADR 0001
// decision 4's branch-slug collision rule guarantees a distinct path), so
// the match is unambiguous in the launch path. Archived threads are
// included in the candidate set: codex knows about a thread the moment it
// writes any row, and a just-launched thread is never archived.
//
// Read-only: codex's data is never written (ADR 0001 decision 2).
func ThreadByCWD(codexHome, cwd string) (string, bool, error) {
	target := filepath.Clean(cwd)

	dbPath, findErr := newestStateDB(codexHome)
	if findErr == nil {
		id, found, err := threadIDByCWD(dbPath, target)
		if err == nil {
			return id, found, nil
		}
		// Fall through to jsonl on a schema-probe failure, matching
		// LoadThreads' degradation posture (ADR 0001 decision 2).
	}

	threads, err := scanSessionsJSONL(filepath.Join(codexHome, sessionsDirName))
	if err != nil {
		return "", false, err
	}
	for _, th := range threads {
		if filepath.Clean(th.CWD) == target {
			return th.ID, true, nil
		}
	}
	return "", false, nil
}

// threadIDByCWD runs a read-only SELECT for the thread id whose cwd matches
// target against dbPath's threads table. It does not filter on archived, so
// an archived thread is still a candidate (see ThreadByCWD's doc comment).
// Any schema-probe failure (missing table/columns after a codex upgrade) is
// returned as an error so the caller can degrade to the jsonl fallback.
func threadIDByCWD(dbPath, target string) (string, bool, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", false, fmt.Errorf("codexstate: open %s: %w", dbPath, err)
	}
	defer db.Close()

	var id string
	err = db.QueryRow(`SELECT id FROM threads WHERE cwd = ? LIMIT 1`, target).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("codexstate: cwd probe failed: %w", err)
	}
	return id, true, nil
}
