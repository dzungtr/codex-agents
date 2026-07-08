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
