package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/dzungtr/codex-agents/internal/codexlaunch"
	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/subthread"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// spawnerFn is the signature of the factory that builds a production
// subthread.Spawner for runSpawn. It is a field on deps so tests inject a
// fake-wired Spawner by constructing deps directly (the same DI pattern
// runOutput uses for state/live), rather than via a package-global override.
type spawnerFn func(codexHome, statePath, startDir string) *subthread.Spawner

// runSpawn implements `cdxa spawn "task" [--profile X]` (ADR 0003 decision
// 2, issue #29): it launches a new codex thread headlessly into a worktree
// + detached tmux session, polls codex's sqlite until that thread
// registers, and prints the JSON result ({"thread_id": "..."}) to stdout.
// It returns an exit code (0 on success, exitOperErr on any failure) and an
// error; run maps a non-nil error to exit 1 with a JSON error object.
//
// Failure modes (ADR 0003 decision 2): tmux failure and registration
// timeout both yield a JSON error object and exit 1.
func runSpawn(args []string, d deps) (int, error) {
	// The ADR 0003 syntax is `cdxa spawn "task" [--profile X]` — the task
	// may come before the flag. Go's flag package stops parsing at the
	// first non-flag argument, so we scan manually: collect --profile /
	// --profile=X from anywhere, and treat the single remaining positional
	// as the task. This keeps both `spawn "t" --profile X` and
	// `spawn --profile X "t"` working.
	var profile string
	var workspaceFlag string
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--profile" || a == "-profile":
			if i+1 >= len(args) {
				return exitOperErr, fmt.Errorf("cdxa spawn: --profile requires a value")
			}
			profile = args[i+1]
			i++
		case strings.HasPrefix(a, "--profile=") || strings.HasPrefix(a, "-profile="):
			profile = strings.SplitN(a, "=", 2)[1]
		case a == "--workspace" || a == "-workspace":
			if i+1 >= len(args) {
				return exitOperErr, fmt.Errorf("cdxa spawn: --workspace requires a value")
			}
			workspaceFlag = args[i+1]
			i++
		case strings.HasPrefix(a, "--workspace=") || strings.HasPrefix(a, "-workspace="):
			workspaceFlag = strings.SplitN(a, "=", 2)[1]
		case strings.HasPrefix(a, "-"):
			return exitOperErr, fmt.Errorf("cdxa spawn: unknown flag %q", a)
		default:
			positionals = append(positionals, a)
		}
	}

	if len(positionals) != 1 {
		return exitOperErr, fmt.Errorf("cdxa spawn: usage: cdxa spawn \"task\" [--profile X]")
	}
	task := positionals[0]
	if task == "" {
		return exitOperErr, fmt.Errorf("cdxa spawn: task must not be empty")
	}

	startDir, err := os.Getwd()
	if err != nil {
		return exitOperErr, fmt.Errorf("cdxa spawn: resolve working directory: %w", err)
	}

	workspaceMode, ok := subthread.ParseWorkspaceMode(workspaceFlag)
	if !ok {
		return exitOperErr, fmt.Errorf("cdxa spawn: invalid --workspace %q (want worktree or inplace)", workspaceFlag)
	}

	spawner := newSpawnerFor(d, d.codexHome, d.statePath, startDir)
	threadID, err := spawner.Spawn(task, profile, workspaceMode)
	if err != nil {
		return exitOperErr, fmt.Errorf("cdxa spawn: %w", err)
	}
	fmt.Fprintf(stdout, "{\"thread_id\":%q}\n", threadID)
	return exitDone, nil
}

// newSpawner wires the production subthread.Spawner: the cockpit's own
// codexlaunch.Launcher (worktree-per-thread, detached tmux, notify-hook
// chaining) adapted to subthread's Launcher interface, and a registrar over
// codexstate.ThreadRegistered (the newest state_*.sqlite threads table,
// ADR 0001 decision 2). Both reuse the cockpit's existing machinery
// unchanged — spawn is a headless surface over the same launch path.
func newSpawner(codexHome, statePath, startDir string) *subthread.Spawner {
	launcher := &codexlaunch.Launcher{
		Git:       codexlaunch.ExecGitRunner{},
		Tmux:      tmuxstatus.ExecRunner{},
		StatePath: statePath,
		CodexHome: codexHome,
	}
	return &subthread.Spawner{
		Launch:     launcherAdapter{l: launcher, startDir: startDir},
		Registered: registrarAdapter{codexHome: codexHome},
	}
}

// newSpawnerFor returns the deps-injected spawner factory when set (tests
// populate d.spawner) and the production newSpawner otherwise. The
// indirection is a field on deps rather than a package global so it follows
// the same DI pattern runOutput uses for state/live.
func newSpawnerFor(d deps, codexHome, statePath, startDir string) *subthread.Spawner {
	if d.spawner != nil {
		return d.spawner(codexHome, statePath, startDir)
	}
	return newSpawner(codexHome, statePath, startDir)
}

// launcherAdapter exposes the subset of *codexlaunch.Launcher that
// subthread.Spawner needs (HeadlessLaunch with a task + profile) as the
// subthread.Launcher interface. The adapter exists so internal/subthread
// depends on its own narrow interface rather than importing
// internal/codexlaunch directly — keeping the two packages decoupled and
// the polling loop testable with a fake launcher.
type launcherAdapter struct {
	l        *codexlaunch.Launcher
	startDir string
}

func (a launcherAdapter) HeadlessLaunch(task, profile string, mode subthread.WorkspaceMode) (string, error) {
	res, err := a.l.HeadlessLaunch(codexlaunch.LaunchRequest{
		StartDir:      a.startDir,
		Task:          task,
		Profile:       profile,
		WorkspaceMode: toCodexlaunchWorkspaceMode(mode),
	})
	if err != nil {
		return "", err
	}
	return res.ThreadID, nil
}

// toCodexlaunchWorkspaceMode translates the subthread-layer WorkspaceMode
// (kept in internal/subthread so that package doesn't import
// internal/codexlaunch) to the codexlaunch-layer enum that LaunchRequest
// carries. The two enums are isomorphic; this is the single translation
// point.
func toCodexlaunchWorkspaceMode(m subthread.WorkspaceMode) codexlaunch.WorkspaceMode {
	switch m {
	case subthread.WorkspaceInPlace:
		return codexlaunch.WorkspaceInPlace
	default:
		return codexlaunch.WorkspaceWorktree
	}
}

// registrarAdapter adapts codexstate.ThreadRegistered to the
// subthread.Registrar interface. It captures codexHome so each poll queries
// the newest state_*.sqlite fresh — registration is detected the moment
// codex writes the thread's row, not from a cached read.
type registrarAdapter struct {
	codexHome string
}

func (r registrarAdapter) ThreadRegistered(threadID string) (bool, error) {
	return codexstate.ThreadRegistered(r.codexHome, threadID)
}
