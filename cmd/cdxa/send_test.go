package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/subthread"
)

// fakeReplier is a cmd/cdxa-local test double for subthread.Replier. It
// records every (threadID, msg) pair and returns the canned err.
type fakeReplier struct {
	calls []struct {
		ThreadID string
		Msg      string
	}
	err error
}

func (f *fakeReplier) QuickReply(threadID, msg string) error {
	f.calls = append(f.calls, struct {
		ThreadID string
		Msg      string
	}{ThreadID: threadID, Msg: msg})
	return f.err
}

// fakeReplierDeps returns a deps whose replier factory returns the supplied
// fake, so runSend exercises the genuine Send call (turn derivation, gone
// check, delivery) rather than a mocked Send. Mirrors fakeSpawnerDeps.
func fakeReplierDeps(t *testing.T, replier *fakeReplier, state fakeState) deps {
	t.Helper()
	return deps{
		codexHome: "/codex",
		statePath: "/state.json",
		state:     state,
		live:      func(string) bool { return true },
		replier:   func(_, _ string) subthread.Replier { return replier },
	}
}

func TestRunSend_PrintsStartedTurnAsJSON(t *testing.T) {
	replier := &fakeReplier{}
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{
			"/r/t1.jsonl": {Completed: []codexstate.Turn{
				{Number: 1, Message: "first"},
				{Number: 2, Message: "second"},
			}},
		},
	}
	d := fakeReplierDeps(t, replier, state)

	out := captureStdout(t, func() {
		code, err := runSend([]string{"t1", "follow up"}, d)
		if err != nil || code != exitDone {
			t.Fatalf("expected exit 0, got code=%d err=%v", code, err)
		}
	})
	var got struct {
		Turn int `json:"turn"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if got.Turn != 3 {
		t.Errorf("turn = %d, want 3 (two completed → next is turn 3)", got.Turn)
	}
	if len(replier.calls) != 1 {
		t.Fatalf("expected exactly one QuickReply call, got %d", len(replier.calls))
	}
	if replier.calls[0].ThreadID != "t1" {
		t.Errorf("QuickReply threadID = %q, want t1", replier.calls[0].ThreadID)
	}
	if replier.calls[0].Msg != "follow up" {
		t.Errorf("QuickReply msg = %q, want %q", replier.calls[0].Msg, "follow up")
	}
}

func TestRunSend_UnknownThread_Exit3(t *testing.T) {
	replier := &fakeReplier{}
	d := fakeReplierDeps(t, replier, fakeState{})

	out := captureStdout(t, func() {
		code, err := runSend([]string{"nope", "msg"}, d)
		if err != nil {
			t.Fatalf("expected nil err for gone (code carries it), got %v", err)
		}
		if code != exitGone {
			t.Errorf("exit code = %d, want %d (gone)", code, exitGone)
		}
	})
	if strings.Contains(out, "turn") {
		t.Errorf("gone response should not contain turn: %q", out)
	}
	if len(replier.calls) != 0 {
		t.Errorf("expected zero QuickReply calls for unknown thread, got %d", len(replier.calls))
	}
}

func TestRunSend_DeadSession_Exit3(t *testing.T) {
	replier := &fakeReplier{}
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1}}}},
	}
	d := deps{
		codexHome: "/codex",
		statePath: "/state.json",
		state:     state,
		live:      func(string) bool { return false }, // dead session
		replier:   func(_, _ string) subthread.Replier { return replier },
	}

	out := captureStdout(t, func() {
		code, err := runSend([]string{"t1", "msg"}, d)
		if err != nil {
			t.Fatalf("expected nil err for gone, got %v", err)
		}
		if code != exitGone {
			t.Errorf("exit code = %d, want %d", code, exitGone)
		}
	})
	if strings.Contains(out, "turn") {
		t.Errorf("dead-session response should not contain turn: %q", out)
	}
	if len(replier.calls) != 0 {
		t.Errorf("expected zero QuickReply calls for dead session, got %d", len(replier.calls))
	}
}

func TestRunSend_OperationalFailure_Exit1JSONError(t *testing.T) {
	replier := &fakeReplier{}
	state := fakeState{findErr: errors.New("sqlite: disk I/O error")}
	d := fakeReplierDeps(t, replier, state)

	out := captureStdout(t, func() {
		code, err := runSend([]string{"t1", "msg"}, d)
		printError(err) // simulate run's mapping
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if !strings.Contains(got.Error, "sqlite: disk I/O error") {
		t.Errorf("error = %q, want it to contain %q", got.Error, "sqlite: disk I/O error")
	}
}

func TestRunSend_ReplierFailure_Exit1JSONError(t *testing.T) {
	replier := &fakeReplier{err: errors.New("tmux boom")}
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{"/r/t1.jsonl": {Completed: []codexstate.Turn{{Number: 1}}}},
	}
	d := fakeReplierDeps(t, replier, state)

	out := captureStdout(t, func() {
		code, err := runSend([]string{"t1", "msg"}, d)
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if !strings.Contains(got.Error, "tmux boom") {
		t.Errorf("error = %q, want it to contain %q", got.Error, "tmux boom")
	}
}

func TestRunSend_MissingArgs_Exit1UsageError(t *testing.T) {
	replier := &fakeReplier{}
	d := fakeReplierDeps(t, replier, fakeState{})

	out := captureStdout(t, func() {
		code, err := runSend([]string{"t1"}, d) // only thread-id, no message
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	if !strings.Contains(out, "usage") {
		t.Errorf("stdout = %q, want it to contain %q", out, "usage")
	}
}

func TestRunSend_EmptyMessage_Exit1UsageError(t *testing.T) {
	replier := &fakeReplier{}
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
	}
	d := fakeReplierDeps(t, replier, state)

	out := captureStdout(t, func() {
		code, err := runSend([]string{"t1", ""}, d)
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	if !strings.Contains(out, "empty") {
		t.Errorf("stdout = %q, want it to contain %q", out, "empty")
	}
}

func TestRunSend_TooManyArgs_Exit1UsageError(t *testing.T) {
	replier := &fakeReplier{}
	d := fakeReplierDeps(t, replier, fakeState{})

	out := captureStdout(t, func() {
		code, err := runSend([]string{"t1", "msg", "extra"}, d)
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	if !strings.Contains(out, "usage") {
		t.Errorf("stdout = %q, want it to contain %q", out, "usage")
	}
}

func TestRunSend_ZeroCompletedTurns_StartedTurn1(t *testing.T) {
	replier := &fakeReplier{}
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{"/r/t1.jsonl": {Completed: nil, InProgress: true}},
	}
	d := fakeReplierDeps(t, replier, state)

	out := captureStdout(t, func() {
		code, err := runSend([]string{"t1", "msg"}, d)
		if err != nil || code != exitDone {
			t.Fatalf("expected exit 0, got code=%d err=%v", code, err)
		}
	})
	var got struct {
		Turn int `json:"turn"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if got.Turn != 1 {
		t.Errorf("turn = %d, want 1 (no completed turns → first turn)", got.Turn)
	}
}

func TestRunSend_OneCompletedOneInProgress_StartedTurn2(t *testing.T) {
	replier := &fakeReplier{}
	state := fakeState{
		threads: map[string]codexstate.Thread{
			"t1": {ID: "t1", RolloutPath: "/r/t1.jsonl"},
		},
		turns: map[string]codexstate.Turns{"/r/t1.jsonl": {
			Completed:  []codexstate.Turn{{Number: 1, Message: "done"}},
			InProgress: true,
		}},
	}
	d := fakeReplierDeps(t, replier, state)

	out := captureStdout(t, func() {
		code, err := runSend([]string{"t1", "msg"}, d)
		if err != nil || code != exitDone {
			t.Fatalf("expected exit 0, got code=%d err=%v", code, err)
		}
	})
	var got struct {
		Turn int `json:"turn"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	if got.Turn != 2 {
		t.Errorf("turn = %d, want 2 (one completed + one in flight)", got.Turn)
	}
}
