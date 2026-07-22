// Notify-hook dispatch for the merged cdxa binary (slice #76, completed
// in #77 by deleting the cmd/codex-agents transition shim). The hidden
// subcommand keeps its stderr-only / exit-0 / never-blocks-codex contract
// - only the dispatch mechanism changes, from a hardcoded `if` in the old
// main to an entry in the cmds map in this package.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/notifyhook"
)

// runNotifyHook implements the hidden `cdxa notify-hook ...` subcommand
// codex invokes when a launched thread's turn ends. Per the PRD #1 /
// issue #4 contract, this must never block or fail codex's own
// turn-completion flow: failures go to stderr and the process still exits
// 0, so a broken hook degrades the cockpit's status derivation (that
// thread simply reads as StatusWorking whenever it's alive) instead of
// disrupting the user's codex session.
func runNotifyHook(args []string) {
	threadID, eventsPath, forward, payload, err := notifyhook.ParseWrapperArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cdxa notify-hook:", err)
		return
	}
	statePath, err := agentstate.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cdxa notify-hook: resolve state path:", err)
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
			fmt.Fprintln(os.Stderr, "cdxa notify-hook: resolve session:", rErr)
		} else if ok {
			resolved = id
		}
	}
	notifyhook.Run(os.Stderr, notifyhook.ExecForwardRunner{}, statePath, resolved, eventsPath, forward, payload, time.Now().UTC())
}

// runNotifyHookCmd adapts runNotifyHook's func([]string) signature to the
// command type used by the cmds dispatch table. It always exits 0 (the
// notify-hook contract forbids propagating failures to codex) and never
// returns an error for the same reason.
func runNotifyHookCmd(args []string, d deps) (int, error) {
	runNotifyHook(args)
	return 0, nil
}
