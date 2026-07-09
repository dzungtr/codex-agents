package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
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

// metaOmitsMissingPartsRows is a dedicated fixture for
// TestGolden_MetaOmitsMissingParts, purpose-built so it doesn't disturb
// fixtureRows (and therefore the existing golden files, whose threads all
// have both a repo and a branch). It covers the three metaColumn cases from
// issue #18: both parts present, repo only, and neither part present.
func metaOmitsMissingPartsRows() []Row {
	base := fixedNow()
	return []Row{
		{
			Thread: codexstate.Thread{
				ID: "m1", Title: "Both parts present", CWD: "/Users/tony/web-app",
				Model: "gpt-5-codex", GitBranch: "add-dark-mode",
				Recency: base.Add(-3 * time.Minute), TokenCount: -1,
			},
			Status: tmuxstatus.StatusWaiting,
		},
		{
			Thread: codexstate.Thread{
				ID: "m2", Title: "Repo only", CWD: "/Users/tony/infra-drainer",
				Model: "gpt-5-codex", GitBranch: "",
				Recency: base.Add(-45 * time.Minute), TokenCount: -1,
			},
			Status: tmuxstatus.StatusWorking,
		},
		{
			Thread: codexstate.Thread{
				ID: "m3", Title: "Neither part present", CWD: "",
				Model: "gpt-5-codex", GitBranch: "",
				Recency: base.Add(-26 * time.Hour), TokenCount: -1,
			},
			Status: tmuxstatus.StatusClosed,
		},
	}
}

func TestGolden_MetaOmitsMissingParts(t *testing.T) {
	m := New(metaOmitsMissingPartsRows()).WithClock(fixedNow)
	teatest.RequireEqualOutput(t, []byte(m.View()))
}
