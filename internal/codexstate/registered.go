package codexstate

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

// errNoStateDB is wrapped by newestStateDB when no state_*.sqlite file
// is present under codexHome. Callers use errors.Is to distinguish a
// "codex has not been run yet" condition (a normal pre-registration
// state for a fresh install) from a real config/IO error.
var errNoStateDB = errors.New("no state database")

// errSchemaProbe is wrapped by threadIDsByCWD / threadExists when the
// sqlite probe fails because the threads table or its required
// columns are missing — i.e. codex has upgraded and the schema has
// drifted. Callers use errors.Is to trigger the jsonl fallback without
// hiding real IO errors.
var errSchemaProbe = errors.New("schema probe failed")

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
// ThreadByCWD returns the most-recent codex thread id whose cwd matches
// cwd, plus whether one was found. It is a thin shim over ThreadsByCWD
// that picks the first (most-recently-active) id. Most callers should
// prefer ThreadsByCWD so a launcher can wait for a freshly-registered
// id rather than latching onto a pre-existing match (issue #67).
func ThreadByCWD(codexHome, cwd string) (string, bool, error) {
	target := filepath.Clean(cwd)

	ids, err := ThreadsByCWD(codexHome, target)
	if err != nil || len(ids) == 0 {
		return "", false, err
	}
	return ids[0], true, nil
}

// ThreadsByCWD returns every codex thread id whose cwd matches cwd, in
// most-recent-first order (matches LoadThreads' recency ordering for
// consistency with the list view). It mirrors ThreadRegistered's
// discovery posture — the newest state_*.sqlite first, the jsonl
// session scan as the schema-probe-failure fallback — but resolves by
// cwd and returns the full set, so a launcher can snapshot the known
// ids before starting its tmux session and then wait for a new id
// rather than latching onto a pre-existing one (issue #67: consecutive
// in-place launches on the same cwd collided because the single-id
// resolver always returned the first launch's id).
//
// The cwd match is exact (after filepath.Clean on both sides). The
// worktree path a launcher just created is unique per launch (ADR 0001
// decision 4's branch-slug collision rule guarantees a distinct path),
// so the match is unambiguous in the worktree launch path. In-place
// launches share a cwd, so a snapshot is required to distinguish the
// freshly-registered id from any pre-existing one — this function is
// the snapshot's source.
//
// Archived threads are included: codex knows about a thread the moment
// it writes any row, and an archived thread sharing this cwd is still
// a known id that must be excluded from the registration poll. A
// just-launched thread is never archived.
//
// An empty codex home (no state_*.sqlite and no jsonl rollouts)
// returns (nil, nil) — a missing-records case is not an error.
// Schema-probe failures on the sqlite path degrade to the jsonl scan,
// matching LoadThreads' posture (ADR 0001 decision 2). Real IO errors
// (cannot glob, cannot open db) propagate.
//
// Read-only: codex's data is never written (ADR 0001 decision 2).
func ThreadsByCWD(codexHome, cwd string) ([]string, error) {
	target := filepath.Clean(cwd)

	dbPath, findErr := newestStateDB(codexHome)
	switch {
	case findErr == nil:
		ids, err := threadIDsByCWD(dbPath, target)
		if err == nil {
			return ids, nil
		}
		if !errors.Is(err, errSchemaProbe) {
			return nil, err
		}
		// Fall through to jsonl on a schema-probe failure, matching
		// LoadThreads' degradation posture (ADR 0001 decision 2).
	case !errors.Is(findErr, errNoStateDB):
		return nil, findErr
	}

	threads, err := scanSessionsJSONL(filepath.Join(codexHome, sessionsDirName))
	if err != nil {
		return nil, err
	}
	type matched struct {
		id      string
		recency time.Time
	}
	seen := make(map[string]struct{}, len(threads))
	matchedThreads := make([]matched, 0, len(threads))
	for _, th := range threads {
		if filepath.Clean(th.CWD) != target {
			continue
		}
		if _, dup := seen[th.ID]; dup {
			continue
		}
		seen[th.ID] = struct{}{}
		matchedThreads = append(matchedThreads, matched{id: th.ID, recency: th.Recency})
	}
	sort.SliceStable(matchedThreads, func(i, j int) bool {
		return matchedThreads[i].recency.After(matchedThreads[j].recency)
	})
	ids := make([]string, len(matchedThreads))
	for i, m := range matchedThreads {
		ids[i] = m.id
	}
	return ids, nil
}

// threadIDsByCWD runs a read-only SELECT for every thread id whose cwd
// matches target against dbPath's threads table, most-recent first. It
// does not filter on archived, so an archived thread is still a
// candidate (see ThreadsByCWD's doc comment). Any schema-probe failure
// (missing table/columns after a codex upgrade) is returned wrapped in
// errSchemaProbe so the caller can degrade to the jsonl fallback; real
// IO errors propagate unwrapped.
func threadIDsByCWD(dbPath, target string) ([]string, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("codexstate: open %s: %w", dbPath, err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, recency_at FROM threads WHERE cwd = ? ORDER BY recency_at DESC`, target)
	if err != nil {
		// A missing threads table or missing id/recency_at column
		// surfaces here — flag it as a schema-probe failure so the
		// caller can fall back to the jsonl scan instead of treating
		// it as a hard error.
		return nil, fmt.Errorf("codexstate: cwd probe failed: %w: %w", errSchemaProbe, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var (
			id        string
			recencyAt int64
		)
		if err := rows.Scan(&id, &recencyAt); err != nil {
			return nil, fmt.Errorf("codexstate: cwd probe scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("codexstate: cwd probe rows: %w", err)
	}
	return ids, nil
}
