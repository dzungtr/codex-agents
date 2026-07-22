// Command codex-agents is the cockpit's entry point (slice #3, superseded
// for installation/deployment by ADR 0005's merged cdxa binary, but kept
// buildable as a transition shim during slice #75). All real wiring lives
// in internal/cockpit; this file is a thin pre-TUI dispatch:
//
//	<codex-agents notify-hook ...>  → runNotifyHook
//	<codex-agents>                   → cockpit.Run(codexHome, statePath)
//
// Once the merged cdxa binary lands, cmd/codex-agents is deleted.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/cockpit"
	"github.com/dzungtr/codex-agents/internal/notifyhook"
)

func main() {
	// Launched threads invoke this binary as their notify hook via `-c
	// notify=[...]` (internal/notifyhook.WrapperArgs); dispatch to that
	// mode before anything else tries to start the bubbletea program.
	if len(os.Args) > 1 && os.Args[1] == notifyhook.Subcommand {
		runNotifyHook(os.Args[2:])
		return
	}

	codexHome, err := cockpit.ResolveCodexHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents:", err)
		os.Exit(1)
	}

	statePath, err := agentstate.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents:", err)
		os.Exit(1)
	}

	if err := cockpit.Run(codexHome, statePath); err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents:", err)
		os.Exit(1)
	}
}

// runNotifyHook implements the hidden `codex-agents notify-hook ...`
// subcommand codex invokes when a launched thread's turn ends. Per the PRD
// #1 / issue #4 contract, this must never block or fail codex's own
// turn-completion flow: failures go to stderr and the process still exits
// 0, so a broken hook degrades the cockpit's status derivation (that
// thread simply reads as StatusWorking whenever it's alive) instead of
// disrupting the user's codex session.
func runNotifyHook(args []string) {
	threadID, eventsPath, forward, payload, err := notifyhook.ParseWrapperArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents notify-hook:", err)
		return
	}
	statePath, err := agentstate.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents notify-hook: resolve state path:", err)
		statePath = ""
	}
	// PRD #48: the wrapper identity positional is the tmux session name
	// (a stable handle from launch time), not codex thread id - codex id
	// is not known when the tmux launch command is built. Resolve it back
	// to codex thread id via agentstate (the entry keyed by codex id,
	// written by Launch, carries TmuxSession) so events.jsonl and
	// agentstate.LastTurnEvent end up keyed by codex id. On resolution
	// failure (e.g. a hook firing for a thread state.json does not know
	// about yet) degrade to the handle as-is rather than failing codex
	// turn-completion flow.
	resolved := threadID
	if statePath != "" {
		if id, ok, rErr := agentstate.FindThreadIDBySession(statePath, threadID); rErr != nil {
			fmt.Fprintln(os.Stderr, "codex-agents notify-hook: resolve session:", rErr)
		} else if ok {
			resolved = id
		}
	}
	notifyhook.Run(os.Stderr, notifyhook.ExecForwardRunner{}, statePath, resolved, eventsPath, forward, payload, time.Now().UTC())
}
