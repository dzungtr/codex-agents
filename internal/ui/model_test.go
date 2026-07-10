package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

func fixedNow() time.Time {
	return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
}

// fixtureRows returns rows covering all three statuses, in the order New's
// grouping+recency sort should already produce them: "Add dark mode"
// (waiting) first — it's both the most attention-worthy status group *and*
// the most recent thread, so ordering assertions further down don't need
// to distinguish which rule put it there — then "Refactor drainer"
// (working), then "Fix auth hook" (closed).
func fixtureRows() []Row {
	base := fixedNow()
	return []Row{
		{
			Thread: codexstate.Thread{
				ID: "t2", Title: "Add dark mode", CWD: "/Users/tony/web-app",
				Model: "gpt-5-codex", GitBranch: "add-dark-mode",
				Recency: base.Add(-3 * time.Minute), Profile: "general-agentic", TokenCount: 8200, MessageCount: 4,
			},
			Status: tmuxstatus.StatusWaiting,
		},
		{
			Thread: codexstate.Thread{
				ID: "t3", Title: "Refactor drainer", CWD: "/Users/tony/infra-drainer",
				Model: "gpt-5-codex", GitBranch: "refactor-drainer",
				Recency: base.Add(-45 * time.Minute), TokenCount: -1, MessageCount: -1,
			},
			Status: tmuxstatus.StatusWorking,
		},
		{
			Thread: codexstate.Thread{
				ID: "t1", Title: "Fix auth hook", CWD: "/Users/tony/web-app",
				Model: "gpt-5-codex", GitBranch: "fix-auth-hook",
				Recency: base.Add(-26 * time.Hour), TokenCount: -1, MessageCount: -1,
			},
			Status: tmuxstatus.StatusClosed,
		},
	}
}

func newFixtureModel() Model {
	return New(fixtureRows()).WithClock(fixedNow)
}

func TestModel_InitialView_OrdersRowsMostRecentFirstAndSelectsFirst(t *testing.T) {
	m := newFixtureModel()
	view := m.View()

	idxDark := strings.Index(view, "Add dark mode")
	idxDrainer := strings.Index(view, "Refactor drainer")
	idxAuth := strings.Index(view, "Fix auth hook")
	if idxDark == -1 || idxDrainer == -1 || idxAuth == -1 {
		t.Fatalf("expected all three titles in view, got:\n%s", view)
	}
	if !(idxDark < idxDrainer && idxDrainer < idxAuth) {
		t.Fatalf("expected order [dark mode, drainer, auth hook], view was:\n%s", view)
	}
}

// TestNew_OrdersByStatusGroupThenRecency feeds New() rows in a
// deliberately scrambled order (closed-but-newest first) and checks it
// re-sorts them per PRD #1's List behavior -> Ordering row: status groups
// waiting -> working -> closed, most-recent first within each group. This
// is the core of issue #4's ordering requirement — fixtureRows above
// happens to already be in the right order, so this test is the one that
// actually exercises sortRows rather than relying on already-sorted input.
func TestNew_OrdersByStatusGroupThenRecency(t *testing.T) {
	base := fixedNow()
	rows := []Row{
		{Thread: codexstate.Thread{ID: "c", Title: "Closed but newest", Recency: base}, Status: tmuxstatus.StatusClosed},
		{Thread: codexstate.Thread{ID: "w", Title: "Working middle", Recency: base.Add(-10 * time.Minute)}, Status: tmuxstatus.StatusWorking},
		{Thread: codexstate.Thread{ID: "a1", Title: "Waiting older", Recency: base.Add(-1 * time.Hour)}, Status: tmuxstatus.StatusWaiting},
		{Thread: codexstate.Thread{ID: "a2", Title: "Waiting newer", Recency: base.Add(-5 * time.Minute)}, Status: tmuxstatus.StatusWaiting},
	}
	m := New(rows).WithClock(fixedNow)
	view := m.View()

	idxNewer := strings.Index(view, "Waiting newer")
	idxOlder := strings.Index(view, "Waiting older")
	idxWorking := strings.Index(view, "Working middle")
	idxClosed := strings.Index(view, "Closed but newest")
	if idxNewer == -1 || idxOlder == -1 || idxWorking == -1 || idxClosed == -1 {
		t.Fatalf("expected all titles present, got:\n%s", view)
	}
	if !(idxNewer < idxOlder && idxOlder < idxWorking && idxWorking < idxClosed) {
		t.Fatalf("expected order [waiting newer, waiting older, working, closed] regardless of input order, got:\n%s", view)
	}
}

// TestModel_DetailLine_ShownOnlyForSelectedRow sends a wide WindowSizeMsg
// first: issue #20 folds detail parts onto line 2 alongside metaColumn and
// truncates that line to the terminal width (decision 8), and the fixture
// row's full meta+detail text is ~109 runes — wider than the 80-fallback
// budget (76) would allow without cutting off "tokens:"/"cwd:". A width
// wide enough to hold it all keeps this test about "is the detail line
// shown for the selected row", not about truncation, which has its own
// coverage in view_test.go's TestRenderMetaLine_OverlongTruncatesNeverWraps.
//
// Design drift gap 3 moved model/profile out of the selected-only detail
// line into a badge cluster shown on every row, so this test now checks
// tokens/cwd stay selected-only while the model badge appears on all three
// rows and the profile badge appears (only the first/selected row has a
// known Profile in fixtureRows).
func TestModel_DetailLine_ShownOnlyForSelectedRow(t *testing.T) {
	m := newFixtureModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(Model)
	view := m.View()

	if !strings.Contains(view, "tokens: 8200") || !strings.Contains(view, "cwd: /Users/tony/web-app") {
		t.Fatalf("expected tokens/cwd detail for the selected (first) row, got:\n%s", view)
	}
	if strings.Count(view, "tokens:") != 1 {
		t.Fatalf("expected exactly one tokens: detail (only for the selected row), got:\n%s", view)
	}
	if strings.Count(view, "[gpt-5-codex]") != 3 {
		t.Fatalf("expected the model badge on all three rows, got:\n%s", view)
	}
	if !strings.Contains(view, "[general-agentic]") {
		t.Fatalf("expected the selected row's profile badge, got:\n%s", view)
	}
}

// TestModel_DetailLine_UnknownFieldsOmitted: see the width note on
// TestModel_DetailLine_ShownOnlyForSelectedRow above — same reasoning. The
// drainer row (fixtureRows' t3) has a known Model but no Profile and a
// negative MessageCount, so its badge cluster should show the model badge
// alone; TokenCount is also unknown there, so tokens: stays omitted too.
func TestModel_DetailLine_UnknownFieldsOmitted(t *testing.T) {
	m := newFixtureModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	view := m.View()

	if !strings.Contains(view, "cwd: /Users/tony/infra-drainer") {
		t.Fatalf("expected known cwd field for the drainer row, got:\n%s", view)
	}
	if strings.Contains(view, "tokens:") {
		t.Fatalf("expected unknown tokens to be omitted, got:\n%s", view)
	}

	idx := strings.Index(view, "Refactor drainer")
	if idx == -1 {
		t.Fatalf("expected drainer row in view, got:\n%s", view)
	}
	metaLine := strings.SplitN(view[idx:], "\n", 3)[1]
	if !strings.Contains(metaLine, "[gpt-5-codex]") {
		t.Fatalf("expected model badge on drainer row's meta line, got:\n%s", metaLine)
	}
	if strings.Contains(metaLine, "msgs") {
		t.Fatalf("expected message count omitted (unknown) on drainer row, got:\n%s", metaLine)
	}
	if strings.Count(metaLine, "[") != 1 {
		t.Fatalf("expected only the model badge (no profile badge) on drainer row, got:\n%s", metaLine)
	}
}

func TestModel_Navigation_JKAndArrowsMoveCursorAndClamp(t *testing.T) {
	m := newFixtureModel()

	move := func(mm Model, key tea.KeyMsg) Model {
		updated, _ := mm.Update(key)
		return updated.(Model)
	}

	m = move(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if !strings.Contains(m.View(), "› ◐ Refactor drainer") {
		t.Fatalf("expected cursor on 'Refactor drainer' after j, got:\n%s", m.View())
	}

	m = move(m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(m.View(), "Fix auth hook") {
		t.Fatalf("expected cursor to advance to 'Fix auth hook', view:\n%s", m.View())
	}

	// Clamp at the bottom.
	m = move(m, tea.KeyMsg{Type: tea.KeyDown})
	view := m.View()
	if !strings.Contains(view, "› ○ Fix auth hook") {
		t.Fatalf("expected cursor clamped at last row, view:\n%s", view)
	}

	m = move(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = move(m, tea.KeyMsg{Type: tea.KeyUp})
	m = move(m, tea.KeyMsg{Type: tea.KeyUp}) // clamp at top
	if !strings.Contains(m.View(), "› ● Add dark mode") {
		t.Fatalf("expected cursor clamped at first row, view:\n%s", m.View())
	}
}

func TestModel_Filter_NarrowsOverTitleRepoAndBranch(t *testing.T) {
	m := newFixtureModel()

	send := func(mm Model, msg tea.Msg) Model {
		updated, _ := mm.Update(msg)
		return updated.(Model)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "auth" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	view := m.View()
	if strings.Contains(view, "Add dark mode") || strings.Contains(view, "Refactor drainer") {
		t.Fatalf("expected filter 'auth' to hide non-matching titles, got:\n%s", view)
	}
	if !strings.Contains(view, "Fix auth hook") {
		t.Fatalf("expected filter 'auth' to keep matching title, got:\n%s", view)
	}

	// Filter by repo (basename of cwd): "infra-drainer" should match only the drainer thread.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "infra-drainer" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	view = m.View()
	if !strings.Contains(view, "Refactor drainer") || strings.Contains(view, "Add dark mode") || strings.Contains(view, "Fix auth hook") {
		t.Fatalf("expected repo filter to isolate the drainer thread, got:\n%s", view)
	}

	// Filter by branch.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "add-dark-mode" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	view = m.View()
	if !strings.Contains(view, "Add dark mode") || strings.Contains(view, "Fix auth hook") {
		t.Fatalf("expected branch filter to isolate the dark-mode thread, got:\n%s", view)
	}
}

func TestModel_Filter_NoMatchesShowsEmptyState(t *testing.T) {
	m := newFixtureModel()
	send := func(mm Model, msg tea.Msg) Model {
		updated, _ := mm.Update(msg)
		return updated.(Model)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "zzz-no-match" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !strings.Contains(m.View(), "No threads match.") {
		t.Fatalf("expected empty-state message, got:\n%s", m.View())
	}
}

func TestModel_Filter_EscClearsQueryAndRestoresFullList(t *testing.T) {
	m := newFixtureModel()
	send := func(mm Model, msg tea.Msg) Model {
		updated, _ := mm.Update(msg)
		return updated.(Model)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "auth" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	view := m.View()
	if !strings.Contains(view, "Add dark mode") || !strings.Contains(view, "Refactor drainer") || !strings.Contains(view, "Fix auth hook") {
		t.Fatalf("expected esc to clear filter and restore full list, got:\n%s", view)
	}
}

func TestModel_EmptyThreadList_ShowsEmptyState(t *testing.T) {
	m := New(nil).WithClock(fixedNow)
	if !strings.Contains(m.View(), "No codex threads found.") {
		t.Fatalf("expected empty-list message, got:\n%s", m.View())
	}
}

func TestModel_HelpOverlay_TogglesAndReplacesListView(t *testing.T) {
	m := newFixtureModel()
	send := func(mm Model, msg tea.Msg) Model {
		updated, _ := mm.Update(msg)
		return updated.(Model)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	view := m.View()
	if !strings.Contains(view, "Keyboard shortcuts") {
		t.Fatalf("expected help overlay, got:\n%s", view)
	}
	if strings.Contains(view, "Add dark mode") {
		t.Fatalf("expected help overlay to replace the list view, got:\n%s", view)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	view = m.View()
	if strings.Contains(view, "Keyboard shortcuts") {
		t.Fatalf("expected esc to close help overlay, got:\n%s", view)
	}
	if !strings.Contains(view, "Add dark mode") {
		t.Fatalf("expected list view restored after closing help, got:\n%s", view)
	}
}

func TestModel_Quit_SetsQuittingAndReturnsQuitCmd(t *testing.T) {
	m := newFixtureModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m2 := updated.(Model)
	if !m2.Quitting() {
		t.Fatalf("expected Quitting() to be true after 'q'")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil tea.Quit command")
	}
}

// TestModel_Interrupt_UntitledRowUsesFirstMessageInStatusLine covers issue
// #17's third user story: interrupting a thread codex never titled should
// show "interrupted <first message>" rather than the degenerate
// "interrupted " that r.Thread.Title alone would produce.
func TestModel_Interrupt_UntitledRowUsesFirstMessageInStatusLine(t *testing.T) {
	rows := []Row{
		{
			Thread: codexstate.Thread{
				ID: "u1", Title: "", FirstMessage: "please add a dark mode toggle",
				Recency: fixedNow(),
			},
			Status: tmuxstatus.StatusWorking,
		},
	}
	m := New(rows).WithClock(fixedNow)
	updated, _ := m.Update(InterruptDoneMsg{ThreadID: "u1"})
	m = updated.(Model)
	if !strings.Contains(m.View(), "interrupted please add a dark mode toggle") {
		t.Fatalf("expected status line to use FirstMessage as fallback title, got:\n%s", m.View())
	}
}
