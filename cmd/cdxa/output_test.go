package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/subthread"
)

// fakeState is a cmd/cdxa-local test double for subthread.StateProvider,
// mirroring the one in internal/subthread's tests. Each output scenario in
// the table test scripts a (thread, turns, live) triple; this fake returns
// the canned answer so runOutput's exit-code mapping is exercisable without
// sqlite, rollout files, or a real tmux server.
type fakeState struct {
	threads map[string]codexstate.Thread
	turns   map[string]codexstate.Turns
	findErr error
	readErr error
}

func (f fakeState) FindThread(_ string, threadID string) (codexstate.Thread, error) {
	if f.findErr != nil {
		return codexstate.Thread{}, f.findErr
	}
	if th, ok := f.threads[threadID]; ok {
		return th, nil
	}
	return codexstate.Thread{}, codexstate.ErrThreadNotFound
}

func (f fakeState) ReadTurns(rolloutPath string) (codexstate.Turns, error) {
	if f.readErr != nil {
		return codexstate.Turns{}, f.readErr
	}
	if turns, ok := f.turns[rolloutPath]; ok {
		return turns, nil
	}
	return codexstate.Turns{}, nil
}

func TestExitCodeFor_Table(t *testing.T) {
	tests := []struct {
		name string
		s    subthread.Status
		want int
	}{
		{"done maps to 0", subthread.StatusDone, 0},
		{"working maps to 2", subthread.StatusWorking, 2},
		{"gone maps to 3", subthread.StatusGone, 3},
		{"unknown status maps to 1 (operational)", subthread.Status(99), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCodeFor(tt.s); got != tt.want {
				t.Errorf("exitCodeFor(%s) = %d, want %d", tt.s, got, tt.want)
			}
		})
	}
}

// outputCase is one row of the end-to-end exit-code table test: a scripted
// (state, live) pair, the expected exit code, and assertions on the JSON
// stdout. Each case exercises one arm of the ADR 0003 exit-code contract.
type outputCase struct {
	name     string
	args     []string
	state    fakeState
	live     func(string) bool
	wantCode int
	// wantStatus/wantTurn/wantMessage assert the JSON object on stdout. When
	// wantCode == 1 (operational error), stdout is a {"error":...} object
	// instead, asserted via wantErrContains.
	wantStatus      string
	wantTurn        int
	wantMessage     string
	wantErrContains string
}

func TestRunOutput_ExitCodeTable(t *testing.T) {
	cases := []outputCase{
		{
			name:       "completed turn available → exit 0",
			args:       []string{"t-done"},
			state:      fakeState{threads: map[string]codexstate.Thread{"t-done": {ID: "t-done", RolloutPath: "/r/d.jsonl"}}, turns: map[string]codexstate.Turns{"/r/d.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "done result"}}}}},
			live:       func(string) bool { return false },
			wantCode:   0,
			wantStatus: "done", wantTurn: 1, wantMessage: "done result",
		},
		{
			name:       "last turn in progress → exit 2",
			args:       []string{"t-working"},
			state:      fakeState{threads: map[string]codexstate.Thread{"t-working": {ID: "t-working", RolloutPath: "/r/w.jsonl"}}, turns: map[string]codexstate.Turns{"/r/w.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "prev result"}}, InProgress: true}}},
			live:       func(string) bool { return false },
			wantCode:   2,
			wantStatus: "working", wantTurn: 1, wantMessage: "prev result",
		},
		{
			name:       "unknown thread id → exit 3",
			args:       []string{"t-unknown"},
			state:      fakeState{},
			live:       func(string) bool { return true },
			wantCode:   3,
			wantStatus: "gone", wantTurn: 0, wantMessage: "",
		},
		{
			name:       "gone without output (dead session, no turns) → exit 3",
			args:       []string{"t-gone"},
			state:      fakeState{threads: map[string]codexstate.Thread{"t-gone": {ID: "t-gone", RolloutPath: "/r/g.jsonl"}}, turns: map[string]codexstate.Turns{"/r/g.jsonl": {Completed: nil, InProgress: true}}},
			live:       func(string) bool { return false },
			wantCode:   3,
			wantStatus: "gone", wantTurn: 0, wantMessage: "",
		},
		{
			name:            "sqlite unreadable → exit 1 with JSON error",
			args:            []string{"t-sql"},
			state:           fakeState{findErr: errors.New("sqlite: disk I/O error")},
			live:            func(string) bool { return false },
			wantCode:        1,
			wantErrContains: "sqlite: disk I/O error",
		},
		{
			name:            "rollout missing → exit 1 with JSON error",
			args:            []string{"t-missing"},
			state:           fakeState{threads: map[string]codexstate.Thread{"t-missing": {ID: "t-missing", RolloutPath: "/r/m.jsonl"}}, readErr: errors.New("open /r/m.jsonl: no such file")},
			live:            func(string) bool { return false },
			wantCode:        1,
			wantErrContains: "open /r/m.jsonl: no such file",
		},
		{
			name:            "missing thread-id arg → exit 1 usage error",
			args:            []string{},
			state:           fakeState{},
			live:            func(string) bool { return false },
			wantCode:        1,
			wantErrContains: "usage",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := deps{state: tc.state, live: tc.live, codexHome: "/codex"}
			// Capture stdout via a writer swap by calling runOutput directly;
			// runOutput prints to os.Stdout, so we redirect through a pipe.
			// Simpler: runOutput uses fmt.Printf to os.Stdout; for testability
			// we capture via a subprocess-free approach by swapping the global
			// stdout the package uses.
			out := captureStdout(t, func() {
				code, err := runOutput(tc.args, d)
				if err != nil {
					// operational error: run returns the error to main, which
					// prints the JSON error object. Simulate that here.
					printError(err)
					if code != tc.wantCode {
						t.Errorf("exit code = %d, want %d", code, tc.wantCode)
					}
					return
				}
				if code != tc.wantCode {
					t.Errorf("exit code = %d, want %d", code, tc.wantCode)
				}
			})

			if tc.wantCode == exitOperErr {
				if tc.wantErrContains != "" && !strings.Contains(out, tc.wantErrContains) {
					t.Errorf("stdout = %q, want it to contain %q", out, tc.wantErrContains)
				}
				// verify it's a JSON error object
				var obj map[string]any
				if err := json.Unmarshal([]byte(out), &obj); err != nil {
					t.Errorf("stdout not valid JSON: %v (got %q)", err, out)
				}
				return
			}

			// success/working/gone: parse the {"status","turn","message"} object
			var obj map[string]any
			if err := json.Unmarshal([]byte(out), &obj); err != nil {
				t.Fatalf("stdout not valid JSON: %v (got %q)", err, out)
			}
			if got, _ := obj["status"].(string); got != tc.wantStatus {
				t.Errorf("status = %q, want %q", got, tc.wantStatus)
			}
			if got, _ := obj["turn"].(float64); int(got) != tc.wantTurn {
				t.Errorf("turn = %v, want %d", got, tc.wantTurn)
			}
			if got, _ := obj["message"].(string); got != tc.wantMessage {
				t.Errorf("message = %q, want %q", got, tc.wantMessage)
			}
		})
	}
}

func TestRunOutput_IdempotentRepeatPolls(t *testing.T) {
	state := fakeState{
		threads: map[string]codexstate.Thread{"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"}},
		turns:   map[string]codexstate.Turns{"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1, Message: "stable"}}}},
	}
	d := deps{state: state, live: func(string) bool { return false }, codexHome: "/codex"}

	first := captureStdout(t, func() {
		if code, err := runOutput([]string{"t1"}, d); err != nil || code != 0 {
			t.Errorf("first poll: code=%d err=%v", code, err)
		}
	})
	second := captureStdout(t, func() {
		if code, err := runOutput([]string{"t1"}, d); err != nil || code != 0 {
			t.Errorf("second poll: code=%d err=%v", code, err)
		}
	})
	if first != second {
		t.Errorf("repeat polls not idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
}

// captureStdout runs fn while swapping the package-level stdout writer to a
// buffer, returning what was written. runOutput and printError emit to stdout
// (not os.Stdout directly), so this swap captures their output for assertion
// without a subprocess or os.Stdout file-descriptor redirect.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	orig := stdout
	stdout = &buf
	defer func() { stdout = orig }()
	fn()
	return buf.String()
}
