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

## Prerequisites

- Go 1.25+
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

## Build / run

```sh
go build ./cmd/cdxa
./cdxa
```

Or without a separate build step:

```sh
go run ./cmd/cdxa
```

Run from the directory you want new threads launched into — the composer starts threads in
that directory (in a per-thread git worktree, if it's a git repo), while the list itself
shows threads across all projects. `$CODEX_HOME` is honored if set, otherwise `~/.codex` is
used.

## Headless delegation (`cdxa` subcommands)

```sh
cdxa spawn "task" --workspace inplace
cdxa output <thread-id>
cdxa send <thread-id> "follow-up"
cdxa skills cdxa-spawn --agent codex
```

A codex thread can delegate work to another codex thread via these subcommands. All
subcommand stdout is JSON-only with a frozen exit-code mapping; the vocabulary (thread,
subthread, turn, leaf thread) is defined in [`CONTEXT.md`](CONTEXT.md).

## Cockpit keybinds

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

Detaching from an attached thread's tmux session (the usual tmux detach chord, e.g.
`ctrl+b d`) returns you to the cockpit with a refreshed list.

Threads move through three statuses, derived rather than self-reported: **working** (tmux
session alive, turn in progress) → **waiting** (tmux session alive, turn ended — needs you) →
**closed** (no tmux session). The list orders waiting → working → closed, most-recent first
within each group — ordering is the attention mechanism; there are no desktop notifications.

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
