package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

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

// TestRenderDetail_AllFieldsKnown pins renderDetail's output for a fully
// known thread to today's format: same labels, same order, same 4-space
// indent, byte-equal to what the pre-#19 dash-fallback implementation
// produced for the same input (since every field is known, there's no
// fallback to differ).
func TestRenderDetail_AllFieldsKnown(t *testing.T) {
	r := Row{
		Thread: codexstate.Thread{Model: "m", Profile: "p", TokenCount: 8200, CWD: "/x"},
		Status: tmuxstatus.StatusWaiting,
	}
	got := renderDetail(r)
	want := detailStyle.Render("    model: m  profile: p  tokens: 8200  cwd: /x")
	if got != want {
		t.Fatalf("renderDetail() = %q, want %q", got, want)
	}
}

// TestRenderDetail_PartialFieldsOmitsUnknown covers a thread with only
// model and cwd known (profile "" and tokens -1, the codexstate "unknown"
// sentinels): the line should contain just those two fields, in canonical
// order, with no "profile:"/"tokens:" labels and no "-" placeholder.
func TestRenderDetail_PartialFieldsOmitsUnknown(t *testing.T) {
	r := Row{
		Thread: codexstate.Thread{Model: "m", Profile: "", TokenCount: -1, CWD: "/x"},
	}
	got := renderDetail(r)
	want := detailStyle.Render("    model: m  cwd: /x")
	if got != want {
		t.Fatalf("renderDetail() = %q, want %q", got, want)
	}
	if strings.Contains(got, "profile:") || strings.Contains(got, "tokens:") || strings.Contains(got, "-") {
		t.Fatalf("expected no profile/tokens labels or dash placeholder, got %q", got)
	}
}

// TestRenderDetail_TokenCountZeroIsKnown asserts TokenCount == 0 renders
// as "tokens: 0" — it's a known zero, not the negative "unknown" sentinel.
func TestRenderDetail_TokenCountZeroIsKnown(t *testing.T) {
	r := Row{
		Thread: codexstate.Thread{TokenCount: 0},
	}
	got := renderDetail(r)
	if !strings.Contains(got, "tokens: 0") {
		t.Fatalf("expected 'tokens: 0' for a known zero token count, got %q", got)
	}
}

// TestRenderDetail_AllFieldsUnknownRendersBlank covers the (practically
// unreachable, since CWD is always recorded) edge where every field is
// unknown: the rendered content is just the 4-space indent, no labels.
func TestRenderDetail_AllFieldsUnknownRendersBlank(t *testing.T) {
	r := Row{
		Thread: codexstate.Thread{TokenCount: -1},
	}
	got := renderDetail(r)
	want := detailStyle.Render("    ")
	if got != want {
		t.Fatalf("renderDetail() = %q, want %q", got, want)
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
