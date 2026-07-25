# ADR 0006: Shell-wrapped codex launch

- Status: accepted
- Date: 2026-07-25
- Updates: ADR 0001 decisions 3 and 4

## Context

When `cdxa` launches a codex thread inside a tmux pane, codex runs as the
pane's foreground process directly — `codex -p <profile> [-c model=…]
[-c notify=[…]] "<task>"`. Codex inherits only whatever environment the
tmux server has, which is often minimal. The tmux server's environment is
set when the server starts (typically from the first client that created
it) and is not refreshed on subsequent client connections; when tmux runs
as a systemd service the environment can be close to bare. The user's
PATH, aliases, and exported variables from their shell rc files
(`~/.profile`, `~/.bash_profile`, `~/.zprofile`, etc.) are not loaded.
This can cause codex to miss tools, PATH entries, or env vars the user
expects to be available — the most common symptom is codex reporting that
a tool the user uses interactively is "not found."

The fix is structural, not per-launch: wrap the codex invocation in a
login shell so the environment is loaded once, then `exec` into codex so
the process tree and tmux pane ownership are unchanged.

## Decisions

### 1. Codex is launched inside a login shell by default

The default invocation is now:

```
<shell> -lc 'exec codex -p <profile> [-c model=…] [-c notify=[…]] "<task>"'
```

The shell sources its rc files via `-l` (login), then `exec` replaces the
shell process with codex. Process tree, signal behaviour, and tmux pane
ownership are identical to the bare-codex path — codex is still the pane's
foreground process — only the environment is loaded first.

### 2. `CDXA_SHELL` controls the shell

| `CDXA_SHELL` | Behaviour |
|---|---|
| unset | `sh` (wrapping **on** — the default) |
| explicitly empty (`""`) | wrapping **off**, bare `codex` (current behaviour) |
| any other value (e.g. `bash`, `zsh`) | that shell wraps codex |

`cmd/cdxa/main.go` reads it via `os.LookupEnv`:

```go
shell, ok := os.LookupEnv("CDXA_SHELL")
if !ok {
    shell = "sh"
}
```

`ok == false` (unset) → `"sh"` (wrapping on); explicitly set to `""` →
`""` (wrapping off). This makes the common case (the user does nothing)
inherit the environment, while preserving an escape hatch for users who
hit a shell-specific issue.

### 3. `exec` replaces the shell, not nests it

`sh -lc 'exec codex …'` rather than `sh -lc 'codex …'`:

- **Process tree**: after `exec`, the shell is gone; codex is PID 1 of
  the pane. `tmux list-panes` shows codex, not `sh`.
- **Signals**: tmux's `send-keys` and signal delivery target codex
  directly — no shell in the middle to swallow or reinterpret them.
- **Exit / `remain-on-exit`**: when codex exits, the pane's foreground
  process is gone; `remain-on-exit` keeps the pane exactly as it does for
  bare codex. The shell does not linger and does not produce an extra
  exit event.
- **`verifyAlive`**: `sh -lc 'exec codex …'` exits with codex's exit code
  if codex fails; the pane's alive/gone status is derived from codex's
  process, not the shell's.

### 4. Manual single-quote escaping, no external dependency

The wrapped command string is built by hand:

- Each arg is wrapped in single quotes: `'arg'`.
- Internal single quotes are escaped as `'\'''` (close, escaped quote,
  reopen).
- All quoted args are joined with spaces.
- `exec ` is prepended to the joined string.

Example: `["codex", "-p", "profile", "fix user's bug"]` →
`["sh", "-lc", "exec codex '-p' 'profile' 'fix user'\\''s bug'"]`.

This is ~10 lines of code and avoids pulling in a shell-quoting library
for a transformation that is fully determined by POSIX single-quote
semantics. Special characters (`$`, backticks, `;`, `"`, `|`, `!`, `{`,
`}`, spaces, tabs) are all safe inside single quotes — only the single
quote itself needs escaping.

### 5. Transparency to existing mechanisms

The wrapper is transparent to every mechanism that observes the pane or
codex's process:

- **`verifyAlive`**: `sh -lc 'exec codex …'` exits with codex's exit code;
  `remain-on-exit` keeps the pane. No change to alive/gone derivation.
- **Notify hook**: `-c notify=[…]` is a codex flag *inside* the wrapped
  command. The hook fires from within codex. No change.
- **`send-keys` (QuickReply/Send)**: types into codex's TUI, which owns
  the pane's stdin after `exec`. No change.
- **`tmuxstatus.SessionName`**: the session is still named `cxa-<prefix>`;
  the wrapper only changes what runs inside it. No change.

### 6. Where the logic lives

A pure function `WrapWithShell(shell string, args []string) []string` in
`internal/codexlaunch/invocation.go`:

- `shell == ""` → return `args` unchanged (no wrapping).
- Otherwise → return `["<shell>", "-lc", "<command string>"]` where
  `<command string>` is the args joined with shell-safe single-quote
  escaping, prepended with `exec `.

`Launcher.Launch` and `Launcher.Resume` call
`WrapWithShell(l.Shell, codexArgs)` on the result of `NewThreadArgs` /
`ResumeArgs` before passing to `tmuxstatus.NewSessionArgs`. A `Shell
string` field is added to `Launcher`; `cmd/cdxa/main.go` populates it from
`CDXA_SHELL` with the defaulting above.

## Alternatives considered

- **Bare codex (status quo).** No environment inheritance — the problem
  this decision solves. Kept as the `CDXA_SHELL=""` escape hatch, not the
  default.
- **`"$@"` positional-parameter pattern** — `sh -lc 'codex "$@"' sh
  -p <profile> …`. Avoids quoting but is unusual (the leading `sh` argv0
  is a POSIX idiom most readers don't recognise) and fragile with
  non-POSIX shells. Rejected for clarity.
- **External shell-quoting library.** Unnecessary dependency for ~10
  lines of fully-determined POSIX single-quote escaping. Rejected.

## Relationship to ADR 0001

This ADR updates the invocation documented in:

- **ADR 0001 decision 3 (tmux-per-thread process model)** — the session
  still runs codex's TUI; the foreground process is still codex (after
  `exec`). The launch form is now shell-wrapped.
- **ADR 0001 decision 4 (worktree-per-thread launch semantics)** — the
  invocation block changes from
  `tmux new-session -d -s cxa-… -c <worktree> codex -p <profile> …` to
  the shell-wrapped form, with `CDXA_SHELL` controlling the shell
  (default `sh`).

No other ADR 0001 decision is affected: the worktree layout, profile
default, thread-identity contract, and status-derivation model are
unchanged.

## Consequences

- Cockpit-launched codex threads inherit the user's login-shell
  environment (PATH, aliases, exports) by default.
- Users who hit a shell-specific incompatibility set `CDXA_SHELL=""` to
  revert to bare codex; users who want a specific shell set
  `CDXA_SHELL=zsh` (etc.).
- The launch command string is longer and shell-quoted; it is built by
  `WrapWithShell`, not by hand at call sites.
- The `notify-hook` wrapper and `verifyAlive` are unaffected (decision 5).

## Measured results

- **`WrapWithShell` contract landed (PR #98).** The function lives in
  `internal/codexlaunch/invocation.go` as a pure
  `func(shell string, args []string) []string`. `shell == ""` returns
  args unchanged; otherwise it returns `["<shell>", "-lc", "exec <quoted args>"]`.
  Manual single-quote escaping, no external dependency. 5 unit tests cover
  the disabled, default-sh, single-quote-in-arg, special-chars, and
  custom-shell cases.
- **Two launcher integration tests** pin the wired behaviour: one asserts
  `Shell: "sh"` produces a shell-wrapped tmux arg; the other asserts
  `Shell: ""` (the zero value, matching every pre-existing launcher test)
  still passes bare `codex` to tmux. The pre-#97 behaviour is the
  regression guard, so future refactors can't silently re-introduce
  wrapping for users who don't want it.
- **All existing tests pass unchanged.** Every pre-existing launcher test
  leaves `Shell` at its zero value, so the bare-codex assertions they
  already made remain accurate. Zero churn to existing test fixtures.
- **`CDXA_SHELL` env var read once in `main.go`.** `os.LookupEnv` cleanly
  distinguishes unset (→ `sh`) from explicitly empty (→ `""` / disabled).
  The shell value is threaded through `deps` → every `Launcher{}`
  construction site (cockpit, spawn, send).
