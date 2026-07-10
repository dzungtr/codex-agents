// Package ui implements the read-only codex thread list screen: a
// bubbletea model over rows produced by internal/codexstate (thread data)
// and internal/tmuxstatus (working/waiting/closed status). It renders per
// the style contract in Codex-Orchestrator-TUI/index.html, adapted to a
// terminal list: every thread renders as exactly two terminal rows — an
// identity line (status dot, title, age right-aligned to the terminal
// width) and a faint metadata line (repo·branch, plus model/profile/tokens/
// cwd detail folded in when the row is selected) — a `/` filter, and a `?`
// help overlay (issue #20). It also owns the list ordering rule (PRD #1's
// List behavior -> Ordering row): status groups waiting -> working ->
// closed, most-recent first within each — see New and sortRows.
package ui

import (
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// Row is a single list entry: a codex thread plus its derived
// working/waiting/closed status (tmuxstatus.StatusFor).
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

	// composerFocused, composerTask and composerProfileIdx hold the
	// composer's state while the user is typing a new task (`i` to
	// focus). composerProfileIdx indexes composerProfiles; `@` cycles it.
	composerFocused    bool
	composerTask       string
	composerProfileIdx int

	// replyFocused, replyText and replyThreadID hold the quick-reply
	// input's state while the user is typing a one-line reply to an alive
	// (waiting or working) thread (`r` to focus; PRD #1's List behavior ->
	// Quick reply row / issue #6). replyThreadID is captured at focus time
	// rather than re-derived from the cursor at submit time, since the
	// cursor can't move while this input is focused anyway (handleKey
	// routes all keys to handleReplyKey instead).
	replyFocused  bool
	replyText     string
	replyThreadID string

	// statusLine is a transient success/error line surfaced by the last
	// Launch or Attach action, shown under the header until the next one.
	statusLine string

	// actions are the side-effecting hooks the composer submit and
	// row-Enter wire up to; see Actions' doc comment.
	actions Actions

	// launchDir is shown in the composer bar's hint line ("Launches
	// detached in <launchDir> ...", design drift gap 4) — the directory a
	// composer-submitted Launch actually starts from. Set via
	// WithLaunchDir; the zero value (unset) just omits that clause.
	launchDir string

	width, height int
	quitting      bool

	now func() time.Time // injectable clock; tests override for deterministic "age" rendering
}

// New builds a list Model from rows in any order: New itself applies the
// PRD #1 List behavior -> Ordering rule (status groups waiting -> working
// -> closed, most-recent first within each group), so callers (main.go's
// loadRows, and this package's own Update handlers below) never need to
// pre-sort.
func New(rows []Row) Model {
	sorted := append([]Row(nil), rows...)
	sortRows(sorted)
	m := Model{rows: sorted, now: time.Now}
	m.applyFilter()
	return m
}

// statusGroupRank orders status groups per PRD #1's List behavior ->
// Ordering row: waiting (needs you) first, then working, then closed last.
func statusGroupRank(s tmuxstatus.Status) int {
	switch s {
	case tmuxstatus.StatusWaiting:
		return 0
	case tmuxstatus.StatusWorking:
		return 1
	default: // tmuxstatus.StatusClosed
		return 2
	}
}

// sortRows orders rows in place by status group, then most-recent-first
// within each group. A stable sort is used so rows with identical recency
// (e.g. zero-value Recency on records the caller hasn't enriched) keep a
// deterministic relative order across calls instead of shuffling.
func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := statusGroupRank(rows[i].Status), statusGroupRank(rows[j].Status)
		if ri != rj {
			return ri < rj
		}
		return rows[i].Thread.Recency.After(rows[j].Thread.Recency)
	})
}

// WithClock overrides the model's clock. Intended for tests that need
// deterministic "age" output; production code leaves this as time.Now.
func (m Model) WithClock(now func() time.Time) Model {
	m.now = now
	return m
}

// WithLaunchDir sets the directory shown in the composer bar's hint line
// (design drift gap 4). Without it (the zero value), the hint omits the
// "in <dir>" clause rather than showing a blank directory.
func (m Model) WithLaunchDir(dir string) Model {
	m.launchDir = dir
	return m
}

// WithActions wires the composer-launch and row-attach side effects.
// Without it (the zero value), submitting the composer or pressing Enter
// on a row is a harmless no-op.
func (m Model) WithActions(a Actions) Model {
	m.actions = a
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
	case ThreadLaunchedMsg:
		row := msg.Row
		// A freshly launched thread has no Recency of its own (main.go's
		// launchAction doesn't know it — codex hasn't written a thread
		// record yet); stamp it with "now" so it sorts to the top of its
		// status group instead of the bottom (a zero-value time.Time reads
		// as infinitely old).
		if row.Thread.Recency.IsZero() {
			row.Thread.Recency = m.now()
		}
		m.rows = append(m.rows, row)
		sortRows(m.rows)
		m.applyFilter()
		m.selectThreadID(row.Thread.ID)
		m.statusLine = "launched " + displayTitle(row.Thread)
		return m, nil
	case ThreadLaunchErrorMsg:
		m.statusLine = "error: " + msg.Err.Error()
		return m, nil
	case AttachDoneMsg:
		m.statusLine = ""
		if m.actions.Refresh != nil {
			return m, m.actions.Refresh()
		}
		return m, nil
	case QuickReplySentMsg:
		// Refresh (like AttachDoneMsg) rather than optimistically flipping
		// the row's status locally: the reply was fired-and-forgotten via
		// tmux send-keys, so the authoritative status still comes from
		// reloading tmux liveness + agentstate (QuickReply already cleared
		// last_turn_event, so the reload should read the row as working).
		m.statusLine = "sent reply"
		if m.actions.Refresh != nil {
			return m, m.actions.Refresh()
		}
		return m, nil
	case RowsRefreshedMsg:
		m.rows = append([]Row(nil), msg.Rows...)
		sortRows(m.rows)
		m.applyFilter()
		return m, nil
	case InterruptDoneMsg:
		// The underlying status transition (working -> waiting) is driven
		// by whatever the Interrupt action recorded in agentstate; refresh
		// picks that back up the same way AttachDoneMsg does after detach.
		m.statusLine = "interrupted " + m.titleFor(msg.ThreadID)
		if m.actions.Refresh != nil {
			return m, m.actions.Refresh()
		}
		return m, nil
	case ArchiveDoneMsg:
		m.rows = removeThread(m.rows, msg.ThreadID)
		m.applyFilter()
		m.statusLine = msg.Note
		return m, nil
	}
	return m, nil
}

// titleFor looks up threadID's title among the model's current rows, for
// status-line messages that need a human-readable name rather than a raw
// ID. Returns threadID itself if the row is no longer present.
func (m Model) titleFor(threadID string) string {
	for _, r := range m.rows {
		if r.Thread.ID == threadID {
			return displayTitle(r.Thread)
		}
	}
	return threadID
}

// removeThread returns rows with threadID's entry dropped, used by
// ArchiveDoneMsg: an archived thread disappears from the list outright
// (PRD #1's List behavior -> Archive row) rather than waiting for the next
// Refresh.
func removeThread(rows []Row, threadID string) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if r.Thread.ID == threadID {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showHelp {
		switch msg.String() {
		case "?", "esc", "q", "ctrl+c":
			m.showHelp = false
		}
		return m, nil
	}

	if m.composerFocused {
		return m.handleComposerKey(msg)
	}

	if m.replyFocused {
		return m.handleReplyKey(msg)
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
	case "i":
		m.composerFocused = true
		m.composerTask = ""
		m.composerProfileIdx = 0
		m.statusLine = ""
	case "r":
		// Quick reply (PRD #1's List behavior -> Quick reply row / issue
		// #6): only meaningful for alive threads — no-op on a closed row,
		// per the acceptance criteria.
		if len(m.visible) > 0 {
			row := m.rows[m.visible[m.cursor]]
			if row.Status != tmuxstatus.StatusClosed {
				m.replyFocused = true
				m.replyText = ""
				m.replyThreadID = row.Thread.ID
				m.statusLine = ""
			}
		}
	case "enter":
		if len(m.visible) > 0 && m.actions.Attach != nil {
			row := m.rows[m.visible[m.cursor]]
			return m, m.actions.Attach(row)
		}
	case "x":
		if len(m.visible) > 0 && m.actions.Interrupt != nil {
			row := m.rows[m.visible[m.cursor]]
			return m, m.actions.Interrupt(row)
		}
	case "a":
		if len(m.visible) > 0 && m.actions.Archive != nil {
			row := m.rows[m.visible[m.cursor]]
			return m, m.actions.Archive(row)
		}
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

// selectThreadID moves the cursor to threadID's row within the current
// visible set, if present. Used after inserting a freshly launched thread
// so the cursor follows it even though its position (top of its status
// group, not necessarily top of the whole list) depends on sortRows.
func (m *Model) selectThreadID(threadID string) {
	for vi, idx := range m.visible {
		if m.rows[idx].Thread.ID == threadID {
			m.cursor = vi
			return
		}
	}
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
