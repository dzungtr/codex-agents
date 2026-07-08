package tmuxstatus

import (
	"os"
	"os/exec"
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
