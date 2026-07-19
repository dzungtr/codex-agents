package main

import (
	"errors"
	"fmt"

	"github.com/dzungtr/codex-agents/internal/codexlaunch"
	"github.com/dzungtr/codex-agents/internal/subthread"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// replierFn is the signature of the factory that builds a production
// subthread.Replier for runSend. It is a field on deps so tests inject a
// fake-wired Replier by constructing deps directly (the same DI pattern
// runSpawn uses for the spawner factory, and runOutput uses for state/live),
// rather than via a package-global override.
type replierFn func(codexHome, statePath string) subthread.Replier

// runSend implements `cdxa send <thread-id> "msg"` (ADR 0003 decision 2,
// issue #31): it delivers a one-line follow-up into a living subthread's
// codex composer via the cockpit's QuickReply mechanism, derives the turn
// number the follow-up started from the rollout file at send time, and
// prints {"turn": N} to stdout. It returns an exit code (0 on success, 3
// when the thread is unknown or its session is dead, 1 on operational
// failure) and an error; run maps a non-nil error to exit 1 with a JSON
// error object.
//
// The delivery mechanism is owned by internal/codexlaunch.QuickReply; this
// slice wraps it (via subthread.Send), it does not modify it. What Send
// adds over QuickReply alone is the gone-check QuickReply deliberately
// omits and the started-turn derivation (ADR 0003 decision 5).
func runSend(args []string, d deps) (int, error) {
	if len(args) != 2 {
		return exitOperErr, fmt.Errorf("cdxa send: usage: cdxa send <thread-id> \"msg\"")
	}
	threadID := args[0]
	msg := args[1]
	if msg == "" {
		return exitOperErr, fmt.Errorf("cdxa send: message must not be empty")
	}

	replier := newReplierFor(d, d.codexHome, d.statePath)
	turn, err := subthread.Send(d.state, d.live, replier, d.codexHome, threadID, msg)
	if err != nil {
		if errors.Is(err, subthread.ErrGone) {
			// Unknown thread / dead session → exit 3 (ADR 0003 decision 2).
			// No JSON object is printed for the gone case (mirroring output's
			// gone path, which prints the status object rather than an error
			// object): the parent discriminates gone from operational error
			// by the exit code alone.
			return exitGone, nil
		}
		return exitOperErr, fmt.Errorf("cdxa send: %w", err)
	}
	fmt.Fprintf(stdout, "{\"turn\":%d}\n", turn)
	return exitDone, nil
}

// newReplier wires the production subthread.Replier: the cockpit's own
// codexlaunch.Launcher, whose QuickReply method (tmux send-keys: literal
// text, then a separate Enter keypress, then clearing the recorded
// last-turn-event) satisfies subthread.Replier. The launcher is constructed
// fresh on every send call — send keeps no job state (ADR 0003 decision 4).
func newReplier(codexHome, statePath string) subthread.Replier {
	return &codexlaunch.Launcher{
		Tmux:      tmuxstatus.ExecRunner{},
		StatePath: statePath,
		CodexHome: codexHome,
	}
}

// newReplierFor returns the deps-injected replier factory when set (tests
// populate d.replier) and the production newReplier otherwise. The
// indirection is a field on deps rather than a package global so it follows
// the same DI pattern runSpawn/runOutput use.
func newReplierFor(d deps, codexHome, statePath string) subthread.Replier {
	if d.replier != nil {
		return d.replier(codexHome, statePath)
	}
	return newReplier(codexHome, statePath)
}
