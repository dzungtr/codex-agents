package codexserver

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner is a processRunner that returns test-controlled pipes
// instead of spawning a real `codex app-server`. The test writes
// server-side responses/notifications on `srvW` and reads client
// requests on `cliR`. release() is a no-op (the pipes are real io.Pipes
// the test owns; no process to reap).
type fakeRunner struct {
	stdin    *io.PipeWriter
	stdout   *io.PipeReader
	requests chan capturedRequest
}

func (f *fakeRunner) Start(codexHome string) (io.WriteCloser, io.ReadCloser, func() error, error) {
	// pipe1 carries client -> test: the manager's Client writes requests
	// to wIn; runFakeServer reads them from rIn and dispatches them
	// (f.requests for tests to inspect, plus an auto-ack so the manager's
	// synchronous Request() / Subscribe() calls unblock).
	// pipe2 carries test -> client: the test writes responses/notifications
	// to wOut, the manager's Client reads them from rOut (passed back as
	// the runner's stdout).
	rIn, wIn := io.Pipe()
	rOut, wOut := io.Pipe()
	f.stdin = wOut
	f.stdout = rIn
	f.requests = make(chan capturedRequest, 32)
	go f.runFakeServer()
	// release closes the test-side pipe ends so runFakeServer unblocks
	// and any pending client write sees EOF. The real execProcessRunner
	// closes its own pipes after reaping the child; the fake has no
	// process so closing the test-owned ends is the symmetric move.
	release := func() error {
		_ = rIn.Close()
		_ = wOut.Close()
		return nil
	}
	return wIn, rOut, release, nil
}

// runFakeServer is the goroutine that drives the manager's io.Pipes
// from the server side. It reads each request the manager's Client
// writes, copies it to f.requests for tests to inspect, and auto-acks
// requests that carry an id so the manager's synchronous Start() and
// Subscribe() calls unblock. The auto-ack response is the minimal
// valid JSON-RPC success result — the manager only decodes the
// initialize result into a map it never inspects and ignores the
// result on every other request. Notifications (id == nil) are
// forwarded to f.requests but no response is written.
func (f *fakeRunner) runFakeServer() {
	defer close(f.requests)
	scan := bufio.NewScanner(f.stdout)
	scan.Buffer(make([]byte, 0, 4096), jsonRPCMaxLine)
	for scan.Scan() {
		raw := scan.Text()
		var env envelope
		_ = json.Unmarshal([]byte(raw), &env)
		f.requests <- capturedRequest{Raw: raw, Env: env}
		if env.ID == nil {
			continue
		}
		resp := envelope{JSONRPC: "2.0", ID: env.ID, Result: json.RawMessage(`{}`)}
		data, _ := json.Marshal(resp)
		if _, err := f.stdin.Write(append(data, '\n')); err != nil {
			// Pipe closed mid-test (release called); let the
			// goroutine exit instead of looping on a dead writer.
			return
		}
	}
}

// readClientRequest reads one newline-delimited JSON-RPC request the
// manager wrote to the client-side stdin pipe. It returns the parsed
// envelope and the raw line (so tests can inspect method/id).
type capturedRequest struct {
	Raw  string
	Env  envelope
}

func (f *fakeRunner) nextRequest(t *testing.T, timeout time.Duration) capturedRequest {
	t.Helper()
	select {
	case r, ok := <-f.requests:
		if !ok {
			t.Fatalf("request stream closed before next request arrived")
			return capturedRequest{}
		}
		return r
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for client request after %v", timeout)
		return capturedRequest{}
	}
}

// writeResponse writes a JSON-RPC response to the client-side stdout
// pipe, matching the request's id.
func (f *fakeRunner) writeResponse(t *testing.T, id *json.RawMessage, result any) {
	t.Helper()
	resp := envelope{JSONRPC: "2.0", ID: id, Result: mustMarshal(t, result)}
	data, _ := json.Marshal(resp)
	f.stdin.Write(append(data, '\n'))
}

// writeNotification writes a JSON-RPC notification (no id) to the
// client-side stdout pipe — the server pushing an event to the client.
func (f *fakeRunner) writeNotification(t *testing.T, method string, params any) {
	t.Helper()
	env := envelope{JSONRPC: "2.0", Method: method, Params: mustMarshal(t, params)}
	data, _ := json.Marshal(env)
	f.stdin.Write(append(data, '\n'))
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestManager_Start_DegradesGracefullyWhenRunnerFails(t *testing.T) {
	// A runner that always errors → Start should return an error
	// AND the manager should accept Subscribe calls as no-ops.
	mgr := newManager("/nonexistent", failingRunner{err: io.ErrUnexpectedEOF}, 0, time.Now)
	if err := mgr.Start(); err == nil {
		t.Fatal("Start returned nil error for a failing runner")
	}
	// Subscribe/Unsubscribe must not panic and must return nil
	// (the ADR 0002 degraded-mode contract).
	if err := mgr.Subscribe("thread-1"); err != nil {
		t.Errorf("Subscribe on degraded manager: %v, want nil", err)
	}
	if err := mgr.Unsubscribe("thread-1"); err != nil {
		t.Errorf("Unsubscribe on degraded manager: %v, want nil", err)
	}
	// Events channel must close on Stop so consumers don't leak.
	mgr.Stop()
	if _, ok := <-mgr.Events(); ok {
		t.Error("Events channel should be closed after Stop on a degraded manager")
	}
}

func TestManager_Start_SendsInitializeAndInitialized(t *testing.T) {
	runner := &fakeRunner{}
	mgr := newManager("/codex", runner, 0, time.Now)
	defer mgr.Stop()

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Manager should send an `initialize` request first.
	req := runner.nextRequest(t, time.Second)
	if req.Env.Method != "initialize" {
		t.Errorf("first request method = %q, want initialize", req.Env.Method)
	}

	// Reply with a result so the manager proceeds to send `initialized`.

	// Next request should be the `initialized` notification (no id).
	req = runner.nextRequest(t, time.Second)
	if req.Env.Method != "initialized" {
		t.Errorf("second request method = %q, want initialized", req.Env.Method)
	}
	if req.Env.ID != nil {
		t.Errorf("initialized should be a notification (no id), got id=%v", *req.Env.ID)
	}
}

func TestManager_Subscribe_ThreadsGoToThreadResume(t *testing.T) {
	runner := &fakeRunner{}
	mgr := newManager("/codex", runner, 0, time.Now)
	defer mgr.Stop()
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Drain the initialize/initialized handshake.
	runner.nextRequest(t, time.Second)
	runner.nextRequest(t, time.Second) // initialized notification — no id to echo

	if err := mgr.Subscribe("thread-abc"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	req := runner.nextRequest(t, time.Second)
	if req.Env.Method != "thread/resume" {
		t.Errorf("Subscribe method = %q, want thread/resume", req.Env.Method)
	}
	if !strings.Contains(req.Raw, `"threadId":"thread-abc"`) {
		t.Errorf("Subscribe params missing threadId: %q", req.Raw)
	}
	// Ack so the manager can mark the thread as subscribed.
}

func TestManager_ItemCompleted_AgentMessageIncrementsCount(t *testing.T) {
	runner := &fakeRunner{}
	mgr := newManager("/codex", runner, 0, time.Now)
	defer mgr.Stop()
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runner.nextRequest(t, time.Second)              // initialize
	runner.nextRequest(t, time.Second)              // initialized

	// One agentMessage completion → Event{MessageCount: 1}.
	runner.writeNotification(t, "item/completed", map[string]any{
		"threadId": "t-1",
		"item":     map[string]any{"id": "i1", "type": "agentMessage", "text": "hi"},
		"turnId":   "turn-1",
	})

	ev := expectEvent(t, mgr.Events(), time.Second)
	if ev.ThreadID != "t-1" {
		t.Errorf("event ThreadID = %q, want t-1", ev.ThreadID)
	}
	if ev.Kind != EventMessageCount || ev.MessageCount != 1 {
		t.Errorf("event = %+v, want EventMessageCount with MessageCount=1", ev)
	}
	// TokenCount must carry the -1 "no change" sentinel on a
	// message-count event so the bubbletea UI's in-place patch
	// (which skips fields == -1) doesn't clobber a known token
	// total with the Go zero value 0.
	if ev.TokenCount != -1 {
		t.Errorf("event TokenCount = %d, want -1 (no change)", ev.TokenCount)
	}

	// A non-agentMessage item (reasoning) must NOT increment the count.
	runner.writeNotification(t, "item/completed", map[string]any{
		"threadId": "t-1",
		"item":     map[string]any{"id": "i2", "type": "reasoning", "content": []any{}, "summary": []any{}},
	})
	// Drain the optional token-usage event we don't get here. Wait
	// for the NEXT event with a short timeout — there shouldn't be one.
	select {
	case ev := <-mgr.Events():
		t.Fatalf("unexpected event after a reasoning item: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}

	// A second agentMessage → count goes to 2.
	runner.writeNotification(t, "item/completed", map[string]any{
		"threadId": "t-1",
		"item":     map[string]any{"id": "i3", "type": "agentMessage", "text": "again"},
	})
	ev = expectEvent(t, mgr.Events(), time.Second)
	if ev.MessageCount != 2 {
		t.Errorf("second agent message: MessageCount = %d, want 2", ev.MessageCount)
	}
}

func TestManager_TokenUsageUpdated_EmitsEventTokenCount(t *testing.T) {
	runner := &fakeRunner{}
	mgr := newManager("/codex", runner, 0, time.Now)
	defer mgr.Stop()
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runner.nextRequest(t, time.Second)
	runner.nextRequest(t, time.Second)

	runner.writeNotification(t, "thread/tokenUsage/updated", map[string]any{
		"threadId": "t-1",
		"turnId":   "turn-1",
		"tokenUsage": map[string]any{
			"last":              map[string]any{"totalTokens": 1234, "inputTokens": 100, "outputTokens": 200, "cachedInputTokens": 0, "reasoningOutputTokens": 0},
			"total":             map[string]any{"totalTokens": 9999, "inputTokens": 0, "outputTokens": 0, "cachedInputTokens": 0, "reasoningOutputTokens": 0},
			"modelContextWindow": 200000,
		},
	})
	ev := expectEvent(t, mgr.Events(), time.Second)
	if ev.Kind != EventTokenCount || ev.TokenCount != 1234 {
		t.Errorf("event = %+v, want EventTokenCount with TokenCount=1234", ev)
	}
	// MessageCount must carry the -1 sentinel on a token-usage
	// event so a token-only update doesn't zero out the thread's
	// known message count in the UI.
	if ev.MessageCount != -1 {
		t.Errorf("event MessageCount = %d, want -1 (no change)", ev.MessageCount)
	}
}

func TestManager_Throttle_DropsEventsWithinWindow(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	runner := &fakeRunner{}
	mgr := newManager("/codex", runner, 500*time.Millisecond, clock.now)
	defer mgr.Stop()
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runner.nextRequest(t, time.Second)
	runner.nextRequest(t, time.Second)

	// First event passes the throttle (no prior emit for this thread).
	runner.writeNotification(t, "thread/tokenUsage/updated", map[string]any{
		"threadId":   "t-1",
		"turnId":     "turn-1",
		"tokenUsage": map[string]any{"last": map[string]any{"totalTokens": 10}, "total": map[string]any{"totalTokens": 10}},
	})
	expectEvent(t, mgr.Events(), time.Second)

	// 100ms later, another event for the same thread — within the
	// 500ms throttle window — must be dropped.
	clock.advance(100 * time.Millisecond)
	runner.writeNotification(t, "thread/tokenUsage/updated", map[string]any{
		"threadId":   "t-1",
		"turnId":     "turn-1",
		"tokenUsage": map[string]any{"last": map[string]any{"totalTokens": 20}, "total": map[string]any{"totalTokens": 20}},
	})
	select {
	case ev := <-mgr.Events():
		t.Fatalf("throttled event leaked through: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}

	// After 500ms the next event for the same thread should be
	// emitted (the manager has the latest token count cached).
	clock.advance(450 * time.Millisecond)
	runner.writeNotification(t, "thread/tokenUsage/updated", map[string]any{
		"threadId":   "t-1",
		"turnId":     "turn-1",
		"tokenUsage": map[string]any{"last": map[string]any{"totalTokens": 30}, "total": map[string]any{"totalTokens": 30}},
	})
	ev := expectEvent(t, mgr.Events(), time.Second)
	if ev.TokenCount != 30 {
		t.Errorf("post-throttle event TokenCount = %d, want 30", ev.TokenCount)
	}
}

func TestManager_Stop_ClosesEventsChannel(t *testing.T) {
	runner := &fakeRunner{}
	mgr := newManager("/codex", runner, 0, time.Now)
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runner.nextRequest(t, time.Second)
	runner.nextRequest(t, time.Second)

	mgr.Stop()
	// The events channel should be closed and draining should not block.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range mgr.Events() {
		}
	}()
	wg.Wait()
}

// expectEvent blocks up to timeout for an Event on ch, failing the
// test if none arrives. It returns the first event seen.
func expectEvent(t *testing.T, ch <-chan Event, timeout time.Duration) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("Events channel closed unexpectedly")
		}
		return ev
	case <-time.After(timeout):
		t.Fatalf("no event received within %v", timeout)
		return Event{}
	}
}

// failingRunner always returns its configured error from Start. Used
// to exercise the "server unavailable" degraded-mode path without
// having to actually take down a real codex binary.
type failingRunner struct{ err error }

func (f failingRunner) Start(string) (io.WriteCloser, io.ReadCloser, func() error, error) {
	return nil, nil, nil, f.err
}

// fakeClock is a deterministic time source for throttle tests. The
// mutex is required: the manager's readLoop goroutine calls
// (*fakeClock).now() while the test goroutine writes to f.t between
// emissions, and -race catches the unprotected read/write.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
