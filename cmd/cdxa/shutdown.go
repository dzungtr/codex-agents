package main

import (
	"errors"
	"fmt"

	"github.com/dzungtr/codex-agents/internal/subthread"
)

// runShutdown implements `cdxa shutdown <thread-id>` (ADR 0007, issue #103).
// It validates argv, delegates to subthread.Shutdown for the completion
// gate, tmux teardown, and codex archive, prints the JSON result on
// stdout, and returns the exit code the ShutdownStatus maps to. No
// business logic lives here — this file is argv parsing, JSON printing,
// and exit-code mapping only (the same separation ADR 0003 decision 1
// establishes for every other cdxa subcommand).
//
// JSON shape is frozen by ADR 0007 decision 1: success prints
// {"status","thread_id"}; operational errors fall through run's standard
// {"error":...} envelope; the working and gone paths print the same
// status/thread_id object so a parent's jq -r .status discriminator works
// across all three non-error outcomes.
func runShutdown(args []string, d deps) (int, error) {
	if len(args) != 1 {
		return exitOperErr, fmt.Errorf("cdxa shutdown: usage: cdxa shutdown <thread-id>")
	}
	threadID := args[0]
	if threadID == "" {
		return exitOperErr, fmt.Errorf("cdxa shutdown: usage: cdxa shutdown <thread-id>")
	}

	sessions := newShutdownSessionStopperFor(d)
	archiver := newShutdownCodexArchiverFor(d)

	result, err := subthread.Shutdown(d.state, d.live, sessions, archiver, d.codexHome, d.statePath, threadID)
	if err != nil {
		if errors.Is(err, subthread.ErrOperational) {
			return exitOperErr, err
		}
		return exitOperErr, err
	}

	// Map ShutdownStatus to the shared cdxa exit-code contract (ADR 0007
	// decision 1: 0/2/3, ADR 0003 decision 2). Operational failures already
	// returned exit 1 above; only the clean refusal and success paths reach
	// here.
	code := exitCodeForShutdown(result.Status)
	fmt.Fprintf(stdout, "{\"status\":%q,\"thread_id\":%q}\n",
		result.Status.String(), result.ThreadID)
	return code, nil
}

// exitCodeForShutdown maps a subthread.ShutdownStatus to its ADR 0007 exit
// code. Kept as a pure function (separate from runShutdown) so the table
// test in shutdown_test.go can exercise every mapping without constructing
// a fake Shutdown call.
func exitCodeForShutdown(s subthread.ShutdownStatus) int {
	switch s {
	case subthread.ShutdownArchived:
		return exitDone
	case subthread.ShutdownWorking:
		return exitWorking
	case subthread.ShutdownGone:
		return exitGone
	default:
		// An unmapped ShutdownStatus is a programming error (a new value
		// added without updating this switch). Surface it as an operational
		// error rather than silently exiting 0.
		return exitOperErr
	}
}

// newShutdownSessionStopperFor returns the deps-injected SessionStopper
// when set (tests populate d.shutdownSessions), otherwise the production
// tmuxstatus-backed default. The indirection mirrors the spawner / replier
// factory pattern so cdxa's tests can swap in a fake without touching the
// subthread package's default wiring.
func newShutdownSessionStopperFor(d deps) subthread.SessionStopper {
	if d.shutdownSessions != nil {
		return d.shutdownSessions()
	}
	return subthread.DefaultSessionStopper()
}

// newShutdownCodexArchiverFor returns the deps-injected CodexArchiver when
// set (tests populate d.shutdownArchiver), otherwise the production
// `codex archive` shell-out. Same DI pattern as shutdownSessions.
func newShutdownCodexArchiverFor(d deps) subthread.CodexArchiver {
	if d.shutdownArchiver != nil {
		return d.shutdownArchiver()
	}
	return subthread.DefaultCodexArchiver()
}

// shutdownSessionsFn is the signature of the factory that builds a
// production subthread.SessionStopper for runShutdown. It is a field on
// deps so tests inject a fake-stopper by constructing deps directly
// (the same DI pattern runSend / runSpawn use for their factories), rather
// than via a package-global override.
type shutdownSessionsFn func() subthread.SessionStopper

// shutdownArchiverFn is the signature of the factory that builds a
// production subthread.CodexArchiver for runShutdown. Same DI pattern as
// shutdownSessionsFn.
type shutdownArchiverFn func() subthread.CodexArchiver
