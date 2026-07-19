# ADR 0001: codex-agents cockpit architecture

- Status: accepted
- Date: 2026-07-08
- PRD: [dzungtr/codex-agents#1](https://github.com/dzungtr/codex-agents/issues/1)

## Context

Running several codex agents in parallel means juggling terminal windows by hand: no single
place to see every conversation, no visibility into which agent is blocked waiting for input,
no safe way to launch parallel agents in one checkout, and no way to jump back into an old
session from a global view. `codex resume`'s picker is per-invocation and cwd-scoped.

**codex-agents** is a terminal cockpit that is *only* a list view; the conversation experience
is codex's own TUI, unmodified. The decisions below were made in the PRD (issue #1) and are
recorded here as the architectural contract for all implementation slices.

## Decisions

### 1. Stack: Go + bubbletea/lipgloss

A single static binary suits a personal terminal tool; bubbletea provides the list-UI model
and `teatest` enables golden-file rendering tests. lipgloss implements the visual style
contract in `Codex-Orchestrator-TUI/index.html` (dark terminal aesthetic, status dots,
lean rows, composer with pills, keybind footer).

### 2. Read-only sqlite as the state source

The thread list is derived from codex's own records: a narrow, read-only SELECT against the
newest `~/.codex/state_*.sqlite` (by glob) `threads` table — id, title, cwd, model,
git_branch, archived, recency, rollout_path — using a pure-Go sqlite driver (no cgo).

- Codex's data is **never written**. The cockpit keeps its own small state file
  (`~/.codex-agents/state.json`: thread_id → tmux session, profile, worktree path,
  last turn event).
- If the schema probe fails (codex upgrade drift), degrade to a best-effort jsonl scan of
  `~/.codex/sessions/` rather than crashing. Fixtures are pinned to schema `state_5`.
- All codex-specific knowledge (sqlite schema, session files, CLI invocation, notify hook)
  lives in **one internal package**, so the codex integration can be generalized later.

Consequence: every codex conversation appears in the cockpit — including ones started in a
plain terminal — not just cockpit-launched ones.
 
 **Thread identity is codex's id, not a cockpit-minted UUID (ratified by #47/#48).** The
 interactive launch path (`internal/codexlaunch.Launcher.Launch`) blocks until codex has
 written the freshly-launched thread to its sqlite, then adopts codex's id as the row's
 `Thread.ID` and as the key for `agentstate` and the notify-hook event log. The cockpit
 mints no UUID of its own for thread identity — the tmux session name (`cxa-<prefix>`) is
 the cockpit's own handle and is deliberately decoupled from thread identity. This brings
 the interactive launch path into compliance with this decision and with ADR 0003 decision
 4 (`spawn` returns codex's own thread id); prior to #47 it contradicted both by minting a
 cockpit UUID that codex never wrote, producing duplicate rows after attach-then-detach.

### 3. tmux-per-thread process model

Each cockpit-launched thread is a detached tmux session named `cxa-<thread-id-prefix>`
running the real codex TUI. Entering a thread = attaching its session (`switch-client` when
already inside tmux); a closed thread gets a fresh session running `codex resume <id>`.
Detach returns to the list.

Consequence: agents survive the cockpit and the terminal closing; there is zero custom
transcript rendering; interrupt/quick-reply can be implemented against tmux primitives.

### 4. Worktree-per-thread launch semantics

Every launch gets its own git worktree at `<repo-root>/.worktrees/<branch>`, branch slug
generated from the task title (collision → numeric suffix). A non-git startup directory runs
in place. Invocation:

```
tmux new-session -d -s cxa-… -c <worktree> codex -p <profile> [-c model=…] "<task>"
```

Profiles are codex config profiles (`$CODEX_HOME/<name>.config.toml`); the composer defaults
to `general-agentic` because a detached launch implies an unattended posture.

Consequence: parallel agents never collide in one checkout, and archive can offer worktree
removal — refusing when there is uncommitted or unpushed work.

### 5. Status derivation model (ordering is the attention mechanism)

Three statuses, derived — never self-reported:

| tmux session | last turn event | status |
|---|---|---|
| alive | turn in progress | **working** |
| alive | turn ended | **waiting** (needs you) |
| gone | — | **closed** |

Turn events come from a notify wrapper chained at launch (`-c notify=[…]`) that records
turn-ended events for the cockpit and forwards to the user's existing notify command. If the
hook is unavailable (e.g. threads not launched by the cockpit), status degrades to
open/closed. The list orders waiting → working → closed, most-recent first within each group;
there are no desktop notifications — the top of the list *is* the inbox.

## Implementation packages

The decisions above were implemented as follows (package names, for readers navigating the
repo):

- `internal/codexstate` — read-only sqlite/jsonl thread-list source (decision 2)
- `internal/tmuxstatus` — tmux session naming and status derivation (decisions 3, 5)
- `internal/agentstate` — `~/.codex-agents/state.json` read/write (decision 2)
- `internal/codexlaunch` — worktree-per-thread launch invocation (decision 4)
- `internal/notifyhook` — turn-ended event recording/forwarding (decision 5)
- `internal/worktreesafety` — uncommitted/unpushed checks backing archive's refusal rule
  (decision 4)
- `internal/ui` — bubbletea list model and lipgloss styling (decision 1)
- `cmd/codex-agents` — the binary entrypoint wiring the packages above together

## Measured results

_Filled from the PRD issue's (#1) Results section at initiative close (all 5 child slices
merged — see #2–#6)._

- **Reliability of `tmux send-keys` quick-reply**: preliminary verdict is **kept** — literal
  `-l --` text delivery plus a separate Enter keypress, covered by a unit test against a fake
  runner and a real-tmux integration test, both green. This is not yet a final verdict: it
  still needs a manual run against real codex to fully confirm reliability in practice. Owner:
  next person doing daily-driver use of the cockpit should confirm and update this line.
- **Time from `codex-agents` start to usable list with ~50 threads**: not measured in this
  build — the sandboxed environment used to build codex-agents has no real `~/.codex`
  installation with real thread history to measure against. **Open, pending human
  measurement**: to be measured during actual daily use of codex-agents against a real
  `~/.codex` state directory with a realistic thread count.
- **Did codex sqlite schema (`state_5`) survive codex upgrades during the build?**: not
  observable in this build — no real codex upgrades occurred during the sandboxed build.
  **Open, pending human observation**: to be tracked across real codex upgrades encountered
  during actual daily use; if the schema drifts, the degrade-to-jsonl-scan path (decision 2)
  should be exercised and this line updated with what changed.
- **Worktree-per-thread overhead per launch**: not measured in this build — no real, repeated
  launches against real repositories occurred. **Open, pending human measurement**: to be
  measured during actual daily use of codex-agents (e.g. wall-clock time from launch to a
  usable attached session, across a few representative repo sizes).
 
- **Thread identity = codex's id (decision 2, ratified by #47/#48)**: filled from the merged
  code PR #51 (squash commit `2b9ff1e`, closing slice #49). The blocking-poll launch path is
  in place: `Launcher.Launch` (`internal/codexlaunch/launcher.go`) polls
  `codexstate.ThreadByCWD` via the `cwdRegistrar`/`Registrar` seam until codex has written
  the thread, then returns codex's id as `LaunchResult.ThreadID` — the cockpit mints no UUID
  of its own for thread identity. The tmux session is created under a cockpit-handle-derived
  name and renamed to `cxa-<codexID>` once codex registers, so downstream consumers that
  derive the session name from the thread id via `tmuxstatus.SessionName` keep resolving to
  the actual session.
  - **Duplicate-row repro (#47) — eliminated by construction.** The optimistic launch row's
    `Thread.ID` is codex's id (the same id `loadRows` later returns from codex's sqlite), so
    `RowsRefreshedMsg`'s merge sees one id, not two, and keeps a single row. Pinned by
    `TestUpdate_RowsRefreshedMsg_LaunchedRowKeyedByCodexIDIsNotDuplicated` in
    `internal/ui/refresh_merge_test.go` (optimistic row keyed by codex id + a refresh
    carrying the same id → exactly one row survives). The pre-#48 attach-then-detach
    duplicate is gone at the model level.
  - **`agentstate` and `events.jsonl` keyed by codex's id.** `Launch` calls
    `agentstate.Upsert(statePath, threadID, entry)` with codex's id; `runNotifyHook`
    (`cmd/codex-agents/main.go`) resolves the notify-hook wrapper's identity positional
    (the original, pre-rename tmux session name) back to codex's id via
    `agentstate.FindThreadIDBySession` before invoking `notifyhook.Run`. So `events.jsonl`
    and `agentstate.LastTurnEvent` are keyed by codex's id, and `turnEndedByThread`/
    `hiddenByThread` lookups in `loadRows` land on codex's rows — the working→waiting
    transition wiring is correct for cockpit-launched threads. On resolution failure
    `runNotifyHook` degrades to the handle as-is, preserving pre-#48 behaviour rather than
    failing codex's turn-completion flow.
  - **`cdxa spawn`'s latent production timeout — fixed for free.** `Spawn`'s
    `ThreadRegistered` poll (`internal/subthread/spawn.go`) now exits on the first check,
    because `HeadlessLaunch` returns a registered id. The belt-and-braces loop is unchanged
    and its tests pass; the latent 30s `DefaultRegistrationWait` timeout that would have
    fired in production (the cockpit UUID was never written by codex) no longer occurs.
  - **Launch-to-registered latency — not observed against real codex.** Same gap ADR 0003
    flags: the 30s `DefaultRegistrationWait` bound is asserted (the registration-timeout
    path is unit-tested — session killed, no state written), but the typical wait was never
    observed against a real codex sqlite. **Open, pending human measurement** during daily
    driver use.
  - **Manual end-to-end verification — deferred.** The production confirmation (launch a
    thread from the composer, attach, detach — confirm a single row remains) requires a
    real codex CLI + `$CODEX_HOME` and was flagged for human verification in PR #51; it
    was not run in the build sandbox. **Open, pending human verification.**
  - **Non-goal held: no `agentstate` schema migration.** Existing `state.json` entries
    keyed by cockpit UUIDs are orphans; acceptable for an unreleased tool with no
    production users. A startup sweep remains explicitly out of scope (PRD #48 non-goal).
