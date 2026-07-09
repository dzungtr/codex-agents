package ui

import (
	"testing"

	"github.com/dzungtr/codex-agents/internal/codexstate"
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
