# codex-agents

A single `cdxa` binary that is both a terminal cockpit for running several
codex agents in parallel and a headless CLI that lets one codex thread
delegate work to another. Launched without a subcommand, `cdxa` opens the
cockpit TUI; with a subcommand (`spawn`, `output`, `send`, `skills`), it
performs headless JSON-only work (ADR 0005).

## Language

**Thread**:
A single codex conversation, identified by codex's own thread id and
recorded in codex's state (sqlite `threads` table, rollout jsonl file).
The cockpit lists threads; it never creates a different kind of
conversation.
_Avoid_: session (that's the tmux process), conversation (vague)

**Subthread**:
A thread launched by another codex thread via `cdxa spawn`, running
unattended with a chosen profile and workspace mode. The spawning thread
is its *parent thread*; the relationship is implicit (carried in the
parent's prompt context), never recorded by cdxa.
_Avoid_: job, task, child, subagent (all imply lifecycle or hierarchy
semantics cdxa does not have — a subthread is an ordinary thread that
outlives its parent's interest in it)

**Turn**:
One completed unit of assistant work within a thread, bounded by codex's
own turn-start/turn-end markers in the rollout file. Subthread consumers
address output by turn number, not by message content.
_Avoid_: message, reply (a turn may contain many messages; only the last
assistant message of a turn is collected)

**Leaf thread**:
A subthread stamped `IDENTITY: leaf` by its spawner via the prompt
envelope. Forbidden from calling `cdxa spawn` (prompt-enforced, not
mechanism-enforced — cdxa keeps no state and has no identity field), but
may decompose its own work using the built-in Codex subagent tool. Caps
cdxa recursion at depth 1: a parent spawns leaves, and delegation ends
there.
_Avoid_: worker, child (imply lifecycle or hierarchy semantics cdxa
does not have — a leaf is an ordinary subthread with a prompt constraint)

**Prompt envelope**:
The structured block the spawner embeds in the task string passed to
`cdxa spawn`. Carries the leaf identity stamp, the profile-lookup
instruction (task-type→profile table in `~/.codex/AGENTS.md`), the
worktree self-determination instruction, and the output contract (the
last assistant message is the only thing the parent reads). Not a cdxa
flag — it is prompt text, consistent with cdxa's stateless posture
(ADR 0003 decision 4).
_Avoid_: system prompt, system message (codex's own concepts; the
envelope is ordinary task-string content the spawner composes)
