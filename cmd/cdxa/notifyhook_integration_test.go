package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dzungtr/codex-agents/internal/agentstate"
)

// TestNotifyHookSubcommand_EndToEnd builds the real cdxa binary and
// invokes it exactly as codex would per internal/notifyhook.WrapperArgs:
// `cdxa notify-hook <session> <eventsPath> <forwardJoined> <payload>`.
// The wrapper identity positional is the tmux session name (PRD #48:
// stable from launch time, since codex thread id is not known until codex
// registers). runNotifyHook resolves the session name back to codex thread
// id via agentstate before recording the event, so events.jsonl and
// state.json end up keyed by codex id. This exercises main()'s hidden
// subcommand dispatch + the runNotifyHookCmd adapter in notifyhook.go
// end-to-end (PRD #1's testing decisions call for an integration test
// covering launch -> status transitions; this is the notify-hook half of
// that loop, without needing a real codex/tmux).
//
// Moved from cmd/codex-agents/notifyhook_integration_test.go in slice
// #77: now that cdxa is the sole binary, this test belongs in the same
// package as the subcommand under test (cmd/cdxa), not in the now-deleted
// cmd/codex-agents transition shim.
func TestNotifyHookSubcommand_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go-build-based integration test in -short mode")
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "cdxa")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build cdxa: %v\n%s", err, out)
	}

	home := t.TempDir()
	eventsPath := filepath.Join(home, "events.jsonl")
	statePath := filepath.Join(home, ".codex-agents", "state.json")
	if err := agentstate.Upsert(statePath, "codex-thread-1", agentstate.Entry{TmuxSession: "cxa-thread-1", Profile: "general-agentic"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	// The hook is invoked with the tmux session name as the identity
	// positional (what Launcher.Launch configured via WrapperArgs).
	cmd := exec.Command(binPath, "notify-hook", "cxa-thread-1", eventsPath, "", `{"type":"agent-turn-complete"}`)
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("notify-hook subcommand failed: %v\nstderr: %s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output on a clean hook invocation, got %q", stderr.String())
	}

	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read events file: %v", err)
	}
	line := strings.TrimSpace(string(data))
	var ev struct {
		ThreadID string `json:"thread_id"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("parse recorded event %q: %v", line, err)
	}
	// The event is keyed by codex id (resolved from the session name),
	// not the session name positional.
	if ev.ThreadID != "codex-thread-1" || ev.Kind != "turn-ended" {
		t.Fatalf("recorded event = %+v, want codex-thread-1/turn-ended", ev)
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	entry := st.Threads["codex-thread-1"]
	if entry.LastTurnEvent == "" {
		t.Fatalf("expected LastTurnEvent populated for codex-thread-1, got %+v", entry)
	}
	if entry.TmuxSession != "cxa-thread-1" {
		t.Fatalf("expected TmuxSession preserved, got %+v", entry)
	}
}
