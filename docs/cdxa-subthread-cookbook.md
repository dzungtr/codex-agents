# cdxa subthread delegation cookbook

Copy-pasteable parent-thread prompt patterns for delegating work to another
codex thread via the headless [`cdxa`](../cmd/cdxa) CLI. Each pattern below
ships as a prompt snippet with exact commands, the JSON shape the command
prints to stdout, and the exit code it returns.

Vocabulary follows [`CONTEXT.md`](../CONTEXT.md): a **thread** is one codex
conversation; a **subthread** is a thread launched by another thread via
`cdxa spawn`; a **turn** is one completed unit of assistant work within a
thread, addressed by turn number. The architectural contract — three
commands, JSON-only stdout, and the frozen exit-code mapping — is
[ADR 0003](adr/0003-cdxa-subthread-cli.md).

## Commands at a glance

| Command | stdout JSON | Exit codes |
|---|---|---|
| `cdxa spawn "task" [--profile X] [--workspace worktree\|inplace]` | `{"thread_id": "…"}` | `0` spawned + registered; `1` launch/timeout/usage error |
| `cdxa output <thread-id> [--wait N]` | `{"status","turn","message"}` | `0` a completed turn is available; `2` still working; `3` thread unknown or gone; `1` operational error |
| `cdxa send <thread-id> "msg"` | `{"turn": N}` | `0` delivered; `3` thread unknown or session dead; `1` operational error |

Exit-code mapping is owned by `run()` in `cmd/cdxa/main.go` and the
`exitCodeFor` switch in `cmd/cdxa/output.go`; the `status` strings
(`"done"`, `"working"`, `"gone"`) come from `internal/subthread.Status`.
Exit codes `0`/`2`/`3`/`1` are API — parent prompts hard-code them, so
changing a value breaks every deployed delegation prompt.

---

## Pattern 1 — Basic spawn → poll loop

Spawn a subthread, then poll its output until a completed turn arrives.
Branch on the exit code of `cdxa output`:

```
You delegate a bounded task to a subthread and consume its first completed
turn. Use cdxa for all delegation — never call the cockpit binary from a
headless context.

1. Spawn the subthread and capture the thread id:
   tid=$(cdxa spawn "Audit the auth package for SQL injection and report
   findings as a markdown summary." --workspace worktree | jq -r .thread_id)

2. Poll for output in a loop. Branch on cdxa output's exit code, not on the
   JSON body:
   - exit 0  → a completed turn is available; read .message, stop polling.
   - exit 2  → still working; sleep and poll again.
   - exit 3  → thread unknown or gone without collectable output; give up.
   - exit 1  → operational error (sqlite unreadable, rollout missing);
     surface the error to the user; do not retry blindly.

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

Expected shapes:
  spawn   →  {"thread_id": "cxa-1a2b3c…"}
  output  →  {"status":"done","turn":1,"message":"<last assistant message of turn 1>"}
             {"status":"working","turn":0,"message":""}   # still working
             {"status":"gone","turn":0,"message":""}        # unknown/dead
```

Notes:
- `spawn` blocks until the thread registers in codex's sqlite (bounded by
  `DefaultRegistrationWait`, 30s) so the returned `thread_id` is immediately
  resolvable by `cdxa output`. A spawn that times out prints a JSON error
  object and exits `1`.
- `--profile` is optional and defaults to `general-agentic`. `--workspace`
  is optional and defaults to `worktree` (see Pattern 5).

---

## Pattern 2 — Turn-tracking idiom

`cdxa output` returns the **highest completed turn** and the last assistant
message of that turn. The poll is idempotent: polling twice against
unchanged state returns the same `turn`/`message`. Track the highest turn
you have already consumed; a poll that returns the same `turn` means nothing
new — keep polling.

```
You delegate a long-running task and consume turns as they complete. Never
re-process a turn you have already consumed.

State: keep a variable `consumed` = 0 (highest turn already consumed).

Loop:
  out=$(cdxa output "$tid"); code=$?
  case $code in
    0)
      turn=$(echo "$out" | jq -r .turn)
      if [ "$turn" -gt "$consumed" ]; then
        echo "$out" | jq -r .message   # new turn → process it
        consumed=$turn
      fi
      # Same turn → nothing new; keep polling.
      ;;
    2) sleep 5 ;;          # still working
    3) break ;;            # gone
    1) echo "error: $out" >&2; break ;;
  esac

Expected output shapes (turn is the 1-based monotonic completed-turn
counter; 0 means no turn has completed yet):
  {"status":"done","turn":1,"message":"…"}
  {"status":"done","turn":2,"message":"…"}
  {"status":"working","turn":1,"message":"<last completed turn's message>"}
```

The `turn` counter is derived from the rollout file's turn markers on every
call (ADR 0003 decision 5) — there is no cursor token to persist beyond
`consumed`, and no stored progress.

---

## Pattern 3 — Send-then-collect follow-up refinement

`cdxa send` delivers a one-line follow-up into a living subthread's codex
composer and returns the turn number the follow-up started. The parent's
next `cdxa output` poll that observes a *new* completed turn reports that
turn — so target the returned turn, not "the latest turn".

```
After the subthread completes turn 1, refine the task with a follow-up
message. send returns the turn the follow-up started; poll output until
that turn completes.

1. Send the follow-up and capture the started turn:
   target=$(cdxa send "$tid" "Now also check the billing service for the
   same bug class." | jq -r .turn)
   # target == N, where N = (completed turns at send time) + 1

2. Poll output until it reports a completed turn >= target:
   while true; do
     out=$(cdxa output "$tid"); code=$?
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

Expected shapes:
  send    →  {"turn":2}                  # follow-up started turn 2
  output  →  {"status":"working","turn":1,"message":"<turn 1 msg>"}   # in flight
             {"status":"done","turn":2,"message":"<turn 2 msg>"}     # refined result
```

Notes:
- `send` exits `3` (no JSON body) when the thread is unknown or its tmux
  session is dead — the gone-check that the cockpit's interactive
  QuickReply deliberately omits. Treat `3` from `send` the same as `3`
  from `output`: the subthread is gone.
- `send` inherits QuickReply's limits: no delivery confirmation, no
  retries. Nothing to send to if the session died — always branch on the
  exit code before parsing JSON.

---

## Pattern 4 — `--wait` for blocking consumers

For parents that genuinely have nothing else to do, `cdxa output --wait N`
blocks up to `N` seconds for a completed turn to appear instead of polling.
`--wait 0` (and an omitted flag) means a point-in-time poll: read the
rollout once and return (Pattern 1's behaviour).

```
You have a single subthread and nothing else to do while it runs. Block on
output instead of hand-rolling a poll loop.

  out=$(cdxa output "$tid" --wait 30); code=$?
  case $code in
    0) echo "$out" | jq -r .message ;;   # a turn completed within 30s
    2) echo "still working after 30s" ;; # timeout; poll again or --wait again
    3) echo "gone" >&2 ;;
    1) echo "error: $out" >&2 ;;
  esac

`--wait` re-reads the rollout on subthread's internal poll cadence until
either a completed turn appears (exit 0) or N seconds elapse (exit 2 if
still working). When the thread is already gone at the first read, it
returns exit 3 immediately — `--wait` does not burn the full N seconds on a
dead thread.
```

`--wait` is sugar over the poll loop, not a different contract: the JSON
shape and exit codes are identical to a point-in-time `cdxa output`. Use it
when you would otherwise `sleep` between polls; keep the hand-rolled loop
(Pattern 1) when you have other work to interleave.

---

## Pattern 5 — Workspace-mode selection

Choose the workspace at spawn time with `--workspace`. The default is
`worktree` — the cockpit's standard worktree-per-thread launch (a fresh git
worktree at `<repo-root>/.worktrees/<branch>` and a detached tmux session
named `cxa-<prefix>`). `inplace` runs the subthread in the parent's cwd
with no worktree and no new branch.

```
Pick the workspace mode by the kind of work the subthread will do:

- worktree (default) — the subthread will WRITE code. It gets its own
  checkout, so it can branch, commit, and leave the parent's tree untouched.
  Use for implementation tasks, refactors, and any slice that produces a
  PR.

    tid=$(cdxa spawn "Implement the foo endpoint with tests." \
      --workspace worktree | jq -r .thread_id)

- inplace — the subthread will only READ. No worktree means no throwaway
  checkout, no stale-read risk, and no branch to clean up. Use for
  exploration, research, debugging, and codebase Q&A.

    tid=$(cdxa spawn "Map every call site of internal/codexlaunch.QuickReply
    and summarise who owns the delivery mechanism." \
      --workspace inplace | jq -r .thread_id)

Both produce the same spawn JSON shape:
  {"thread_id": "cxa-…"}

Sandbox hardening for inplace subthreads is deferred; discipline is by
prompt convention, as in the cockpit today. If a subthread might write,
default to worktree — the worktree is the safety boundary.
```

---

## See also

- [ADR 0003 — cdxa subthread CLI](adr/0003-cdxa-subthread-cli.md): the
  architectural contract (three commands, exit-code mapping, workspace
  strategy, turn addressing).
- [`CONTEXT.md`](../CONTEXT.md): vocabulary (thread, subthread, turn) used
  consistently across cdxa and the cockpit.
- [ADR 0001 — codex-agents cockpit architecture](adr/0001-codex-agents-cockpit-architecture.md):
  worktree-per-thread launch semantics and codex's sqlite as the
  single source of truth, which cdxa inherits without writing its own state.
