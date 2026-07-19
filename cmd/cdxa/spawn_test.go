package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dzungtr/codex-agents/internal/subthread"
)

type fakeSubthreadLauncher struct {
	task    string
	profile string
	id      string
	err     error
}

func (f *fakeSubthreadLauncher) HeadlessLaunch(task, profile string, _ subthread.WorkspaceMode) (string, error) {
	f.task = task
	f.profile = profile
	return f.id, f.err
}

type fakeSubthreadRegistrar struct {
	registered bool
	calls      int
}

func (f *fakeSubthreadRegistrar) ThreadRegistered(string) (bool, error) {
	f.calls++
	return f.registered, nil
}

// fakeSpawnerDeps returns a deps whose spawner factory builds a real
// subthread.Spawner wired to the supplied fakes, so runSpawn exercises the
// genuine Spawn loop (default profile, polling, timeout) rather than a
// mocked Spawn.
func fakeSpawnerDeps(t *testing.T, launcher *fakeSubthreadLauncher, registrar *fakeSubthreadRegistrar) deps {
	t.Helper()
	return deps{
		codexHome: "/codex",
		statePath: "/state.json",
		spawner: func(_, _, _ string) *subthread.Spawner {
			return &subthread.Spawner{
				Launch:           launcher,
				Registered:       registrar,
				PollInterval:     time.Millisecond,
				RegistrationWait: time.Second,
				Sleep:            func(time.Duration) {},
			}
		},
	}
}

func TestRunSpawn_PrintsThreadIDAsJSON(t *testing.T) {
	launcher := &fakeSubthreadLauncher{id: "spawned-1234"}
	registrar := &fakeSubthreadRegistrar{registered: true}
	d := fakeSpawnerDeps(t, launcher, registrar)

	out := captureStdout(t, func() {
		code, err := runSpawn([]string{"explore the module graph", "--profile", "general-agentic"}, d)
		if err != nil || code != 0 {
			t.Fatalf("expected exit 0, got code=%d err=%v", code, err)
		}
	})
	var got struct {
		ThreadID string `json:"thread_id"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if got.ThreadID != "spawned-1234" {
		t.Errorf("thread_id = %q, want spawned-1234", got.ThreadID)
	}
	if launcher.task != "explore the module graph" {
		t.Errorf("launch task = %q, want %q", launcher.task, "explore the module graph")
	}
}

func TestRunSpawn_DefaultProfileIsGeneralAgentic(t *testing.T) {
	launcher := &fakeSubthreadLauncher{id: "spawned-1"}
	registrar := &fakeSubthreadRegistrar{registered: true}
	d := fakeSpawnerDeps(t, launcher, registrar)

	captureStdout(t, func() {
		if code, err := runSpawn([]string{"do a thing"}, d); err != nil || code != 0 {
			t.Fatalf("expected exit 0, got code=%d err=%v", code, err)
		}
	})
	if launcher.profile != "general-agentic" {
		t.Errorf("profile = %q, want general-agentic (the default)", launcher.profile)
	}
}

func TestRunSpawn_LaunchFailure_JSONErrorExit1(t *testing.T) {
	launcher := &fakeSubthreadLauncher{err: errors.New("tmux boom")}
	registrar := &fakeSubthreadRegistrar{}
	d := fakeSpawnerDeps(t, launcher, registrar)

	out := captureStdout(t, func() {
		code, err := runSpawn([]string{"task"}, d)
		// runSpawn returns (exitOperErr, err); run maps it to exit 1 and
		// prints the JSON error object. Simulate run's mapping here.
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if !strings.Contains(got.Error, "tmux boom") {
		t.Errorf("error = %q, want it to contain %q", got.Error, "tmux boom")
	}
}

func TestRunSpawn_RegistrationTimeout_JSONErrorExit1(t *testing.T) {
	launcher := &fakeSubthreadLauncher{id: "thread-never"}
	registrar := &fakeSubthreadRegistrar{registered: false}
	d := deps{
		codexHome: "/codex",
		statePath: "/state.json",
		spawner: func(_, _, _ string) *subthread.Spawner {
			return &subthread.Spawner{
				Launch:           launcher,
				Registered:       registrar,
				PollInterval:     time.Millisecond,
				RegistrationWait: 5 * time.Millisecond,
				Sleep:            func(time.Duration) {},
			}
		},
	}

	out := captureStdout(t, func() {
		code, err := runSpawn([]string{"task"}, d)
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if !strings.Contains(got.Error, "register") && !strings.Contains(got.Error, "timeout") {
		t.Errorf("error = %q, want it to mention registration/timeout", got.Error)
	}
}

func TestRunSpawn_NoTask_JSONErrorExit1(t *testing.T) {
	d := deps{codexHome: "/codex", statePath: "/state.json"}
	out := captureStdout(t, func() {
		code, err := runSpawn([]string{}, d)
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if !strings.Contains(got.Error, "usage") && !strings.Contains(got.Error, "task") {
		t.Errorf("error = %q, want usage/task message", got.Error)
	}
}

// TestNewSpawner_ProductionWiring is a smoke test that the production
// newSpawner (used when no test override is set) wires a non-nil launcher
// and registrar without requiring them to actually run. It guards against
// silent regressions in the adapter wiring that the fake-based tests above
// can't see.
func TestNewSpawner_ProductionWiring(t *testing.T) {
	s := newSpawner("/tmp/nonexistent-codexhome", "/tmp/nonexistent-state.json", "/tmp")
	if s == nil {
		t.Fatal("expected non-nil Spawner")
	}
	if s.Launch == nil {
		t.Error("expected Launch to be wired")
	}
	if s.Registered == nil {
		t.Error("expected Registered to be wired")
	}
}

// fakeSubthreadLauncherWithMode records the workspace mode passed by
// runSpawn, so the inplace/worktree/invalid tests can assert the mode
// reaches the launcher without depending on a real codexlaunch.Launcher.
type fakeSubthreadLauncherWithMode struct {
	task    string
	profile string
	mode    subthread.WorkspaceMode
	id      string
	err     error
}

func (f *fakeSubthreadLauncherWithMode) HeadlessLaunch(task, profile string, mode subthread.WorkspaceMode) (string, error) {
	f.task = task
	f.profile = profile
	f.mode = mode
	return f.id, f.err
}

func fakeSpawnerDepsWithMode(t *testing.T, launcher *fakeSubthreadLauncherWithMode, registrar *fakeSubthreadRegistrar) deps {
	t.Helper()
	return deps{
		codexHome: "/codex",
		statePath: "/state.json",
		spawner: func(_, _, _ string) *subthread.Spawner {
			return &subthread.Spawner{
				Launch:           launcher,
				Registered:       registrar,
				PollInterval:     time.Millisecond,
				RegistrationWait: time.Second,
				Sleep:            func(time.Duration) {},
			}
		},
	}
}

func TestRunSpawn_WorkspaceInPlace_PassesInPlaceMode(t *testing.T) {
	launcher := &fakeSubthreadLauncherWithMode{id: "spawned-inplace"}
	registrar := &fakeSubthreadRegistrar{registered: true}
	d := fakeSpawnerDepsWithMode(t, launcher, registrar)

	out := captureStdout(t, func() {
		code, err := runSpawn([]string{"explore", "--workspace", "inplace"}, d)
		if err != nil || code != 0 {
			t.Fatalf("expected exit 0, got code=%d err=%v", code, err)
		}
	})
	var got struct {
		ThreadID string `json:"thread_id"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if got.ThreadID != "spawned-inplace" {
		t.Errorf("thread_id = %q, want spawned-inplace", got.ThreadID)
	}
	if launcher.mode != subthread.WorkspaceInPlace {
		t.Errorf("mode = %v, want WorkspaceInPlace", launcher.mode)
	}
}

func TestRunSpawn_WorkspaceWorktree_PassesWorktreeMode(t *testing.T) {
	launcher := &fakeSubthreadLauncherWithMode{id: "spawned-wt"}
	registrar := &fakeSubthreadRegistrar{registered: true}
	d := fakeSpawnerDepsWithMode(t, launcher, registrar)

	captureStdout(t, func() {
		if code, err := runSpawn([]string{"explore", "--workspace", "worktree"}, d); err != nil || code != 0 {
			t.Fatalf("expected exit 0, got code=%d err=%v", code, err)
		}
	})
	if launcher.mode != subthread.WorkspaceWorktree {
		t.Errorf("mode = %v, want WorkspaceWorktree", launcher.mode)
	}
}

func TestRunSpawn_WorkspaceOmitted_DefaultsToWorktree(t *testing.T) {
	launcher := &fakeSubthreadLauncherWithMode{id: "spawned-default"}
	registrar := &fakeSubthreadRegistrar{registered: true}
	d := fakeSpawnerDepsWithMode(t, launcher, registrar)

	captureStdout(t, func() {
		if code, err := runSpawn([]string{"explore"}, d); err != nil || code != 0 {
			t.Fatalf("expected exit 0, got code=%d err=%v", code, err)
		}
	})
	if launcher.mode != subthread.WorkspaceWorktree {
		t.Errorf("mode = %v, want WorkspaceWorktree (the default)", launcher.mode)
	}
}

func TestRunSpawn_InvalidWorkspace_JSONErrorExit1(t *testing.T) {
	launcher := &fakeSubthreadLauncherWithMode{id: "should-not-launch"}
	registrar := &fakeSubthreadRegistrar{registered: true}
	d := fakeSpawnerDepsWithMode(t, launcher, registrar)

	out := captureStdout(t, func() {
		code, err := runSpawn([]string{"explore", "--workspace", "bogus"}, d)
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if !strings.Contains(got.Error, "workspace") {
		t.Errorf("error = %q, want it to mention --workspace", got.Error)
	}
	if launcher.task != "" {
		t.Errorf("launcher should not have been called for an invalid --workspace value; got task=%q", launcher.task)
	}
}
