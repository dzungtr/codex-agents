---
name: cdxa-spawn
description: Delegate work to another codex thread via `cdxa spawn`. Use when you would reach for the built-in subagent tool — bounded implementation slices, exploration, research, parallel work — and want the subthread in its own tmux session with resumable, inspectable output.
---

# cdxa-spawn

Self-contained playbook for delegating work to another codex thread via
`cdxa spawn` (ADR 0003). Use this when you would otherwise reach for the
built-in subagent tool and want the subthread in its own tmux session with
resumable, inspectable output. The subthread runs as a **leaf** — cdxa
recursion is capped at depth 1.

## When to delegate (and when not to)

Delegate via `cdxa spawn` when the work benefits from a separate codex
session: a long-running or asynchronous job, a bounded implementation
slice, exploration, research, or several independent tasks you want to
run in parallel. Each subthread gets its own tmux session, its own
rollout, and survives across your own context boundaries — you can come
back later, see exactly what it did, and follow up.

Do not delegate when:

- The work is a trivial lookup you can answer with one tool call.
- The task is tightly coupled to your own edits — the subthread would
  race you on the same files, and the back-and-forth is more friction
  than just doing it.
- The work is single-process and would not benefit from a separate tmux
  session. In that case, reach for the **built-in subagent tool**
  instead. It keeps everything in your own session; cdxa-spawn buys you
  nothing there.

Rule of thumb: if you would `cdxa spawn` and then forget the thread id,
you should not have spawned at all.

## Spawning

```
tid=$(cdxa spawn "<task with prompt envelope>" --workspace inplace | jq -r .thread_id)
```

Shape returned on stdout:

```
{"thread_id": "cxa-1a2b3c…"}
```

Exit `0` on success. The command blocks until the subthread registers in
codex's sqlite (bounded by `DefaultRegistrationWait`, 30s) so the
returned `thread_id` is immediately resolvable by `cdxa output` — you
can poll right away. A spawn that times out, fails to launch, or is
misused prints a JSON error object and exits `1`.

`--workspace inplace` is the default for delegating threads: you inherit
your own cwd, and the subthread decides whether to create an isolated
git worktree (see [The prompt envelope](#the-prompt-envelope)). Use
`--workspace worktree` when you want cdxa to create a worktree for the
subthread itself rather than letting the subthread do it.

## Profile

Profile selection is **user policy, not skill policy**. You maintain a
task-type→profile table in `~/.codex/AGENTS.md` (the AGENTS.md the
codex CLI reads at startup). Look up the profile for the task type you
are delegating:

- Match found → pass it: `cdxa spawn "..." --profile my-profile --workspace inplace`
- No match → omit `--profile` and let cdxa use its default.

Do not hard-code profile names here. The user owns the table, and the
skill is versioned separately from their policy.

## The prompt envelope

Every task string must embed a **prompt envelope** as plain text. There
is no new flag — the envelope travels inside the task. Stamp it before
the actual task description so the subthread parses it first.

The envelope is the contract that makes delegation safe: it sets the
identity (leaf), points the subthread at the profile table, tells it
how to choose a workspace, and fixes the output contract the parent
relies on.

Literal envelope block (include verbatim, then append your task text
after `TASK:`):

```
IDENTITY: leaf
You are a leaf thread, spawned by another codex thread via cdxa spawn. You are forbidden
from calling cdxa spawn yourself — you may decompose your own work using the built-in
subagent tool, but cdxa delegation ends with you.

PROFILE: look up the profile for this task type in the AGENTS.md task-type→profile table;
if none matches, omit --profile and let cdxa use its default.

WORKSPACE: decide whether your task needs an isolated git worktree. If yes, create one at
<repo-root>/.worktrees/<branch> and work there. If no, work in place in your inherited cwd.

OUTPUT CONTRACT: your last assistant message is the ONLY thing your parent reads. No tool
calls, files you read, or reasoning are visible to it. End your final message with a
complete, self-contained summary of what you did, structured for machine parsing.

TASK: <your task here>
```

`cdxa spawn` does not parse the envelope — it is just text the subthread
reads. Omitting the envelope breaks the leaf rule and the output
contract; do not ship a task string without it.

## Leaf rule

A **leaf** is a subthread that may not delegate via cdxa. The envelope's
`IDENTITY: leaf` line tells the subthread this on its first turn. This
is the recursion cap:

- Leaf → `cdxa spawn` is **forbidden**. Do not invoke it. Even one level
  of leaf-spawns-leaf breaks the model: the parent loses the bounded
  fan-out, errors compound across the chain, and the prompt-envelope
  discipline becomes a maze of inheritance rules.
- Leaf → the **built-in Codex subagent tool is allowed**. You can still
  decompose your own work; cdxa delegation just ends with you.

The rule is prompt-enforced, not mechanism-enforced. `cdxa` keeps no
identity state and has no `--identity` field (per ADR 0003 decision
4) — it would have nothing to check. A leaf that rationalizes
`$(cdxa spawn ...)` from a shell can bypass it. Discipline is by prompt
convention, same as inplace sandbox hardening (ADR 0003 decision 6).
Net effect: cdxa recursion caps at depth 1 (main → leaf). The runaway
fan-out dies at the first leaf, because a leaf cannot cdxa-spawn.

"Keep spawning instead of doing work" still applies — it means the
main thread should spawn **multiple parallel leaves** for independent
pieces of work, not a deep tree.

## Output contract

The subthread's **last assistant message is the only thing you read**.
No tool calls, files the subthread read, intermediate reasoning, or
scratch output are visible to you — the parent thread only sees the
final message text returned by `cdxa output`.

Collect the output with a poll loop or a blocking wait:

```
# Poll loop (Pattern 1) — when you have other work to interleave
while true; do
  out=$(cdxa output "$tid"); code=$?
  case $code in
    0) echo "$out" | jq -r .message; break ;;   # completed turn available
    2) sleep 5 ;;                                 # still working
    3) echo "subthread gone" >&2; break ;;        # unknown or dead
    1) echo "cdxa error: $out" >&2; break ;;      # operational error
  esac
done

# Blocking wait (Pattern 4) — when you have nothing else to do
out=$(cdxa output "$tid" --wait 30); code=$?
case $code in
  0) echo "$out" | jq -r .message ;;   # a turn completed within 30s
  2) echo "still working after 30s" ;; # timeout; poll again or --wait again
  3) echo "gone" >&2 ;;
  1) echo "error: $out" >&2 ;;
esac
```

Output shapes:

```
{"status":"done","turn":1,"message":"<last assistant message of turn 1>"}
{"status":"working","turn":0,"message":""}
{"status":"gone","turn":0,"message":""}
```

The exit code is the contract — branch on `$?`, not on the JSON body.
The `status` strings are advisory.

Because the parent only sees the final message, **end your final
message with a complete, self-contained summary** of what you did:
what changed, what you decided not to change and why, any blockers,
and the next concrete step. The summary is the only artifact the
parent has to reason about your work — structure it for machine
parsing (key facts first, free text second), and assume the parent
cannot ask you clarifying questions without another round trip.

## Follow-ups

To push a follow-up into a still-living subthread:

```
target=$(cdxa send "$tid" "<refinement or new direction>" | jq -r .turn)
```

`send` returns the turn number the follow-up started:

```
{"turn": 2}
```

`send` exits `0` on delivery, `3` when the thread is unknown or its
tmux session is dead (treat it the same as `3` from `output`: the
subthread is gone), and `1` on operational error. It inherits the
cockpit's QuickReply limits — no delivery confirmation, no retries;
always branch on the exit code before parsing JSON.

Then collect exactly that turn:

```
while true; do
  out=$(cdxa output "$tid" --wait 60); code=$?
  case $code in
    0)
      turn=$(echo "$out" | jq -r .turn)
      [ "$turn" -ge "$target" ] && { echo "$out" | jq -r .message; break; }
      ;;
    2) sleep 5 ;;
    3) echo "subthread gone" >&2; break ;;
    1) echo "error: $out" >&2; break ;;
  esac
done
```

`send` returns the turn the follow-up **started**; poll until the
completed turn is `>= target`. The returned `turn` is a 1-based
monotonic completed-turn counter — there is no cursor token to
persist, and no stored progress (ADR 0003 decision 5).

## Cleanup

Subthreads are **ordinary threads** that outlive the parent's
interest. They are not jobs you own and reap; they are conversations
that continue running in their own tmux session until you (or the
cockpit) decide otherwise. `cdxa` has no kill — by design.

When you are done with a subthread:

- **Leave it alone** if you might want to come back to it. The thread
  id is a stable handle; the rollout is in codex's sqlite; you can
  read it later with `cdxa output` or by browsing the cockpit.
- **Archive it via the cockpit** if you want it marked inactive
  without losing the history. The cockpit exposes an archive action
  for any thread it knows about.
- **Do not** try to terminate it from cdxa — there is no command for
  that. The subthread is a peer codex session, not a child process.

## `cdxa skills` exit codes (frozen, ADR 0004)

When you install or update a skill via `cdxa skills <name> --agent
<claude|agents|codex>`, the command writes a `.md` file into the
agent's skill folder and prints a JSON result to stdout. The
exit-code/JSON contract is API — changing it breaks every consumer
(deployed prompts, CI) — same posture as ADR 0003.

| Exit | Meaning | stdout |
|---|---|---|
| `0` | Success | `{"path": "<agent-home>/skills/<name>/SKILL.md", "written": true, "changed": true|false}` |
| `1` | Operational error (unresolvable home, write failure, unknown skill, unknown agent) | `{"error": "<message>"}` |

`changed: false` means the installed file already matches the embedded
content byte-for-byte — the install was a no-op. `changed: true`
means a new file was written or an existing one was overwritten with
different bytes. The installer is idempotent: re-running it is safe
and will report `changed: false` once the file is in place.
