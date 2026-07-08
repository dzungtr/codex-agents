package codexlaunch

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Fix auth hook", "fix-auth-hook"},
		{"  Add   dark MODE!! ", "add-dark-mode"},
		{"Refactor drainer/cleanup (v2)", "refactor-drainer-cleanup-v2"},
		{"", "task"},
		{"!!!", "task"},
		{"日本語 title", "title"},
	}
	for _, tt := range tests {
		if got := Slugify(tt.title); got != tt.want {
			t.Errorf("Slugify(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}

func TestUniqueSlug_NoCollision(t *testing.T) {
	taken := func(string) bool { return false }
	if got := UniqueSlug("fix-auth-hook", taken); got != "fix-auth-hook" {
		t.Errorf("UniqueSlug() = %q, want unchanged base", got)
	}
}

func TestUniqueSlug_CollisionAppendsNumericSuffix(t *testing.T) {
	takenSet := map[string]bool{
		"fix-auth-hook":   true,
		"fix-auth-hook-2": true,
	}
	taken := func(s string) bool { return takenSet[s] }
	if got := UniqueSlug("fix-auth-hook", taken); got != "fix-auth-hook-3" {
		t.Errorf("UniqueSlug() = %q, want fix-auth-hook-3", got)
	}
}

func TestBranchSlug_CombinesSlugifyAndUniqueSlug(t *testing.T) {
	takenSet := map[string]bool{"fix-auth-hook": true}
	taken := func(s string) bool { return takenSet[s] }
	if got := BranchSlug("Fix auth hook", taken); got != "fix-auth-hook-2" {
		t.Errorf("BranchSlug() = %q, want fix-auth-hook-2", got)
	}
}
