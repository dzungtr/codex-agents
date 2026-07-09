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
// thread to today's format: same labels, same canonical order. Ported from
// issue #19's TestRenderDetail_AllFieldsKnown now that the parts-building
// logic lives in detailParts rather than renderDetail (issue #20).
func TestDetailParts_AllFieldsKnown(t *testing.T) {
	th := codexstate.Thread{Model: "m", Profile: "p", TokenCount: 8200, CWD: "/x"}
	got := detailParts(th)
	want := []string{"model: m", "profile: p", "tokens: 8200", "cwd: /x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detailParts(%+v) = %v, want %v", th, got, want)
	}
}

// TestDetailParts_PartialFieldsOmitsUnknown covers a thread with only model
// and cwd known (profile "" and tokens -1, the codexstate "unknown"
// sentinels): the result should contain just those two parts, in canonical
// order, with no "profile:"/"tokens:" labels and no "-" placeholder. Ported
// from issue #19's TestRenderDetail_PartialFieldsOmitsUnknown.
func TestDetailParts_PartialFieldsOmitsUnknown(t *testing.T) {
	th := codexstate.Thread{Model: "m", Profile: "", TokenCount: -1, CWD: "/x"}
	got := detailParts(th)
	want := []string{"model: m", "cwd: /x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detailParts(%+v) = %v, want %v", th, got, want)
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

// TestRenderMetaLine_NotSelected_MetaOnly covers issue #20 decision 4: a
// non-selected row's line 2 is just the faint 4-space indent plus
// metaColumn's output — detail parts never appear unless selected.
func TestRenderMetaLine_NotSelected_MetaOnly(t *testing.T) {
	r := Row{Thread: codexstate.Thread{
		CWD: "/Users/tony/web-app", GitBranch: "add-dark-mode",
		Model: "m", Profile: "p", TokenCount: 1,
	}}
	got := renderMetaLine(r, false, 80)
	want := detailStyle.Render("    web-app · add-dark-mode")
	if got != want {
		t.Fatalf("renderMetaLine(selected=false) = %q, want %q", got, want)
	}
}

// TestRenderMetaLine_Selected_AppendsDetailParts covers issue #20 decision
// 4: a selected row's line 2 appends detailParts after metaColumn, joined
// with the same two-space gap used between other parts.
func TestRenderMetaLine_Selected_AppendsDetailParts(t *testing.T) {
	r := Row{Thread: codexstate.Thread{CWD: "/x", Model: "m", Profile: "", TokenCount: -1}}
	got := renderMetaLine(r, true, 80)
	want := detailStyle.Render("    x  model: m  cwd: /x")
	if got != want {
		t.Fatalf("renderMetaLine(selected=true) = %q, want %q", got, want)
	}
}

// TestRenderMetaLine_EmptyMeta_NotSelected_IndentOnly covers issue #20
// decision 7: a non-selected row with no repo/branch still renders line 2
// as the bare faint 4-space indent (constant block height).
func TestRenderMetaLine_EmptyMeta_NotSelected_IndentOnly(t *testing.T) {
	r := Row{Thread: codexstate.Thread{}}
	got := renderMetaLine(r, false, 80)
	want := detailStyle.Render("    ")
	if got != want {
		t.Fatalf("renderMetaLine() = %q, want %q", got, want)
	}
}

// TestRenderMetaLine_OverlongTruncatesNeverWraps covers issue #20 decision
// 8: line 2 content longer than the available width truncates with "…"
// instead of wrapping into a third terminal row.
func TestRenderMetaLine_OverlongTruncatesNeverWraps(t *testing.T) {
	r := Row{Thread: codexstate.Thread{CWD: "/" + strings.Repeat("a", 100)}}
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
// before the first WindowSizeMsg, floored at 20 in degenerate terminals.
func TestListWidth(t *testing.T) {
	cases := []struct {
		width int
		want  int
	}{
		{0, 80},
		{10, 20},
		{120, 120},
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
