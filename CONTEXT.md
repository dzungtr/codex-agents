# codex-agents

A terminal cockpit for running several codex agents in parallel, plus a
headless CLI (`cdxa`) that lets one codex thread delegate work to another.

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
