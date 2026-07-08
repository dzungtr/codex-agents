// Package ui implements the read-only codex thread list screen: a
// bubbletea model over rows produced by internal/codexstate (thread data)
// and internal/tmuxstatus (open/closed liveness). It renders per the style
// contract in Codex-Orchestrator-TUI/index.html, adapted to a terminal
// list: status dot, title, repo·branch, age, a detail line on the selected
// row, a `/` filter, and a `?` help overlay.
package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// Row is a single list entry: a codex thread plus its liveness-derived
// open/closed status.
type Row struct {
	Thread codexstate.Thread
	Status tmuxstatus.Status
}

// Model is the bubbletea model for the thread list screen. It owns no
// codex-specific or tmux-specific knowledge itself — it only renders Rows
// it's given and handles list navigation, filtering, and the help overlay.
type Model struct {
	rows    []Row // expected pre-sorted most-recent-first by the caller
	visible []int // indices into rows matching the current filter
	cursor  int   // index into visible

	filtering   bool
	filterQuery string
	showHelp    bool

	width, height int
	quitting      bool

	now func() time.Time // injectable clock; tests override for deterministic "age" rendering
}

// New builds a list Model from rows already ordered most-recent-first.
func New(rows []Row) Model {
	m := Model{rows: rows, now: time.Now}
	m.applyFilter()
	return m
}

// WithClock overrides the model's clock. Intended for tests that need
// deterministic "age" output; production code leaves this as time.Now.
func (m Model) WithClock(now func() time.Time) Model {
	m.now = now
	return m
}

// Quitting reports whether the user has asked to quit (q / ctrl+c).
func (m Model) Quitting() bool { return m.quitting }

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showHelp {
		switch msg.String() {
		case "?", "esc", "q", "ctrl+c":
			m.showHelp = false
		}
		return m, nil
	}

	if m.filtering {
		switch msg.Type {
		case tea.KeyEsc:
			m.filtering = false
			m.filterQuery = ""
			m.applyFilter()
		case tea.KeyEnter:
			m.filtering = false
		case tea.KeyBackspace:
			if runes := []rune(m.filterQuery); len(runes) > 0 {
				m.filterQuery = string(runes[:len(runes)-1])
			}
			m.applyFilter()
		case tea.KeySpace:
			m.filterQuery += " "
			m.applyFilter()
		case tea.KeyRunes:
			m.filterQuery += string(msg.Runes)
			m.applyFilter()
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "/":
		m.filtering = true
	case "?":
		m.showHelp = true
	}
	return m, nil
}

// applyFilter recomputes m.visible from m.rows and m.filterQuery, matching
// over title + repo + branch per the PRD's list-behavior contract. Clamps
// the cursor into range afterward so a narrowing filter can't leave it
// pointing past the end of the visible set.
func (m *Model) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(m.filterQuery))
	visible := make([]int, 0, len(m.rows))
	for i, r := range m.rows {
		if query == "" || matchesQuery(r, query) {
			visible = append(visible, i)
		}
	}
	m.visible = visible
	if m.cursor >= len(m.visible) {
		m.cursor = len(m.visible) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func matchesQuery(r Row, query string) bool {
	return strings.Contains(strings.ToLower(r.Thread.Title), query) ||
		strings.Contains(strings.ToLower(r.Thread.Repo()), query) ||
		strings.Contains(strings.ToLower(r.Thread.GitBranch), query)
}

func (m *Model) moveCursor(delta int) {
	if len(m.visible) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.visible)-1 {
		m.cursor = len(m.visible) - 1
	}
}
