package main

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/subthread"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// fakeShutdownSessions is the cmd/cdxa-local SessionStopper test double.
// It records the thread ids Stop was called with and returns the canned
// err. Mirrors fakeReplier / fakeSpawnerDeps used by runSend / runSpawn so
// runShutdown's argv/JSON/exit mapping is exercisable without a real tmux
// server or subthread package internals.
type fakeShutdownSessions struct {
	calls []string
	err   error
}

func (f *fakeShutdownSessions) Stop(threadID string) error {
	f.calls = append(f.calls, threadID)
	return f.err
}

// fakeShutdownArchiver is the cmd/cdxa-local CodexArchiver test double.
// Same pattern as fakeShutdownSessions: records Archive calls and returns
// the canned err.
type fakeShutdownArchiver struct {
	calls []string
	err   error
}

func (f *fakeShutdownArchiver) Archive(threadID string) error {
	f.calls = append(f.calls, threadID)
	return f.err
}

// fakeShutdownDeps returns a deps whose shutdownSessions / shutdownArchiver
// factories return the supplied fakes, so runShutdown exercises the genuine
// subthread.Shutdown call (completion gate, teardown ordering) rather than
// a mocked Shutdown. Mirrors fakeReplierDeps used by runSend.
func fakeShutdownDeps(_ *testing.T, sessions *fakeShutdownSessions, archiver *fakeShutdownArchiver, state fakeState, live func(string) bool) deps {
	return deps{
		codexHome: "/codex",
		statePath: "/state.json",
		state:     state,
		live:      live,
		shutdownSessions: func() subthread.SessionStopper { return sessions },
		shutdownArchiver: func() subthread.CodexArchiver { return archiver },
	}
}

// shutdownCase is one row of the end-to-end exit-code table test: a
// scripted (state, live, sessions, archiver) tuple, the expected exit
// code, and the JSON object that should land on stdout. Each case
// exercises one arm of the ADR 0007 exit-code contract.
type shutdownCase struct {
	name          string
	args          []string
	state         fakeState
	live          func(string) bool
	sessionsErr   error
	archiverErr   error
	wantCode      int
	wantStatus    string
	wantThreadID  string
	wantStopCalls int    // how many times SessionStopper.Stop should be called
	wantArcCalls  int    // how many times CodexArchiver.Archive should be called
	wantErrSubs   string // substring expected in {"error":...} stdout when wantCode == 1
}

func TestRunShutdown_ExitCodeTable(t *testing.T) {
	cases := []shutdownCase{
		{
			name: "completed turn + live session → archived (exit 0)",
			args: []string{"t1"},
			state: fakeState{
				threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
				turns: map[string]codexstate.Turns{
					"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "done"}}},
				},
			},
			live:          func(string) bool { return true },
			wantCode:      exitDone,
			wantStatus:    "archived",
			wantThreadID:  "t1",
			wantStopCalls: 1,
			wantArcCalls:  1,
		},
		{
			name: "completed turn + already-gone session → archived (exit 0)",
			args: []string{"t1"},
			state: fakeState{
				threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
				turns: map[string]codexstate.Turns{
					"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "done"}}},
				},
			},
			live:          func(string) bool { return false },
			wantCode:      exitDone,
			wantStatus:    "archived",
			wantThreadID:  "t1",
			wantStopCalls: 1,
			wantArcCalls:  1,
		},
		{
			name: "turn in progress → working (exit 2, no mutation)",
			args: []string{"t1"},
			state: fakeState{
				threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
				turns: map[string]codexstate.Turns{
					"/r/t1.jsonl": {
						Completed:  []codexstate.Turn{{Number: 1, Message: "wip"}},
						InProgress: true,
					},
				},
			},
			live:          func(string) bool { return true },
			wantCode:      exitWorking,
			wantStatus:    "working",
			wantThreadID:  "t1",
			wantStopCalls: 0,
			wantArcCalls:  0,
		},
		{
			name: "no turn + live session → working (exit 2, no mutation)",
			args: []string{"t1"},
			state: fakeState{
				threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
				turns: map[string]codexstate.Turns{
					"/r/t1.jsonl": {Completed: nil, InProgress: true},
				},
			},
			live:          func(string) bool { return true },
			wantCode:      exitWorking,
			wantStatus:    "working",
			wantThreadID:  "t1",
			wantStopCalls: 0,
			wantArcCalls:  0,
		},
		{
			name: "no turn + dead session → gone (exit 3, no mutation)",
			args: []string{"t1"},
			state: fakeState{
				threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
				turns: map[string]codexstate.Turns{
					"/r/t1.jsonl": {Completed: nil, InProgress: true},
				},
			},
			live:          func(string) bool { return false },
			wantCode:      exitGone,
			wantStatus:    "gone",
			wantThreadID:  "t1",
			wantStopCalls: 0,
			wantArcCalls:  0,
		},
		{
			name:          "unknown thread id → gone (exit 3, no mutation)",
			args:          []string{"nope"},
			state:         fakeState{},
			live:          func(string) bool { return true },
			wantCode:      exitGone,
			wantStatus:    "gone",
			wantThreadID:  "nope",
			wantStopCalls: 0,
			wantArcCalls:  0,
		},
		{
			name: "tmux failure → operational (exit 1, no archive attempted)",
			args: []string{"t1"},
			state: fakeState{
				threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
				turns: map[string]codexstate.Turns{
					"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "done"}}},
				},
			},
			live:          func(string) bool { return true },
			sessionsErr:   errors.New("tmux: server unreachable"),
			wantCode:      exitOperErr,
			wantStopCalls: 1,
			wantArcCalls:  0,
			wantErrSubs:   "tmux: server unreachable",
		},
		{
			name: "codex archive failure → operational (exit 1, after tmux stop)",
			args: []string{"t1"},
			state: fakeState{
				threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
				turns: map[string]codexstate.Turns{
					"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "done"}}},
				},
			},
			live:          func(string) bool { return true },
			archiverErr:   errors.New("codex archive failed: no such thread"),
			wantCode:      exitOperErr,
			wantStopCalls: 1,
			wantArcCalls:  1,
			wantErrSubs:   "codex archive failed: no such thread",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessions := &fakeShutdownSessions{err: tc.sessionsErr}
			archiver := &fakeShutdownArchiver{err: tc.archiverErr}
			d := fakeShutdownDeps(t, sessions, archiver, tc.state, tc.live)

			out := captureStdout(t, func() {
				code, err := runShutdown(tc.args, d)
				if err != nil {
					printError(err) // simulate run's mapping
				}
				if code != tc.wantCode {
					t.Errorf("exit code = %d, want %d", code, tc.wantCode)
				}
			})

			if tc.wantErrSubs != "" {
				var obj struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal([]byte(out), &obj); err != nil {
					t.Fatalf("stdout not valid JSON: %v (got %q)", err, out)
				}
				if !strings.Contains(obj.Error, tc.wantErrSubs) {
					t.Errorf("error = %q, want it to contain %q", obj.Error, tc.wantErrSubs)
				}
			} else {
				var obj struct {
					Status   string `json:"status"`
					ThreadID string `json:"thread_id"`
				}
				if err := json.Unmarshal([]byte(out), &obj); err != nil {
					t.Fatalf("stdout not valid JSON: %v (got %q)", err, out)
				}
				if obj.Status != tc.wantStatus {
					t.Errorf("status = %q, want %q", obj.Status, tc.wantStatus)
				}
				if obj.ThreadID != tc.wantThreadID {
					t.Errorf("thread_id = %q, want %q", obj.ThreadID, tc.wantThreadID)
				}
			}

			if len(sessions.calls) != tc.wantStopCalls {
				t.Errorf("sessions.Stop calls = %d, want %d (calls=%v)",
					len(sessions.calls), tc.wantStopCalls, sessions.calls)
			}
			if len(archiver.calls) != tc.wantArcCalls {
				t.Errorf("archiver.Archive calls = %d, want %d (calls=%v)",
					len(archiver.calls), tc.wantArcCalls, archiver.calls)
			}
		})
	}
}

func TestRunShutdown_NoArgs_Exit1UsageError(t *testing.T) {
	sessions := &fakeShutdownSessions{}
	archiver := &fakeShutdownArchiver{}
	d := fakeShutdownDeps(t, sessions, archiver, fakeState{}, func(string) bool { return false })

	out := captureStdout(t, func() {
		code, err := runShutdown([]string{}, d)
		if err != nil {
			printError(err)
		}
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d (usage)", code, exitOperErr)
		}
	})
	if !strings.Contains(out, "usage") {
		t.Errorf("stdout = %q, want it to contain %q", out, "usage")
	}
	if len(sessions.calls) != 0 || len(archiver.calls) != 0 {
		t.Errorf("usage path must not touch tmux/codex; got Stop=%v Archive=%v",
			sessions.calls, archiver.calls)
	}
}

func TestRunShutdown_EmptyArg_Exit1UsageError(t *testing.T) {
	// Empty thread id is treated as a usage error so a stray
	// `cdxa shutdown ""` cannot silently no-op into gone (exit 3) and
	// mask the parent's bug. Same posture as runSend's empty-message check.
	sessions := &fakeShutdownSessions{}
	archiver := &fakeShutdownArchiver{}
	d := fakeShutdownDeps(t, sessions, archiver, fakeState{}, func(string) bool { return false })

	out := captureStdout(t, func() {
		code, err := runShutdown([]string{""}, d)
		if err != nil {
			printError(err)
		}
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d (empty arg)", code, exitOperErr)
		}
	})
	if !strings.Contains(out, "usage") {
		t.Errorf("stdout = %q, want it to contain %q", out, "usage")
	}
}

func TestRunShutdown_TooManyArgs_Exit1UsageError(t *testing.T) {
	sessions := &fakeShutdownSessions{}
	archiver := &fakeShutdownArchiver{}
	d := fakeShutdownDeps(t, sessions, archiver, fakeState{}, func(string) bool { return false })

	out := captureStdout(t, func() {
		code, err := runShutdown([]string{"t1", "extra"}, d)
		if err != nil {
			printError(err)
		}
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	if !strings.Contains(out, "usage") {
		t.Errorf("stdout = %q, want it to contain %q", out, "usage")
	}
}

func TestRunShutdown_UnknownSubcommandStillMapped(t *testing.T) {
	// Sanity check that exitCodeForShutdown is a pure function: each
	// ShutdownStatus maps to its ADR 0007 exit code without going through
	// runShutdown. Covers the same table as the dispatch test but as a
	// pure-function test, matching exitCodeFor's TestExitCodeFor_Table.
	tests := []struct {
		s    subthread.ShutdownStatus
		want int
	}{
		{subthread.ShutdownArchived, exitDone},
		{subthread.ShutdownWorking, exitWorking},
		{subthread.ShutdownGone, exitGone},
		{subthread.ShutdownStatus(99), exitOperErr},
	}
	for _, tt := range tests {
		if got := exitCodeForShutdown(tt.s); got != tt.want {
			t.Errorf("exitCodeForShutdown(%s) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

func TestRunShutdown_RealTmuxStopsDisposableSession(t *testing.T) {
	// End-to-end smoke against a real tmux server: create a disposable
	// cxa-<id> session, invoke runShutdown against a thread whose rollout
	// has a completed turn, and assert the session is gone afterwards.
	// Matches the PRD's testing decision to skip cleanly when tmux is
	// unavailable (mirrors internal/tmuxstatus's TestExecRunner_RealTmux).
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in this environment")
	}

	const threadID = "shutdown-rd"
	session := tmuxstatus.SessionName(threadID)
	runner := tmuxstatus.ExecRunner{}
	_ = runner.Run(tmuxstatus.KillSessionArgs(session))
	if err := runner.Run(tmuxstatus.NewSessionArgs(session, ".", []string{"sleep", "30"})); err != nil {
		t.Fatalf("start disposable session: %v", err)
	}
	t.Cleanup(func() { runner.Run(tmuxstatus.KillSessionArgs(session)) })

	sessions := subthread.DefaultSessionStopper()
	archiver := &fakeShutdownArchiver{} // skip real codex archive
	state := fakeState{
		threads: map[string]codexstate.Thread{threadID: {ID: threadID, RolloutPath: "/r/" + threadID + ".jsonl"}},
		turns: map[string]codexstate.Turns{
			"/r/" + threadID + ".jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "ok"}}},
		},
	}
	d := deps{
		codexHome: "/codex",
		statePath: "/state.json",
		state:     state,
		live:      func(string) bool { return true },
		shutdownSessions: func() subthread.SessionStopper { return sessions },
		shutdownArchiver: func() subthread.CodexArchiver { return archiver },
	}

	out := captureStdout(t, func() {
		code, err := runShutdown([]string{threadID}, d)
		if err != nil {
			printError(err)
		}
		if code != exitDone {
			t.Errorf("exit code = %d, want %d (archived)", code, exitDone)
		}
	})
	if !strings.Contains(out, `"status":"archived"`) {
		t.Errorf("stdout = %q, want it to contain status:archived", out)
	}

	// Verify the tmux session is gone.
	live, err := tmuxstatus.ListLiveSessions()
	if err != nil {
		t.Fatalf("ListLiveSessions: %v", err)
	}
	for _, name := range live {
		if name == session {
			t.Errorf("expected session %q to be gone, still listed in %v", session, live)
		}
	}
}
