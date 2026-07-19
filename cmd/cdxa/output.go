package main

import (
	"flag"
	"fmt"

	"github.com/dzungtr/codex-agents/internal/subthread"
)

// exit codes, per ADR 0003 decision 2. These are API: parent-thread prompts
// hard-code them, so changing a value breaks every deployed delegation
// prompt.
const (
	exitDone    = 0 // a completed turn is available
	exitWorking = 2 // still working (last turn in progress)
	exitGone    = 3 // thread unknown or gone without collectable output
	exitOperErr = 1 // operational error (sqlite unreadable, rollout missing)
)

// runOutput implements `cdxa output <thread-id>`. It is intentionally thin:
// it parses the single thread-id positional arg, delegates to
// subthread.Output for all the real work, prints the result as a single JSON
// object on stdout, and returns the exit code the Status maps to. No
// business logic lives here — this file is flag parsing, JSON printing, and
// exit-code mapping only (ADR 0003 decision 1).
//
// --wait N (blocking poll sugar) is a separate slice (#32) and is NOT
// implemented here; the flag is reserved so a later slice can add it without
// restructuring this file's arg parsing.
func runOutput(args []string, d deps) (int, error) {
	fs := flag.NewFlagSet("output", flag.ContinueOnError)
	fs.SetOutput(nil) // silence flag.Usage; cdxa prints its own errors
	if err := fs.Parse(args); err != nil {
		return exitOperErr, fmt.Errorf("cdxa output: parse flags: %w", err)
	}

	if fs.NArg() != 1 {
		return exitOperErr, fmt.Errorf("cdxa output: usage: cdxa output <thread-id>")
	}
	threadID := fs.Arg(0)

	result, err := subthread.Output(d.state, d.live, d.codexHome, threadID)
	if err != nil {
		// subthread.Output returns ErrOperational for the exit-1 cases; the
		// error itself carries the detail. main prints the JSON error object
		// and maps to exit 1.
		return exitOperErr, err
	}

	// Map the Status to its exit code and print the JSON object. The JSON
	// shape is frozen: {"status","turn","message"} (ADR 0003 decision 2).
	code := exitCodeFor(result.Status)
	fmt.Fprintf(stdout, "{\"status\":%q,\"turn\":%d,\"message\":%q}\n",
		result.Status.String(), result.Turn, result.Message)
	return code, nil
}

// exitCodeFor maps a subthread.Status to its ADR 0003 exit code. Kept as a
// pure function (separate from runOutput) so the table test in output_test.go
// can exercise every mapping without constructing a fake subthread call.
func exitCodeFor(s subthread.Status) int {
	switch s {
	case subthread.StatusDone:
		return exitDone
	case subthread.StatusWorking:
		return exitWorking
	case subthread.StatusGone:
		return exitGone
	default:
		// An unmapped Status is a programming error (a new Status added
		// without updating this switch). Surface it as an operational error
		// rather than silently exiting 0.
		return exitOperErr
	}
}
