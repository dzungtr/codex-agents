# ADR 0005: Unified cdxa binary

- Status: accepted
- Date: 2026-07-22
- Supersedes: ADR 0003 decision 1

## Context

The project shipped two binaries built from the same Go module:

- `cmd/codex-agents` — the cockpit TUI (bubbletea list of every codex
  thread, composer, attach/detach, archive). 618 lines of entry-point
  logic plus 576 lines of tests, all in package `main`.
- `cmd/cdxa` — the headless JSON-only CLI for codex thread delegation
  (`spawn`, `output`, `send`, `skills`). 155-line thin dispatch layer
  wiring `internal/subthread`.

ADR 0003 decision 1 justified the split: "a parent codex thread invoking
[the cockpit] for a headless call would couple delegation to a TUI
lifecycle it never sees." The cockpit binary bootstraps bubbletea on
startup before any dispatch, so a headless call to it starts a TUI that
never renders and exits with a TTY error.

In practice this split caused a deployment hazard: the two binaries are
installed to the same directory and one was overwritten by the other,
producing a state where `cdxa spawn` silently launched the cockpit TUI
instead of executing the subcommand. The root cause is structural — two
binaries from one module, one install path, no mechanism distinguishing
them at the filesystem level.

The `notify-hook` subcommand already lives inside the cockpit binary as
a hidden pre-TUI dispatch (`if os.Args[1] == "notify-hook"`), proving
that subcommand dispatch before bubbletea init is viable within a single
binary. This ADR generalizes that pattern: one binary, one dispatch
table, no split.

## Decisions

### 1. Single binary: `cdxa`

`cmd/codex-agents` is deleted. `cmd/cdxa` is the sole binary. The
binary name `cdxa` is chosen because:

- The `notify-hook` wrapper (`-c notify=["/usr/local/bin/cdxa",
  "notify-hook", ...]`) already embeds `cdxa` into every launched
  thread's codex config — it is the path codex invokes on turn-end.
- `codex-agents` had no external consumers beyond interactive human use;
  `cdxa` is the machine-facing name already wired into production.
- No `os.Args[0]` inspection, no symlink, no backwards-compat shim. The
  binary is `cdxa`; `codex-agents` is retired.

### 2. No-subcommand launches the cockpit TUI

```
cdxa                     # → cockpit TUI (was: codex-agents)
cdxa spawn "task" ...    # → headless spawn
cdxa output <id>         # → headless output
cdxa send <id> "msg"     # → headless send
cdxa skills <name> ...   # → headless skills install
cdxa notify-hook ...     # → hidden notify-hook (codex invokes this)
```

No subcommand → `cockpit.Run(codexHome, statePath)`, which bootstraps
the bubbletea program. A non-interactive caller (no TTY) gets the
existing "could not open a new TTY" error from bubbletea — accepted,
since calling `cdxa` with no subcommand from a headless context is a
usage error, same as calling `codex-agents` was.

### 3. Extract TUI wiring into `internal/cockpit`

The cockpit's entry-point logic (`run`, `loadRows`, `loadAgentState`,
`turnEndedByThread`, `hiddenByThread`, `archiveAction`,
`refreshAction`, `codexArchive`, `archiveWorktree`, the `tea.Program`
wiring) moves from `cmd/codex-agents/main.go` into a new deep module
`internal/cockpit`. This follows the repo's existing pattern — every
concern lives in `internal/` (`subthread`, `codexlaunch`, `codexstate`,
`notifyhook`, `ui`, `tmuxstatus`, `agentstate`); the cockpit is the
only major logic that lived in `cmd/`.

`cmd/cdxa/main.go` stays a thin dispatch layer (its current character):
a dispatch table mapping subcommand names to `command` funcs, plus the
no-args default calling `cockpit.Run`. The cmd layer never touches
`tea.Program` directly.

`internal/ui` (the bubbletea model/view/update) remains the rendering
layer; `internal/cockpit` is the wiring that feeds data into it.

### 4. `notify-hook` becomes a regular dispatch-table entry

The pre-TUI special case:

```go
if len(os.Args) > 1 && os.Args[1] == notifyhook.Subcommand {
    runNotifyHook(os.Args[2:])
    return
}
```

is removed. `notify-hook` joins the dispatch table as a `command` entry.
A thin adapter wraps `runNotifyHook`'s `func(args []string)` signature
to the `command` type (`func(args []string, deps deps) (int, error)`),
always returning `(0, nil)` — preserving the "never block codex's
turn-completion flow" contract (ADR 0001, notify-hook). The function
keeps its stderr-only error reporting and exit-0 behavior; only the
dispatch mechanism changes (from hardcoded `if` to uniform table entry).

### 5. ADR 0003 decision 1 is superseded

ADR 0003 decision 1 ("Separate `cdxa` binary, headless and JSON-only")
is superseded. The new rationale: a single binary with subcommand
dispatch before bubbletea init achieves the same isolation (headless
subcommands never touch TUI code) without the deployment hazard of two
binaries sharing one install path.

ADR 0003 decisions 2–7 (exit-code contract, async-by-default, rollout
as sole source of truth, thread-id-as-job-id, turn-addressed output,
workspace strategy, cockpit unchanged) are **unaffected** and remain in
force. The JSON-only-stdout contract for `spawn`/`output`/`send`/
`skills` is unchanged — those subcommands never touch the TUI code path.

## Consequences

- One binary to build, install, and version. No more dual-install hazard.
- `cmd/cdxa` grows by one dispatch-table entry (`notify-hook`) and one
  no-args default (`cockpit.Run`). The `command` type and `deps` struct
  are already shared infrastructure.
- `internal/cockpit` is a new package with a single entry point
  (`Run(codexHome, statePath) error`). The 576 lines of tests in
  `cmd/codex-agents/main_test.go` move to `internal/cockpit/` alongside
  the functions they test (`turnEndedByThread`, `hiddenByThread`,
  `loadAgentState`, `loadRows` are pure functions that belong in a
  package, not in `cmd/`).
- The `notify-hook` integration test
  (`cmd/codex-agents/notifyhook_integration_test.go`) builds the binary
  as `codex-agents`; it must be updated to build as `cdxa` and assert
  the same end-to-end behavior.
- README and CONTEXT.md references to `codex-agents` as a binary name
  are updated to `cdxa`.
- `codex-agents` as a binary name is gone. Users who had shell aliases
  or muscle memory for `codex-agents` update to `cdxa`.
