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
	if !strings.Contains(view, "profile: general-agentic") {
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
	if !strings.Contains(m.View(), "profile: design-session") {
		t.Fatalf("expected profile to cycle to design-session, got:\n%s", m.View())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "profile: review") {
		t.Fatalf("expected profile to cycle to review, got:\n%s", m.View())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "profile: general-agentic") {
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
	if strings.Contains(view, "cycle profile") {
		t.Fatalf("expected esc to close the composer entirely, got:\n%s", view)
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
	if strings.Contains(m.View(), "profile:") && strings.Contains(m.View(), "@ cycle") {
		t.Fatalf("expected composer to close after submit, got:\n%s", m.View())
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
