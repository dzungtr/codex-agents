package codexlaunch

import (
	"fmt"
	"regexp"
	"strings"
)

// nonSlugRun matches any run of characters that aren't lowercase ascii
// letters or digits, so it can be collapsed to a single hyphen. Non-ASCII
// title text is dropped rather than transliterated (a title of only
// non-ASCII words falls back to "task").
var nonSlugRun = regexp.MustCompile(`[^a-z0-9]+`)

// fallbackSlug is used when a title slugifies to nothing (empty title, or a
// title made entirely of punctuation/non-ASCII text).
const fallbackSlug = "task"

// Slugify turns a task title into a branch-name-safe slug: lowercased,
// non-alphanumeric runs collapsed to single hyphens, leading/trailing
// hyphens trimmed. An empty result falls back to "task".
func Slugify(title string) string {
	lower := strings.ToLower(title)
	slug := nonSlugRun.ReplaceAllString(lower, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return fallbackSlug
	}
	return slug
}

// UniqueSlug returns base unless taken(base) is true, in which case it
// tries base-2, base-3, ... until it finds a slug that isn't taken. This is
// the collision-suffixing rule from PRD #1's Launch semantics table.
func UniqueSlug(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !taken(candidate) {
			return candidate
		}
	}
}

// BranchSlug is the combined "title -> unique branch slug" pipeline used
// when resolving a launch's worktree.
func BranchSlug(title string, taken func(string) bool) string {
	return UniqueSlug(Slugify(title), taken)
}
