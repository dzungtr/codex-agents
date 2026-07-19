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
	// selectedStyle washes a selected row's background (both terminal
	// lines), mirroring the style contract's
	// `.session-row.is-selected { background: var(--accent-soft) }` — a
	// soft accent tint over the whole row, not the `›` cursor glyph alone
	// (which is kept too, since it still helps in a colorless terminal).
	// #2e3a48 is a dark cyan-leaning blue-gray that approximates what the
	// style contract's `--accent-soft: oklch(80% 0.1 200 / 0.14)` would
	// actually render as when blended over the surface background, while
	// staying visible on real terminals — the previous color 237 (a neutral
	// #3a3a3a gray) was indistinguishable from the dark surface on most
	// terminals, so the highlight effectively disappeared; the earlier
	// color 24 (a heavy saturated navy) read as a solid block rather than
	// a soft wash (design drift gap 2).
	selectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("#2e3a48"))
	detailStyle   = lipgloss.NewStyle().Faint(true)
	// badgeStyle renders the per-row model/profile badges: a muted color
	// distinct from detailStyle's Faint so they read as a separate visual
	// element, echoing the style contract's `.badge` pill treatment
	// (bordered pill box) as closely as inline terminal text reasonably
	// can — a real bordered box needs its own top/bottom border lines,
	// which would break the two-terminal-lines-per-row contract (issue
	// #20), so brackets stand in for the border here.
	badgeStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	helpKeyStyle    = lipgloss.NewStyle().Bold(true)
	waitingDotStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	workingDotStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
	closedDotStyle  = lipgloss.NewStyle().Faint(true)
	// titleBarStyle draws only a bottom border under the title line, per
	// the style contract's `.term-titlebar` (which is a plain flex row
	// with `border-bottom`, not a fully boxed titlebar).
	titleBarStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.Color("240"))
	// composerRuleStyle draws the horizontal rule above the composer bar,
	// per the style contract's `.composer-wrap { border-top: 1px solid
	// var(--border) }` (design drift gap 4's fourth gap) — same rule glyph
	// and border color as titleBarStyle, standalone rather than attached to
	// a box border since nothing above it needs enclosing.
	composerRuleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func (m Model) View() string {
	if m.showHelp {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(m.titleBar())
	b.WriteString("\n")
	b.WriteString(m.headerLine())
	if m.statusLine != "" && !m.replyFocused {
		b.WriteString("\n")
		b.WriteString(detailStyle.Render(m.statusLine))
	}
	b.WriteString("\n\n")
	b.WriteString(m.listView())
	b.WriteString("\n")
	b.WriteString(m.composerRule())
	b.WriteString("\n")
	b.WriteString(m.composerBar())
	b.WriteString("\n")
	b.WriteString(m.footerLine())
	return b.String()
}

// composerRule renders the horizontal rule separating the list from the
// composer bar (design drift gap 4's border-top) — see composerRuleStyle's
// doc comment. It replaces what used to be a second blank line here, so the
// separator reads as an intentional rule rather than adding to the vertical
// gap.
func (m Model) composerRule() string {
	return composerRuleStyle.Render(strings.Repeat("─", m.listWidth()))
}

// titleBar renders the bordered title bar (issue: design drift gap 1):
// a left spacer, the centered "codex — orchestrator" title, and a "[?]"
// help affordance at the right — matching index.html's
// .term-titlebar (spacer + centered .term-title + .term-help-btn), with a
// bottom border standing in for the mockup's border-bottom rule. The
// spacer is sized to match the help affordance so the title lands
// genuinely centered rather than skewed by the help button's width.
func (m Model) titleBar() string {
	width := m.listWidth()
	const title = "codex — orchestrator"
	const helpBtn = "[?]"

	spacerLen := len([]rune(helpBtn))
	inner := width - spacerLen - len([]rune(helpBtn))
	if inner < 0 {
		inner = 0
	}
	titleRunes := []rune(title)
	if len(titleRunes) > inner {
		titleRunes = titleRunes[:inner]
	}
	leftPad := (inner - len(titleRunes)) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	rightPad := inner - len(titleRunes) - leftPad
	if rightPad < 0 {
		rightPad = 0
	}
	line := strings.Repeat(" ", spacerLen) + strings.Repeat(" ", leftPad) + string(titleRunes) + strings.Repeat(" ", rightPad) + helpBtn
	return titleBarStyle.Width(width).Render(line)
}

// headerLine shows the filter prompt while filtering, the reply prompt
// while replying, otherwise a summary of total threads and how many are
// waiting/working (PRD #1's List behavior -> Statuses row: these are the
// two "alive" states; closed threads aren't attention-worthy so they're
// left out of the summary the same way the old open/closed count was).
// The composer (`i`) no longer has a header branch: it moved to a
// persistent bar at the bottom of the layout (issue: design drift gap 4).
func (m Model) headerLine() string {
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
	width := m.listWidth()
	var b strings.Builder
	for vi, idx := range m.visible {
		row := m.rows[idx]
		selected := vi == m.cursor
		b.WriteString(renderRow(row, selected, now, width))
		if vi != len(m.visible)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// maxContentWidth caps listWidth on a wide terminal, mirroring the style
// contract's `.term-window { max-width: 1180px }` (roughly 140 monospace
// columns at the mockup's 14px font) — without it, a wide real terminal
// stretches row content edge to edge and the gap between a row's meta text
// and its right-aligned badge cluster grows huge and incoherent (design
// drift's column-layout-looseness note).
const maxContentWidth = 140

// listWidth is the effective width renderRow, titleBar and composerBar lay
// their content out to: the model's current m.width once a
// tea.WindowSizeMsg has arrived, or 80 before the first one (the initial
// frame, and every unit/model test that never sends one). Floored at 20 so
// the truncation math in renderRow/renderMetaLine never goes negative in a
// degenerate terminal (issue #20 decision 2), and capped at
// maxContentWidth so a very wide terminal doesn't stretch row content
// edge-to-edge.
func (m Model) listWidth() int {
	w := m.width
	if w <= 0 {
		w = 80
	}
	if w < 20 {
		w = 20
	}
	if w > maxContentWidth {
		w = maxContentWidth
	}
	return w
}

// statusDotGlyph returns r's plain (unstyled) status-dot glyph. Used on
// selected rows, where the whole line is styled as one unit afterward (see
// renderRow) — pre-styling the glyph there would embed a reset code
// mid-line that would cut the outer selectedStyle background short.
func statusDotGlyph(s tmuxstatus.Status) string {
	switch s {
	case tmuxstatus.StatusWaiting:
		return dotWaiting
	case tmuxstatus.StatusWorking:
		return dotWorking
	default:
		return dotClosed
	}
}

// statusDot renders the status-dot glyph for r.Status, styled per the
// style contract (see the dot glyph constants' doc comment for why a
// static render can't reproduce the "pulse for alive" animation). Used on
// unselected rows only — see statusDotGlyph's doc comment for why selected
// rows use the plain glyph instead.
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

// renderRow renders r as exactly two terminal rows (issue #20): line 1 is
// the identity line — cursor, status dot, displayTitle (#17's fallback),
// and age right-aligned to width; line 2 is renderMetaLine's faint
// metadata line plus the badge cluster (model/profile/message count).
// width is the caller's listWidth(), never a raw m.width, so the
// floor/fallback rule always applies.
//
// Line 1's budget math is rune-count based, not cell-width based, so a
// wide-glyph (CJK) title can still throw the right edge off in a real
// terminal — the same pre-existing quirk issue #17 declared out of scope
// for truncate.
func renderRow(r Row, selected bool, now time.Time, width int) string {
	cursor := "  "
	if selected {
		cursor = "› "
	}
	age := ageString(now, r.Thread.Recency)
	// Budget: 4 fixed cells (cursor 2 + dot 1 + gap 1) + age + a minimum
	// two-space gap between title and age, all subtracted from width.
	title := truncate(displayTitle(r.Thread), width-4-len([]rune(age))-2)
	pad := width - 4 - len([]rune(title)) - len([]rune(age))
	if pad < 1 {
		pad = 1
	}
	if selected {
		// Build line 1 entirely unstyled, then wrap the whole thing in one
		// selectedStyle.Render call (design drift gap 2's background wash
		// over the whole row) — see statusDotGlyph's doc comment for why.
		line := cursor + statusDotGlyph(r.Status) + " " + title + strings.Repeat(" ", pad) + age
		return selectedStyle.Render(line) + "\n" + renderMetaLine(r, selected, width)
	}
	line := cursor + statusDot(r.Status) + " " + title + strings.Repeat(" ", pad) + age
	return line + "\n" + renderMetaLine(r, selected, width)
}

// renderMetaLine builds line 2: metaColumn's repo·branch (#18) on the left,
// plus — only when the row is selected — detailParts' known tokens/cwd
// parts, and badgeClusterPlain's model/profile/message-count cluster on
// the right, shown on every row regardless of selection (design drift gap
// 3). The whole line is truncated/padded to width so an overlong row's
// tail never wraps into a third terminal row (issue #20 decisions 4 and 8)
// and so a selected row's background wash reaches the full row width.
//
// A selected row is built as one plain (unstyled) string and wrapped in a
// single selectedStyle.Render call, same as renderRow's line 1 — badges
// lose their distinct badgeStyle coloring when selected, but the row's
// background wash already sets it apart, and building it any other way
// would embed a reset code mid-line (see statusDotGlyph's doc comment).
func renderMetaLine(r Row, selected bool, width int) string {
	var leftParts []string
	if meta := metaColumn(r.Thread); meta != "" {
		leftParts = append(leftParts, meta)
	}
	if selected {
		leftParts = append(leftParts, detailParts(r.Thread)...)
	}
	left := strings.Join(leftParts, "  ")
	right := badgeClusterPlain(r.Thread)

	avail := width - 4
	if avail < 0 {
		avail = 0
	}
	if rightLen := len([]rune(right)); rightLen > avail {
		right = truncate(right, avail)
	}

	var leftTrunc string
	var pad int
	if right == "" {
		// No badges: lay out exactly like before design drift gap 3 (no
		// trailing pad), so a badge-less row's non-selected line 2 stays
		// just the faint indent + meta, unchanged.
		leftTrunc = truncate(left, avail)
	} else {
		const gap = 2
		rightLen := len([]rune(right))
		leftBudget := avail - rightLen - gap
		if leftBudget < 0 {
			leftBudget = 0
		}
		leftTrunc = truncate(left, leftBudget)
		pad = avail - len([]rune(leftTrunc)) - rightLen
		if pad < 0 {
			pad = 0
		}
	}

	if selected {
		line := "    " + leftTrunc + strings.Repeat(" ", pad) + right
		if fill := width - len([]rune(line)); fill > 0 {
			line += strings.Repeat(" ", fill)
		}
		return selectedStyle.Render(line)
	}
	metaPart := detailStyle.Render("    " + leftTrunc + strings.Repeat(" ", pad))
	if right == "" {
		return metaPart
	}
	return metaPart + badgeStyle.Render(right)
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

// detailParts builds the selected row's detail parts from only the known
// fields, omitting unknown ones rather than substituting a "-" placeholder
// — see PRD #19. "Unknown" means a negative TokenCount (the data layer's
// explicit -1 sentinel; TokenCount == 0 is a known zero and still renders).
// Model and Profile used to live here too, but design drift gap 3 moved
// them into badgeClusterPlain's per-row badge cluster (shown on every row,
// not just the selected one) — see its doc comment. cwd used to live here
// too: it crowded out the model/profile/msg-count badge cluster on the
// selected row specifically (renderMetaLine truncates the left side to make
// room for the right-hand badges, so a long cwd could push badges off
// entirely) — making a *selected* row show *less* than an unselected one.
// It's also redundant with the composer bar's "Launches detached in <dir>"
// hint line, so it's dropped rather than truncated.
func detailParts(t codexstate.Thread) []string {
	var parts []string
	if t.TokenCount >= 0 {
		parts = append(parts, fmt.Sprintf("tokens: %d", t.TokenCount))
	}
	return parts
}

// badgeClusterPlain builds line 2's right-hand cluster — model badge,
// profile badge, then message count — shown on every row regardless of
// selection (design drift gap 3: the mockup's model/profile `.badge`
// columns and message-count column apply to every session row, not just a
// selected one). Fields are omitted when unknown, the same rule detailParts
// uses: an empty Model/Profile, or a negative MessageCount (codexstate's
// "not enriched yet" sentinel; MessageCount == 0 is a known zero and still
// renders), leaves that part out rather than rendering a placeholder.
// Returned plain (unstyled) so callers can lay it out with rune-accurate
// width math before applying badgeStyle.
func badgeClusterPlain(t codexstate.Thread) string {
	var parts []string
	if t.Model != "" {
		parts = append(parts, "["+t.Model+"]")
	}
	if t.Profile != "" {
		parts = append(parts, "["+t.Profile+"]")
	}
	if t.MessageCount >= 0 {
		parts = append(parts, fmt.Sprintf("%d msgs", t.MessageCount))
	}
	return strings.Join(parts, " ")
}

// composerMaxLines caps how many wrapped text lines composerBar shows
// (mirroring the design's auto-growing-textarea intent while still bounding
// it so a very long task can't push the footer off an already-short
// terminal). Past this many lines, composerBar keeps the tail — the end,
// where the cursor sits — rather than the head, on the theory that the
// most relevant part of a long in-progress task is what's being typed now.
const composerMaxLines = 6

// composerBar renders the persistent composer bar pinned above the footer
// (design drift gap 4): the mockup's composer-wrap is always visible at
// the bottom of the window, not popped into the header only while focused.
// It word-wraps the live-typed task text to listWidth() instead of
// emitting one unbounded line (issue: composer overflow) — mirroring the
// design's auto-growing textarea. The profile pill (badgeStyle, matching
// the mockup's pill-styled model/profile tags — there's no independent
// "model" selection in this composer, only the profile cycle composer.go's
// `@` key drives, so only one pill is shown) is reserved on line 1 and
// right-aligned there via padding; continuation lines are indented two
// columns (aligning under the text after "› ") and use the full width, no
// pill. It shows a faint placeholder (unchanged, single line) when idle and
// empty, and the "Launches detached in <dir>" hint line below either way.
// It only renders state; composer.go's handleComposerKey still owns all
// composer key handling.
func (m Model) composerBar() string {
	const placeholder = "Describe a task and press Enter to launch a thread…"
	width := m.listWidth()
	pill := "[" + m.composerProfile() + "]"
	pillWidth := len([]rune(pill))
	renderedPill := badgeStyle.Render(pill)

	hintText := "Launches detached — closing this window won't stop it."
	if m.launchDir != "" {
		hintText = fmt.Sprintf("Launches detached in %s — closing this window won't stop it.", m.launchDir)
	}
	hint := detailStyle.Render(hintText)

	if !m.composerFocused && m.composerTask == "" {
		// Idle+empty stays a single faint line, same shape as before this
		// fix — truncate only bites in a narrower terminal than the
		// placeholder already fits, which is the CRITICAL width rule (every
		// rendered line <= listWidth()) rather than a behavior change.
		budget := width - 2 - 2 - pillWidth
		if budget < 0 {
			budget = 0
		}
		text := truncate(placeholder, budget)
		line := "› " + detailStyle.Render(text) + "  " + renderedPill
		return line + "\n" + hint
	}

	text := m.composerTask
	if m.composerFocused {
		text += "_"
	}

	contWidth := width - 2
	if contWidth < 1 {
		contWidth = 1
	}
	firstWidth := width - 2 - 2 - pillWidth
	if firstWidth < 1 {
		firstWidth = 1
	}

	lines := wrapComposerText(text, firstWidth, contWidth)
	if len(lines) > composerMaxLines {
		lines = lines[len(lines)-composerMaxLines:]
	}

	first := lines[0]
	if n := len([]rune(first)); n > firstWidth {
		// Only reachable right after the tail-crop above: line 1 is now a
		// formerly-continuation line wrapped at the wider contWidth, not
		// the pill-reserving firstWidth budget it would have gotten as the
		// true first line. Truncating the little that's left over here
		// keeps the pill fitting and the CRITICAL width rule holding even
		// in this rare very-long-task edge case.
		first = truncate(first, firstWidth)
	}
	pad := firstWidth - len([]rune(first))
	if pad < 0 {
		pad = 0
	}

	var b strings.Builder
	b.WriteString("› ")
	b.WriteString(first)
	b.WriteString(strings.Repeat(" ", pad))
	b.WriteString("  ")
	b.WriteString(renderedPill)
	for _, l := range lines[1:] {
		b.WriteString("\n  ")
		b.WriteString(l)
	}
	b.WriteString("\n")
	b.WriteString(hint)
	return b.String()
}

// wrapComposerText word-wraps s into lines whose rune width never exceeds
// firstWidth (line 1) or contWidth (every following line). It wraps on
// whitespace boundaries where possible (strings.Fields, so runs of
// whitespace collapse the same way composer.go's Space key already builds
// them up one space at a time); a word wider than the width it would start
// on doesn't get an exception — it hard-breaks rune-safe across as many
// lines as it takes, same rune-safety rule as truncate. Always returns at
// least one (possibly empty) line, so composerBar always has a first line
// to attach the pill to.
func wrapComposerText(s string, firstWidth, contWidth int) []string {
	if firstWidth < 1 {
		firstWidth = 1
	}
	if contWidth < 1 {
		contWidth = 1
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}

	var lines []string
	width := firstWidth
	var cur []rune
	for _, word := range words {
		w := []rune(word)
		for len(w) > 0 {
			avail := width - len(cur)
			if len(cur) > 0 {
				avail-- // room for the separating space this word needs
			}
			if avail <= 0 {
				lines = append(lines, string(cur))
				cur = nil
				width = contWidth
				avail = width
			} else if len(cur) > 0 {
				cur = append(cur, ' ')
			}
			take := avail
			if take > len(w) {
				take = len(w)
			}
			cur = append(cur, w[:take]...)
			w = w[take:]
		}
	}
	lines = append(lines, string(cur))
	return lines
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
