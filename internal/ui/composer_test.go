package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

func TestComposer_IFocusesAndShowsDefaultProfile(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	view := m.View()
	if !strings.Contains(view, "[general-agentic]") {
		t.Fatalf("expected composer to default to general-agentic, got:\n%s", view)
	}
}

func TestComposer_TypingAppendsToTask(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range "fix bug" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !strings.Contains(m.View(), "fix bug") {
		t.Fatalf("expected composer text 'fix bug' in view, got:\n%s", m.View())
	}
}

func TestComposer_AtCyclesProfile(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "[design-session]") {
		t.Fatalf("expected profile to cycle to design-session, got:\n%s", m.View())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "[review]") {
		t.Fatalf("expected profile to cycle to review, got:\n%s", m.View())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "[general-agentic]") {
		t.Fatalf("expected profile to cycle back to general-agentic, got:\n%s", m.View())
	}
}

func TestComposer_EscCancelsAndClearsTask(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range "fix bug" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	view := m.View()
	if strings.Contains(view, "fix bug") {
		t.Fatalf("expected esc to discard composer task, got:\n%s", view)
	}
	// The composer bar is persistent (design drift gap 4), so "closed"
	// means it reverts to the idle placeholder rather than disappearing.
	if !strings.Contains(view, "Describe a task and press Enter to launch a thread") {
		t.Fatalf("expected esc to revert the composer bar to its placeholder, got:\n%s", view)
	}
}

func TestComposer_EnterWithoutActionsIsNoop(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range "fix bug" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.Launch is unset")
	}
	if strings.Contains(m.View(), "fix bug") {
		t.Fatalf("expected composer to close even without an actions.Launch hook")
	}
}

func TestComposer_EnterCallsLaunchWithTaskAndProfileThenCloses(t *testing.T) {
	var gotTask, gotProfile string
	actions := Actions{Launch: func(task, profile string) tea.Cmd {
		gotTask, gotProfile = task, profile
		return func() tea.Msg { return ThreadLaunchedMsg{} }
	}}
	m := newFixtureModel().WithActions(actions)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range "fix bug" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")}) // -> design-session
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from submitting the composer")
	}
	if gotTask != "fix bug" || gotProfile != "design-session" {
		t.Fatalf("Launch called with (%q, %q), want (\"fix bug\", \"design-session\")", gotTask, gotProfile)
	}
	// The composer bar is persistent (design drift gap 4): "closed" means
	// the task text clears and the profile resets, not that the bar itself
	// disappears.
	view := m.View()
	if strings.Contains(view, "fix bug") {
		t.Fatalf("expected composer task to clear after submit, got:\n%s", view)
	}
	if !strings.Contains(view, "[general-agentic]") {
		t.Fatalf("expected composer profile to reset to general-agentic after submit, got:\n%s", view)
	}
}

// TestUpdate_ThreadLaunchedMsg_InsertsRowAtTopOfWorkingGroup reflects issue
// #4's ordering rule: a freshly launched thread is StatusWorking, so it
// sorts above other working/closed rows (it's stamped with "now", the most
// recent possible) but still *below* any StatusWaiting row — a working
// thread never outranks one that's actually waiting on the user, per PRD
// #1's List behavior -> Ordering row.
func TestUpdate_ThreadLaunchedMsg_InsertsRowAtTopOfWorkingGroup(t *testing.T) {
	m := newFixtureModel()
	newRow := Row{Thread: codexstate.Thread{ID: "new1", Title: "Brand new thread"}, Status: tmuxstatus.StatusWorking}
	updated, _ := m.Update(ThreadLaunchedMsg{Row: newRow})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "Brand new thread") {
		t.Fatalf("expected new row in view, got:\n%s", view)
	}
	idxWaiting := strings.Index(view, "Add dark mode")           // StatusWaiting: stays above
	idxNew := strings.Index(view, "◐ Brand new thread")          // the row itself, not the "launched ..." status line above the list
	idxOlderWorking := strings.Index(view, "◐ Refactor drainer") // StatusWorking, older
	if idxWaiting == -1 || idxNew == -1 || idxOlderWorking == -1 {
		t.Fatalf("expected all three titles in view, got:\n%s", view)
	}
	if !(idxWaiting < idxNew && idxNew < idxOlderWorking) {
		t.Fatalf("expected order [waiting group, new working row, older working row], view:\n%s", view)
	}
}

// TestUpdate_ThreadLaunchedMsg_TriggersCheckLiveness reflects the fix for
// "compose task -> Enter -> row stays stuck showing working forever even
// after the tmux session has already died": the optimistic insert can't
// safely be followed by a full Refresh (codex may not have written this
// thread's own record yet, and Refresh replaces the whole row set from
// that record — see Actions.CheckLiveness's doc comment), so a launch
// instead schedules a narrower liveness recheck for just this thread.
func TestUpdate_ThreadLaunchedMsg_TriggersCheckLiveness(t *testing.T) {
	var gotThreadID string
	called := false
	actions := Actions{CheckLiveness: func(threadID string) tea.Cmd {
		called = true
		gotThreadID = threadID
		return func() tea.Msg { return ThreadLivenessMsg{ThreadID: threadID, Status: tmuxstatus.StatusClosed} }
	}}
	m := newFixtureModel().WithActions(actions)
	newRow := Row{Thread: codexstate.Thread{ID: "new1", Title: "Brand new thread"}, Status: tmuxstatus.StatusWorking}
	updated, cmd := m.Update(ThreadLaunchedMsg{Row: newRow})
	_ = updated.(Model)
	if !called {
		t.Fatalf("expected ThreadLaunchedMsg to trigger CheckLiveness")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from ThreadLaunchedMsg")
	}
	if gotThreadID != "new1" {
		t.Fatalf("CheckLiveness called with %q, want new1", gotThreadID)
	}
}

func TestUpdate_ThreadLaunchedMsg_WithoutCheckLivenessIsStillANoopCmd(t *testing.T) {
	m := newFixtureModel()
	newRow := Row{Thread: codexstate.Thread{ID: "new1", Title: "Brand new thread"}, Status: tmuxstatus.StatusWorking}
	updated, cmd := m.Update(ThreadLaunchedMsg{Row: newRow})
	_ = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.CheckLiveness is unset")
	}
}

// TestUpdate_ThreadLivenessMsg_UpdatesJustThatRowsStatus is the other half
// of the fix: once CheckLiveness reports the thread actually died, the row
// should flip to Closed in place — not disappear (unlike a RowsRefreshedMsg
// replace) and not require a full reload.
func TestUpdate_ThreadLivenessMsg_UpdatesJustThatRowsStatus(t *testing.T) {
	m := newFixtureModel()
	// t2 ("Add dark mode") is StatusWaiting in the fixture; assert only its
	// Status field flips, everything else about the list stays put.
	updated, cmd := m.Update(ThreadLivenessMsg{ThreadID: "t2", Status: tmuxstatus.StatusClosed})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected ThreadLivenessMsg to yield no further Cmd, got %v", cmd)
	}
	view := m.View()
	if !strings.Contains(view, "Add dark mode") {
		t.Fatalf("expected the row to remain in the list, got:\n%s", view)
	}
	found := false
	for _, r := range m.rows {
		if r.Thread.ID == "t2" {
			found = true
			if r.Status != tmuxstatus.StatusClosed {
				t.Fatalf("expected t2's Status to become Closed, got %v", r.Status)
			}
		}
	}
	if !found {
		t.Fatalf("expected row t2 to still be present")
	}
}

func TestUpdate_ThreadLivenessMsg_UnknownThreadIDIsNoop(t *testing.T) {
	m := newFixtureModel()
	before := append([]Row(nil), m.rows...)
	updated, _ := m.Update(ThreadLivenessMsg{ThreadID: "does-not-exist", Status: tmuxstatus.StatusClosed})
	m = updated.(Model)
	if len(m.rows) != len(before) {
		t.Fatalf("expected row count unchanged, got %d want %d", len(m.rows), len(before))
	}
}

func TestUpdate_ThreadLaunchErrorMsg_ShowsErrorLine(t *testing.T) {
	m := newFixtureModel()
	updated, _ := m.Update(ThreadLaunchErrorMsg{Err: errors.New("boom")})
	m = updated.(Model)
	if !strings.Contains(m.View(), "boom") {
		t.Fatalf("expected error text in view, got:\n%s", m.View())
	}
}

func TestUpdate_EnterOnRow_CallsAttachWithSelectedRow(t *testing.T) {
	var gotRow Row
	called := false
	actions := Actions{Attach: func(row Row) tea.Cmd {
		called = true
		gotRow = row
		return func() tea.Msg { return AttachDoneMsg{} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	_ = m
	if !called {
		t.Fatalf("expected Attach to be called")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from Enter on a row")
	}
	if gotRow.Thread.Title != "Add dark mode" {
		t.Fatalf("expected Attach called with the selected row, got %+v", gotRow)
	}
}

func TestUpdate_EnterOnRow_WithoutActionsIsNoop(t *testing.T) {
	m := newFixtureModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.Attach is unset")
	}
}

func TestUpdate_AttachDoneMsg_TriggersRefresh(t *testing.T) {
	refreshCalled := false
	actions := Actions{Refresh: func() tea.Cmd {
		refreshCalled = true
		return func() tea.Msg { return RowsRefreshedMsg{} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(AttachDoneMsg{})
	_ = updated.(Model)
	if !refreshCalled {
		t.Fatalf("expected AttachDoneMsg to trigger Refresh")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from AttachDoneMsg")
	}
}

func TestUpdate_XOnRow_CallsInterruptWithSelectedRow(t *testing.T) {
	var gotRow Row
	called := false
	actions := Actions{Interrupt: func(row Row) tea.Cmd {
		called = true
		gotRow = row
		return func() tea.Msg { return InterruptDoneMsg{ThreadID: row.Thread.ID} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	_ = updated.(Model)
	if !called {
		t.Fatalf("expected Interrupt to be called")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from 'x' on a row")
	}
	if gotRow.Thread.Title != "Add dark mode" {
		t.Fatalf("expected Interrupt called with the selected row, got %+v", gotRow)
	}
}

func TestUpdate_XOnRow_WithoutActionsIsNoop(t *testing.T) {
	m := newFixtureModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	_ = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.Interrupt is unset")
	}
}

func TestUpdate_InterruptDoneMsg_ShowsStatusLineAndTriggersRefresh(t *testing.T) {
	refreshCalled := false
	actions := Actions{Refresh: func() tea.Cmd {
		refreshCalled = true
		return func() tea.Msg { return RowsRefreshedMsg{} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(InterruptDoneMsg{ThreadID: "t2"})
	m = updated.(Model)
	if !refreshCalled {
		t.Fatalf("expected InterruptDoneMsg to trigger Refresh")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from InterruptDoneMsg")
	}
	if !strings.Contains(m.View(), "interrupted Add dark mode") {
		t.Fatalf("expected status line naming the interrupted thread, got:\n%s", m.View())
	}
}

func TestUpdate_AOnRow_CallsArchiveWithSelectedRow(t *testing.T) {
	var gotRow Row
	called := false
	actions := Actions{Archive: func(row Row) tea.Cmd {
		called = true
		gotRow = row
		return func() tea.Msg { return ArchiveDoneMsg{ThreadID: row.Thread.ID, Note: "archived " + row.Thread.Title} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	_ = updated.(Model)
	if !called {
		t.Fatalf("expected Archive to be called")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from 'a' on a row")
	}
	if gotRow.Thread.Title != "Add dark mode" {
		t.Fatalf("expected Archive called with the selected row, got %+v", gotRow)
	}
}

func TestUpdate_AOnRow_WithoutActionsIsNoop(t *testing.T) {
	m := newFixtureModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	_ = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.Archive is unset")
	}
}

func TestUpdate_ArchiveDoneMsg_RemovesRowAndShowsNote(t *testing.T) {
	m := newFixtureModel()
	updated, _ := m.Update(ArchiveDoneMsg{ThreadID: "t2", Note: "archived; worktree removed"})
	m = updated.(Model)
	view := m.View()
	if strings.Contains(view, "Add dark mode") {
		t.Fatalf("expected archived thread's row to disappear from the list, got:\n%s", view)
	}
	if !strings.Contains(view, "archived; worktree removed") {
		t.Fatalf("expected archive note in status line, got:\n%s", view)
	}
	// The other two fixture rows are untouched.
	if !strings.Contains(view, "Refactor drainer") || !strings.Contains(view, "Fix auth hook") {
		t.Fatalf("expected other rows to remain, got:\n%s", view)
	}
}

func TestUpdate_RowsRefreshedMsg_ReplacesRows(t *testing.T) {
	m := newFixtureModel()
	newRows := []Row{{Thread: codexstate.Thread{ID: "only1", Title: "Only thread"}, Status: tmuxstatus.StatusClosed}}
	updated, _ := m.Update(RowsRefreshedMsg{Rows: newRows})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "Only thread") {
		t.Fatalf("expected replaced rows in view, got:\n%s", view)
	}
	if strings.Contains(view, "Add dark mode") {
		t.Fatalf("expected old rows to be gone after refresh, got:\n%s", view)
	}
}
