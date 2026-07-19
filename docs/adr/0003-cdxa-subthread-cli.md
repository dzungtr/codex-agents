# ADR 0003: cdxa subthread CLI

- Status: accepted
- Date: 2026-07-19

## Context

A codex thread often needs to delegate work to another codex thread —
exploration, research, a bounded implementation slice — and consume the
result when it finishes, the way Claude Code subagents behave. Codex has
no native thread-spawns-thread primitive: threads are isolated TUI
processes with no channel between them. The cockpit already owns the two
mechanisms such delegation needs (worktree-per-thread launch in
`internal/codexlaunch`, turn/status derivation in `internal/tmuxstatus` +
`internal/codexstate`), so the feature is a new headless surface over
existing machinery, not new machinery.

## Decisions

### 1. Separate `cdxa` binary, headless and JSON-only

`cmd/cdxa` builds a second binary from the same module, sharing
`internal/`. The cockpit binary (`cmd/codex-agents`) bootstraps bubbletea
on startup; a parent codex thread invoking it for a headless call would
couple delegation to a TUI lifecycle it never sees. All three commands
print JSON to stdout — the consumers are codex threads parsing tool
output, not humans.

### 2. Three commands, async by default

- `cdxa spawn "task" [--profile X] [--workspace worktree|inplace]` —
  launches a detached tmux codex thread (codexlaunch semantics), blocks
  until the thread registers in codex's sqlite, prints the thread id.
- `cdxa output <thread-id> [--wait N]` — returns
  `{"status", "turn", "message"}`. Exit codes: 0 = a completed turn is
  available, 2 = still working, 3 = thread unknown or gone without
  collectable output, 1 = operational error. `--wait` blocks up to N
  seconds for completion — sugar over the parent's poll loop, for
  parents that genuinely have nothing else to do.
- `cdxa send <thread-id> "msg"` — tmux send-keys follow-up into the
  living session (codexlaunch.QuickReply), prints the turn number the
  follow-up started.

Async (spawn returns immediately, parent polls) over sync (spawn blocks
until done): the parent stays free to take input and do other work while
the subthread runs, which is the Claude Code behavior being replicated.

### 3. Rollout file is the sole source of truth

Completion detection and message extraction both read the thread's
rollout jsonl (`~/.codex/sessions/…`, path resolved from the sqlite
`threads.rollout_path`, falling back to codexstate's jsonl scan). No App
Server dependency: the app server only streams events to subscribers
connected *at the time*, so a poll arriving after completion would see
nothing — the rollout file is the only race-free historical record, and
`internal/codexstate` already owns parsing it. Live event streaming via
codex's app-server daemon (`codex app-server daemon`, control socket
under `~/.codex/app-server-control/`) remains available as a future
progress-reporting upgrade, explicitly out of scope here.

### 4. Thread id is the job id; cdxa keeps no job state

`spawn` returns codex's own thread id; `output`/`send` resolve it back
to rollout path and tmux session (`cxa-<prefix>`) via codex's sqlite on
every call. No records in `~/.codex-agents/state.json` beyond what the
cockpit already keeps. Consistent with ADR 0001 decision 2: codex's
data is the single source of truth, cdxa's data is never written. The
brief block at spawn time (until the thread appears in sqlite) is the
price of returning a real, resolvable id instead of a promise.

### 5. Output is addressed by turn number

`output` returns a monotonic `turn` counter derived from the rollout
file's turn markers, plus the last assistant message of that turn. The
parent tracks the highest turn it has consumed; a poll returning an
already-seen turn means nothing new. This makes polling idempotent and
keeps `send`-then-poll unambiguous (the follow-up's output is the next
turn, never a re-read of the previous one). Turn counting is derived
from the rollout on every call — no cursor tokens, no stored progress.

### 6. Workspace strategy is chosen at spawn time

`--workspace worktree` (default) gives the subthread its own checkout at
`<repo-root>/.worktrees/<branch>` (ADR 0001 decision 4).
`--workspace inplace` runs it in the parent's cwd with no worktree — for
read-only work (exploration, research, debugging) where a throwaway
checkout is pure overhead and risks stale reads. Sandbox hardening for
inplace subthreads is deferred; discipline is by prompt convention, as
in the cockpit today.

### 7. The cockpit does not change

Subthreads appear in the cockpit as ordinary threads (their tmux
sessions match the `cxa-` naming). The parent↔subthread relationship is
not recorded anywhere and is not visualized; it lives in the parent's
prompt context. Relationship tracking is a separate cockpit feature if
it ever earns one.

## Consequences

- A subthread's output contract is exactly "last assistant message of
  turn N". Anything richer (structured result files, partial streaming)
  is a new contract and a new ADR.
- Exit codes 0/2/3/1 are API. Parent-thread prompts will hard-code them;
  changing them breaks every deployed delegation prompt.
- `send` inherits QuickReply's limits: no delivery confirmation, no
  retries, and nothing to send to if the subthread's tmux session died
  (callers exclude closed threads first).
- The feature inherits ADR 0001's schema-drift posture: if codex changes
  the sqlite layout or rollout format, cdxa degrades the same way the
  cockpit does, because it shares `internal/codexstate`.

## Measured results

_To be filled at initiative close: spawn-to-registered latency, typical
poll intervals observed in real delegation prompts, rollout-parse
failures encountered across codex upgrades._
