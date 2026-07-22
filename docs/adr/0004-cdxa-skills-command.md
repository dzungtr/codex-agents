# ADR 0004: cdxa skills command

- Status: accepted
- Date: 2026-07-22

## Context

A codex thread that should delegate work to another codex thread via `cdxa
spawn` (ADR 0003) has no discoverable, version-stable instructions. The
cookbook (`docs/cdxa-subthread-cookbook.md`) is the canonical reference,
but it lives outside `$CODEX_HOME` and an agent has no reason to know it
exists — it reaches for the built-in subagent tool by default. Worse, a
naive delegation prompt produces unbounded cdxa recursion: main → spawn A
→ A spawns B → B spawns C → … never bottoms out. Two problems, one
feature: discoverability of the delegation surface, and a recursion cap
that doesn't exist in cdxa's mechanism (it keeps no state, per ADR 0003
decision 4).

## Decisions

### 1. Write-only installer, no print mode

`cdxa skills <name> --agent <claude|agents|codex>` writes a `.md` skill
file into the agent's skill folder. There is no stdout-markdown print
mode. This preserves ADR 0003's JSON-only-stdout contract — no prose
exception is carved. The consumers of this command are humans or agents
running an install (a mutating action), not programs parsing prose;
mutations report a JSON result, same as every other cdxa command.

### 2. Idempotent overwrite + byte-compare

Every invocation writes the full skill file unconditionally (creating
dirs as needed), byte-compares against existing content, and reports
`changed: true|false`. The binary is the source of truth; the installed
file is a cache of it. Repeat-after-upgrade "just works" — the
drift-killing property: skill and CLI are versioned together and can
never drift, because the binary that executes the commands is the same
binary that ships the instructions.

### 3. Embedded content via go:embed

Skill markdown lives as plain `.md` files in the repo, embedded into the
binary via `go:embed` into a `map[string][]byte` registry keyed by skill
name. Writers edit real markdown with full tooling; the binary is
self-contained (no runtime file lookups, works wherever it is copied).

### 4. The leaf model caps cdxa recursion at depth 1

The spawner stamps `IDENTITY: leaf` into the subthread's prompt envelope
at spawn time. A leaf thread is **forbidden from `cdxa spawn`**
(prompt-enforced — cdxa keeps no state and has no identity field, per
ADR 0003 decision 4) but **may use the built-in Codex subagent tool**
for its own sub-decomposition. Net effect: cdxa recursion is capped at
depth 1 (main → leaf). The runaway fan-out (main → A → B → C) dies at
the first leaf, because a leaf cannot cdxa-spawn. "Keep spawning instead
of doing work" still holds — it means main spawns *multiple parallel
leaves* rather than a deep tree.

### 5. Profile is user policy, not skill policy

The skill does not encode which task-type gets which profile. The user
maintains a task-type→profile table in `~/.codex/AGENTS.md`; the skill
instructs the spawner to look up the profile for the task type there,
falling back to default if none matches. This keeps policy in the
user's hands and out of the versioned binary.

### 6. Worktree is subthread self-determination

The skill teaches the spawner to spawn `--workspace inplace` (neutral —
inherits the spawner's cwd), and the prompt envelope instructs the
subthread: "decide whether your task needs an isolated git worktree; if
yes, create one at `<repo-root>/.worktrees/<branch>` and work there; if
no, work in place." Zero cdxa changes — the subthread is a full agent
with shell access and can `git worktree add` itself. Inplace sandbox
hardening remains deferred (ADR 0003 decision 6 — accepted).

## Consequences

- The exit-code/JSON contract is API. Deployed prompts and CI will
  hard-code `path`/`written`/`changed` and exit 0/1; changing them
  breaks every consumer, same posture as ADR 0003.
- The leaf rule is prompt discipline, not a mechanism. A leaf that
  rationalizes shelling out to cdxa (`$(cdxa spawn ...)`) can bypass it.
  Accepted — same posture as inplace sandbox hardening (ADR 0003
  decision 6): discipline by prompt convention, not enforcement.
- One skill ships now (`cdxa-spawn`, self-contained). Separate
  `cdxa-output`/`cdxa-send` skills would duplicate content already
  inlined in the spawn skill; defer until a focused skill earns its own
  existence.

## Measured results

_Promoted at initiative close (PRD #53). Stub — filled with durable,
cross-initiative learnings (sizing, measured latency, telemetry gaps)
once the implementation lands and runs against real agent homes._
