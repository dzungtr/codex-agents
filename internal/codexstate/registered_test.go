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
