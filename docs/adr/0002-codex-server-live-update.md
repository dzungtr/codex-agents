# ADR 0002: Codex App Server live-update for message count in the list view

- Status: proposed
- Date: 2026-07-12
- Depends on: [ADR 0001](0001-codex-agents-cockpit-architecture.md)

## Context

### The problem

The cockpit's list view shows a message count (`N msgs`) per thread, sourced from
`codexstate.enrichFromSessionFiles` — a one-shot parse of each thread's rollout JSONL
file (`~/.codex/sessions/*.jsonl`). This parse runs only when the list is (re)loaded:

- Once at startup (`main.go` → `loadRows` → `codexstate.LoadThreads`).
- Again on each `Refresh` action (after attach-detach, quick-reply, interrupt).

Between refreshes the message count is stale. A thread can accumulate dozens of messages
while the cockpit sits idle, and the user has no way to see that activity without manually
triggering a refresh (or re-entering the thread's tmux session). The existing notify hook
fires only on **turn-ended** events — it doesn't stream per-message updates, and it doesn't
carry message count or token count in its payload.

### What changed in Codex: the App Server

Codex now exposes a structured **App Server** — a JSON-RPC 2.0 protocol over stdio that
streams live conversation events as they happen. The cockpit can spawn a single App Server
instance and subscribe to events for all managed threads simultaneously, receiving a
real-time feed including:

- `thread.run.updated` — fired on every assistant message, turn step, token-count change.
- `thread.run.completed` — fired when a turn ends (same trigger as the existing notify hook).
- `thread.message` — individual message records with role, content, and metadata.
- `token_count` updates — streaming token usage per turn.

This is a superset of what the notify hook provides today: the hook's `turn-ended` event
maps to `thread.run.completed`, but the App Server additionally gives us **intra-turn**
updates — the live message count and token count that change *while the agent is working*.

### Design goal

Enable the cockpit to show a **live-updating message count** (and eventually token count)
in the list view while a thread is working, without requiring manual refreshes and without
polling the rollout JSONL file. The mechanism must:

1. Coexist with the existing sqlite/JSONL + tmux-liveness + notify-hook architecture (ADR 0001).
2. Degrade gracefully when the App Server is unavailable (older codex, sandboxed env, etc.).
3. Never block the UI thread — all server communication happens in background goroutines
   that emit bubbletea messages.
4. Respect the existing read-only contract: the cockpit never writes to codex's data; the
   App Server is a *read-only consumer* of live events, not a control channel.

## Decision

### 1. Spawn a single shared Codex App Server per cockpit instance

The cockpit spawns **one** App Server process at startup — a single `codex app-server`
child process that lives for the entire cockpit session. All managed threads' live events
flow through this one server. The App Server is spawned as a child of the cockpit (not
inside tmux), communicating over the child process's stdin/stdout pipes.

**Rationale:** A single shared server minimises process overhead (one background process
regardless of how many threads are active), simplifies lifecycle management (start once at
startup, stop once on quit), and centralises the event tap. The App Server protocol
supports subscribing to multiple thread conversations through a single connection, so there
is no need for per-thread isolation at the process level.

**Process model:**
```
cockpit (bubbletea)
  ├─ tmux session cxa-<id-1> (codex TUI, user-facing)
  ├─ tmux session cxa-<id-2> (codex TUI, user-facing)
  ├─ tmux session cxa-<id-n> (codex TUI, user-facing)
  └─ app-server child process (single, codex app-server, background)
       └─ stdin/stdout JSON-RPC pipe → cockpit goroutine → tea.Msg
            ├─ events for thread-1
            ├─ events for thread-2
            └─ events for thread-n
```

The App Server is **separate from the tmux sessions**. Each tmux session runs the
interactive codex TUI for the user. The App Server is a single headless background process
that the cockpit uses purely as a live event subscriber across all threads. It subscribes
to each managed thread's conversation by thread ID and multiplexes events into one channel.

### 2. New internal package: `internal/codexserver`

```
internal/codexserver/
  client.go        — JSON-RPC 2.0 client over stdio (spawn + request/notify)
  events.go        — event type definitions matching the App Server's streaming schema
  manager.go       — lifecycle: start/stop the single server, subscribe to threads,
                     route events to the UI
  manager_test.go
  client_test.go
  events_test.go
```

This package owns all knowledge of the Codex App Server protocol — the JSON-RPC envelope,
the event names, the request/response shapes, and the process lifecycle. Nothing outside
this package knows that the live feed comes from a JSON-RPC stdio server.

**Client (`client.go`):**
- `Client` wraps an `exec.Cmd` running `codex app-server`.
- Sends JSON-RPC requests over stdin; reads newline-delimited JSON-RPC responses/notifications
  from stdout in a background goroutine.
- Exposes a `Subscribe(threadID string) error` method that subscribes the single server to
  a specific thread's event stream.
- Exposes an `Unsubscribe(threadID string) error` method to stop receiving events for a
  thread that is no longer alive.
- Routes all decoded events into a single multiplexed channel, tagged with the thread ID.

**Events (`events.go`):**
```go
type EventKind int
const (
    EventMessage EventKind = iota  // a new message was added to the thread
    EventTokenUpdate               // token count changed
    EventTurnCompleted             // turn ended (same semantic as notify hook's turn-ended)
)

type Event struct {
    ThreadID     string
    Kind         EventKind
    MessageCount int    // best-effort, -1 if unknown
    TokenCount   int    // best-effort, -1 if unknown
    At           time.Time
}
```

The client maps raw App Server JSON-RPC notifications to these `Event` values. Unknown
notification types are skipped (forward compatibility).

**Manager (`manager.go`):**
```go
type Manager struct {
    codexHome string
    client    *Client           // single shared client
    events    chan Event         // multiplexed channel consumed by the UI
    mu        sync.Mutex
    started   bool
}

func NewManager(codexHome string) *Manager
func (m *Manager) Start() error                    // spawn the single server process
func (m *Manager) Subscribe(threadID string) error // subscribe to a thread's events
func (m *Manager) Unsubscribe(threadID string) error // unsubscribe from a thread
func (m *Manager) Stop()                           // kill the server, clean up
func (m *Manager) Events() <-chan Event            // the UI's event tap
```

The manager is created in `main.go` and wired into the bubbletea program. `Start` is called
once at startup; `Subscribe` is called for each alive thread (and for newly launched/resumed
threads); `Unsubscribe` is called when a thread is archived or its session dies; `Stop` is
called on cockpit quit.

### 3. Bubbletea integration: `tea.Msg` from server events

A new message type carries live updates into the bubbletea model:

```go
// in internal/ui/composer.go (or a new messages.go)
type ThreadLiveUpdateMsg struct {
    ThreadID     string
    MessageCount int
    TokenCount   int
}
```

The `main.go` entry point spawns a goroutine that reads from `manager.Events()` and
posts `ThreadLiveUpdateMsg` into the bubbletea program via `program.Send()`. This is the
standard bubbletea pattern for external event sources:

```go
// in run(), after tea.NewProgram:
go func() {
    for ev := range manager.Events() {
        program.Send(ui.ThreadLiveUpdateMsg{
            ThreadID:     ev.ThreadID,
            MessageCount: ev.MessageCount,
            TokenCount:   ev.TokenCount,
        })
    }
}()
```

The model's `Update` handler patches the matching row in place:

```go
case ThreadLiveUpdateMsg:
    for i := range m.rows {
        if m.rows[i].Thread.ID == msg.ThreadID {
            if msg.MessageCount >= 0 {
                m.rows[i].Thread.MessageCount = msg.MessageCount
            }
            if msg.TokenCount >= 0 {
                m.rows[i].Thread.TokenCount = msg.TokenCount
            }
            // No sortRows needed — message count doesn't affect ordering
            break
        }
    }
    return m, nil
```

This is a **targeted in-place patch** (same pattern as `ThreadLivenessMsg`), not a full
reload. It avoids the cost and race risk of re-running `loadRows` on every message.

### 4. Server lifecycle: subscribe/unsubscribe per thread, one server

| Event | Action |
|---|---|
| Cockpit startup | `Start()` (spawn the single server), then `Subscribe` for each thread with an alive tmux session |
| `ThreadLaunchedMsg` (new thread) | `Subscribe` after the liveness check confirms the tmux session is alive |
| `AttachDoneMsg` (detach from thread) | `Subscribe` if the thread is still alive and not already subscribed |
| `ArchiveDoneMsg` | `Unsubscribe` for the archived thread |
| Cockpit quit (`q`/`ctrl+c`) | `Stop()` — kill the single server before the program exits |

Subscriptions are **not** created for closed threads — a closed thread has no tmux session
and no running codex process, so there's nothing to stream. If a closed thread is resumed
(`enter` on a closed row → `codex resume`), the subsequent `AttachDoneMsg` → `Refresh`
cycle will pick up the now-alive tmux session and `Subscribe` to its events.

### 5. App Server availability detection and graceful degradation

Not all environments will have the App Server:
- Older codex versions that don't ship `codex app-server`.
- Sandboxed environments where spawning a child process isn't permitted.
- Codex upgrades that change the subcommand name or protocol.

**Detection:** `Manager.Start` attempts to spawn `codex app-server`. If the binary
doesn't exist (`exec.ErrNotFound`) or exits immediately with a non-zero code, the manager
marks itself as "server unavailable" and falls back to the existing model:
the message count stays as whatever `enrichFromSessionFiles` last parsed, and the
notify hook continues to drive status transitions as before. All subsequent `Subscribe`
calls become no-ops.

**The degraded state is silent:** no error banner, no status-line message. The cockpit
just doesn't get live updates — it behaves exactly as it does today.
This matches ADR 0001's philosophy that missing data degrades quietly rather than crashing.

### 6. Relationship to the existing notify hook

The App Server's `thread.run.completed` event is semantically identical to the notify
hook's `turn-ended` event. The two will **coexist** in v1:

- The **notify hook** remains the primary status-derivation mechanism (it's already wired,
  tested, and works for threads not launched by the cockpit).
- The **App Server** is a *supplement* that adds intra-turn live updates (message count,
  token count) on top of the notify hook's turn-level events.

In a future iteration, the App Server's `thread.run.completed` could replace the notify
hook for cockpit-launched threads, but that's out of scope for this ADR — the goal here
is to add live message count, not to re-architect status derivation.

### 7. Relationship to the sqlite/JSONL state source

The App Server does **not** replace the sqlite/JSONL thread-list source (ADR 0001 decision 2).
The initial list is still loaded from `codexstate.LoadThreads` at startup. The App Server
only provides **live deltas** (updated message count, token count) for threads that are
currently alive and subscribed. If the server is unavailable, counts revert to the last
value from the JSONL enrichment — no data is lost, it's just not as fresh.

### 8. Rendering: no changes needed

The existing `badgeClusterPlain` in `internal/ui/view.go` already renders `%d msgs` from
`Thread.MessageCount` and `tokens: %d` from `Thread.TokenCount`. Since the live update
patches these fields in place, the rendering layer requires no changes — the badge will
update automatically on the next `View()` call after a `ThreadLiveUpdateMsg`.

## Consequences

- **Positive:** The message count in the list view updates in real time as agents work,
  without manual refresh. The user can see at a glance which threads are actively producing
  output, giving a secondary activity signal beyond the working/waiting status dot.
- **Positive:** The architecture extends cleanly to future live features (streaming token
  counts, turn-progress indicators, live title generation) since the event channel is
  general-purpose.
- **Positive:** Full backward compatibility — if the App Server is unavailable, the cockpit
  behaves exactly as it does today.
- **Positive:** Minimal process overhead — one background `codex app-server` process
  regardless of how many threads are active, instead of one per thread.
- **Positive:** Simple lifecycle — start once at startup, stop once on quit. Subscriptions
  are add/remove on the single server, not process spawn/kill.
- **Negative:** A single server crash takes down the live feed for all threads at once
  (mitigated by the graceful degradation to static counts; no data loss).
- **Negative:** Complexity — a new package, a new process lifecycle, a new event path into
  the bubbletea model. This is the most complex addition since the notify hook.
- **Risk:** The App Server protocol is not yet covered by a stability guarantee in the
  same way the sqlite schema is pinned to `state_5`. If the protocol changes, the manager's
  event mapping needs updating. The `events.go` layer isolates this to one package.

## Implementation packages (delta from ADR 0001)

| Package | Change |
|---|---|
| `internal/codexserver` | **NEW** — App Server client, event types, single-instance lifecycle manager |
| `cmd/codex-agents` | Wire `codexserver.Manager` into `run()`; spawn event-tap goroutine; subscribe/unsubscribe on lifecycle events |
| `internal/ui` | Add `ThreadLiveUpdateMsg` and its `Update` handler (targeted in-place row patch) |
| `internal/codexstate` | No changes (still the startup/fallback source) |
| `internal/notifyhook` | No changes (still the status-derivation source) |
| `internal/tmuxstatus` | No changes |
| `internal/agentstate` | No changes |

## Open questions

1. **App Server subcommand name:** This design assumes `codex app-server` (or similar).
   The exact subcommand and flags need to be confirmed against the installed codex version.
   If the subcommand doesn't exist, the manager's `Start` will fail gracefully and the
   cockpit will fall back to the current static-count behavior.

2. **Reconnect on server crash:** If the single App Server process dies unexpectedly,
   should the manager attempt to restart it and re-subscribe to all threads? The v1 design
   above does not — it marks the server as unavailable and continues with stale counts.
   Auto-reconnect with re-subscription is a v2 concern.

3. **Throttling:** A fast-working agent could emit dozens of `thread.run.updated` events
   per second. The manager should batch or throttle events before posting to the bubbletea
   program to avoid flooding the render loop. A simple approach: coalesce events per thread
   and emit at most one `ThreadLiveUpdateMsg` per thread per 500ms.

4. **Non-cockpit-launched threads:** Threads started in a plain terminal (not via the
   cockpit's tmux launcher) can still be subscribed to on the shared server (the server
   subscribes by thread ID, not by launch origin). However, their tmux session naming
   won't match the `cxa-` prefix convention, so the cockpit may not know they're alive
   unless they appear in the sqlite threads table. This is the same gap the notify hook has
   for non-cockpit threads, and is acceptable for v1.
