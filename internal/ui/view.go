package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dzungtr/codex-agents/internal/codexstate"
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
	if m.statusLine != "" && !m.composerFocused && !m.replyFocused {
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
	if m.replyFocused {
		return fmt.Sprintf("reply> %s_  (enter send · esc cancel)", m.replyText)
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
	meta := metaColumn(r.Thread)
	line := fmt.Sprintf("%s%s %-42s %-28s %5s", cursor, statusDot(r.Status), truncate(displayTitle(r.Thread), 42), truncate(meta, 28), ageString(now, r.Thread.Recency))
	if selected {
		return selectedStyle.Render(line)
	}
	return line
}

// displayTitle returns the thread's codex Title, falling back to its first
// user message (collapsed to one line) when codex never titled the thread.
// Empty when both are absent. Every call site that displays a row's title —
// renderRow's title column, and model.go's titleFor and the "launched "
// status line — goes through this helper so the fallback behaves
// identically everywhere (issue #17). matchesQuery (model.go) deliberately
// does not: matching over fallback text would alter filter semantics.
func displayTitle(t codexstate.Thread) string {
	if strings.TrimSpace(t.Title) != "" {
		return t.Title
	}
	return strings.Join(strings.Fields(t.FirstMessage), " ")
}

// metaColumn builds the repo · branch meta column from only the parts that
// are present, so a missing repo or branch never leaves a dangling " · "
// separator (issue #18). "Present" means non-empty: t.Repo() is "" only
// when CWD is unset, and GitBranch is "" when codex recorded no branch. No
// trimming — codex-sourced values aren't whitespace-padded, and the filter
// (rowMatches) matches against these same untrimmed values.
func metaColumn(t codexstate.Thread) string {
	var parts []string
	if repo := t.Repo(); repo != "" {
		parts = append(parts, repo)
	}
	if t.GitBranch != "" {
		parts = append(parts, t.GitBranch)
	}
	return strings.Join(parts, " · ")
}

// renderDetail builds the selected row's detail line from only the known
// fields (model, profile, tokens, cwd, in that fixed order), omitting
// unknown ones rather than substituting a "-" placeholder — see PRD #19.
// "Unknown" means an empty string for Model/Profile/CWD, or a negative
// TokenCount (the data layer's explicit -1 sentinel; TokenCount == 0 is a
// known zero and still renders).
func renderDetail(r Row) string {
	var parts []string
	if r.Thread.Model != "" {
		parts = append(parts, "model: "+r.Thread.Model)
	}
	if r.Thread.Profile != "" {
		parts = append(parts, "profile: "+r.Thread.Profile)
	}
	if r.Thread.TokenCount >= 0 {
		parts = append(parts, fmt.Sprintf("tokens: %d", r.Thread.TokenCount))
	}
	if r.Thread.CWD != "" {
		parts = append(parts, "cwd: "+r.Thread.CWD)
	}
	return detailStyle.Render("    " + strings.Join(parts, "  "))
}

func (m Model) footerLine() string {
	return "↑/k ↓/j navigate    enter attach    i compose    r reply    x interrupt    a archive    / filter    ? help    q quit"
}

func (m Model) helpView() string {
	entries := [][2]string{
		{"↑ ↓ / j k", "move selection"},
		{"enter", "attach alive thread / resume closed thread"},
		{"i", "focus composer (@ swap profile, enter launch, esc cancel)"},
		{"r", "quick-reply to the selected alive thread (enter send, esc cancel)"},
		{"x", "interrupt the current turn (thread -> waiting)"},
		{"a", "archive: kill session, hide thread, offer worktree removal"},
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

// truncate shortens s to at most n runes, appending "…" when it cuts
// content off. Rune-based (not byte-indexed) so a multibyte character is
// never split in half — ASCII input (the only kind seen in today's goldens)
// behaves byte-identically to a byte-indexed slice, so existing golden
// files don't change (issue #17).
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
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
