package tmuxstatus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewSessionArgs(t *testing.T) {
	got := NewSessionArgs("cxa-abcd1234", "/repo/.worktrees/fix-auth-hook", []string{"codex", "-p", "general-agentic", "do the thing"})
	want := []string{"new-session", "-d", "-s", "cxa-abcd1234", "-c", "/repo/.worktrees/fix-auth-hook", "codex", "-p", "general-agentic", "do the thing"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewSessionArgs() = %v, want %v", got, want)
	}
}

func TestAttachArgs(t *testing.T) {
	got := AttachArgs("cxa-abcd1234")
	want := []string{"attach-session", "-t", "cxa-abcd1234"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AttachArgs() = %v, want %v", got, want)
	}
}

func TestSwitchClientArgs(t *testing.T) {
	got := SwitchClientArgs("cxa-abcd1234")
	want := []string{"switch-client", "-t", "cxa-abcd1234"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SwitchClientArgs() = %v, want %v", got, want)
	}
}

func TestSendKeysArgs(t *testing.T) {
	got := SendKeysArgs("cxa-abcd1234", "proceed with option B")
	want := []string{"send-keys", "-t", "cxa-abcd1234", "-l", "--", "proceed with option B"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SendKeysArgs() = %v, want %v", got, want)
	}
}

func TestSendKeysArgs_TextThatLooksLikeAKeyNameStaysLiteral(t *testing.T) {
	// -l plus the -- separator is what keeps a reply like "C-c" or "Enter"
	// from ever being interpreted as a tmux key name.
	got := SendKeysArgs("cxa-abcd1234", "C-c")
	want := []string{"send-keys", "-t", "cxa-abcd1234", "-l", "--", "C-c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SendKeysArgs() = %v, want %v", got, want)
	}
}

func TestSendEnterArgs(t *testing.T) {
	got := SendEnterArgs("cxa-abcd1234")
	want := []string{"send-keys", "-t", "cxa-abcd1234", "Enter"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SendEnterArgs() = %v, want %v", got, want)
	}
}

func TestInterruptArgs(t *testing.T) {
	got := InterruptArgs("cxa-abcd1234")
	want := []string{"send-keys", "-t", "cxa-abcd1234", "C-c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InterruptArgs() = %v, want %v", got, want)
	}
}

func TestKillSessionArgs(t *testing.T) {
	got := KillSessionArgs("cxa-abcd1234")
	want := []string{"kill-session", "-t", "cxa-abcd1234"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("KillSessionArgs() = %v, want %v", got, want)
	}
}

func TestRemainOnExitArgs(t *testing.T) {
	got := RemainOnExitArgs()
	want := []string{"set-option", "-g", "remain-on-exit", "on"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RemainOnExitArgs() = %v, want %v", got, want)
	}
}
func TestMouseOnArgs(t *testing.T) {
	got := MouseOnArgs()
	want := []string{"set-option", "-g", "mouse", "on"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MouseOnArgs() = %v, want %v", got, want)
	}
}

func TestChainArgs(t *testing.T) {
	got := ChainArgs(
		RemainOnExitArgs(),
		NewSessionArgs("cxa-abcd1234", "/repo", []string{"codex", "do it"}),
	)
	want := []string{
		"set-option", "-g", "remain-on-exit", "on",
		";",
		"new-session", "-d", "-s", "cxa-abcd1234", "-c", "/repo", "codex", "do it",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChainArgs() = %v, want %v", got, want)
	}
}

func TestChainArgs_SingleGroupNoSeparator(t *testing.T) {
	got := ChainArgs(KillSessionArgs("cxa-abcd1234"))
	want := []string{"kill-session", "-t", "cxa-abcd1234"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChainArgs() = %v, want %v", got, want)
	}
}

func TestInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	if InsideTmux() {
		t.Errorf("InsideTmux() = true with TMUX unset, want false")
	}
	t.Setenv("TMUX", "/tmp/tmux-501/default,1234,0")
	if !InsideTmux() {
		t.Errorf("InsideTmux() = false with TMUX set, want true")
	}
}

// fakeRunner records the args it was called with instead of shelling out,
// so ExecRunner-consuming code can be tested without a real tmux binary.
type fakeRunner struct {
	calls [][]string
	err   error
}

func (f *fakeRunner) Run(args []string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	return f.err
}

func TestFakeRunner_RecordsCalls(t *testing.T) {
	var r Runner = &fakeRunner{}
	if err := r.Run([]string{"new-session", "-d"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestExecRunner_RealTmux exercises ExecRunner against a real tmux server:
// start a detached session, confirm it's alive via ListLiveSessions, then
// kill it. Skips gracefully when tmux isn't installed in this environment
// (per the PRD's testing decisions).
func TestExecRunner_RealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in this environment")
	}

	const session = "cxa-testexecrunner"
	runner := ExecRunner{}

	// Best-effort cleanup in case a previous run left this session behind.
	_ = runner.Run([]string{"kill-session", "-t", session})

	if err := runner.Run(NewSessionArgs(session, ".", []string{"sleep", "30"})); err != nil {
		t.Fatalf("start detached session: %v", err)
	}
	defer runner.Run([]string{"kill-session", "-t", session})

	live, err := ListLiveSessions()
	if err != nil {
		t.Fatalf("ListLiveSessions: %v", err)
	}
	found := false
	for _, s := range live {
		if s == session {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %q among live sessions, got %v", session, live)
	}
}

// TestQuickReply_RealTmux_DeliversTextAndSubmits exercises the actual
// send-keys delivery path issue #6's quick-reply feature relies on, against
// a real tmux server: a detached session reads one line from its pane and
// writes it verbatim to a file, standing in for a real codex composer since
// no real codex binary is available in this environment. This is the
// closest this suite gets to the PRD's "manual verify checklist against
// real codex" for the cheap-path send-keys mechanism. Skips gracefully when
// tmux isn't installed.
func TestQuickReply_RealTmux_DeliversTextAndSubmits(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in this environment")
	}

	const session = "cxa-testquickreply"
	runner := ExecRunner{}
	_ = runner.Run([]string{"kill-session", "-t", session})

	outFile := filepath.Join(t.TempDir(), "out.txt")
	shCmd := fmt.Sprintf("read line; printf '%%s' \"$line\" > %s", outFile)
	if err := runner.Run(NewSessionArgs(session, ".", []string{"sh", "-c", shCmd})); err != nil {
		t.Fatalf("start detached session: %v", err)
	}
	defer runner.Run([]string{"kill-session", "-t", session})

	// Includes a string that looks like a tmux key name (C-c) to confirm -l
	// keeps it literal instead of tmux interpreting it as a keypress.
	text := "proceed with option B (C-c should stay literal)"
	if err := runner.Run(SendKeysArgs(session, text)); err != nil {
		t.Fatalf("send text: %v", err)
	}
	if err := runner.Run(SendEnterArgs(session)); err != nil {
		t.Fatalf("send enter: %v", err)
	}

	var got []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ = os.ReadFile(outFile)
		if len(got) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(got) != text {
		t.Fatalf("delivered text = %q, want %q (send-keys delivery unreliable)", got, text)
	}
}

// TestInspectPane_RealTmux_AliveThenDead exercises the exact race this
// package's remain-on-exit/InspectPane pair exists to catch: a pane whose
// command exits almost immediately. Without remain-on-exit set first,
// tmux would tear the session down the instant the command exits, and
// list-panes below would simply fail with "session not found" instead of
// reporting a dead pane. Skips gracefully when tmux isn't installed.
func TestInspectPane_RealTmux_AliveThenDead(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in this environment")
	}

	const session = "cxa-testinspectpane"
	runner := ExecRunner{}
	_ = runner.Run([]string{"kill-session", "-t", session})

	// exit 7 after printing something, so we can assert both the exit code
	// and that captured output carries diagnostic content. remain-on-exit
	// must be chained into the same invocation as new-session (see
	// RemainOnExitArgs's doc comment) rather than set beforehand.
	chained := ChainArgs(RemainOnExitArgs(), NewSessionArgs(session, ".", []string{"sh", "-c", "echo boom; exit 7"}))
	if err := runner.Run(chained); err != nil {
		t.Fatalf("start detached session: %v", err)
	}
	defer runner.Run([]string{"kill-session", "-t", session})

	var state PaneState
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		state, err = InspectPane(session)
		if err != nil {
			t.Fatalf("InspectPane: %v", err)
		}
		if state.Dead {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !state.Dead {
		t.Fatalf("expected pane to be reported dead within the deadline, got %+v", state)
	}
	if state.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", state.ExitCode)
	}
	if !strings.Contains(state.Output, "boom") {
		t.Errorf("Output = %q, want it to contain %q", state.Output, "boom")
	}
}

// TestInspectPane_RealTmux_AliveCommandReportsNotDead confirms InspectPane
// doesn't false-positive a still-running command as dead.
func TestInspectPane_RealTmux_AliveCommandReportsNotDead(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in this environment")
	}

	const session = "cxa-testinspectpanealive"
	runner := ExecRunner{}
	_ = runner.Run([]string{"kill-session", "-t", session})
	chained := ChainArgs(RemainOnExitArgs(), NewSessionArgs(session, ".", []string{"sleep", "5"}))
	if err := runner.Run(chained); err != nil {
		t.Fatalf("start detached session: %v", err)
	}
	defer runner.Run([]string{"kill-session", "-t", session})

	state, err := InspectPane(session)
	if err != nil {
		t.Fatalf("InspectPane: %v", err)
	}
	if state.Dead {
		t.Fatalf("expected a still-running command to report Dead=false, got %+v", state)
	}
}
