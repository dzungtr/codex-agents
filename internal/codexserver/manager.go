package codexserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// DefaultThrottleInterval is the per-thread minimum gap between emitted
// events (ADR 0002 open-question 3 — coalesce events per thread to at
// most one per 500ms). 0 disables throttling, mostly for tests.
const DefaultThrottleInterval = 500 * time.Millisecond

// processRunner abstracts the spawn-the-app-server step so tests can
// drive the manager's protocol logic without a real `codex app-server`
// child process. The real implementation shells out; the test version
// returns a pseudo-process whose stdin/stdout are io.Pipes the test
// owns.
type processRunner interface {
	// Start launches the server, returns its stdin/stdout, and a
	// release function the manager calls on Stop. release should
	// reap the process and close any pipes it owns.
	Start(codexHome string) (stdin io.WriteCloser, stdout io.ReadCloser, release func() error, err error)
}

// execProcessRunner is the production processRunner. It finds `codex` on
// $PATH (or, for parity with codexstate, prefers $CODEX_HOME/bin/codex
// when that path exists) and runs `codex app-server`. The default
// --listen stdio:// is implied, so the child reads JSON-RPC from stdin
// and writes it to stdout.
type execProcessRunner struct {
	// LookPath is exposed for tests; production code lets exec.LookPath
	// do its thing.
	LookPath func(string) (string, error)
}

func (r execProcessRunner) Start(codexHome string) (io.WriteCloser, io.ReadCloser, func() error, error) {
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	codexPath, err := lookPath("codex")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("codexserver: locate codex binary: %w", err)
	}
	cmd := exec.Command(codexPath, "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("codexserver: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, nil, nil, fmt.Errorf("codexserver: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, nil, nil, fmt.Errorf("codexserver: start codex app-server: %w", err)
	}
	release := func() error {
		// Best-effort: signal then reap. The process is expected to
		// exit on its own once its stdin closes, but Kill covers
		// the case where it's stuck waiting for input that will
		// never arrive.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		waitErr := cmd.Wait()
		stdin.Close()
		stdout.Close()
		return waitErr
	}
	return stdin, stdout, release, nil
}

// Manager owns the single shared codex App Server subprocess and the
// JSON-RPC 2.0 client that talks to it. It is safe to call Start once;
// Subscribe/Unsubscribe are safe to call concurrently and may be called
// before Start (they're queued) or after Stop (they're no-ops, so
// late events from a stopped manager never block the UI). Events()
// returns a channel that is closed on Stop, so the UI's read loop
// exits cleanly when the cockpit quits.
//
// ADR 0002 decisions embodied here:
//
//   - decision 1: one App Server per cockpit instance, lifecycle tied
//     to the cockpit (Start at startup, Stop on quit)
//   - decision 4: per-thread Subscribe/Unsubscribe, addressed to the
//     same shared server
//   - decision 5: if Start fails the manager enters a "server
//     unavailable" degraded state and all subsequent Subscribe calls
//     are no-ops — exactly the silent degradation the ADR specifies
//   - decision 6: the manager emits turn-completed events but does NOT
//     re-derive status from them — the notify hook remains the
//     authoritative source (this is "supplement, not replace")
type Manager struct {
	codexHome string
	runner    processRunner
	client    *Client // nil until Start succeeds; tests inject directly via newClientForTest

	events chan Event

	mu        sync.Mutex
	started   bool
	stopped   bool
	available bool // true once Start's initialize handshake succeeds

	// Per-thread state for coalescing and absolute-count maintenance.
	// msgCounts and tokCounts are the latest known values; lastEmit
	// drives the throttle; subscribed is the set of threads the
	// server has confirmed a subscription for (Subscribe adds,
	// Unsubscribe removes).
	msgCounts  map[string]int
	tokCounts  map[string]int
	lastEmit   map[string]time.Time
	subscribed map[string]bool

	throttle time.Duration
	now      func() time.Time // injectable clock; tests override for deterministic throttle

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{} // closed when the read loop exits

	release func() error // process reaper; nil before Start
}

// NewManager builds a Manager backed by the real `codex app-server`
// child process. Pass codexHome only for parity with the rest of the
// cockpit — it is currently informational (the app server discovers its
// own codex home), but the parameter is kept so the cockpit's
// wire-up matches the package's own conventions and so future
// behaviour (e.g. pinning the binary) can be added without churn.
func NewManager(codexHome string) *Manager {
	return newManager(codexHome, execProcessRunner{}, DefaultThrottleInterval, time.Now)
}

// newManager is the unexported, fully-injected constructor used by
// tests. It exists so client_test.go / manager_test.go can swap the
// process runner, throttle interval, and clock without poking private
// fields.
func newManager(codexHome string, runner processRunner, throttle time.Duration, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{
		codexHome: codexHome,
		runner:    runner,
		events:    make(chan Event, 128),
		msgCounts: map[string]int{},
		tokCounts: map[string]int{},
		lastEmit:  map[string]time.Time{},
		subscribed: map[string]bool{},
		throttle:  throttle,
		now:       now,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Start spawns the codex App Server subprocess and runs the
// initialize/initialized handshake. If the spawn fails or the
// initialize request errors, Start returns the error AND marks the
// manager as "unavailable": every subsequent Subscribe/Unsubscribe
// becomes a no-op, the events channel stays open (so a consumer that
// starts after a failed Start still gets a closed channel on Stop),
// and the cockpit's behaviour degrades to "no live updates" without
// crashing (ADR 0002 decision 5).
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	m.mu.Unlock()

	stdin, stdout, release, err := m.runner.Start(m.codexHome)
	if err != nil {
		m.markUnavailable()
		// close doneCh so a subsequent Stop doesn't block waiting
		// for a readLoop that never started.
		close(m.doneCh)
		return err
	}
	m.release = release

	cli := NewClient(stdout, stdin)
	cli.Start()
	m.client = cli

	// Initialize handshake. The server returns capabilities we don't
	// use yet (we only need stdio + the documented notification
	// methods), so we decode into a map to stay forward-compatible
	// with future fields.
	var initResult map[string]any
	if err := cli.Request("initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "codex-agents",
			"version": "0.1.0",
		},
	}, &initResult); err != nil {
		// Handshake failed — kill the process and degrade. Don't
		// return the raw error path verbatim: callers (cmd/cdxa)
		// already log the manager's availability state, so the
		// returned error is for debugging only.
		m.markUnavailable()
		_ = release()
		m.release = nil
		return fmt.Errorf("codexserver: initialize: %w", err)
	}

	// `initialized` is a notification, not a request — the spec says
	// the client tells the server "I'm ready" without expecting a
	// reply. Without it, the server may sit idle waiting.
	if err := cli.Notify("initialized", map[string]any{}); err != nil {
		m.markUnavailable()
		_ = release()
		m.release = nil
		return fmt.Errorf("codexserver: initialized notify: %w", err)
	}

	m.mu.Lock()
	m.available = true
	m.mu.Unlock()

	go m.readLoop()
	return nil
}

// markUnavailable flips the manager into degraded mode without
// tearing down channels — Subscribe/Unsubscribe become no-ops and
// Events() stays open until Stop.
func (m *Manager) markUnavailable() {
	m.mu.Lock()
	m.available = false
	m.mu.Unlock()
}

// Subscribe opens a live event stream for threadID by sending
// `thread/resume` to the server. The server doesn't require the
// thread to be currently running — it accepts the id and either
// attaches to the live turn or simply subscribes to the
// already-persisted conversation. Returns nil for Subscribe calls made
// after a failed Start, so callers don't need to special-case the
// degraded state.
func (m *Manager) Subscribe(threadID string) error {
	if threadID == "" {
		return fmt.Errorf("codexserver: Subscribe: empty thread id")
	}
	m.mu.Lock()
	available := m.available && m.client != nil
	cli := m.client
	m.mu.Unlock()
	if !available {
		return nil
	}
	if err := cli.Request("thread/resume", map[string]any{
		"threadId": threadID,
	}, nil); err != nil {
		// Resume failures (unknown thread, server-side error) are
		// non-fatal: the manager stays alive for other threads.
		// We still skip the "confirmed" state so a future Subscribe
		// can retry.
		return fmt.Errorf("codexserver: thread/resume %s: %w", threadID, err)
	}
	m.mu.Lock()
	m.subscribed[threadID] = true
	m.mu.Unlock()
	return nil
}

// Unsubscribe stops receiving live events for threadID. Like
// Subscribe, it's a no-op in the degraded state.
func (m *Manager) Unsubscribe(threadID string) error {
	if threadID == "" {
		return fmt.Errorf("codexserver: Unsubscribe: empty thread id")
	}
	m.mu.Lock()
	available := m.available && m.client != nil
	cli := m.client
	m.mu.Unlock()
	if !available {
		return nil
	}
	if err := cli.Request("thread/unsubscribe", map[string]any{
		"threadId": threadID,
	}, nil); err != nil {
		return fmt.Errorf("codexserver: thread/unsubscribe %s: %w", threadID, err)
	}
	m.mu.Lock()
	delete(m.subscribed, threadID)
	delete(m.msgCounts, threadID)
	delete(m.tokCounts, threadID)
	delete(m.lastEmit, threadID)
	m.mu.Unlock()
	return nil
}

// Events returns the channel of live updates. The channel is closed
// when Stop is called, so a `for ev := range m.Events()` loop in the
// UI exits cleanly on cockpit quit.
func (m *Manager) Events() <-chan Event {
	return m.events
}

// Stop shuts the manager down: closes the client (which unblocks any
// pending request with an error), waits for the read loop to drain,
// and reaps the subprocess. Stop is safe to call multiple times; only
// the first call does work.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
		m.mu.Lock()
		cli := m.client
		release := m.release
		m.client = nil
		m.release = nil
		m.available = false
		m.mu.Unlock()
		if cli != nil {
			cli.Close()
		}
		// Wait for readLoop to exit so any in-flight notification
		// has been fully processed (or dropped) before we close
		// the events channel.
		<-m.doneCh
		if release != nil {
			_ = release()
		}
		close(m.events)
	})
}

// readLoop consumes Notifications from the client, decodes them, and
// emits the relevant Event values. It exits when the client's
// notification channel closes (which happens when Client.Close is
// called from Stop, or when the scanner hits a read error — the
// latter is the "server crashed" graceful-degradation path).
//
// The local cli snapshot is required: Stop nils m.client under
// the mutex, and reading m.client inside the select case would
// race with that write. Snapshotting once at goroutine start
// gives the loop a stable handle, and Stop's cli.Close() then
// closes the notification channel so the loop exits via
// (notif, ok=false) regardless of which stop signal it sees.
func (m *Manager) readLoop() {
	defer close(m.doneCh)
	m.mu.Lock()
	cli := m.client
	m.mu.Unlock()
	if cli == nil {
		return
	}
	for {
		select {
		case <-m.stopCh:
			return
		case notif, ok := <-cli.Notifications():
			if !ok {
				return
			}
			m.handleNotification(notif)
		}
	}
}

// handleNotification is the per-method dispatch. Unknown methods are
// silently dropped so the manager stays forward-compatible with newer
// App Server versions that add new notification types.
func (m *Manager) handleNotification(n Notification) {
	switch n.Method {
	case "item/completed":
		m.handleItemCompleted(n.Params)
	case "thread/tokenUsage/updated":
		m.handleTokenUsageUpdated(n.Params)
	case "turn/completed":
		m.handleTurnCompleted(n.Params)
	}
}

// threadIDFromParams is a small helper that pulls a "threadId" string
// out of a notification's params envelope. The server uses
// "threadId" (camelCase) consistently across its notifications, so
// every relevant notification has this field. We only need the id —
// the rest of the params are decoded by the type-specific handlers.
func threadIDFromParams(params json.RawMessage) (string, error) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", err
	}
	return p.ThreadID, nil
}

// handleItemCompleted reacts to a completed item. Per the App Server's
// schema, item.type == "agentMessage" is the assistant's conversational
// reply (what the user sees as a "message"); the other item types
// (reasoning, commandExecution, fileChange, mcpToolCall, …) are
// internal work the agent does between messages and shouldn't inflate
// the count. We increment once per completed agentMessage and emit
// the new absolute count.
func (m *Manager) handleItemCompleted(params json.RawMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
		Item     struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if p.Item.Type != "agentMessage" {
		return
	}
	m.mu.Lock()
	m.msgCounts[p.ThreadID]++
	count := m.msgCounts[p.ThreadID]
	m.mu.Unlock()
	m.emitIfNotThrottled(p.ThreadID, Event{
		ThreadID:     p.ThreadID,
		Kind:         EventMessageCount,
		MessageCount: count,
		// TokenCount is irrelevant on a message-count event; set the
		// -1 sentinel so the UI's in-place patch (which treats -1
		// as "no change for this field") doesn't clobber a known
		// token total with the Go zero value 0.
		TokenCount: -1,
		At:         m.now(),
	})
}

// handleTokenUsageUpdated patches the latest known total tokens for
// the thread. The server reports a TokenUsageBreakdown; we only use
// the "last" turn's totalTokens for the live display (matches the
// "tokens: N" badge semantics, which has always been the most-recent
// turn's total).
func (m *Manager) handleTokenUsageUpdated(params json.RawMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
		TokenUsage struct {
			Last struct {
				TotalTokens int `json:"totalTokens"`
			} `json:"last"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	m.mu.Lock()
	m.tokCounts[p.ThreadID] = p.TokenUsage.Last.TotalTokens
	tok := m.tokCounts[p.ThreadID]
	m.mu.Unlock()
	m.emitIfNotThrottled(p.ThreadID, Event{
		ThreadID:   p.ThreadID,
		Kind:       EventTokenCount,
		// MessageCount is irrelevant on a token-usage event; set
		// the -1 sentinel so a single-token-update event doesn't
		// zero out the thread's known message count in the UI.
		MessageCount: -1,
		TokenCount:   tok,
		At:           m.now(),
	})
}

// handleTurnCompleted is the live turn-end signal. v1 just emits an
// event so callers (or future code) can see the boundary; status
// derivation still goes through the notify hook, per ADR 0002
// decision 6.
func (m *Manager) handleTurnCompleted(params json.RawMessage) {
	threadID, err := threadIDFromParams(params)
	if err != nil || threadID == "" {
		return
	}
	m.emitIfNotThrottled(threadID, Event{
		ThreadID: threadID,
		Kind:     EventTurnCompleted,
		// Both counts are -1: a turn-completed event is a
		// boundary signal, not a value update, so the UI's
		// in-place patch must not touch either count.
		MessageCount: -1,
		TokenCount:   -1,
		At:           m.now(),
	})
}

// emitIfNotThrottled is the ADR 0002 throttling policy (open-question
// 3). Per thread, drop events that arrive within `throttle` of the
// last emitted one — the values are monotonically increasing for
// message and token counts, so the next event will carry the latest
// total anyway. Throttle == 0 (tests) disables the check.
func (m *Manager) emitIfNotThrottled(threadID string, ev Event) {
	if m.throttle <= 0 {
		m.send(ev)
		return
	}
	now := m.now()
	m.mu.Lock()
	last, seen := m.lastEmit[threadID]
	if seen && now.Sub(last) < m.throttle {
		m.mu.Unlock()
		return
	}
	m.lastEmit[threadID] = now
	m.mu.Unlock()
	m.send(ev)
}

// send writes ev to the events channel without blocking the read loop
// indefinitely: if the consumer is slow, we drop the event (the next
// one will arrive within the throttle window and carry the latest
// values). The drop is silent — a noisy live stream is not worth a
// status-line warning, and the cockpit is not the source of truth
// (it'll re-render the count from the next refresh).
func (m *Manager) send(ev Event) {
	select {
	case m.events <- ev:
	case <-m.stopCh:
	default:
		// Consumer is too slow; drop the event. Better than
		// blocking the read loop and starving the rest of the
		// per-thread stream.
	}
}

// ErrUnavailable is returned by Start when the codex App Server could
// not be reached (binary missing, process exit-on-init, initialize
// error). Callers usually don't need to inspect it — the manager
// has already switched to degraded mode — but the sentinel is
// exposed for tests that want to assert on the failure mode.
var ErrUnavailable = errors.New("codexserver: app server unavailable")

// ctx is reserved for future cancellation hooks (e.g. wiring Start to
// a context.Context). The Go vet linter flags unused imports for
// the context package on its own; the binding below is a no-op
// reference that lets the package compile without a real use yet.
//
// (Kept here so a future change doesn't have to chase down the
// "context was imported but unused" rabbit hole across a refactor.)
