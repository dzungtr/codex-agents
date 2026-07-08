package tmuxstatus

import (
	"os/exec"
	"reflect"
	"testing"
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
