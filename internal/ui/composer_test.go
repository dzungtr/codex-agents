package ui

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

func TestComposer_IFocusesAndShowsDefaultProfile(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	view := m.View()
	if !strings.Contains(view, "[general-agentic]") {
		t.Fatalf("expected composer to default to general-agentic, got:\n%s", view)
	}
}

func TestComposer_TypingAppendsToTask(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range "fix bug" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !strings.Contains(m.View(), "fix bug") {
		t.Fatalf("expected composer text 'fix bug' in view, got:\n%s", m.View())
	}
}

func TestComposer_AtCyclesProfile(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "[design-session]") {
		t.Fatalf("expected profile to cycle to design-session, got:\n%s", m.View())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "[review]") {
		t.Fatalf("expected profile to cycle to review, got:\n%s", m.View())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "[general-agentic]") {
		t.Fatalf("expected profile to cycle back to general-agentic, got:\n%s", m.View())
	}
}

// TestComposer_LongTaskWrapsWithinListWidth covers the composer-overflow
// fix: a typed task longer than listWidth() must word-wrap into multiple
// lines rather than overflowing off the right edge of the terminal, the
// profile pill still lands on line 1, and every rendered line (including
// the placeholder/hint lines composerBar always emits) stays within
// listWidth() runes.
func TestComposer_LongTaskWrapsWithinListWidth(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	long := strings.Repeat("wrap this composer text please ", 6)
	for _, r := range long {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	bar := m.composerBar()
	lines := strings.Split(bar, "\n")
	width := m.listWidth()
	if len(lines) < 3 {
		t.Fatalf("expected the long task to wrap into multiple lines, got %d: %q", len(lines), bar)
	}
	for i, l := range lines {
		if n := utf8.RuneCountInString(stripANSI(l)); n > width {
			t.Fatalf("line %d exceeds listWidth %d: %q", i, width, stripANSI(l))
		}
	}
	if !strings.Contains(stripANSI(lines[0]), "[general-agentic]") {
		t.Fatalf("expected the profile pill on line 1, got %q", stripANSI(lines[0]))
	}
	// Last line before the hint is the wrapped text's true end, where the
	// trailing "_" cursor belongs.
	textLines := lines[:len(lines)-1]
	last := stripANSI(textLines[len(textLines)-1])
	if !strings.HasSuffix(last, "_") {
		t.Fatalf("expected the cursor at the true end of the wrapped text, got %q", last)
	}
}

// TestComposer_OverlongWordHardBreaksRuneSafe covers wrapComposerText's
// hard-break path: a single "word" (no spaces) longer than any line's
// budget must still wrap instead of overflowing, splitting rune-safe (never
// producing invalid UTF-8) the same way truncate does.
func TestComposer_OverlongWordHardBreaksRuneSafe(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range strings.Repeat("é", 150) {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	bar := m.composerBar()
	if !utf8.ValidString(bar) {
		t.Fatalf("composerBar() produced invalid UTF-8: %q", bar)
	}
	width := m.listWidth()
	for i, l := range strings.Split(bar, "\n") {
		if n := utf8.RuneCountInString(stripANSI(l)); n > width {
			t.Fatalf("line %d exceeds listWidth %d: %q", i, width, stripANSI(l))
		}
	}
}

func TestComposer_EscCancelsAndClearsTask(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range "fix bug" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	view := m.View()
	if strings.Contains(view, "fix bug") {
		t.Fatalf("expected esc to discard composer task, got:\n%s", view)
	}
	// The composer bar is persistent (design drift gap 4), so "closed"
	// means it reverts to the idle placeholder rather than disappearing.
	if !strings.Contains(view, "Describe a task and press Enter to launch a thread") {
		t.Fatalf("expected esc to revert the composer bar to its placeholder, got:\n%s", view)
	}
}

func TestComposer_EnterWithoutActionsIsNoop(t *testing.T) {
	m := newFixtureModel()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range "fix bug" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.Launch is unset")
	}
	if strings.Contains(m.View(), "fix bug") {
		t.Fatalf("expected composer to close even without an actions.Launch hook")
	}
}

func TestComposer_EnterCallsLaunchWithTaskAndProfileThenCloses(t *testing.T) {
	var gotTask, gotProfile string
	actions := Actions{Launch: func(task, profile string) tea.Cmd {
		gotTask, gotProfile = task, profile
		return func() tea.Msg { return ThreadLaunchedMsg{} }
	}}
	m := newFixtureModel().WithActions(actions)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range "fix bug" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")}) // -> design-session
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from submitting the composer")
	}
	if gotTask != "fix bug" || gotProfile != "design-session" {
		t.Fatalf("Launch called with (%q, %q), want (\"fix bug\", \"design-session\")", gotTask, gotProfile)
	}
	// The composer bar is persistent (design drift gap 4): "closed" means
	// the task text clears and the profile resets, not that the bar itself
	// disappears.
	view := m.View()
	if strings.Contains(view, "fix bug") {
		t.Fatalf("expected composer task to clear after submit, got:\n%s", view)
	}
	if !strings.Contains(view, "[general-agentic]") {
		t.Fatalf("expected composer profile to reset to general-agentic after submit, got:\n%s", view)
	}
}

// TestUpdate_ThreadLaunchedMsg_InsertsRowAtTopOfWorkingGroup reflects issue
// #4's ordering rule: a freshly launched thread is StatusWorking, so it
// sorts above other working/closed rows (it's stamped with "now", the most
// recent possible) but still *below* any StatusWaiting row — a working
// thread never outranks one that's actually waiting on the user, per PRD
// #1's List behavior -> Ordering row.
func TestUpdate_ThreadLaunchedMsg_InsertsRowAtTopOfWorkingGroup(t *testing.T) {
	m := newFixtureModel()
	newRow := Row{Thread: codexstate.Thread{ID: "new1", Title: "Brand new thread"}, Status: tmuxstatus.StatusWorking}
	updated, _ := m.Update(ThreadLaunchedMsg{Row: newRow})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "Brand new thread") {
		t.Fatalf("expected new row in view, got:\n%s", view)
	}
	idxWaiting := strings.Index(view, "Add dark mode")           // StatusWaiting: stays above
	idxNew := strings.Index(view, "◐ Brand new thread")          // the row itself, not the "launched ..." status line above the list
	idxOlderWorking := strings.Index(view, "◐ Refactor drainer") // StatusWorking, older
	if idxWaiting == -1 || idxNew == -1 || idxOlderWorking == -1 {
		t.Fatalf("expected all three titles in view, got:\n%s", view)
	}
	if !(idxWaiting < idxNew && idxNew < idxOlderWorking) {
		t.Fatalf("expected order [waiting group, new working row, older working row], view:\n%s", view)
	}
}

// TestUpdate_ThreadLaunchedMsg_TriggersCheckLiveness reflects the fix for
// "compose task -> Enter -> row stays stuck showing working forever even
// after the tmux session has already died": the optimistic insert can't
// safely be followed by a full Refresh (codex may not have written this
// thread's own record yet, and Refresh replaces the whole row set from
// that record — see Actions.CheckLiveness's doc comment), so a launch
// instead schedules a narrower liveness recheck for just this thread.
func TestUpdate_ThreadLaunchedMsg_TriggersCheckLiveness(t *testing.T) {
	var gotThreadID string
	called := false
	actions := Actions{CheckLiveness: func(threadID string) tea.Cmd {
		called = true
		gotThreadID = threadID
		return func() tea.Msg { return ThreadLivenessMsg{ThreadID: threadID, Status: tmuxstatus.StatusClosed} }
	}}
	m := newFixtureModel().WithActions(actions)
	newRow := Row{Thread: codexstate.Thread{ID: "new1", Title: "Brand new thread"}, Status: tmuxstatus.StatusWorking}
	updated, cmd := m.Update(ThreadLaunchedMsg{Row: newRow})
	_ = updated.(Model)
	if !called {
		t.Fatalf("expected ThreadLaunchedMsg to trigger CheckLiveness")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from ThreadLaunchedMsg")
	}
	if gotThreadID != "new1" {
		t.Fatalf("CheckLiveness called with %q, want new1", gotThreadID)
	}
}

func TestUpdate_ThreadLaunchedMsg_WithoutCheckLivenessIsStillANoopCmd(t *testing.T) {
	m := newFixtureModel()
	newRow := Row{Thread: codexstate.Thread{ID: "new1", Title: "Brand new thread"}, Status: tmuxstatus.StatusWorking}
	updated, cmd := m.Update(ThreadLaunchedMsg{Row: newRow})
	_ = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.CheckLiveness is unset")
	}
}

// TestUpdate_ThreadLivenessMsg_UpdatesJustThatRowsStatus is the other half
// of the fix: once CheckLiveness reports the thread actually died, the row
// should flip to Closed in place — not disappear (unlike a RowsRefreshedMsg
// replace) and not require a full reload.
func TestUpdate_ThreadLivenessMsg_UpdatesJustThatRowsStatus(t *testing.T) {
	m := newFixtureModel()
	// t2 ("Add dark mode") is StatusWaiting in the fixture; assert only its
	// Status field flips, everything else about the list stays put.
	updated, cmd := m.Update(ThreadLivenessMsg{ThreadID: "t2", Status: tmuxstatus.StatusClosed})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected ThreadLivenessMsg to yield no further Cmd, got %v", cmd)
	}
	view := m.View()
	if !strings.Contains(view, "Add dark mode") {
		t.Fatalf("expected the row to remain in the list, got:\n%s", view)
	}
	found := false
	for _, r := range m.rows {
		if r.Thread.ID == "t2" {
			found = true
			if r.Status != tmuxstatus.StatusClosed {
				t.Fatalf("expected t2's Status to become Closed, got %v", r.Status)
			}
		}
	}
	if !found {
		t.Fatalf("expected row t2 to still be present")
	}
}

func TestUpdate_ThreadLivenessMsg_UnknownThreadIDIsNoop(t *testing.T) {
	m := newFixtureModel()
	before := append([]Row(nil), m.rows...)
	updated, _ := m.Update(ThreadLivenessMsg{ThreadID: "does-not-exist", Status: tmuxstatus.StatusClosed})
	m = updated.(Model)
	if len(m.rows) != len(before) {
		t.Fatalf("expected row count unchanged, got %d want %d", len(m.rows), len(before))
	}
}

// TestUpdate_ThreadLiveUpdateMsg_PatchesOnlyTheTargetedRow covers ADR
// 0002's live-update message: the codex App Server pushes a
// message/token count and the UI patches the matching row in place
// (no full Refresh, no notify-hook involvement, no status
// re-derivation). The fixture's t2 starts at MessageCount=4,
// TokenCount=8200; the patch should land there and leave t1/t3 alone.
func TestUpdate_ThreadLiveUpdateMsg_PatchesOnlyTheTargetedRow(t *testing.T) {
	m := newFixtureModel()
	updated, cmd := m.Update(ThreadLiveUpdateMsg{
		ThreadID:     "t2",
		MessageCount: 7,
		TokenCount:   12345,
	})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no follow-up Cmd, got %v", cmd)
	}
	for _, r := range m.rows {
		switch r.Thread.ID {
		case "t2":
			if r.Thread.MessageCount != 7 {
				t.Errorf("t2 MessageCount = %d, want 7", r.Thread.MessageCount)
			}
			if r.Thread.TokenCount != 12345 {
				t.Errorf("t2 TokenCount = %d, want 12345", r.Thread.TokenCount)
			}
		case "t1", "t3":
			if r.Thread.MessageCount != -1 {
				t.Errorf("%s MessageCount = %d, want -1 (untouched)", r.Thread.ID, r.Thread.MessageCount)
			}
			if r.Thread.TokenCount != -1 {
				t.Errorf("%s TokenCount = %d, want -1 (untouched)", r.Thread.ID, r.Thread.TokenCount)
			}
		}
	}
}

// TestUpdate_ThreadLiveUpdateMsg_NegativeSentinelSkipsThatField is the
// half of the contract that lets one event carry only a token-usage
// delta without clobbering a known message count, and vice versa.
// The manager emits a single Event per coalesced notification with
// either field set to -1 to mean "no change for this field" — the UI
// must respect that, otherwise a token-only update would zero out the
// message count on the same row.
func TestUpdate_ThreadLiveUpdateMsg_NegativeSentinelSkipsThatField(t *testing.T) {
	m := newFixtureModel()
	// t2 fixture: MessageCount=4, TokenCount=8200. Send a token-only
	// update (MessageCount explicitly set to the -1 sentinel); the
	// handler must skip that field and leave the fixture's known 4
	// intact. Note the -1 is the contract from codexserver.Event
	// ("no change for this field"), not a Go zero value — 0 is a
	// valid "zero known messages" count and must patch.
	updated, _ := m.Update(ThreadLiveUpdateMsg{
		ThreadID:     "t2",
		MessageCount: -1,
		TokenCount:   9999,
	})
	m = updated.(Model)
	for _, r := range m.rows {
		if r.Thread.ID != "t2" {
			continue
		}
		if r.Thread.MessageCount != 4 {
			t.Errorf("MessageCount = %d, want 4 (unchanged by a token-only update)", r.Thread.MessageCount)
		}
		if r.Thread.TokenCount != 9999 {
			t.Errorf("TokenCount = %d, want 9999", r.Thread.TokenCount)
		}
	}
}

// TestUpdate_ThreadLaunchedMsg_FiresLiveSubscribe locks in the
// ADR 0002 contract that the UI calls Actions.LiveSubscribe for a
// freshly-launched thread — otherwise the manager never opens a
// live event stream and the new row's counts will lag behind the
// server until the user does a manual Refresh. We don't drive a
// real Manager here (the ui package must not import codexserver);
// the test asserts only the call boundary.
func TestUpdate_ThreadLaunchedMsg_FiresLiveSubscribe(t *testing.T) {
	var got []string
	actions := Actions{
		CheckLiveness: func(threadID string) tea.Cmd { return nil },
		LiveSubscribe: func(threadID string) tea.Cmd {
			got = append(got, threadID)
			return nil
		},
	}
	m := newFixtureModel().WithActions(actions)
	newRow := Row{Thread: codexstate.Thread{ID: "new-live", Title: "New live thread"}, Status: tmuxstatus.StatusWorking}
	_, _ = m.Update(ThreadLaunchedMsg{Row: newRow})
	if len(got) != 1 || got[0] != "new-live" {
		t.Errorf("LiveSubscribe calls = %v, want [new-live]", got)
	}
}

// TestUpdate_ArchiveDoneMsg_FiresLiveUnsubscribe is the matching
// half: when a thread is archived and the row leaves the list, the
// UI must ask the manager to drop the subscription so the App Server
// stops streaming events the cockpit no longer renders.
func TestUpdate_ArchiveDoneMsg_FiresLiveUnsubscribe(t *testing.T) {
	var got []string
	actions := Actions{
		LiveUnsubscribe: func(threadID string) tea.Cmd {
			got = append(got, threadID)
			return nil
		},
	}
	m := newFixtureModel().WithActions(actions)
	_, _ = m.Update(ArchiveDoneMsg{ThreadID: "t2", Note: "archived"})
	if len(got) != 1 || got[0] != "t2" {
		t.Errorf("LiveUnsubscribe calls = %v, want [t2]", got)
	}
}

// TestUpdate_ThreadLivenessMsg_CloseFlipsToClosedAndUnsubscribes
// verifies the third hook: when a liveness check downgrades a row to
// StatusClosed, the UI must unsubscribe from the live event stream
// (ADR 0002: only subscribe to alive threads). A no-op liveness
// re-check that doesn't change the status must not churn the
// subscription list.
func TestUpdate_ThreadLivenessMsg_CloseFlipsToClosedAndUnsubscribes(t *testing.T) {
	var sub, unsub []string
	actions := Actions{
		LiveSubscribe:   func(threadID string) tea.Cmd { sub = append(sub, threadID); return nil },
		LiveUnsubscribe: func(threadID string) tea.Cmd { unsub = append(unsub, threadID); return nil },
	}
	m := newFixtureModel().WithActions(actions)
	// t2 is StatusWaiting in the fixture. Flip it to Closed and
	// expect a LiveUnsubscribe("t2") call.
	updated, _ := m.Update(ThreadLivenessMsg{ThreadID: "t2", Status: tmuxstatus.StatusClosed})
	m = updated.(Model)
	if len(unsub) != 1 || unsub[0] != "t2" {
		t.Errorf("LiveUnsubscribe calls = %v, want [t2]", unsub)
	}
	if len(sub) != 0 {
		t.Errorf("expected no LiveSubscribe on a close flip, got %v", sub)
	}
	// Sanity: the row's status did flip to Closed.
	for _, r := range m.rows {
		if r.Thread.ID == "t2" && r.Status != tmuxstatus.StatusClosed {
			t.Errorf("t2 Status = %v, want Closed", r.Status)
		}
	}
	// And a no-op liveness re-check (same status) must not churn.
	updated, _ = m.Update(ThreadLivenessMsg{ThreadID: "t2", Status: tmuxstatus.StatusClosed})
	_ = updated.(Model)
	if len(sub) != 0 || len(unsub) != 1 {
		t.Errorf("subscription churn: sub=%v unsub=%v (expected no new calls)", sub, unsub)
	}
}

// TestUpdate_ThreadLiveUpdateMsg_UnknownThreadIDIsNoop mirrors the
// ThreadLivenessMsg no-op: a live-update for a thread the cockpit
// isn't tracking (e.g. the server's subscription list is ahead of
// the cockpit's row set after a quick re-launch) must be dropped
// silently, not panic and not surface a status-line error.
func TestUpdate_ThreadLiveUpdateMsg_UnknownThreadIDIsNoop(t *testing.T) {
	m := newFixtureModel()
	before := append([]Row(nil), m.rows...)
	updated, _ := m.Update(ThreadLiveUpdateMsg{ThreadID: "missing", MessageCount: 1, TokenCount: 1})
	m = updated.(Model)
	if len(m.rows) != len(before) {
		t.Fatalf("expected row count unchanged, got %d want %d", len(m.rows), len(before))
	}
}

func TestUpdate_ThreadLaunchErrorMsg_ShowsErrorLine(t *testing.T) {
	m := newFixtureModel()
	updated, _ := m.Update(ThreadLaunchErrorMsg{Err: errors.New("boom")})
	m = updated.(Model)
	if !strings.Contains(m.View(), "boom") {
		t.Fatalf("expected error text in view, got:\n%s", m.View())
	}
}

func TestUpdate_EnterOnRow_CallsAttachWithSelectedRow(t *testing.T) {
	var gotRow Row
	called := false
	actions := Actions{Attach: func(row Row) tea.Cmd {
		called = true
		gotRow = row
		return func() tea.Msg { return AttachDoneMsg{} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	_ = m
	if !called {
		t.Fatalf("expected Attach to be called")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from Enter on a row")
	}
	if gotRow.Thread.Title != "Add dark mode" {
		t.Fatalf("expected Attach called with the selected row, got %+v", gotRow)
	}
}

func TestUpdate_EnterOnRow_WithoutActionsIsNoop(t *testing.T) {
	m := newFixtureModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.Attach is unset")
	}
}

func TestUpdate_AttachDoneMsg_TriggersRefresh(t *testing.T) {
	refreshCalled := false
	actions := Actions{Refresh: func() tea.Cmd {
		refreshCalled = true
		return func() tea.Msg { return RowsRefreshedMsg{} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(AttachDoneMsg{})
	_ = updated.(Model)
	if !refreshCalled {
		t.Fatalf("expected AttachDoneMsg to trigger Refresh")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from AttachDoneMsg")
	}
}

func TestUpdate_XOnRow_CallsInterruptWithSelectedRow(t *testing.T) {
	var gotRow Row
	called := false
	actions := Actions{Interrupt: func(row Row) tea.Cmd {
		called = true
		gotRow = row
		return func() tea.Msg { return InterruptDoneMsg{ThreadID: row.Thread.ID} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	_ = updated.(Model)
	if !called {
		t.Fatalf("expected Interrupt to be called")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from 'x' on a row")
	}
	if gotRow.Thread.Title != "Add dark mode" {
		t.Fatalf("expected Interrupt called with the selected row, got %+v", gotRow)
	}
}

func TestUpdate_XOnRow_WithoutActionsIsNoop(t *testing.T) {
	m := newFixtureModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	_ = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.Interrupt is unset")
	}
}

func TestUpdate_InterruptDoneMsg_ShowsStatusLineAndTriggersRefresh(t *testing.T) {
	refreshCalled := false
	actions := Actions{Refresh: func() tea.Cmd {
		refreshCalled = true
		return func() tea.Msg { return RowsRefreshedMsg{} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(InterruptDoneMsg{ThreadID: "t2"})
	m = updated.(Model)
	if !refreshCalled {
		t.Fatalf("expected InterruptDoneMsg to trigger Refresh")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from InterruptDoneMsg")
	}
	if !strings.Contains(m.View(), "interrupted Add dark mode") {
		t.Fatalf("expected status line naming the interrupted thread, got:\n%s", m.View())
	}
}

func TestUpdate_AOnRow_CallsArchiveWithSelectedRow(t *testing.T) {
	var gotRow Row
	called := false
	actions := Actions{Archive: func(row Row) tea.Cmd {
		called = true
		gotRow = row
		return func() tea.Msg { return ArchiveDoneMsg{ThreadID: row.Thread.ID, Note: "archived " + row.Thread.Title} }
	}}
	m := newFixtureModel().WithActions(actions)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	_ = updated.(Model)
	if !called {
		t.Fatalf("expected Archive to be called")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from 'a' on a row")
	}
	if gotRow.Thread.Title != "Add dark mode" {
		t.Fatalf("expected Archive called with the selected row, got %+v", gotRow)
	}
}

func TestUpdate_AOnRow_WithoutActionsIsNoop(t *testing.T) {
	m := newFixtureModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	_ = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no cmd when Actions.Archive is unset")
	}
}

func TestUpdate_ArchiveDoneMsg_RemovesRowAndShowsNote(t *testing.T) {
	m := newFixtureModel()
	updated, _ := m.Update(ArchiveDoneMsg{ThreadID: "t2", Note: "archived; worktree removed"})
	m = updated.(Model)
	view := m.View()
	if strings.Contains(view, "Add dark mode") {
		t.Fatalf("expected archived thread's row to disappear from the list, got:\n%s", view)
	}
	if !strings.Contains(view, "archived; worktree removed") {
		t.Fatalf("expected archive note in status line, got:\n%s", view)
	}
	// The other two fixture rows are untouched.
	if !strings.Contains(view, "Refactor drainer") || !strings.Contains(view, "Fix auth hook") {
		t.Fatalf("expected other rows to remain, got:\n%s", view)
	}
}

// TestUpdate_RowsRefreshedMsg_MergesRows covers the refresh side of the
// issue #25 Bug 1 merge: rows present in the refreshed set land in the
// model (and rows present in both take the refreshed version — see
// TestUpdate_RowsRefreshedMsg_RefreshedRowsWin in refresh_merge_test.go).
// Existing rows absent from the refreshed set are no longer dropped
// wholesale: they are kept unless archived, which is what protects a
// just-launched thread codex hasn't persisted yet.
func TestUpdate_RowsRefreshedMsg_MergesRows(t *testing.T) {
	m := newFixtureModel()
	newRows := []Row{{Thread: codexstate.Thread{ID: "only1", Title: "Only thread"}, Status: tmuxstatus.StatusClosed}}
	updated, _ := m.Update(RowsRefreshedMsg{Rows: newRows})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "Only thread") {
		t.Fatalf("expected refreshed rows in view, got:\n%s", view)
	}
	// The fixture rows are neither in the refreshed set nor archived, so
	// the merge keeps them alongside the new row.
	if !strings.Contains(view, "Add dark mode") || !strings.Contains(view, "Refactor drainer") || !strings.Contains(view, "Fix auth hook") {
		t.Fatalf("expected existing rows to survive the merge, got:\n%s", view)
	}
}

// TestModel_WithProfilesNilYieldsEmptyProfile guards the no-profiles-on-disk
// state: if the caller (or a test, or a future main.go) forgets to call
// WithProfiles, or hands us an empty slice, the composer's selected profile
// must be "" — the launch then goes ahead with no -p flag, letting codex
// fall back to its own default. It must never panic on a nil-index or
// silently substitute a hard-coded profile name.
func TestModel_WithProfilesNilYieldsEmptyProfile(t *testing.T) {
	for _, in := range [][]string{nil, {}} {
		m := New(fixtureRows()).WithProfiles(in)
		if got := m.composerProfile(); got != "" {
			t.Errorf("WithProfiles(%v): composerProfile() = %q, want %q", in, got, "")
		}
	}
}

// TestModel_AtOnEmptyProfilesIsNoOp confirms that pressing @ while the
// composer is focused and no profiles are on disk is a no-op: no panic
// from a divide-by-zero on the empty slice, the pill stays empty
// (composerProfile() keeps returning ""), and the launch that follows
// gets an empty profile string. Using composerProfile() directly (rather
// than scanning the rendered view) avoids picking up the row meta badge
// ([general-agentic]) that fixtureRows still carries on its
// add-dark-mode row — that badge is the row's *thread profile*, not the
// composer's selected profile.
func TestModel_AtOnEmptyProfilesIsNoOp(t *testing.T) {
	m := newFixtureModel().WithProfiles(nil)
	if got := m.composerProfile(); got != "" {
		t.Fatalf("before @: composerProfile() = %q, want %q", got, "")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if got := m.composerProfile(); got != "" {
		t.Fatalf("after focus: composerProfile() = %q, want %q", got, "")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if got := m.composerProfile(); got != "" {
		t.Fatalf("after @: composerProfile() = %q, want %q", got, "")
	}
	// Sanity: the view should still render the empty pill, not silently
	// fall back to a hard-coded name.
	if !strings.Contains(m.View(), "[]") {
		t.Fatalf("expected empty pill [] in view after @, got:\n%s", m.View())
	}
}

// TestModel_WithProfilesCustomListCoversAtCycle confirms the discovered
// list flows through to the composer's @ cycle: a custom two-name list
// rotates through both and wraps, instead of using the old hard-coded
// three-name cycle. This is the behaviour change the PR is shipping.
func TestModel_WithProfilesCustomListCoversAtCycle(t *testing.T) {
	m := New(fixtureRows()).WithProfiles([]string{"alpha", "beta"})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !strings.Contains(m.View(), "[alpha]") {
		t.Fatalf("expected default profile 'alpha' in view, got:\n%s", m.View())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "[beta]") {
		t.Fatalf("expected profile to cycle to 'beta', got:\n%s", m.View())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !strings.Contains(m.View(), "[alpha]") {
		t.Fatalf("expected profile to cycle back to 'alpha', got:\n%s", m.View())
	}
}
