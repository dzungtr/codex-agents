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

// TestGolden_InitialView, TestGolden_CursorMovedToSecondRow and
// TestGolden_FilterNarrowsToAuth each send an explicit
// tea.WindowSizeMsg{Width: 80, Height: 24} first (issue #20 Testing
// Decisions item 1) instead of relying on listWidth's 80 fallback, so the
// golden fixture pins a width deliberately rather than by omission. Their
// goldens are regenerated for the new two-line layout — every row's shape
// changes, so this churn is the point of the slice, not a regression.
func TestGolden_InitialView(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

func TestGolden_CursorMovedToSecondRow(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

func TestGolden_FilterNarrowsToAuth(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "auth" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

// TestGolden_TwoLineNarrowWidth (issue #20 Testing Decisions item 3) pins a
// narrow-terminal fixture at Width: 60: it proves age right-aligns to
// m.width (not a fixed fallback) and that an overlong line 2 (the selected
// row's meta+detail) truncates deterministically instead of wrapping into
// a third terminal row.
func TestGolden_TwoLineNarrowWidth(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.WindowSizeMsg{Width: 60, Height: 24})
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
				Recency: base.Add(-3 * time.Minute), TokenCount: -1, MessageCount: -1,
			},
			Status: tmuxstatus.StatusWaiting,
		},
		{
			Thread: codexstate.Thread{
				ID: "m2", Title: "Repo only", CWD: "/Users/tony/infra-drainer",
				Model: "gpt-5-codex", GitBranch: "",
				Recency: base.Add(-45 * time.Minute), TokenCount: -1, MessageCount: -1,
			},
			Status: tmuxstatus.StatusWorking,
		},
		{
			Thread: codexstate.Thread{
				ID: "m3", Title: "Neither part present", CWD: "",
				Model: "gpt-5-codex", GitBranch: "",
				Recency: base.Add(-26 * time.Hour), TokenCount: -1, MessageCount: -1,
			},
			Status: tmuxstatus.StatusClosed,
		},
	}
}

func TestGolden_MetaOmitsMissingParts(t *testing.T) {
	m := New(metaOmitsMissingPartsRows()).WithClock(fixedNow)
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

// titleFallsBackToFirstMessageRows is a dedicated fixture for
// TestGolden_TitleFallsBackToFirstMessage, purpose-built so it doesn't
// disturb fixtureRows (and therefore the existing golden files): one row
// with a real codex Title (renders exactly as before), one untitled row
// whose multiline FirstMessage collapses to one line and is long enough to
// exercise renderRow's truncate(..., 42), and one untitled row with an
// empty FirstMessage (both empty -> blank title cell, unchanged behavior).
func titleFallsBackToFirstMessageRows() []Row {
	base := fixedNow()
	return []Row{
		{
			Thread: codexstate.Thread{
				ID: "f1", Title: "Add dark mode", CWD: "/Users/tony/web-app",
				Model: "gpt-5-codex", GitBranch: "add-dark-mode",
				FirstMessage: "please add a dark mode toggle to the settings page",
				Recency:      base.Add(-3 * time.Minute), TokenCount: -1, MessageCount: -1,
			},
			Status: tmuxstatus.StatusWaiting,
		},
		{
			Thread: codexstate.Thread{
				ID: "f2", Title: "", CWD: "/Users/tony/infra-drainer",
				Model: "gpt-5-codex", GitBranch: "refactor-drainer",
				FirstMessage: "please refactor the\ndrainer so it retries\nnetwork calls with backoff and jitter",
				Recency:      base.Add(-45 * time.Minute), TokenCount: -1, MessageCount: -1,
			},
			Status: tmuxstatus.StatusWorking,
		},
		{
			Thread: codexstate.Thread{
				ID: "f3", Title: "", CWD: "/Users/tony/web-app",
				Model: "gpt-5-codex", GitBranch: "fix-auth-hook",
				FirstMessage: "",
				Recency:      base.Add(-26 * time.Hour), TokenCount: -1, MessageCount: -1,
			},
			Status: tmuxstatus.StatusClosed,
		},
	}
}

func TestGolden_TitleFallsBackToFirstMessage(t *testing.T) {
	m := New(titleFallsBackToFirstMessageRows()).WithClock(fixedNow)
	teatest.RequireEqualOutput(t, []byte(m.View()))
}
