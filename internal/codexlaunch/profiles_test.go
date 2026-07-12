package codexlaunch

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDiscoverProfiles_MissingDirReturnsEmptyNoError(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	got, err := DiscoverProfiles(missing)
	if err != nil {
		t.Fatalf("DiscoverProfiles(%q) returned err = %v, want nil", missing, err)
	}
	if got == nil {
		t.Fatalf("DiscoverProfiles returned nil slice; want empty non-nil")
	}
	if len(got) != 0 {
		t.Errorf("DiscoverProfiles = %v, want empty", got)
	}
}

func TestDiscoverProfiles_EmptyDirReturnsEmptyNoError(t *testing.T) {
	dir := t.TempDir()
	got, err := DiscoverProfiles(dir)
	if err != nil {
		t.Fatalf("DiscoverProfiles returned err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("DiscoverProfiles = %v, want empty", got)
	}
}

func TestDiscoverProfiles_EmptyCodexHomeReturnsEmptyNoError(t *testing.T) {
	got, err := DiscoverProfiles("")
	if err != nil {
		t.Fatalf("DiscoverProfiles(\"\") returned err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("DiscoverProfiles = %v, want empty", got)
	}
}

// TestDiscoverProfiles_AlphabeticalSort proves the discoverer returns
// the discovered names in alphabetical order, regardless of write
// order on disk. There is no promotion of any name (e.g. an old
// "general-agentic" sentinel) to the front - the composer's default
// profile is simply whatever lands at index 0.
func TestDiscoverProfiles_AlphabeticalSort(t *testing.T) {
	dir := t.TempDir()
	// Write in non-alphabetical order to prove sort runs.
	files := []string{
		"review.config.toml",
		"general-agentic.config.toml",
		"design-session.config.toml",
		"fast.config.toml",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# stub\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	got, err := DiscoverProfiles(dir)
	if err != nil {
		t.Fatalf("DiscoverProfiles returned err = %v, want nil", err)
	}
	want := []string{"design-session", "fast", "general-agentic", "review"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DiscoverProfiles = %v, want %v", got, want)
	}
}

// TestDiscoverProfiles_AlphabeticalOrderPreservedWhenFirstBySort is a
// smaller companion to AlphabeticalSort: when a name that happens
// to land at alphabetical index 0 ("general-agentic" sorted against
// a list where it is already index 0) is on disk, the result is just
// the alphabetical list. The test guards against accidental
// re-promotion of any specific name in a future refactor.
func TestDiscoverProfiles_AlphabeticalOrderPreservedWhenFirstBySort(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"general-agentic.config.toml", "review.config.toml"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# stub\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	got, err := DiscoverProfiles(dir)
	if err != nil {
		t.Fatalf("DiscoverProfiles returned err = %v, want nil", err)
	}
	want := []string{"general-agentic", "review"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DiscoverProfiles = %v, want %v", got, want)
	}
}

// TestDiscoverProfiles_AlphabeticalOrderWithoutGeneralAgentic guards
// against the previous promotion logic returning: a directory with
// no "general-agentic" file (the old default) must still get a
// sorted result, with whatever does exist at index 0 - never a
// synthesised name.
func TestDiscoverProfiles_AlphabeticalOrderWithoutGeneralAgentic(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"alpha.config.toml", "beta.config.toml"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# stub\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	got, err := DiscoverProfiles(dir)
	if err != nil {
		t.Fatalf("DiscoverProfiles returned err = %v, want nil", err)
	}
	want := []string{"alpha", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DiscoverProfiles = %v, want %v", got, want)
	}
}

func TestDiscoverProfiles_IgnoresNonMatchingFilesAndDirs(t *testing.T) {
	dir := t.TempDir()
	// Real config file.
	if err := os.WriteFile(filepath.Join(dir, "review.config.toml"), []byte("# stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-`.config.toml` file: should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file with a similar but wrong suffix: should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "almost.config.yml"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory with a `.config.toml` name: should be ignored
	// (DiscoverProfiles only looks at regular files).
	if err := os.Mkdir(filepath.Join(dir, "subdir.config.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A subdirectory: should be ignored.
	if err := os.Mkdir(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverProfiles(dir)
	if err != nil {
		t.Fatalf("DiscoverProfiles returned err = %v, want nil", err)
	}
	want := []string{"review"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DiscoverProfiles = %v, want %v", got, want)
	}
}

func TestDiscoverProfiles_NonExistentNotConfusedWithOtherErrors(t *testing.T) {
	// A path that points to a file (not a directory) should produce a
	// non-NotExist error, distinct from the missing-dir case. This
	// guards against an over-broad `os.IsNotExist` check swallowing
	// real failures.
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverProfiles(file); err == nil {
		t.Fatalf("DiscoverProfiles on a file path should error, got nil")
	}
}

// guard: confirm the tests above don't accidentally depend on a
// particular order beyond what DiscoverProfiles guarantees. (This is a
// belt-and-braces test; if it ever fails, someone changed the sort
// guarantee, which is a public API change for the UI's
// alphabetical-then-default-first behaviour.)
func TestDiscoverProfiles_SortIsStable(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"zeta.config.toml", "alpha.config.toml", "mike.config.toml"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# stub\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DiscoverProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("expected sorted result, got %v", got)
	}
}
