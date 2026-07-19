package tmuxstatus

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// NewSessionArgs builds the argument list for `tmux new-session -d -s
// <session> -c <workdir> <command...>`: a detached session running command
// with its working directory set to workdir. This is pure tmux knowledge —
// callers (internal/codexlaunch) supply the command line, which is where
// codex-specific invocation knowledge (profile/model flags) lives.
func NewSessionArgs(session, workdir string, command []string) []string {
	args := []string{"new-session", "-d", "-s", session, "-c", workdir}
	return append(args, command...)
}

// AttachArgs builds the argument list for `tmux attach-session -t <session>`,
// used when the cockpit itself isn't already running inside tmux.
func AttachArgs(session string) []string {
	return []string{"attach-session", "-t", session}
}

// SwitchClientArgs builds the argument list for `tmux switch-client -t
// <session>`, used instead of attach-session when the cockpit is itself
// already running inside a tmux client (attach-session would nest clients).
func SwitchClientArgs(session string) []string {
	return []string{"switch-client", "-t", session}
}

// InsideTmux reports whether the current process is itself running inside
// a tmux client, per tmux's own convention of setting $TMUX.
func InsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// SendKeysArgs builds the argument list for `tmux send-keys -t <session> -l
// -- <text>`: types text literally into session's active pane (a thread's
// codex composer, for quick-reply) without tmux interpreting any of it as a
// key name. Pair with SendEnterArgs to submit — see that function's doc
// comment for why the two can't be combined into one send-keys call.
func SendKeysArgs(session, text string) []string {
	return []string{"send-keys", "-t", session, "-l", "--", text}
}

// SendEnterArgs builds the argument list for `tmux send-keys -t <session>
// Enter`: presses the Enter key in session's active pane. This must be a
// separate call from SendKeysArgs: tmux's `-l` (literal) flag disables
// key-name lookup for every argument on that command line, so a trailing
// "Enter" passed alongside literal text would be typed as the word "Enter"
// rather than pressed as a key.
func SendEnterArgs(session string) []string {
	return []string{"send-keys", "-t", session, "Enter"}
}

// InterruptArgs builds the argument list for `tmux send-keys -t <session>
// C-c`: the interrupt sequence used by the Interrupt (`x`) list action
// (PRD #1's List behavior -> Interrupt row) to stop a thread's current turn
// without killing its session. Whether that transition actually lands as
// StatusWaiting is decided by whoever calls this (cmd/codex-agents) in
// concert with agentstate's last-turn-event bookkeeping — this function is
// pure tmux-argument-building knowledge, same layering as NewSessionArgs.
func InterruptArgs(session string) []string {
	return []string{"send-keys", "-t", session, "C-c"}
}

// KillSessionArgs builds the argument list for `tmux kill-session -t
// <session>`, used by the Archive (`a`) list action to hard-stop a thread's
// tmux session. Unlike Interrupt, which only stops the in-progress turn,
// this ends the session outright — archiving is the only action allowed to
// do that (PRD #1's List behavior -> Interrupt row: "No hard-kill outside
// archive").
func KillSessionArgs(session string) []string {
	return []string{"kill-session", "-t", session}
}

// RenameSessionArgs builds the argument list for `tmux rename-session -t
// <old> <new>`. Launcher.Launch creates a thread tmux session under a
// cockpit-derived name (cxa-<handle>, stable from launch time, since codex
// own thread id is not known until codex registers) and then, once codex
// registers, renames it to cxa-<codexID> (SessionName(codexID)) so every
// downstream consumer (attach, liveness, interrupt, quick-reply, archive)
// that derives the session name from the thread id via SessionName keeps
// resolving to the actual session (PRD #48: thread id is codex id).
func RenameSessionArgs(oldSession, newSession string) []string {
	return []string{"rename-session", "-t", oldSession, newSession}
}

// Runner executes a tmux subcommand given its argument list (as built by
// NewSessionArgs/AttachArgs/SwitchClientArgs). Production code uses
// ExecRunner; tests inject a fake so launch/attach orchestration can be
// exercised without a real tmux server.
type Runner interface {
	Run(args []string) error
}

// ExecRunner shells out to the real tmux binary.
type ExecRunner struct{}

func (ExecRunner) Run(args []string) error {
	cmd := exec.Command("tmux", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RemainOnExitArgs builds the argument list for `tmux set-option -g
// remain-on-exit on`. `tmux new-session -d` reports success as soon as tmux
// itself accepts the command — not once the pane's command has proven it
// stays running. Without remain-on-exit, a pane whose command dies within
// milliseconds (missing binary, bad flags) takes its session down with it
// before anything gets a chance to notice the difference between "still
// starting up" and "already dead".
//
// This must be applied via ChainArgs together with the NewSessionArgs it
// guards, in a single tmux invocation — not as a separate prior Tmux.Run
// call. Two reasons: (1) `set-option -g` on its own requires a tmux server
// to already be running and does not start one itself (unlike
// new-session), so it fails with "no server running" the first time a
// machine launches a thread; (2) tmux's server exits once idle with no
// sessions, so even after a first successful session existed, a bare
// `set-option -g` between two separate invocations isn't guaranteed to
// still find a live server to talk to. Chaining into one invocation
// sidesteps both: new-session starts the server if needed, and the
// set-option is guaranteed to apply before the chained new-session creates
// the pane (and forks its command) — see ChainArgs.
func RemainOnExitArgs() []string {
	return []string{"set-option", "-g", "remain-on-exit", "on"}
}

// MouseOnArgs builds the argument list for tmux options that make mouse
// wheel scrolling inside a cockpit-launched pane scroll the application's
// conversation history instead of cycling input history.
//
// It enables `set-option -g mouse on` so tmux intercepts wheel events,
// then rebinds `WheelUpPane`/`WheelDownPane` in the root key table to send
// `PageUp`/`PageDown` to the pane's foreground process. Codex's TUI maps
// Up/Down arrows to input-history navigation and PageUp/PageDown to
// conversation-history scrolling; tmux's default wheel bindings send
// Up/Down, so without the rebinding a user scrolling inside an attached
// thread's pane would cycle through previous prompts rather than scrolling
// the conversation.
//
// This must be applied via ChainArgs together with the NewSessionArgs it
// guards, in a single tmux invocation — same rationale as RemainOnExitArgs:
// bare `set-option -g` between separate tmux process invocations is not
// guaranteed to find a live server to talk to, whereas chaining after
// new-session starts the server if needed and applies the option before the
// new-session creates the pane.
func MouseOnArgs() []string {
	return []string{
		"set-option", "-g", "mouse", "on",
		";",
		"bind-key", "-T", "root", "WheelUpPane", "send-keys", "-N", "1", "PageUp",
		";",
		"bind-key", "-T", "root", "WheelDownPane", "send-keys", "-N", "1", "PageDown",
	}
}

// wheelForwardFormat is the condition shared by WheelUpArgs and
// WheelDownArgs: forward the wheel event to the pane as a real mouse
// escape sequence (`send-keys -M`) whenever the pane is in copy mode
// (already scrolling the tmux scrollback — keep that working) or in the
// alternate screen (a fullscreen TUI like codex's, which is the case
// the previous MouseOnArgs change was added for and where the default
// 3.5+ wheel bindings otherwise translate wheel events into 3x Up/Down
// arrow keys — which codex (legacy) binds to cycling input history, the
// exact wrong target).
const wheelForwardFormat = "#{||:#{pane_in_mode},#{alternate_on}}"

// WheelUpArgs builds the argument list for `tmux bind-key -n
// WheelUpPane ...`. It overrides tmux's default wheel-up binding so the
// event reaches the pane's foreground process as a real mouse escape
// sequence (via `send-keys -M`) rather than as 3x Up arrow keys
// (tmux 3.5+ default in alt-screen) or as PageUp (older tmux) — both of
// which codex (legacy) maps to cycling the input history instead of
// scrolling the conversation. Outside copy/alt mode the binding falls
// back to `copy-mode -e`, preserving the default behaviour of opening
// the tmux scrollback for the user. Must be chained via ChainArgs
// alongside MouseOnArgs and NewSessionArgs in the same tmux invocation
// (same rationale as RemainOnExitArgs: bare `bind-key -n` between
// separate tmux process invocations is not guaranteed to find a live
// server to talk to, whereas chaining inside the same invocation
// guarantees the binding is in place before the new-session's pane
// forks its foreground process).
func WheelUpArgs() []string {
	return []string{
		"bind-key", "-n", "WheelUpPane",
		"if-shell", "-F", wheelForwardFormat,
		"send-keys -M",
		"copy-mode -e",
	}
}

// WheelDownArgs builds the argument list for `tmux bind-key -n
// WheelDownPane ...`. The mirror of WheelUpArgs: forward as a real
// mouse event in copy/alt mode, and do nothing otherwise (matching
// tmux's default WheelDownPane behaviour, which intentionally never
// opens copy mode). See WheelUpArgs for the rationale on why the
// override is needed and the same single-invocation chaining rule.
func WheelDownArgs() []string {
	return []string{
		"bind-key", "-n", "WheelDownPane",
		"if-shell", "-F", wheelForwardFormat,
		"send-keys -M",
	}
}

// extendedKeysVersionGuard is the tmux format condition shared by the
// extended-keys lines in ModifierKeysArgs: true when the server's version
// is 3.2 or later, i.e. new enough to know the `extended-keys` option
// (added in tmux 3.2) and the `extkeys` terminal feature. The regex matches
// 3.2–3.9, any 4.x–9.x, and any multi-digit major. Guarding with
// `if-shell -F` on the built-in #{version} format keeps the same command
// queue valid on older tmux builds — the unknown option/terminal-feature
// never gets parsed there — instead of failing the whole chained
// invocation (and with it the new-session that launches the thread) just
// because the host tmux predates extended keys.
const extendedKeysVersionGuard = `#{m/r:^(3\.[2-9]|[4-9]\.|[1-9][0-9]+\.),#{version}}`

// ModifierKeysArgs builds the argument list for the tmux options that make
// modified keys (Shift+Enter, Ctrl+Shift+U, Alt+Backspace, ...) reach a
// cockpit-launched pane instead of being dropped by tmux. With tmux's
// defaults, `xterm-keys` is off and no `extkeys` terminal feature is
// advertised, so the outer terminal's xterm-style modifier escape
// sequences arrive unrecognised and tmux swallows them before they ever
// reach codex's TUI — the pane sees neither a newline nor a submit.
//
// Three layers, oldest-safest first:
//   - `set-option -g xterm-keys on` (available since tmux 1.x): baseline
//     decode of xterm-style modified keys into the pane.
//   - `set-option -g extended-keys on` (tmux 3.2+): enables the
//     extended-keys (CSI u / kitty keyboard protocol) path, which is what
//     unambiguously encodes combos like Shift+Enter.
//   - `set-option -as terminal-features ,xterm*:extkeys` (tmux 3.2+):
//     advertises the extkeys capability to panes whose outer TERM matches
//     xterm* so the application knows it may emit/expect extended keys.
//
// The two extended-keys lines run under `if-shell -F` version guards so
// they no-op cleanly on tmux < 3.2 instead of erroring out the whole
// invocation; the xterm-keys baseline is version-safe everywhere.
//
// This must be applied via ChainArgs together with the NewSessionArgs it
// guards, in a single tmux invocation, placed before NewSessionArgs so
// the options are set before the pane forks its command — same rationale
// as RemainOnExitArgs: a bare `set-option -g` between separate tmux
// process invocations is not guaranteed to find a live server to talk to,
// whereas chaining guarantees the option is in place before the
// new-session's pane forks its foreground process.
func ModifierKeysArgs() []string {
	return []string{
		"set-option", "-g", "xterm-keys", "on",
		";",
		"if-shell", "-F", extendedKeysVersionGuard,
		"set-option -g extended-keys on",
		";",
		"if-shell", "-F", extendedKeysVersionGuard,
		"set-option -as terminal-features ,xterm*:extkeys",
	}
}

// ChainArgs joins multiple tmux command argument groups into the argument
// list for a single tmux invocation, using tmux's own ";" command
// separator so they run as one sequential command queue on the same
// server connection rather than as separate `tmux` process invocations.
// See RemainOnExitArgs's doc comment for why that distinction matters here.
func ChainArgs(groups ...[]string) []string {
	var out []string
	for i, g := range groups {
		if i > 0 {
			out = append(out, ";")
		}
		out = append(out, g...)
	}
	return out
}

// PaneState reports whether a tmux pane's foreground command has already
// exited (Dead), and if so, its exit code and the pane's captured output —
// the diagnostic InspectPane needs to tell "new-session -d succeeded and
// the command is still running" apart from "new-session -d succeeded but
// the command immediately died".
type PaneState struct {
	Dead     bool
	ExitCode int
	Output   string
}

// InspectPane reports session's first pane's liveness. It requires the
// session to have been created after RemainOnExitArgs was applied —
// otherwise a dead pane (and its session, if it was the only pane) is torn
// down by tmux before this can observe it, and list-panes below simply
// fails as "no such session".
func InspectPane(session string) (PaneState, error) {
	cmd := exec.Command("tmux", "list-panes", "-t", session, "-F", "#{pane_dead} #{pane_dead_status}")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return PaneState{}, fmt.Errorf("tmux list-panes: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	// A freshly created session can have already spawned more panes than
	// the one we launched (unlikely for cockpit-managed sessions, but
	// list-panes returns one line per pane) — the first line is always the
	// launched command's own pane.
	line := strings.SplitN(strings.TrimSpace(stdout.String()), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return PaneState{}, fmt.Errorf("tmux list-panes: unexpected output %q", stdout.String())
	}

	state := PaneState{Dead: fields[0] == "1"}
	if len(fields) > 1 {
		state.ExitCode, _ = strconv.Atoi(fields[1])
	}
	if state.Dead {
		state.Output = capturePane(session)
	}
	return state, nil
}

// capturePane returns session's first pane's captured content (best
// effort — an empty string if capture-pane itself fails), used to give a
// launch failure some diagnostic teeth beyond a bare exit code.
func capturePane(session string) string {
	// -S - captures from the start of the pane's scrollback history: the
	// visible viewport alone (capture-pane's default) can be mostly blank
	// padding by the time a short-lived command's actual output has
	// scrolled up, leaving only tmux's own "Pane is dead" marker visible.
	cmd := exec.Command("tmux", "capture-pane", "-t", session, "-p", "-S", "-")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}
