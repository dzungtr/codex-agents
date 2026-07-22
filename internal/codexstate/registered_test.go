package codexstate

import (
	"fmt"
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

func TestThreadByCWD_FoundInNewestSQLite(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "spawned-by-cwd", Title: "Spawned", CWD: "/repo/.worktrees/fix-auth-hook", Model: "m", GitBranch: "main", RecencyAgo: time.Second},
	})

	id, found, err := ThreadByCWD(dir, "/repo/.worktrees/fix-auth-hook")
	if err != nil {
		t.Fatalf("ThreadByCWD: %v", err)
	}
	if !found {
		t.Fatalf("expected to find a thread by cwd, got not found")
	}
	if id != "spawned-by-cwd" {
		t.Errorf("id = %q, want spawned-by-cwd", id)
	}
}

func TestThreadByCWD_NotYetPresent(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "other-1", Title: "Other", CWD: "/repo/.worktrees/other", Model: "m", GitBranch: "main", RecencyAgo: time.Second},
	})

	id, found, err := ThreadByCWD(dir, "/repo/.worktrees/fix-auth-hook")
	if err != nil {
		t.Fatalf("ThreadByCWD: %v", err)
	}
	if found {
		t.Errorf("expected not found, got id=%q", id)
	}
}

func TestThreadByCWD_ArchivedStillResolvable(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "archived-cwd", Title: "Archived", CWD: "/repo/.worktrees/old", Model: "m", GitBranch: "main", Archived: true, RecencyAgo: time.Second},
	})

	id, found, err := ThreadByCWD(dir, "/repo/.worktrees/old")
	if err != nil {
		t.Fatalf("ThreadByCWD: %v", err)
	}
	if !found || id != "archived-cwd" {
		t.Errorf("expected archived-cwd resolvable by cwd, got id=%q found=%v", id, found)
	}
}

func TestThreadByCWD_NormalizesCWD(t *testing.T) {
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "norm-1", Title: "Norm", CWD: "/repo/.worktrees/x", Model: "m", GitBranch: "main", RecencyAgo: time.Second},
	})

	// A trailing-slash cwd must still match the stored (clean) path.
	id, found, err := ThreadByCWD(dir, "/repo/.worktrees/x/")
	if err != nil {
		t.Fatalf("ThreadByCWD: %v", err)
	}
	if !found || id != "norm-1" {
		t.Errorf("expected norm-1 via trailing-slash cwd, got id=%q found=%v", id, found)
	}
}

func TestThreadByCWD_NoStateDBFallsBackToJSONL(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := dir + "/sessions"
	writeRolloutFile(t, sessionsDir+"/rollout.jsonl", sessionMetaPayload{
		ID:    "jsonl-by-cwd",
		Title: "JSONL",
		CWD:   "/repo/.worktrees/jsonl",
		Model: "m",
	}, 0, false)

	id, found, err := ThreadByCWD(dir, "/repo/.worktrees/jsonl")
	if err != nil {
		t.Fatalf("ThreadByCWD: %v", err)
	}
	if !found || id != "jsonl-by-cwd" {
		t.Errorf("expected jsonl-by-cwd via jsonl fallback, got id=%q found=%v", id, found)
	}
}

func TestThreadByCWD_NoDataAtAllIsNotFound(t *testing.T) {
	dir := t.TempDir()
	id, found, err := ThreadByCWD(dir, "/repo/.worktrees/whatever")
	if err != nil {
		t.Fatalf("ThreadByCWD on empty codexHome: %v", err)
	}
	if found {
		t.Errorf("expected not found when no codex data exists at all, got id=%q", id)
	}
}

func TestThreadsByCWD_ReturnsAllMatchingIDsInNewestSQLite(t *testing.T) {
	// Two prior threads already live for this cwd; a third, just-
	// registered one will appear after the snapshot. ThreadsByCWD must
	// return *all* known ids, recency-sorted, so the launcher can
	// snapshot them and wait for a new id distinct from either.
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "older", Title: "Older", CWD: "/repo", Model: "m", GitBranch: "main", RecencyAgo: 3 * time.Second},
		{ID: "newer", Title: "Newer", CWD: "/repo", Model: "m", GitBranch: "main", RecencyAgo: time.Second},
		{ID: "other", Title: "Other", CWD: "/elsewhere", Model: "m", GitBranch: "main", RecencyAgo: time.Second},
	})

	got, err := ThreadsByCWD(dir, "/repo")
	if err != nil {
		t.Fatalf("ThreadsByCWD: %v", err)
	}
	want := []string{"newer", "older"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ThreadsByCWD(/repo) = %v, want %v", got, want)
	}
}

func TestThreadsByCWD_ArchivedThreadsAreIncluded(t *testing.T) {
	// A thread codex has archived is still one codex knows about —
	// registration-detection (and therefore the pre-launch snapshot)
	// must include archived rows so an in-place launch waiting for a
	// new id is not lulled into accepting the archived one.
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "archived-cwd", Title: "Archived", CWD: "/repo", Model: "m", GitBranch: "main", Archived: true, RecencyAgo: time.Second},
	})

	got, err := ThreadsByCWD(dir, "/repo")
	if err != nil {
		t.Fatalf("ThreadsByCWD: %v", err)
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{"archived-cwd"}) {
		t.Errorf("ThreadsByCWD = %v, want [archived-cwd]", got)
	}
}

func TestThreadsByCWD_NoMatchesReturnsNilNil(t *testing.T) {
	// A non-empty codexHome with rows for *other* cwds must return an
	// empty slice (and a nil error) — not a false-positive "not
	// registered" because the cwd is merely unfamiliar.
	dir := t.TempDir()
	buildFixtureDB(t, dir, "state_5.sqlite", []fixtureThread{
		{ID: "other-1", Title: "Other", CWD: "/elsewhere", Model: "m", GitBranch: "main", RecencyAgo: time.Second},
	})

	got, err := ThreadsByCWD(dir, "/repo")
	if err != nil {
		t.Fatalf("ThreadsByCWD: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ThreadsByCWD = %v, want empty slice", got)
	}
}

func TestThreadsByCWD_NoDataAtAllReturnsNilNil(t *testing.T) {
	// A missing codexHome is not an error: codex has simply not been
	// run yet. The launcher's pre-launch snapshot gets an empty
	// exclusion set, which is correct (no prior ids to exclude).
	dir := t.TempDir()
	got, err := ThreadsByCWD(dir, "/repo")
	if err != nil {
		t.Fatalf("ThreadsByCWD on empty codexHome: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ThreadsByCWD = %v, want empty slice", got)
	}
}

func TestThreadsByCWD_NoStateDBFallsBackToJSONL(t *testing.T) {
	// When no state_*.sqlite exists, the jsonl scan must succeed and
	// return the matching id. (Issue #67's in-place path runs in cwd
	// that is also a non-git dir with no prior state DB, so this is
	// the common-case first-launch path.)
	dir := t.TempDir()
	sessionsDir := dir + "/sessions"
	writeRolloutFile(t, sessionsDir+"/rollout.jsonl", sessionMetaPayload{
		ID:    "jsonl-by-cwd",
		Title: "JSONL",
		CWD:   "/repo",
		Model: "m",
	}, 0, false)

	got, err := ThreadsByCWD(dir, "/repo")
	if err != nil {
		t.Fatalf("ThreadsByCWD: %v", err)
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{"jsonl-by-cwd"}) {
		t.Errorf("ThreadsByCWD = %v, want [jsonl-by-cwd]", got)
	}
}

func TestThreadsByCWD_SchemaProbeFailureDegradesToJSONL(t *testing.T) {
	// A drifted schema (missing recency_at column) must trigger the
	// jsonl fallback, matching LoadThreads' degradation posture. The
	// alternative — propagating the schema-probe error — would block
	// every launch on a codex upgrade until the cockpit's sqlite
	// reader is updated.
	dir := t.TempDir()
	buildLegacySchemaDB(t, dir, "state_5.sqlite")
	sessionsDir := dir + "/sessions"
	writeRolloutFile(t, sessionsDir+"/rollout.jsonl", sessionMetaPayload{
		ID:    "jsonl-after-schema-drift",
		Title: "JSONL",
		CWD:   "/repo",
		Model: "m",
	}, 0, false)

	got, err := ThreadsByCWD(dir, "/repo")
	if err != nil {
		t.Fatalf("ThreadsByCWD: %v", err)
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{"jsonl-after-schema-drift"}) {
		t.Errorf("ThreadsByCWD = %v, want [jsonl-after-schema-drift]", got)
	}
}
