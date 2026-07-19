package subthread

import (
	"errors"
	"testing"
	"time"
)

// fakeLauncher scripts HeadlessLaunch calls: returns the next queued
// result, or fails the test if called more times than scripted.
type fakeLauncher struct {
	results []fakeLaunchResult
	calls   []fakeLaunchCall
}
type fakeLaunchResult struct {
	id  string
	err error
}
type fakeLaunchCall struct {
	Task    string
	Profile string
}

func (f *fakeLauncher) HeadlessLaunch(task, profile string) (string, error) {
	f.calls = append(f.calls, fakeLaunchCall{Task: task, Profile: profile})
	if len(f.calls) > len(f.results) {
		return "", errors.New("fakeLauncher: scripted results exhausted")
	}
	r := f.results[len(f.calls)-1]
	return r.id, r.err
}

// fakeRegistrar scripts ThreadRegistered responses across successive calls,
// modelling codex taking a few polls before the freshly launched thread
// appears in its sqlite.
type fakeRegistrar struct {
	responses []bool
	calls     int
}

func (f *fakeRegistrar) ThreadRegistered(threadID string) (bool, error) {
	f.calls++
	// Beyond the scripted responses, default to "not yet registered" —
	// this is what a real codexstate.ThreadRegistered call returns for a
	// thread codex hasn't written yet, so the timeout path exercises the
	// deadline rather than a synthetic registrar error.
	if f.calls > len(f.responses) {
		return false, nil
	}
	return f.responses[f.calls-1], nil
}

func newSpawnerForTest(l Launcher, r Registrar) *Spawner {
	return &Spawner{
		Launch:           l,
		Registered:       r,
		PollInterval:     5 * time.Millisecond,
		RegistrationWait: 200 * time.Millisecond,
		Sleep:            func(time.Duration) {},
	}
}

func TestSpawn_RegisteredOnFirstPoll_ReturnsThreadID(t *testing.T) {
	launcher := &fakeLauncher{results: []fakeLaunchResult{{id: "thread-abc"}}}
	registrar := &fakeRegistrar{responses: []bool{true}}
	s := newSpawnerForTest(launcher, registrar)

	id, err := s.Spawn("explore the graph", "general-agentic")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if id != "thread-abc" {
		t.Errorf("id = %q, want thread-abc", id)
	}
	if len(launcher.calls) != 1 {
		t.Errorf("expected exactly one launch call, got %d", len(launcher.calls))
	}
	if launcher.calls[0].Task != "explore the graph" {
		t.Errorf("launch task = %q, want %q", launcher.calls[0].Task, "explore the graph")
	}
	if launcher.calls[0].Profile != "general-agentic" {
		t.Errorf("launch profile = %q, want general-agentic", launcher.calls[0].Profile)
	}
}

func TestSpawn_RegisteredAfterSeveralPolls_ReturnsThreadID(t *testing.T) {
	launcher := &fakeLauncher{results: []fakeLaunchResult{{id: "thread-xyz"}}}
	registrar := &fakeRegistrar{responses: []bool{false, false, true}}
	s := newSpawnerForTest(launcher, registrar)

	id, err := s.Spawn("do the thing", "general-agentic")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if id != "thread-xyz" {
		t.Errorf("id = %q, want thread-xyz", id)
	}
	if registrar.calls != 3 {
		t.Errorf("expected 3 registration checks, got %d", registrar.calls)
	}
}

func TestSpawn_LaunchFails_ReturnsWrappedError(t *testing.T) {
	launchErr := errors.New("tmux boom")
	launcher := &fakeLauncher{results: []fakeLaunchResult{{err: launchErr}}}
	registrar := &fakeRegistrar{}
	s := newSpawnerForTest(launcher, registrar)

	_, err := s.Spawn("task", "general-agentic")
	if err == nil {
		t.Fatalf("expected launch failure to surface, got nil")
	}
	if !errors.Is(err, launchErr) {
		t.Errorf("expected error to wrap %v, got %v", launchErr, err)
	}
	if registrar.calls != 0 {
		t.Errorf("expected zero registration checks on launch failure, got %d", registrar.calls)
	}
}

func TestSpawn_RegistrationTimeout_ReturnsTimeoutError(t *testing.T) {
	launcher := &fakeLauncher{results: []fakeLaunchResult{{id: "thread-never"}}}
	// Never registers: enough false responses to outlast the wait budget.
	registrar := &fakeRegistrar{responses: []bool{false, false, false, false, false, false, false, false, false, false}}
	s := newSpawnerForTest(launcher, registrar)
	s.RegistrationWait = 10 * time.Millisecond
	s.PollInterval = 2 * time.Millisecond

	_, err := s.Spawn("task", "general-agentic")
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !errors.Is(err, ErrRegistrationTimeout) {
		t.Errorf("expected ErrRegistrationTimeout, got %v", err)
	}
}

func TestSpawn_EmptyProfileDefaultsToGeneralAgentic(t *testing.T) {
	launcher := &fakeLauncher{results: []fakeLaunchResult{{id: "thread-1"}}}
	registrar := &fakeRegistrar{responses: []bool{true}}
	s := newSpawnerForTest(launcher, registrar)

	if _, err := s.Spawn("task", ""); err != nil {
		t.Fatalf("Spawn with empty profile: %v", err)
	}
	if launcher.calls[0].Profile != "general-agentic" {
		t.Errorf("empty profile should default to general-agentic, got %q", launcher.calls[0].Profile)
	}
}

func TestSpawn_RegistrarError_Propagates(t *testing.T) {
	launcher := &fakeLauncher{results: []fakeLaunchResult{{id: "thread-1"}}}
	registrarErr := errors.New("sqlite corrupted")
	registrar := &errorRegistrar{err: registrarErr}
	s := newSpawnerForTest(launcher, registrar)

	_, err := s.Spawn("task", "general-agentic")
	if err == nil {
		t.Fatalf("expected registrar error to surface, got nil")
	}
	if !errors.Is(err, registrarErr) {
		t.Errorf("expected error to wrap %v, got %v", registrarErr, err)
	}
}

type errorRegistrar struct{ err error }

func (e *errorRegistrar) ThreadRegistered(string) (bool, error) { return false, e.err }
