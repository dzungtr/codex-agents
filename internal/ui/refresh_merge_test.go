package ui

import (
	"strings"
	"testing"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// TestUpdate_RowsRefreshedMsg_KeepsJustLaunchedRowNotYetPersisted is the
// regression test for issue #25 Bug 1: after a composer launch, any
// Refresh that fires before codex has persisted the new thread's record
// (codex writes session_meta/threads-table rows asynchronously, seconds
// later) comes back with Rows that omit the launched thread. The previous
// wholesale replace silently dropped the launched row until restart; the
// merge must keep it.
func TestUpdate_RowsRefreshedMsg_KeepsJustLaunchedRowNotYetPersisted(t *testing.T) {
	m := newFixtureModel()
	newRow := Row{Thread: codexstate.Thread{ID: "new1", Title: "Brand new thread"}, Status: tmuxstatus.StatusWorking}
	m = send(m, ThreadLaunchedMsg{Row: newRow})

	// A refresh fires (e.g. attach-then-detach seconds after launch) with
	// rows from codex's state — which doesn't know about "new1" yet.
	m = send(m, RowsRefreshedMsg{Rows: fixtureRows()})

	if !hasRow(m, "new1") {
		t.Fatalf("expected launched row to survive a refresh that predates codex persistence; rows: %v", rowIDs(m))
	}
	// The dot-prefixed assertion pins the row itself, not the "launched
	// ..." status line, which survives the refresh and contains the same
	// title text.
	if view := m.View(); !strings.Contains(view, "◐ Brand new thread") {
		t.Fatalf("expected launched row rendered in view, got:\n%s", view)
	}
	// The refreshed rows are all still there too.
	if !hasRow(m, "t1") || !hasRow(m, "t2") || !hasRow(m, "t3") {
		t.Fatalf("expected refreshed rows to remain; rows: %v", rowIDs(m))
	}
}

// TestUpdate_RowsRefreshedMsg_RefreshedRowsWin confirms the merge keeps
// refresh authoritative for threads it *does* contain: an upstream change
// (e.g. a working -> waiting status flip picked up by loadRows) must land
// in the model even when the thread also exists locally.
func TestUpdate_RowsRefreshedMsg_RefreshedRowsWin(t *testing.T) {
	m := newFixtureModel()
	refreshed := fixtureRows()
	for i := range refreshed {
		if refreshed[i].Thread.ID == "t3" {
			refreshed[i].Status = tmuxstatus.StatusWaiting
			refreshed[i].Thread.Title = "Refactor drainer (done)"
		}
	}
	m = send(m, RowsRefreshedMsg{Rows: refreshed})

	view := m.View()
	if !strings.Contains(view, "Refactor drainer (done)") {
		t.Fatalf("expected refreshed version of the row to win, got:\n%s", view)
	}
	if strings.Contains(view, "◐ Refactor drainer") {
		t.Fatalf("expected stale working status to be replaced, got:\n%s", view)
	}
}

// TestUpdate_RowsRefreshedMsg_DoesNotResurrectArchivedRow guards the other
// half of the merge: an archived thread also disappears from msg.Rows
// (loadRows filters archived rows upstream), so the keep-missing rule must
// exclude threads the model already saw archived via ArchiveDoneMsg —
// otherwise archiving a thread would bring its row straight back on the
// next refresh.
func TestUpdate_RowsRefreshedMsg_DoesNotResurrectArchivedRow(t *testing.T) {
	m := newFixtureModel()
	m = send(m, ArchiveDoneMsg{ThreadID: "t2", Note: "archived"})

	// Refresh arrives carrying the remaining (non-archived) rows: t2 is
	// "missing" from the set, but because it was archived, not because it
	// is unpersisted.
	m = send(m, RowsRefreshedMsg{Rows: []Row{fixtureRows()[1], fixtureRows()[2]}})

	if hasRow(m, "t2") {
		t.Fatalf("expected archived row to stay gone after refresh; rows: %v", rowIDs(m))
	}
	if view := m.View(); strings.Contains(view, "● Add dark mode") {
		t.Fatalf("expected archived row to stay out of the view, got:\n%s", view)
	}
	if !hasRow(m, "t1") || !hasRow(m, "t3") {
		t.Fatalf("expected other rows to remain; rows: %v", rowIDs(m))
	}
}

func hasRow(m Model, threadID string) bool {
	for _, r := range m.rows {
		if r.Thread.ID == threadID {
			return true
		}
	}
	return false
}

func rowIDs(m Model) []string {
	ids := make([]string, 0, len(m.rows))
	for _, r := range m.rows {
		ids = append(ids, r.Thread.ID)
	}
	return ids
}
