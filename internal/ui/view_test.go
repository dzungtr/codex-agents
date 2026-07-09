package ui

import (
	"strings"
	"testing"

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
