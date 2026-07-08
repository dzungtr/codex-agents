package codexlaunch

import (
	"reflect"
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

func TestResumeArgs(t *testing.T) {
	got := ResumeArgs("thread-abc123")
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
