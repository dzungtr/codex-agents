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

// TestNotifyHookSubcommand_EndToEnd builds the real codex-agents binary and
// invokes it exactly as codex would per internal/notifyhook.WrapperArgs:
// `codex-agents notify-hook <threadID> <eventsPath> <forwardJoined>
// <payload>`. This exercises main()'s hidden-subcommand dispatch
// end-to-end (PRD #1's testing decisions call for an integration test
// covering launch -> status transitions; this is the notify-hook half of
// that loop, without needing a real codex/tmux).
func TestNotifyHookSubcommand_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go-build-based integration test in -short mode")
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "codex-agents")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build codex-agents: %v\n%s", err, out)
	}

	home := t.TempDir()
	eventsPath := filepath.Join(home, "events.jsonl")
	statePath := filepath.Join(home, ".codex-agents", "state.json")
	if err := agentstate.Upsert(statePath, "thread-1", agentstate.Entry{TmuxSession: "cxa-thread-1", Profile: "general-agentic"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	cmd := exec.Command(binPath, "notify-hook", "thread-1", eventsPath, "", `{"type":"agent-turn-complete"}`)
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
	if ev.ThreadID != "thread-1" || ev.Kind != "turn-ended" {
		t.Fatalf("recorded event = %+v, want thread-1/turn-ended", ev)
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	entry := st.Threads["thread-1"]
	if entry.LastTurnEvent == "" {
		t.Fatalf("expected LastTurnEvent to be populated, got %+v", entry)
	}
	if entry.TmuxSession != "cxa-thread-1" {
		t.Fatalf("expected TmuxSession preserved, got %+v", entry)
	}
}
