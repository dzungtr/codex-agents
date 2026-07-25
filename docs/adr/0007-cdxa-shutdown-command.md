# ADR 0007: cdxa shutdown command

- Status: accepted
- Date: 2026-07-25
- Updates: ADR 0003 decisions 2, 4, and 7

## Context

The headless subthread lifecycle currently has an open end. A parent can
launch work with `cdxa spawn`, collect completed turns with `cdxa output`,
and request another turn with `cdxa send`, but it cannot retire a completed
subthread through the same machine-readable surface. The Codex thread stays
active in Codex's state and its detached `cxa-*` tmux session may remain
alive after the parent has consumed the result.

The cockpit's Archive action already proves the two teardown mechanisms:
stop the tmux session and invoke Codex's sanctioned `archive` command. A
headless command must add a stricter safety gate and deterministic JSON/exit
outcomes because its callers are parent prompts, not humans reading a status
line.

## Decisions

### 1. `shutdown` is the headless lifecycle close

The command is:

```text
cdxa shutdown <thread-id>
```

It accepts exactly one non-empty Codex thread id. It has no confirmation
prompt: the id is explicit, the command is intended for unattended parents,
and the Codex transition is a reversible archive rather than permanent
deletion.

The response and exit contract is:

| Outcome | stdout JSON | Exit |
|---|---|---:|
| both lifecycle targets clean | `{"status":"archived","thread_id":"…"}` | `0` |
| a turn is in progress, or no turn has completed while the session is live | `{"status":"working","thread_id":"…"}` | `2` |
| unknown, already archived, or dead before producing a completed turn | `{"status":"gone","thread_id":"…"}` | `3` |
| usage, state, rollout, tmux, or Codex archive failure | `{"error":"…"}` | `1` |

This extends ADR 0003's existing meanings rather than assigning new exit
codes. stdout remains JSON-only.

### 2. Rollout completion is the safety gate

Shutdown resolves the thread through the existing Codex state provider and
reads its rollout through the existing turn parser. A thread is eligible
only when at least one turn has completed and the rollout does not end with
an in-progress turn.

Tmux liveness does not define completion. A live session waiting for more
input is eligible after its latest turn completes. Conversely, registration
alone is not completion: a live thread with no completed turn is `working`,
while one that died before producing output is `gone`. These are the same
distinctions already exposed by `cdxa output`.

### 3. Stop tmux, then archive Codex

After the completion gate passes, shutdown:

1. targets the session derived by `tmuxstatus.SessionName(threadID)`;
2. stops it when present, treating an already-missing target as clean; then
3. invokes `codex archive <thread-id>`.

The order preserves a retry path. A real tmux failure stops the sequence
before the Codex record is archived, so the record still identifies the
live lifecycle. If Codex archive fails after tmux cleanup, the still-active
record remains resolvable and a retry can finish the transition. Success is
reported only after both targets are clean.

The tmux adapter must distinguish an absent target from a real server or
command failure. It may not flatten every liveness error into “already
gone,” because that could archive the record while leaving an unverified
session behind.

### 4. Archive is soft; direct state mutation is forbidden

“Clean up the Codex thread record” means moving it out of Codex's active and
resume views through Codex's own `archive` command. The conversation history
remains recoverable with `codex unarchive`.

Shutdown does not invoke `codex delete`, update Codex SQLite, or move/remove
rollout files itself. Codex remains the sole owner of its persisted state,
preserving ADR 0001's schema-drift boundary and ADR 0003's stateless cdxa
posture.

### 5. Shutdown is idempotent at lifecycle boundaries

An already-missing tmux session is accepted as clean. After a successful
Codex archive, a repeat call resolves the thread as `gone` and exits `3`,
matching `output` and `send` for an unavailable subthread. A retry after a
Codex archive failure can still resolve the unarchived record and finish
cleanup even though tmux is already gone.

### 6. Worktrees and local bookkeeping are not shutdown targets

Shutdown does not remove the subthread's worktree or branch. The parent may
still need to inspect, commit, push, review, or merge the subthread's output;
automatic worktree removal would conflate process cleanup with source-code
safety.

The command creates no lifecycle database and no parent/subthread record.
Existing cockpit `agentstate` metadata may remain for the cockpit's own UI
and worktree behavior. Codex archive is the authoritative thread transition.

### 7. The decision logic lives behind narrow adapters

The subthread domain module owns completion gating, teardown ordering,
status selection, and retry semantics. It composes three narrow boundaries:
the existing thread/turn provider, a tmux session controller, and a Codex
archiver. The `cmd/cdxa` adapter only validates argv, invokes the operation,
serializes the frozen result, and returns the shared exit code.

Existing Codex-state parsing, tmux session-name derivation, kill arguments,
and cockpit archive process behavior are reused or extracted; shutdown must
not grow competing parsers or direct persistence knowledge.

## Consequences

- Parent prompts can close the lifecycle immediately after consuming the
  final turn without shelling out to tmux or Codex themselves.
- An active turn cannot be killed accidentally through `shutdown`; callers
  receive exit `2` and continue their normal output poll.
- Partial failure is visible as exit `1`, and the teardown order leaves a
  useful retry path instead of claiming best-effort success.
- Codex versions without `archive` support fail operationally rather than
  falling back to an incomplete local hide.
- Source work survives lifecycle cleanup. Worktree removal remains an
  explicit, separately safety-checked action.
- Permanent deletion, active-turn interruption, remote app-server archive,
  and parent/subthread relationship tracking remain separate features.

## Alternatives considered

### Permanently delete with `codex delete`

Rejected. Deletion is irreversible, requires a different confirmation and
safety contract, and is stronger than the requested archive lifecycle.

### Archive Codex before stopping tmux

Rejected. A later tmux failure would leave a live process whose authoritative
thread record is already hidden, making diagnosis and retry less reliable.

### Reuse the cockpit's best-effort status-note behavior unchanged

Rejected. A human-facing note can describe partial cleanup; a headless parent
needs a non-zero, machine-readable failure and must not treat partial cleanup
as success.

### Remove the worktree as part of shutdown

Rejected. Completion of a Codex turn does not prove that source changes are
committed, pushed, reviewed, or merged.

## Measured results

Filled from PRD [#102](https://github.com/dzungtr/codex-agents/issues/102)'s
Results section at initiative close. Both slice PRs — docs [#106](https://github.com/dzungtr/codex-agents/pull/106)
and code [#107](https://github.com/dzungtr/codex-agents/pull/107) — merged
before this promotion; this section is the durable record of what landed.

| Measure | Result |
|---|---|
| Implementation PR and review rounds | Docs slice ([PR #106](https://github.com/dzungtr/codex-agents/pull/106), 278 lines added, 24 removed) merged pre-implementation as the contract-of-record. Code slice ([PR #107](https://github.com/dzungtr/codex-agents/pull/107), 1284 lines added, 3 removed) had **one blocking finding on review** — issue #103 AC #14 asked for the slice's findings to be recorded in #102's Handoffs/Results before the slice closed — and one fix round that did not touch code: the fix landed in #102's Handoffs table (this PR's parent commit is the fix, plus the docs PR body carries the same record). No code or contract-drift rework — the deep `subthread.Shutdown` operation (253 lines in `internal/subthread/shutdown.go`), the `cmd/cdxa/shutdown.go` adapter (105 lines), and the dispatch wiring in `cmd/cdxa/main.go` matched decisions 1–7 as ratified. |
| Unit/integration coverage added | 23 new tests across two files, all green. `internal/subthread/shutdown_test.go` has 17 domain tests with table-style fakes for `StateProvider`, `LivenessProvider`, `SessionStopper`, and `CodexArchiver` — every ADR 0007 branch is exercised (`CompletedAndSessionLive_StopsAndArchives`, `CompletedAndSessionAlreadyGone_StillArchives`, `ListerSeesSessionMissing_SkipKillButArchive`, `TurnInProgress_ReturnsWorkingNoMutation`, `NoTurnLiveSession_ReturnsWorkingNoMutation`, `NoTurnDeadSession_ReturnsGoneNoMutation`, `UnknownThread_ReturnsGoneNoMutation`, `HiddenThread_ReturnsGoneNoMutation`, `TmuxFailure_PreventsArchiveReturnsOperational`, `CodexArchiveFailure_AfterTmuxStopReturnsOperational`, `RetryAfterCodexFailure_FinishesArchive`, `SqliteUnreadableReturnsErrOperational`, `RolloutMissingReturnsErrOperational`, `ShutdownStatusString`, plus the three `DefaultSessionStopper` fall-through paths). `cmd/cdxa/shutdown_test.go` has 6 command-layer tests: the `0`/`2`/`3`/`1` exit-code table (`TestRunShutdown_ExitCodeTable`), three argv-validation tests asserting the `{"error":"…"}` envelope, `TestRunShutdown_UnknownSubcommandStillMapped` for `run`'s default fallback, and `TestRunShutdown_RealTmuxStopsDisposableSession` — a real-tmux smoke that creates a disposable `cxa-shutdown-rd` session, runs shutdown, and asserts the session is gone afterwards, skipping cleanly when tmux is absent. |
| Real Codex/tmux smoke outcome | The `TestRunShutdown_RealTmuxStopsDisposableSession` smoke ran on the implementation branch and asserts a disposable `cxa-shutdown-rd` tmux session is gone after `cdxa shutdown`. The end-to-end Codex path is exercised through `subthread.DefaultCodexArchiver`, which invokes the same `codex archive <thread-id>` shell-out the cockpit already uses — failure modes (missing `codex`, non-zero exit) surface as `{"error":"…"}` exit `1` rather than silent best-effort success, matching decisions 1 and 3. |
| Observed shutdown latency and partial-failure behavior | **Standalone stop latency was not measured** — PRD #102 specifies no timing budget, and no PR #107 check reports a number. The teardown path uses `tmuxstatus.ListLiveSessions` (shells out to `tmux list-sessions`) and, when the derived session is present, runs `tmuxstatus.KillSessionArgs` which is `["kill-session", "-t", <session>]` — i.e. a single-session `tmux kill-session`, not a `kill-server` flow. Ordering and partial-failure outcomes are evidenced by tests: a real tmux failure surfaces as `{"error":"…"}` exit `1` *before* the Codex archive attempt (`TestShutdown_TmuxFailure_PreventsArchiveReturnsOperational`), leaving the authoritative thread record live. A Codex archive failure after tmux teardown surfaces as exit `1` with the tmux target already stopped; `TestShutdown_RetryAfterCodexFailure_FinishesArchive` proves the retry path — the second call sees the absent tmux as clean (decision 5) and completes the Codex archive. `TestShutdown_HiddenThread_ReturnsGoneNoMutation` proves the cockpit-archive path converges on the same `gone` outcome as the headless shutdown, so the two surfaces agree on end state. |
