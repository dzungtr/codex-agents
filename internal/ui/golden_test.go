package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// send applies a single message to a Model and returns the updated Model,
// mirroring the helper duplicated across model_test.go's table-driven tests.
func send(m Model, msg tea.Msg) Model {
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func TestGolden_InitialView(t *testing.T) {
	m := newFixtureModel()
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

func TestGolden_CursorMovedToSecondRow(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

func TestGolden_FilterNarrowsToAuth(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "auth" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

func TestGolden_HelpOverlay(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	teatest.RequireEqualOutput(t, []byte(m.View()))
}
