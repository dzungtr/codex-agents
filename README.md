# cdxa

A single binary that is both a **terminal cockpit** for running several
[codex](https://github.com/openai/codex) agents in parallel and a **headless CLI**
for codex thread delegation. Launched without a subcommand, `cdxa` opens the
cockpit TUI; with a subcommand (`spawn`, `output`, `send`, `skills`), it performs
headless JSON-only work. The cockpit is *only* a list view — the conversation
experience is codex's own TUI, unmodified.

![codex-agents cockpit: list of codex threads in the terminal](assets/cdxa-preview.png)

## Problem

Running several codex agents in parallel means juggling terminal windows by hand: no single
place to see every conversation, no visibility into which agent is blocked waiting for input,
no safe way to launch parallel agents in one checkout, and no way to jump back into an old
session from a global view. `codex resume`'s picker is per-invocation and cwd-scoped.

`cdxa` derives a list of every codex thread (running, waiting, or finished) straight from
codex's own records, so every conversation shows up — not just ones launched from the cockpit
— with the threads that need your input surfaced at the top.

## Download

Pre-built binaries are available on the
[GitHub Releases page](https://github.com/dzungtr/codex-agents/releases). Download the
binary for your platform, make it executable, and place it on your `$PATH`:

```sh
# Example: Linux amd64
curl -L -o cdxa https://github.com/dzungtr/codex-agents/releases/latest/download/cdxa-linux-amd64
chmod +x cdxa
sudo mv cdxa /usr/local/bin/
cdxa
```

Or install from source with Go:

```sh
go install github.com/dzungtr/codex-agents/cmd/cdxa@latest
```

## Prerequisites

- [tmux](https://github.com/tmux/tmux) installed and on `$PATH`
- A working `codex` CLI installation with state under `$CODEX_HOME` (default `~/.codex`)

Every cockpit-launched thread runs inside a detached tmux session on the **default tmux
socket**, so a tmux server must already be running outside the terminal's cgroup. Otherwise
closing the launching terminal kills every spawned thread (see
[#69](https://github.com/dzungtr/codex-agents/issues/69)). On systemd-logind hosts (Fedora,
etc.) run tmux as a `systemd --user` service so its `KillMode=process` cgroup outlives the
originating terminal:

```ini
# ~/.config/systemd/user/tmux-server.service
[Unit]
Description=Persistent tmux server for cdxa threads
After=default.target

[Service]
Type=forking
ExecStart=/usr/bin/tmux -f /dev/null start-server \; set-option -g exit-empty off
ExecStop=-/usr/bin/tmux kill-server
RemainAfterExit=yes
KillMode=process

[Install]
WantedBy=default.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now tmux-server.service
loginctl enable-linger $USER
```

`exit-empty off` is what keeps the server alive after the last session detaches. `loginctl
enable-linger` keeps the user service alive across logout. The cockpit never starts a tmux
server of its own; whatever server owns the default socket when the cockpit launches is what
the spawned sessions attach to.

## Build from source

```sh
git clone https://github.com/dzungtr/codex-agents
cd codex-agents
go build ./cmd/cdxa
./cdxa
```

Run from the directory you want new threads launched into — the composer starts threads in
that directory (in a per-thread git worktree, if it's a git repo), while the list itself
shows threads across all projects. `$CODEX_HOME` is honored if set, otherwise `~/.codex` is
used.

## Features

### Cockpit TUI

Launched without a subcommand, `cdxa` opens a terminal list view of every codex thread
across all projects. The list is derived from codex's own sqlite state — not self-reported —
so every conversation shows up regardless of how it was started.

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | Move selection |
| `enter` | Attach an alive thread's tmux session, or resume and attach a closed one |
| `i` | Focus the composer to launch a new thread |
| `r` | Quick-reply to the selected alive thread |
| `x` | Interrupt the selected thread's current turn (thread moves to **waiting**) |
| `a` | Archive: kill the tmux session, hide the thread, offer worktree removal |
| `/` | Filter the list by title, repo, or branch |
| `?` | Toggle the help overlay |
| `q` / `ctrl+c` | Quit |

Threads move through three statuses: **working** (tmux session alive, turn in progress) →
**waiting** (tmux session alive, turn ended — needs you) → **closed** (no tmux session).
The list orders waiting → working → closed, most-recent first within each group — ordering
is the attention mechanism; there are no desktop notifications.

### Headless delegation

A codex thread can delegate work to another codex thread via four subcommands. All
subcommand stdout is JSON-only with a frozen exit-code mapping; the vocabulary (thread,
subthread, turn, leaf thread) is defined in [`CONTEXT.md`](CONTEXT.md).

**`cdxa spawn`** — Launch a subthread into its own git worktree (default) or in-place in
the parent's cwd (`--workspace inplace`). Returns `{"thread_id": "…"}` and blocks until
the thread registers in codex's sqlite, so the returned id is immediately resolvable by
`output` and `send`.

```sh
tid=$(cdxa spawn "Audit the auth package for SQL injection." --workspace inplace | jq -r .thread_id)
```

**`cdxa output`** — Collect a subthread's completed turn as `{"status", "turn", "message"}`.
The `turn` counter is monotonic, derived from the rollout file on every call — no cursors or
stored progress. Poll repeatedly: exit `0` means a completed turn is available, `2` still
working, `3` thread gone, `1` operational error. Use `--wait N` to block up to N seconds
instead of hand-rolling a poll loop.

```sh
cdxa output "$tid"
cdxa output "$tid" --wait 30
```

**`cdxa send`** — Follow up into a living subthread with a new message. Returns
`{"turn": N}` — the turn number the follow-up started — so the parent can target the next
completed turn in its poll loop. Exits `3` if the thread is unknown or its tmux session is
dead.

```sh
cdxa send "$tid" "Now also check the billing service."
```

**`cdxa skills`** — Install an embedded skill file into an agent's skill folder. Skills are
embedded in the binary via `go:embed`, so the installed file is always byte-identical to
what the binary shipped — re-running after an upgrade is idempotent (unchanged skills are
skipped, changed ones overwritten). Reports `{"path", "written", "changed"}`.

```sh
cdxa skills cdxa-spawn --agent codex
```

For copy-pasteable parent-thread prompt patterns — poll loops, turn tracking, send-then-
collect refinement, `--wait` blocking, and workspace selection — see the
[cdxa subthread cookbook](docs/cdxa-subthread-cookbook.md).

## Documentation

All architectural decisions, design records, and reference material live in the
[`docs/`](docs/) folder:

```
docs/
├── adr/
│   ├── 0001-codex-agents-cockpit-architecture.md
│   ├── 0002-codex-server-live-update.md
│   ├── 0003-cdxa-subthread-cli.md
│   ├── 0004-cdxa-skills-command.md
│   └── 0005-unified-cdxa-binary.md
└── cdxa-subthread-cookbook.md
```

The docs are indexed for semantic search via [memsearch](https://crates.io/crates/memsearch)
(collection `codex_agents`, configured in [`.memsearch.toml`](.memsearch.toml)). To find
documentation relevant to a topic, run:

```sh
memsearch search "your topic" -c codex_agents --top-k 5
```

For example:

```sh
memsearch search "subthread delegation" -c codex_agents --top-k 5
memsearch search "unified binary" -c codex_agents --top-k 5
memsearch search "worktree launch" -c codex_agents --top-k 5
```

The vocabulary (thread, subthread, turn, leaf thread, prompt envelope) is defined in
[`CONTEXT.md`](CONTEXT.md).
