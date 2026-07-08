package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// Status dot glyphs, per the style contract's status-dot styling
// (Codex-Orchestrator-TUI/index.html): distinct glyphs so the three states
// are still distinguishable in a colorless terminal, not just by color.
// waiting uses the same filled dot the old two-state "open" status used, so
// it reads as "the alive state that most wants your attention"; working
// uses a half-fill to suggest "in progress"; closed stays hollow.
//
// The style contract calls for a pulsing animation on alive rows; this
// static render doesn't attempt that (no continuous re-render loop exists
// to drive it) — color/glyph differentiation carries the same signal here.
const (
	dotWaiting = "●"
	dotWorking = "◐"
	dotClosed  = "○"
)

var (
	selectedStyle   = lipgloss.NewStyle().Reverse(true)
	detailStyle     = lipgloss.NewStyle().Faint(true)
	helpKeyStyle    = lipgloss.NewStyle().Bold(true)
	waitingDotStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	workingDotStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
	closedDotStyle  = lipgloss.NewStyle().Faint(true)
)

func (m Model) View() string {
	if m.showHelp {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(m.headerLine())
	if m.statusLine != "" && !m.composerFocused {
		b.WriteString("\n")
		b.WriteString(detailStyle.Render(m.statusLine))
	}
	b.WriteString("\n\n")
	b.WriteString(m.listView())
	b.WriteString("\n\n")
	b.WriteString(m.footerLine())
	return b.String()
}

// headerLine shows the composer prompt while focused, the filter prompt
// while filtering, otherwise a summary of total threads and how many are
// waiting/working (PRD #1's List behavior -> Statuses row: these are the
// two "alive" states; closed threads aren't attention-worthy so they're
// left out of the summary the same way the old open/closed count was).
func (m Model) headerLine() string {
	if m.composerFocused {
		return fmt.Sprintf("> %s_  [profile: %s]  (@ cycle profile · enter launch · esc cancel)", m.composerTask, m.composerProfile())
	}
	if m.filtering {
		return "/" + m.filterQuery
	}
	total := len(m.rows)
	waiting, working := 0, 0
	for _, r := range m.rows {
		switch r.Status {
		case tmuxstatus.StatusWaiting:
			waiting++
		case tmuxstatus.StatusWorking:
			working++
		}
	}
	noun := "threads"
	if total == 1 {
		noun = "thread"
	}
	return fmt.Sprintf("%d %s · %d waiting · %d working", total, noun, waiting, working)
}

func (m Model) listView() string {
	if len(m.rows) == 0 {
		return "No codex threads found."
	}
	if len(m.visible) == 0 {
		return "No threads match."
	}

	now := m.now()
	var b strings.Builder
	for vi, idx := range m.visible {
		row := m.rows[idx]
		selected := vi == m.cursor
		b.WriteString(renderRow(row, selected, now))
		if selected {
			b.WriteString("\n")
			b.WriteString(renderDetail(row))
		}
		if vi != len(m.visible)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// statusDot renders the status-dot glyph for r.Status, styled per the
// style contract (see the dot glyph constants' doc comment for why a
// static render can't reproduce the "pulse for alive" animation).
func statusDot(s tmuxstatus.Status) string {
	switch s {
	case tmuxstatus.StatusWaiting:
		return waitingDotStyle.Render(dotWaiting)
	case tmuxstatus.StatusWorking:
		return workingDotStyle.Render(dotWorking)
	default:
		return closedDotStyle.Render(dotClosed)
	}
}

func renderRow(r Row, selected bool, now time.Time) string {
	cursor := "  "
	if selected {
		cursor = "› "
	}
	meta := fmt.Sprintf("%s · %s", r.Thread.Repo(), r.Thread.GitBranch)
	line := fmt.Sprintf("%s%s %-42s %-28s %5s", cursor, statusDot(r.Status), truncate(r.Thread.Title, 42), truncate(meta, 28), ageString(now, r.Thread.Recency))
	if selected {
		return selectedStyle.Render(line)
	}
	return line
}

func renderDetail(r Row) string {
	profile := r.Thread.Profile
	if profile == "" {
		profile = "-"
	}
	tokens := "-"
	if r.Thread.TokenCount >= 0 {
		tokens = fmt.Sprintf("%d", r.Thread.TokenCount)
	}
	line := fmt.Sprintf("    model: %s  profile: %s  tokens: %s  cwd: %s", r.Thread.Model, profile, tokens, r.Thread.CWD)
	return detailStyle.Render(line)
}

func (m Model) footerLine() string {
	return "↑/k ↓/j navigate    enter attach    i compose    / filter    ? help    q quit"
}

func (m Model) helpView() string {
	entries := [][2]string{
		{"↑ ↓ / j k", "move selection"},
		{"enter", "attach alive thread / resume closed thread"},
		{"i", "focus composer (@ swap profile, enter launch, esc cancel)"},
		{"/", "filter by title, repo, branch"},
		{"?", "toggle this help"},
		{"q / ctrl+c", "quit"},
	}
	var b strings.Builder
	b.WriteString("Keyboard shortcuts\n\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("  %s  %s\n", helpKeyStyle.Render(fmt.Sprintf("%-10s", e[0])), e[1]))
	}
	b.WriteString("\nesc / ? to close")
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func ageString(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
