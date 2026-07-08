package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestReply_ROpensInputOnWaitingThread covers the primary quick-reply case
// from PRD #1's List behavior -> Quick reply row: `r` on a waiting thread
// opens the inline one-line input.
func TestReply_ROpensInputOnWaitingThread(t *testing.T) {
	m := newFixtureModel() // cursor starts on "Add dark mode" (StatusWaiting)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	view := m.View()
	if !strings.Contains(view, "reply>") {
		t.Fatalf("expected reply input prompt, got:\n%s", view)
	}
}

// TestReply_ROpensInputOnWorkingThread confirms the brief's "alive thread
// (working or waiting status)" scope: working threads can be quick-replied
// too, not just waiting ones.
func TestReply_ROpensInputOnWorkingThread(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}) // -> "Refactor drainer" (StatusWorking)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	view := m.View()
	if !strings.Contains(view, "reply>") {
		t.Fatalf("expected reply input prompt on a working thread, got:\n%s", view)
	}
}

// TestReply_RNoOpOnClosedThread is the explicit acceptance criterion:
// "Closed threads cannot be quick-replied."
func TestReply_RNoOpOnClosedThread(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}) // working
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}) // -> "Fix auth hook" (StatusClosed)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	view := m.View()
	if strings.Contains(view, "reply>") {
		t.Fatalf("expected 'r' to be a no-op on a closed thread, got:\n%s", view)
	}
}

func TestReply_TypingAppendsToReplyText(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	for _, r := range "proceed with option B" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !strings.Contains(m.View(), "proceed with option B") {
		t.Fatalf("expected typed reply text in view, got:\n%s", m.View())
	}
}

func TestReply_EscCancelsAndClosesInput(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	for _, r := range "proceed" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	view := m.View()
	if strings.Contains(view, "reply>") || strings.Contains(view, "proceed") {
		t.Fatalf("expected esc to discard and close the reply input, got:\n%s", view)
	}
}

func TestReply_EnterWithoutActionsIsNoop(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	for _, r := range "proceed" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.QuickReply is unset")
	}
	if strings.Contains(m.View(), "reply>") {
		t.Fatalf("expected the reply input to close even without an actions.QuickReply hook")
	}
}

func TestReply_EnterCallsQuickReplyWithThreadIDAndTextThenCloses(t *testing.T) {
	var gotThreadID, gotText string
	actions := Actions{QuickReply: func(threadID, text string) tea.Cmd {
		gotThreadID, gotText = threadID, text
		return func() tea.Msg { return QuickReplySentMsg{ThreadID: threadID} }
	}}
	m := newFixtureModel().WithActions(actions)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}) // selected row is "Add dark mode" (t2)
	for _, r := range "proceed with option B" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from submitting the reply")
	}
	if gotThreadID != "t2" || gotText != "proceed with option B" {
		t.Fatalf("QuickReply called with (%q, %q), want (\"t2\", \"proceed with option B\")", gotThreadID, gotText)
	}
	if strings.Contains(m.View(), "reply>") {
		t.Fatalf("expected reply input to close after submit, got:\n%s", m.View())
	}
}

func TestReply_BlankTextDoesNotCallQuickReply(t *testing.T) {
	called := false
	actions := Actions{QuickReply: func(threadID, text string) tea.Cmd {
		called = true
		return nil
	}}
	m := newFixtureModel().WithActions(actions)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = updated.(Model)
	if called {
		t.Fatalf("expected blank reply text not to call QuickReply")
	}
}

func TestUpdate_QuickReplySentMsg_SetsStatusLineAndTriggersRefresh(t *testing.T) {
	refreshCalled := false
	actions := Actions{Refresh: func() tea.Cmd {
		refreshCalled = true
		return func() tea.Msg { return RowsRefreshedMsg{} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(QuickReplySentMsg{ThreadID: "t2"})
	m = updated.(Model)
	if !refreshCalled {
		t.Fatalf("expected QuickReplySentMsg to trigger Refresh")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from QuickReplySentMsg")
	}
	if !strings.Contains(m.View(), "sent reply") {
		t.Fatalf("expected a status line confirming the reply was sent, got:\n%s", m.View())
	}
}
