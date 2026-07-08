package codexlaunch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeConfig(t *testing.T, dir, profile, content string) {
	t.Helper()
	path := filepath.Join(dir, profile+".config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestExistingNotifyCommand_FindsSingleLineArray(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "general-agentic", "model = \"gpt-5-codex\"\nnotify = [\"/usr/bin/terminal-notifier\", \"-title\", \"codex\"]\n")

	got := ExistingNotifyCommand(dir, "general-agentic")
	want := []string{"/usr/bin/terminal-notifier", "-title", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExistingNotifyCommand() = %v, want %v", got, want)
	}
}

func TestExistingNotifyCommand_NoNotifyKeyReturnsNil(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "general-agentic", "model = \"gpt-5-codex\"\n")

	if got := ExistingNotifyCommand(dir, "general-agentic"); got != nil {
		t.Fatalf("expected nil with no notify key, got %v", got)
	}
}

func TestExistingNotifyCommand_MissingConfigFileReturnsNil(t *testing.T) {
	dir := t.TempDir()
	if got := ExistingNotifyCommand(dir, "no-such-profile"); got != nil {
		t.Fatalf("expected nil for a missing config file, got %v", got)
	}
}

func TestExistingNotifyCommand_EmptyCodexHomeOrProfileReturnsNil(t *testing.T) {
	if got := ExistingNotifyCommand("", "general-agentic"); got != nil {
		t.Fatalf("expected nil with empty codexHome, got %v", got)
	}
	if got := ExistingNotifyCommand(t.TempDir(), ""); got != nil {
		t.Fatalf("expected nil with empty profile, got %v", got)
	}
}

func TestExistingNotifyCommand_MalformedArrayReturnsNil(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "general-agentic", "notify = [this is not json]\n")

	if got := ExistingNotifyCommand(dir, "general-agentic"); got != nil {
		t.Fatalf("expected nil for a malformed notify array, got %v", got)
	}
}
