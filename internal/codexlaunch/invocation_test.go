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

// TestNewThreadArgs_EmptyProfileOmitsPFlag is the contract for the
// composer's "no profile files on disk" launch path: an empty Profile
// means "let codex use its own default", which is signalled to codex
// by omitting the -p flag entirely (rather than falling back to a
// hard-coded name like general-agentic — that decision now belongs to
// codex, not to the cockpit).
func TestNewThreadArgs_EmptyProfileOmitsPFlag(t *testing.T) {
	got := NewThreadArgs(NewThreadSpec{Task: "fix the auth hook"})
	want := []string{"codex", "fix the auth hook"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewThreadArgs() = %v, want %v", got, want)
	}
	// Defence in depth: make sure no -p sneaks in anywhere.
	for i, a := range got {
		if a == "-p" && i+1 < len(got) {
			t.Errorf("expected no -p flag for empty Profile, got -p %q in %v", got[i+1], got)
		}
	}
}

func TestNewThreadArgs_NotifyAppendsConfigFlagBeforeTask(t *testing.T) {
	got := NewThreadArgs(NewThreadSpec{
		Profile: "general-agentic",
		Task:    "fix the auth hook",
		Notify:  []string{"/bin/cdxa", "notify-hook", "t1", "/home/x/.codex-agents/events.jsonl", ""},
	})
	want := []string{
		"codex", "-p", "general-agentic",
		"-c", `notify=["/bin/cdxa","notify-hook","t1","/home/x/.codex-agents/events.jsonl",""]`,
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
		Notify:  []string{"/bin/cdxa", "notify-hook", "t1", "/events.jsonl", ""},
	})
	want := []string{
		"codex", "-p", "general-agentic",
		"-c", "model=o3",
		"-c", `notify=["/bin/cdxa","notify-hook","t1","/events.jsonl",""]`,
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
