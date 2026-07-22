---
name: cdxa-spawn
description: Delegate work to another codex thread via cdxa spawn. Use when you would reach for the built-in subagent tool — bounded implementation slices, exploration, research, parallel work — and want the subthread in its own tmux session with resumable, inspectable output.
---

# cdxa-spawn

Self-contained playbook for delegating work to another codex thread via the
`cdxa` CLI. You are reading this because you reached for (or considered) the
built-in subagent tool — read on, because cdxa delegation has a different
shape, a hard depth limit, and a strict output contract that built-in
subagents do not enforce.

Architecture: the leaf model caps cdxa recursion at depth 1. You (the
spawner) call `cdxa spawn`; the subthread you create is a leaf that cannot
`cdxa spawn` itself. Bottom out at one level, then fan out by spawning more
leaves — never by nesting.

## When to delegate (and when not to)

Delegate to a subthread when the work is one of:

- A bounded implementation slice with a clear, reviewable deliverable (one
  PR, one function, one config change, one file fix).
- An exploration or research sweep (read N files, summarise, return a
  report).
- Parallel work the parent would otherwise serialise — multiple independent
  slices you can dispatch simultaneously and join on the way back.

Do not delegate when the work is:

- A trivial lookup, single file read, or one-shot shell command you can run
  yourself in a turn.
- A tightly-coupled edit that needs to interleave with code you are about to
  write in the same turn — context will collide before the subthread returns.
- Anything where the cost of a full thread start (sqlite registration,
  rollout, tmux session) exceeds the value of the result. Subthread setup
  is not free; a 2-second `cat` should stay in the parent thread.

When in doubt, do the work yourself. Subthreads are parallelism, not a
shortcut for laziness.

## Spawning

Spawn a subthread with the `cdxa spawn` command. The first positional
argument is the task string and **must include the prompt envelope**
(see [The prompt envelope](#the-prompt-envelope) below). The second piece
is the workspace flag:

```
cdxa spawn "<task + envelope>" --workspace inplace
```

- `--workspace inplace` is the default for skill-driven delegation. The
  subthread inherits your cwd and decides on its own whether to create a
  worktree (see `WORKSPACE:` in the envelope). Pass `--workspace worktree`
  only if you have already done the worktree work yourself.
- `cdxa spawn` returns a JSON object on stdout:
  `{"thread_id": "cxa-1a2b3c…"}`. Capture the id; you will use it for
  output polling and follow-up sends.
- Spawn blocks until the subthread registers in codex's sqlite (bounded by
  `DefaultRegistrationWait`, 30 seconds). A spawn that times out prints a
  JSON error object and exits `1`; treat that as a hard failure — do not
  retry blindly.

Example:

```
tid=$(cdxa spawn "$(cat <<'EOF'
IDENTITY: leaf
PROFILE: code-review
WORKSPACE: yes
OUTPUT CONTRACT: report the audit findings as a markdown summary.
EOF
)" --workspace inplace | jq -r .thread_id)
```

## Profile

`--profile` is **user policy, not skill policy**. This skill does not tell
you which profile to use for which task type. You look it up in your own
`~/.codex/AGENTS.md` task-type→profile table; if no row matches, omit
`--profile` and let cdxa use its default.

To change which profile a task type uses, edit `~/.codex/AGENTS.md` — never
hardcode profile names into the envelope, and never fork this skill to
encode policy. Profile selection is yours; the skill is the mechanics.

## The prompt envelope

The envelope is embedded directly in the task string passed to
`cdxa spawn` — there is no separate flag for it, by design. The envelope
is what makes the leaf model work, what gives the subthread the workspace
cue, and what sets the output contract your parent will read. **Always
include the full envelope verbatim.**

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
```

The envelope is byte-stable. Do not rewrite, reword, or "improve" it. Sibling
sanity tests assert its presence; downstream consumers (including the
parent prompt that will read your output) depend on the exact phrasing.

## Leaf rule

A leaf thread is **forbidden from `cdxa spawn`**. The rule is
prompt-enforced — cdxa keeps no state and has no identity field, so the
enforcer is you, the model, reading the `IDENTITY: leaf` line in your own
spawn-time prompt.

You may still decompose your own work using the **built-in Codex subagent
tool** (the same one this skill is replacing for the parent). Use it for
sub-tasks you would otherwise have spawned a cdxa leaf for. The net effect:
cdxa recursion is capped at depth 1 (parent → leaf), and the runaway
fan-out dies at the first leaf, because a leaf cannot cdxa-spawn.

If a parent task demands depth ≥ 2 of cdxa delegation, that is a planning
error in the parent, not something the leaf fixes. Return the constraint
violation to the parent in your last assistant message and let the parent
re-decompose.

## Collecting output

Once you have a `thread_id`, poll for output with `cdxa output`. Branch on
the exit code, not on the JSON body — exit codes are the frozen API
(ADR 0003) and parent prompts hard-code them.

```
while true; do
  out=$(cdxa output "$tid")
  code=$?
  case $code in
    0) echo "$out" | jq -r .message; break ;;
    2) sleep 5 ;;
    3) echo "subthread gone" >&2; break ;;
    1) echo "cdxa error: $out" >&2; break ;;
  esac
done
```

Exit code mapping (frozen, do not hardcode anything else):

- `0` — a completed turn is available; `.message` is your subthread's
  last assistant message of the latest turn. **This is the only output
  the parent should consume.**
- `2` — still working; sleep and poll again. Pair with `--wait N` to block
  up to N seconds before returning, reducing poll churn.
- `3` — thread unknown or gone without collectable output. Give up; do
  not retry; surface the loss to the user.
- `1` — operational error (sqlite unreadable, rollout missing, malformed
  input). Surface to the user; do not retry blindly.

The `.message` field is **your subthread's last assistant message** of
the current turn — exactly what the envelope's `OUTPUT CONTRACT` requires
the leaf to produce. Everything else the leaf did (tool calls, file reads,
intermediate reasoning) is invisible to you. That is the contract; respect
it.

`--wait N` blocks for up to N seconds waiting for a completed turn before
returning exit `2`. Use it to cut down on `sleep` churn for subthreads you
expect to finish in roughly bounded time. For long-running leaves, fall
back to the plain poll loop with a fixed sleep.

## Follow-ups

Send a follow-up message to a running subthread with `cdxa send`:

```
cdxa send "$tid" "<message>"
```

The command returns the new turn number that the message opened:

```
{"turn": 2}
```

After sending, poll for **exactly that turn** — do not assume the
previous turn's `.message` is still current. A safe follow-up pattern:

```
turn=$(cdxa send "$tid" "Continue: also fix the lint errors you flagged." | jq -r .turn)
while true; do
  out=$(cdxa output "$tid")
  code=$?
  case $code in
    0)
      msg_turn=$(echo "$out" | jq -r .turn)
      if [ "$msg_turn" -ge "$turn" ]; then
        echo "$out" | jq -r .message; break
      fi
      sleep 2
      ;;
    2) sleep 5 ;;
    3) echo "subthread gone" >&2; break ;;
    1) echo "cdxa error: $out" >&2; break ;;
  esac
done
```

Follow-ups are how you steer a subthread that has already started. The
envelope is set at spawn time and cannot be retro-applied — if you need
a different output contract mid-flight, you are talking to the wrong
subthread; spawn a new one.

## Cleanup

Subthreads are ordinary codex threads that happen to have been spawned
by another thread. They outlive the parent's interest. There is no
`cdxa kill`; there is no "close the subthread when the parent exits."

Practical cleanup options:

- **Leave it.** If the subthread's work is done and its thread id is no
  longer interesting, just stop polling. The subthread's rollout stays in
  codex's sqlite and can be reattached via the cockpit at any time.
- **Archive via the cockpit.** If you want the subthread out of your
  active list without deleting the rollout, archive it through the
  cockpit UI or whatever archival surface your codex build provides.
- **Do not** try to terminate the subthread's tmux session from a parent
  prompt. You have no authority to do that without a dedicated cdxa
  kill command, and none exists by design (ADR 0003 decision 4 — cdxa
  keeps no job state).

If the subthread is still working when you are done with it, just walk
away. The work continues, the rollout is preserved, and a future turn or
human visit can pick it up.
