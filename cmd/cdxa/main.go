// Command cdxa is the headless, JSON-only surface for codex threads that
// delegate work to other codex threads (ADR 0003). It is a second binary
// built from the same module as the cockpit (cmd/codex-agents), sharing
// internal/. The cockpit binary bootstraps bubbletea on startup; a parent
// codex thread invoking it for a headless call would couple delegation to a
// TUI lifecycle it never sees, so cdxa is a separate, minimal entry point.
//
// This file wires the subcommand dispatch and $CODEX_HOME resolution; each
// subcommand (output, spawn, send — the latter arrives in sibling slice
// #31) lives in its own file. The dispatch table is a map so sibling
// slices add their subcommand by appending one entry, without touching this
// file's structure.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/subthread"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// command is a cdxa subcommand. Each subcommand parses its own args, does its
// work, and writes its JSON to stdout; main maps the returned error to the
// exit-code contract. Subcommands never call os.Exit themselves — that keeps
// the exit-code mapping in exactly one place (run) and makes the contract
// table-testable without a subprocess.
type command func(args []string, deps deps) (exitCode int, err error)

// deps holds the collaborators a subcommand needs. Constructed once in main
// and passed to every subcommand so tests inject fakes by constructing deps
// directly. Production wiring lives in newDeps, not in the subcommands.
type deps struct {
	state subthread.StateProvider
	live  subthread.LivenessProvider
	// codexHome is resolved once from $CODEX_HOME (or ~/.codex) and reused
	// across subcommands so a single env var read covers a whole cdxa run.
	codexHome string
	// statePath is the cockpit's state.json location, resolved once and
	// reused by spawn (which writes launch bookkeeping to the same file the
	// cockpit reads — ADR 0001 decision 2).
	statePath string
	// replier builds a subthread.Replier for runSend. nil in production
	// (newReplier is used); tests set it to inject a fake-wired Replier, the
	// same DI pattern runSpawn uses for the spawner factory.
	replier replierFn
	// spawner builds a subthread.Spawner for runSpawn. nil in production
	// (newSpawner is used); tests set it to inject a fake-wired Spawner, the
	// same DI pattern runOutput uses for state/live.
	spawner spawnerFn
	// homeResolver maps an agent name to that agent's skill-home
	// directory for runSkills. nil in production (resolveAgentHome is
	// used); tests set it to inject a t.TempDir()-rooted fake so the
	// suite never touches a real agent home, the same DI pattern runSpawn
	// uses for the spawner factory.
	homeResolver homeResolverFn
	// skillLookup fetches the embedded skill bytes by name for
	// runSkills. nil in production (subthread.Lookup is used); tests
	// set it to inject a canned registry, same DI pattern as spawner.
	skillLookup skillLookupFn
}

// stdout is the writer runOutput/runSpawn and printError emit JSON to.
// os.Stdout in production; tests swap it to capture output for assertion.
var stdout io.Writer = os.Stdout

func main() {
	os.Exit(run(os.Args[1:]))
}

// run parses argv, dispatches to the subcommand, and maps the outcome to an
// exit code. It is the single owner of the exit-code contract (ADR 0003
// decision 2): 0 = done, 2 = still working, 3 = unknown/gone, 1 = error. The
// subcommand returns a (code, err) pair; a non-zero code from the
// subcommand is honoured as-is (subthread statuses already know their code),
// and an error from the subcommand always maps to 1 with a JSON error object
// on stdout.
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "cdxa: usage: cdxa <command> [args]")
		fmt.Fprintln(os.Stderr, "commands: output, spawn, send, skills")
		return 1
	}

	cmds := map[string]command{
		"output": runOutput,
		"spawn":  runSpawn,
		"send":   runSend,
		"skills": runSkills,
	}

	name := args[0]
	cmd, ok := cmds[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "cdxa: unknown command %q\n", name)
		fmt.Fprintln(os.Stderr, "commands: output, spawn, send, skills")
		return 1
	}

	d, err := newDeps()
	if err != nil {
		printError(err)
		return 1
	}

	code, err := cmd(args[1:], d)
	if err != nil {
		printError(err)
		return 1
	}
	return code
}

// newDeps resolves $CODEX_HOME and the cockpit state.json path, and wires
// the production StateProvider and LivenessProvider. Failures here (e.g.
// $HOME unresolvable) surface as exit 1 with a JSON error object, same as
// any other operational error.
func newDeps() (deps, error) {
	codexHome, err := resolveCodexHome()
	if err != nil {
		return deps{}, fmt.Errorf("cdxa: resolve codex home: %w", err)
	}
	statePath, err := resolveStatePath()
	if err != nil {
		return deps{}, fmt.Errorf("cdxa: resolve state path: %w", err)
	}
	return deps{
		state:     subthread.DefaultStateProvider{},
		live:      subthread.DefaultLiveness(tmuxstatus.ListLiveSessions),
		codexHome: codexHome,
		statePath: statePath,
		homeResolver: resolveAgentHome,
		skillLookup:  subthread.Lookup,
	}, nil
}

// resolveCodexHome honors $CODEX_HOME (as codex's own CLI does) before
// falling back to ~/.codex. Mirrors cmd/codex-agents so both binaries agree
// on where codex's state lives.
func resolveCodexHome() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home, nil
	}
	return codexstate.DefaultCodexHome()
}

// printError writes a JSON error object to stdout. ADR 0003 decision 2: an
// operational error (exit 1) carries a JSON error object on stdout, not
// just a stderr line — the consumers are codex threads parsing tool output,
// and a bare stderr string isn't parseable.
func printError(err error) {
	fmt.Fprintf(stdout, "{\"error\":%q}\n", err.Error())
}
