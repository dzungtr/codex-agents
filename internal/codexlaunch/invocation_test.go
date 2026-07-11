package codexlaunch

import (
	"reflect"
	"strings"
	"testing"
)

func TestNewThreadArgs_ProfileOnly(t *testing.T) {
	got := NewThreadArgs(NewThreadSpec{Profile: "general-agentic", Task: "fix the auth hook"})
	want := []string{"codex", "-p", "general-agentic", "fix the auth hook"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewThreadArgs() = %v, want %v", got, want)
	}
}

func TestNewThreadArgs_ExplicitModelLayersOnTop(t *testing.T) {
	got := NewThreadArgs(NewThreadSpec{Profile: "general-agentic", Model: "o3", Task: "fix the auth hook"})
	want := []string{"codex", "-p", "general-agentic", "-c", "model=o3", "fix the auth hook"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewThreadArgs() = %v, want %v", got, want)
	}
}

func TestNewThreadArgs_EmptyProfileDefaultsToGeneralAgentic(t *testing.T) {
	got := NewThreadArgs(NewThreadSpec{Task: "fix the auth hook"})
	want := []string{"codex", "-p", DefaultProfile, "fix the auth hook"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewThreadArgs() = %v, want %v", got, want)
	}
}

func TestNewThreadArgs_NotifyAppendsConfigFlagBeforeTask(t *testing.T) {
	got := NewThreadArgs(NewThreadSpec{
		Profile: "general-agentic",
		Task:    "fix the auth hook",
		Notify:  []string{"/bin/codex-agents", "notify-hook", "t1", "/home/x/.codex-agents/events.jsonl", ""},
	})
	want := []string{
		"codex", "-p", "general-agentic",
		"-c", `notify=["/bin/codex-agents","notify-hook","t1","/home/x/.codex-agents/events.jsonl",""]`,
		"fix the auth hook",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewThreadArgs() = %v, want %v", got, want)
	}
}

func TestNewThreadArgs_ModelAndNotifyBothLayerOnTop(t *testing.T) {
	got := NewThreadArgs(NewThreadSpec{
		Profile: "general-agentic",
		Model:   "o3",
		Task:    "fix the auth hook",
		Notify:  []string{"/bin/codex-agents", "notify-hook", "t1", "/events.jsonl", ""},
	})
	want := []string{
		"codex", "-p", "general-agentic",
		"-c", "model=o3",
		"-c", `notify=["/bin/codex-agents","notify-hook","t1","/events.jsonl",""]`,
		"fix the auth hook",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewThreadArgs() = %v, want %v", got, want)
	}
}

func TestNewThreadArgs_EmptyNotifyOmitsFlag(t *testing.T) {
	got := NewThreadArgs(NewThreadSpec{Profile: "general-agentic", Task: "fix the auth hook"})
	for i, a := range got {
		if a == "-c" && i+1 < len(got) && strings.HasPrefix(got[i+1], "notify=") {
			t.Fatalf("expected no notify flag when Notify is unset, got %v", got)
		}
	}
}

func TestResumeArgs(t *testing.T) {
	got := ResumeArgs("thread-abc123", "general-agentic")
	want := []string{"codex", "-p", "general-agentic", "resume", "thread-abc123"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResumeArgs() = %v, want %v", got, want)
	}
}

func TestResumeArgs_EmptyProfileOmitsFlag(t *testing.T) {
	got := ResumeArgs("thread-abc123", "")
	want := []string{"codex", "resume", "thread-abc123"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResumeArgs() = %v, want %v", got, want)
	}
}

func TestKnownProfiles_IncludesDefault(t *testing.T) {
	found := false
	for _, p := range KnownProfiles {
		if p == DefaultProfile {
			found = true
		}
	}
	if !found {
		t.Errorf("expected KnownProfiles %v to include DefaultProfile %q", KnownProfiles, DefaultProfile)
	}
}
