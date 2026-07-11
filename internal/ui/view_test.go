package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dzungtr/codex-agents/internal/codexstate"
)

// stripANSI removes SGR escape sequences (the only kind lipgloss emits) so
// width/suffix assertions below operate on the runes a terminal would
// actually lay out, not the styling bytes around them. Test-run lipgloss
// output has none anyway (no tty -> no color profile, per the existing
// golden files), but stripping keeps these assertions robust either way.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestMetaColumn covers the Testing Decisions in issue #18: metaColumn joins
// only the present parts (repo label, git branch) with " · ", so a missing
// part never leaves a dangling separator.
func TestMetaColumn(t *testing.T) {
	th := codexstate.Thread{CWD: "/Users/tony/web-app", GitBranch: "add-dark-mode"}
	want := "web-app · add-dark-mode"
	if got := metaColumn(th); got != want {
		t.Errorf("metaColumn(%+v) = %q, want %q", th, got, want)
	}
}

func TestMetaColumn_RepoOnly(t *testing.T) {
	th := codexstate.Thread{CWD: "/Users/tony/web-app", GitBranch: ""}
	want := "web-app"
	if got := metaColumn(th); got != want {
		t.Errorf("metaColumn(%+v) = %q, want %q (no trailing separator)", th, got, want)
	}
}

func TestMetaColumn_BranchOnly(t *testing.T) {
	th := codexstate.Thread{CWD: "", GitBranch: "add-dark-mode"}
	want := "add-dark-mode"
	if got := metaColumn(th); got != want {
		t.Errorf("metaColumn(%+v) = %q, want %q (no leading separator)", th, got, want)
	}
}

func TestMetaColumn_Neither(t *testing.T) {
	th := codexstate.Thread{CWD: "", GitBranch: ""}
	want := ""
	if got := metaColumn(th); got != want {
		t.Errorf("metaColumn(%+v) = %q, want %q", th, got, want)
	}
}

// TestDetailParts_AllFieldsKnown pins detailParts' output for a fully known
// thread to today's format: same label, same (now single-element) result.
// Ported from issue #19's TestRenderDetail_AllFieldsKnown now that the
// parts-building logic lives in detailParts rather than renderDetail (issue
// #20). Model and Profile are set on th to prove detailParts ignores them
// now that design drift gap 3 moved those two into badgeClusterPlain
// instead. CWD is set too, to prove detailParts ignores that as well: the
// composer-fidelity fix dropped cwd from detailParts entirely (see its doc
// comment) — it crowded out the selected row's badge cluster and duplicated
// the composer bar's "Launches detached in <dir>" hint.
func TestDetailParts_AllFieldsKnown(t *testing.T) {
	th := codexstate.Thread{Model: "m", Profile: "p", TokenCount: 8200, CWD: "/x"}
	got := detailParts(th)
	want := []string{"tokens: 8200"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detailParts(%+v) = %v, want %v", th, got, want)
	}
}

// TestDetailParts_PartialFieldsOmitsUnknown covers a thread with CWD known
// but tokens unknown (-1, the codexstate "unknown" sentinel): since cwd is
// no longer a detailParts field at all, the result should be empty, not a
// "-" placeholder. Ported from issue #19's
// TestRenderDetail_PartialFieldsOmitsUnknown.
func TestDetailParts_PartialFieldsOmitsUnknown(t *testing.T) {
	th := codexstate.Thread{Model: "m", Profile: "", TokenCount: -1, CWD: "/x"}
	got := detailParts(th)
	if len(got) != 0 {
		t.Fatalf("detailParts(%+v) = %v, want empty", th, got)
	}
}

// TestBadgeClusterPlain_AllFieldsKnown pins badgeClusterPlain's output for
// a fully known thread: model and profile bracketed, message count as a
// bare "N msgs" suffix, in that order (design drift gap 3).
func TestBadgeClusterPlain_AllFieldsKnown(t *testing.T) {
	th := codexstate.Thread{Model: "m", Profile: "p", MessageCount: 4}
	want := "[m] [p] 4 msgs"
	if got := badgeClusterPlain(th); got != want {
		t.Fatalf("badgeClusterPlain(%+v) = %q, want %q", th, got, want)
	}
}

// TestBadgeClusterPlain_PartialFieldsOmitsUnknown covers a thread with only
// Model known (Profile "" and MessageCount -1, both "unknown" sentinels):
// only the model badge should render.
func TestBadgeClusterPlain_PartialFieldsOmitsUnknown(t *testing.T) {
	th := codexstate.Thread{Model: "m", Profile: "", MessageCount: -1}
	want := "[m]"
	if got := badgeClusterPlain(th); got != want {
		t.Fatalf("badgeClusterPlain(%+v) = %q, want %q", th, got, want)
	}
}

// TestBadgeClusterPlain_MessageCountZeroIsKnown asserts MessageCount == 0
// produces a "0 msgs" part — it's a known zero, not the negative "unknown"
// sentinel (mirrors TestDetailParts_TokenCountZeroIsKnown's reasoning).
func TestBadgeClusterPlain_MessageCountZeroIsKnown(t *testing.T) {
	th := codexstate.Thread{MessageCount: 0}
	want := "0 msgs"
	if got := badgeClusterPlain(th); got != want {
		t.Fatalf("badgeClusterPlain(%+v) = %q, want %q", th, got, want)
	}
}

// TestBadgeClusterPlain_AllFieldsUnknownReturnsEmpty covers the edge where
// every field is unknown: badgeClusterPlain returns "", no labels.
func TestBadgeClusterPlain_AllFieldsUnknownReturnsEmpty(t *testing.T) {
	th := codexstate.Thread{MessageCount: -1}
	if got := badgeClusterPlain(th); got != "" {
		t.Fatalf("badgeClusterPlain(%+v) = %q, want empty", th, got)
	}
}

// TestDetailParts_TokenCountZeroIsKnown asserts TokenCount == 0 produces a
// "tokens: 0" part — it's a known zero, not the negative "unknown" sentinel.
// Ported from issue #19's TestRenderDetail_TokenCountZeroIsKnown.
func TestDetailParts_TokenCountZeroIsKnown(t *testing.T) {
	th := codexstate.Thread{TokenCount: 0}
	got := detailParts(th)
	want := []string{"tokens: 0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detailParts(%+v) = %v, want %v", th, got, want)
	}
}

// TestDetailParts_AllFieldsUnknownReturnsEmpty covers the (practically
// unreachable, since CWD is always recorded) edge where every field is
// unknown: detailParts returns an empty slice, no labels. Ported from issue
// #19's TestRenderDetail_AllFieldsUnknownRendersBlank.
func TestDetailParts_AllFieldsUnknownReturnsEmpty(t *testing.T) {
	th := codexstate.Thread{TokenCount: -1}
	got := detailParts(th)
	if len(got) != 0 {
		t.Fatalf("detailParts(%+v) = %v, want empty", th, got)
	}
}

// TestRenderMetaLine_NotSelected_ShowsMetaAndBadgesNotDetail covers design
// drift gap 3: a non-selected row's line 2 shows metaColumn's repo·branch
// plus the badge cluster (model/profile — shown regardless of selection),
// but never detailParts' tokens/cwd, which stay selected-only (issue #20
// decision 4, still true for the fields that remain in detailParts).
func TestRenderMetaLine_NotSelected_ShowsMetaAndBadgesNotDetail(t *testing.T) {
	r := Row{Thread: codexstate.Thread{
		CWD: "/Users/tony/web-app", GitBranch: "add-dark-mode",
		Model: "m", Profile: "p", TokenCount: 1, MessageCount: -1,
	}}
	got := stripANSI(renderMetaLine(r, false, 80))
	if !strings.Contains(got, "web-app · add-dark-mode") {
		t.Fatalf("expected metaColumn in a non-selected row's line 2, got %q", got)
	}
	if !strings.Contains(got, "[m]") || !strings.Contains(got, "[p]") {
		t.Fatalf("expected model/profile badges on a non-selected row, got %q", got)
	}
	if strings.Contains(got, "tokens:") {
		t.Fatalf("expected tokens (detailParts) to stay selected-only, got %q", got)
	}
}

// TestRenderMetaLine_Selected_AppendsDetailPartsAndBadges covers issue #20
// decision 4 (detailParts appends after metaColumn when selected) plus
// design drift gap 3 (the badge cluster also appears, and a selected row's
// line 2 is padded to the full width for the background wash — see
// renderRow's line 1 doing the same, and selectedStyle's doc comment). CWD
// is set on th too, to prove it stays out of the rendered line now that the
// composer-fidelity fix dropped cwd from detailParts entirely.
func TestRenderMetaLine_Selected_AppendsDetailPartsAndBadges(t *testing.T) {
	r := Row{Thread: codexstate.Thread{CWD: "/x", Model: "m", Profile: "", TokenCount: 100, MessageCount: 2}}
	got := stripANSI(renderMetaLine(r, true, 80))
	if !strings.Contains(got, "tokens: 100") {
		t.Fatalf("expected detailParts' tokens on the selected row, got %q", got)
	}
	if strings.Contains(got, "cwd:") {
		t.Fatalf("expected cwd to stay out of the selected row's detail line, got %q", got)
	}
	if !strings.Contains(got, "[m]") {
		t.Fatalf("expected model badge on the selected row, got %q", got)
	}
	if !strings.Contains(got, "2 msgs") {
		t.Fatalf("expected message-count badge on the selected row, got %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 80 {
		t.Fatalf("expected a selected row's line 2 padded to the full width (background wash), got length %d: %q", n, got)
	}
}

// TestRenderMetaLine_EmptyMeta_NotSelected_IndentOnly covers issue #20
// decision 7: a non-selected row with no repo/branch and no known badge
// fields still renders line 2 as the bare faint 4-space indent (constant
// block height).
func TestRenderMetaLine_EmptyMeta_NotSelected_IndentOnly(t *testing.T) {
	r := Row{Thread: codexstate.Thread{MessageCount: -1}}
	got := renderMetaLine(r, false, 80)
	want := detailStyle.Render("    ")
	if got != want {
		t.Fatalf("renderMetaLine() = %q, want %q", got, want)
	}
}

// TestRenderMetaLine_OverlongTruncatesNeverWraps covers issue #20 decision
// 8: line 2 content longer than the available width truncates with "…"
// instead of wrapping into a third terminal row. MessageCount is pinned to
// -1 (unknown) so the badge cluster stays empty and this test isolates the
// left-side (meta) truncation math design drift gap 3 didn't change.
func TestRenderMetaLine_OverlongTruncatesNeverWraps(t *testing.T) {
	r := Row{Thread: codexstate.Thread{CWD: "/" + strings.Repeat("a", 100), MessageCount: -1}}
	got := renderMetaLine(r, false, 30)
	if strings.Contains(got, "\n") {
		t.Fatalf("renderMetaLine() wrapped into multiple lines: %q", got)
	}
	stripped := stripANSI(got)
	if !strings.Contains(stripped, "…") {
		t.Fatalf("expected overlong line 2 to truncate with an ellipsis, got %q", stripped)
	}
	if n := utf8.RuneCountInString(stripped); n > 30 {
		t.Fatalf("renderMetaLine() rune length = %d, want <= 30 (width), got %q", n, stripped)
	}
}

// TestRenderRow_ReturnsExactlyTwoLines covers issue #20 decision 1:
// renderRow always returns line1 + "\n" + line2, exactly one newline.
func TestRenderRow_ReturnsExactlyTwoLines(t *testing.T) {
	r := Row{Thread: codexstate.Thread{Title: "Add dark mode", Recency: fixedNow()}}
	got := renderRow(r, false, fixedNow(), 80)
	if n := strings.Count(got, "\n"); n != 1 {
		t.Fatalf("renderRow() has %d newlines, want exactly 1 (two lines), got %q", n, got)
	}
}

// TestRenderRow_Line1WidthTracksWidthParam covers issue #20 Testing
// Decisions item 2: at widths 80 and 120, the ANSI-stripped line 1 is
// exactly width runes and ends with the age string (age right-aligns to
// the terminal width regardless of title length).
func TestRenderRow_Line1WidthTracksWidthParam(t *testing.T) {
	for _, width := range []int{80, 120} {
		r := Row{Thread: codexstate.Thread{Title: "Add dark mode", Recency: fixedNow().Add(-3 * time.Minute)}}
		got := renderRow(r, false, fixedNow(), width)
		line1 := strings.SplitN(got, "\n", 2)[0]
		stripped := stripANSI(line1)
		if n := utf8.RuneCountInString(stripped); n != width {
			t.Fatalf("width %d: line1 rune length = %d, want %d (line1=%q)", width, n, width, stripped)
		}
		age := ageString(fixedNow(), r.Thread.Recency)
		if !strings.HasSuffix(stripped, age) {
			t.Fatalf("width %d: line1 = %q, want suffix %q", width, stripped, age)
		}
	}
}

// TestRenderRow_LongTitleTruncatesRuneSafe covers issue #20 Testing
// Decisions item 4: a title longer than its budget truncates with #17's
// rune-safe truncate, the age still terminates the line, and a multibyte
// title never produces invalid UTF-8.
func TestRenderRow_LongTitleTruncatesRuneSafe(t *testing.T) {
	r := Row{Thread: codexstate.Thread{Title: strings.Repeat("é", 200), Recency: fixedNow()}}
	got := renderRow(r, false, fixedNow(), 80)
	line1 := strings.SplitN(got, "\n", 2)[0]
	if !utf8.ValidString(line1) {
		t.Fatalf("renderRow() line1 is not valid UTF-8: %q", line1)
	}
	if !strings.Contains(line1, "…") {
		t.Fatalf("expected a truncated title with an ellipsis, got %q", line1)
	}
	age := ageString(fixedNow(), r.Thread.Recency)
	if !strings.HasSuffix(stripANSI(line1), age) {
		t.Fatalf("expected line1 to end with age %q, got %q", age, line1)
	}
}

// TestListWidth covers issue #20 decision 2: m.width when positive, 80
// before the first WindowSizeMsg, floored at 20 in degenerate terminals,
// and capped at maxContentWidth on a very wide one (design drift's
// column-layout-looseness note: uncapped, a wide real terminal leaves a
// huge incoherent gap between a row's meta text and its badge cluster).
func TestListWidth(t *testing.T) {
	cases := []struct {
		width int
		want  int
	}{
		{0, 80},
		{10, 20},
		{120, 120},
		{300, maxContentWidth},
	}
	for _, c := range cases {
		m := Model{width: c.width}
		if got := m.listWidth(); got != c.want {
			t.Errorf("listWidth() with m.width=%d = %d, want %d", c.width, got, c.want)
		}
	}
}

// TestDisplayTitle covers issue #17's fallback order: Title (if non-blank
// after TrimSpace) wins outright; otherwise FirstMessage collapsed to one
// line via strings.Fields/Join; otherwise "".

func TestDisplayTitle_TitleWins(t *testing.T) {
	th := codexstate.Thread{Title: "Add dark mode", FirstMessage: "please add a dark mode toggle"}
	want := "Add dark mode"
	if got := displayTitle(th); got != want {
		t.Errorf("displayTitle(%+v) = %q, want %q", th, got, want)
	}
}

func TestDisplayTitle_EmptyTitleFallsBackToFirstMessage(t *testing.T) {
	th := codexstate.Thread{Title: "", FirstMessage: "please add a dark mode toggle"}
	want := "please add a dark mode toggle"
	if got := displayTitle(th); got != want {
		t.Errorf("displayTitle(%+v) = %q, want %q", th, got, want)
	}
}

func TestDisplayTitle_WhitespaceOnlyTitleFallsBackToFirstMessage(t *testing.T) {
	th := codexstate.Thread{Title: "   ", FirstMessage: "please add a dark mode toggle"}
	want := "please add a dark mode toggle"
	if got := displayTitle(th); got != want {
		t.Errorf("displayTitle(%+v) = %q, want %q", th, got, want)
	}
}

func TestDisplayTitle_FirstMessageWithNewlinesAndTabsCollapsesToOneLine(t *testing.T) {
	th := codexstate.Thread{Title: "", FirstMessage: "please add\na dark\tmode  toggle"}
	want := "please add a dark mode toggle"
	if got := displayTitle(th); got != want {
		t.Errorf("displayTitle(%+v) = %q, want %q", th, got, want)
	}
}

func TestDisplayTitle_BothEmptyReturnsEmptyString(t *testing.T) {
	th := codexstate.Thread{Title: "", FirstMessage: ""}
	want := ""
	if got := displayTitle(th); got != want {
		t.Errorf("displayTitle(%+v) = %q, want %q", th, got, want)
	}
}

// TestTruncate covers issue #17's rune-safety requirement: truncate must
// operate on runes, not bytes, so a multibyte string is never cut
// mid-character (which would produce invalid UTF-8), while ASCII behavior
// (byte-identical to the previous implementation) and existing golden files
// stay unchanged.

func TestTruncate_ShortInputReturnedAsIs(t *testing.T) {
	s := "short"
	if got := truncate(s, 42); got != s {
		t.Errorf("truncate(%q, 42) = %q, want %q (unchanged)", s, got, s)
	}
}

func TestTruncate_ASCIIBehaviorUnchanged(t *testing.T) {
	s := "this is a longer ascii string that needs truncating"
	n := 38 // rune count, not byte count (the "…" suffix is 3 bytes but 1 rune)
	want := "this is a longer ascii string that ne…"
	got := truncate(s, n)
	if got != want {
		t.Errorf("truncate(%q, %d) = %q, want %q", s, n, got, want)
	}
}

func TestTruncate_MultibyteInputCutRuneSafeWithEllipsis(t *testing.T) {
	// Each "é" is a single rune but two bytes in UTF-8; a byte-indexed slice
	// at an odd boundary would split one in half and produce invalid UTF-8.
	s := "éééééééééé" // 10 runes, 20 bytes
	got := truncate(s, 5)
	if !utf8.ValidString(got) {
		t.Errorf("truncate(%q, 5) = %q, not valid UTF-8", s, got)
	}
	want := "éééé…"
	if got != want {
		t.Errorf("truncate(%q, 5) = %q, want %q", s, got, want)
	}
}
