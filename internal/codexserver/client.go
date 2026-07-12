package codexserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// envelope is the on-the-wire shape of every JSON-RPC 2.0 message the
// codex App Server speaks: a request, a response, or a notification.
// JSON-RPC doesn't separate the three in a typed envelope, so we use a
// single struct with optional fields and dispatch by which fields are
// present. id is required for request/response and absent for a
// notification; method is required for request/notification and absent
// for a response; result is set on a success response, error on a
// failure response.
type envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *jsonRPCError    `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("jsonrpc: code %d: %s", e.Code, e.Message)
}

// Notification is an incoming JSON-RPC notification from the App Server
// (the server-to-client `method`+`params` shape, no id). Client code
// that wants a specific event switches on Method; unknown methods are
// simply dropped by the manager (ADR 0002 forward-compatibility note).
type Notification struct {
	Method string
	Params json.RawMessage
}

// Client is a JSON-RPC 2.0 client over a byte stream (one JSON object
// per line). It is deliberately small: the manager wraps it with the
// process lifecycle, and tests can drive it with io.Pipe so no real
// codex process is needed to exercise the protocol. Client.Start must
// be called once before any Request/Notify; Close stops the read loop
// and releases the pending-request map (any in-flight requests then
// error rather than blocking forever).
type Client struct {
	reader  io.Reader
	writer  io.Writer
	scan    *bufio.Scanner
	writeMu sync.Mutex // serialises Request/Notify writes — JSON-RPC lines must not interleave

	nextID  atomic.Int64
	pending sync.Map // map[string]chan *envelope (string key = JSON-encoded id)

	notif chan Notification
	closeOnce sync.Once
	closed   chan struct{}
}

// jsonRPCMaxLine caps the bufio.Scanner line buffer for a single
// JSON-RPC message. The app server's largest notification is well under
// 1 MiB; 16 MiB leaves headroom for unexpected payloads without
// risking scanner-buffer overflows (Scanner grows by realloc, so an
// unbounded line would still cap at MaxScanTokenSize, but we'd rather
// fail fast on a runaway server).
const jsonRPCMaxLine = 16 * 1024 * 1024

// NewClient wires a Client to the given byte streams. Start must be
// called before Request/Notify; until then, no goroutine is reading
// from r and writes will block waiting for a response that never
// arrives.
func NewClient(r io.Reader, w io.Writer) *Client {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), jsonRPCMaxLine)
	return &Client{
		reader: r,
		writer: w,
		scan:   scan,
		notif:  make(chan Notification, 64),
		closed: make(chan struct{}),
	}
}

// Start begins the background read loop, dispatching each line to a
// pending request by id (if it has one) or to the notifications channel
// (if it's a method+params without an id). Start is single-shot: a
// second call returns immediately, matching the lifecycle the manager
// drives (one Start per server process).
func (c *Client) Start() {
	go c.readLoop()
}

// Close shuts down the read loop. Any pending Request calls will return
// an error rather than block, and the notifications channel is closed
// so consumers can range over it without leaking goroutines. Safe to
// call multiple times.
func (c *Client) Close() {
	c.closeOnce.Do(func() { close(c.closed) })
}

// Notifications returns the channel of incoming server-to-client
// notifications. The channel is closed when Close is called, so a
// `for n := range client.Notifications()` loop in the consumer will
// exit cleanly on shutdown.
func (c *Client) Notifications() <-chan Notification {
	return c.notif
}

// readLoop is the single goroutine that owns the scanner. It runs until
// the scanner errors (process exit, EOF, or a line longer than the
// configured cap) or Close is called; in either case it signals all
// pending requests so they unblock.
func (c *Client) readLoop() {
	for {
		select {
		case <-c.closed:
			return
		default:
		}
		if !c.scan.Scan() {
			c.failPending(c.scan.Err())
			return
		}
		line := c.scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var env envelope
		if err := json.Unmarshal(line, &env); err != nil {
			// Skip malformed lines rather than killing the connection:
			// the App Server's stream is well-formed in practice, but
			// a single torn line shouldn't drop every other thread's
			// live events.
			continue
		}
		if env.ID != nil {
			if ch, ok := c.pending.Load(idKey(env.ID)); ok {
				ch.(chan *envelope) <- &env
			}
			// A response for an unknown id is ignored — likely a
			// request that already timed out and was removed.
		} else if env.Method != "" {
			select {
			case c.notif <- Notification{Method: env.Method, Params: env.Params}:
			case <-c.closed:
				return
			}
		}
	}
}

// failPending unblocks every in-flight Request with an error so the
// manager (or test) can decide whether to retry or shut down. Called
// once when the read loop exits.
func (c *Client) failPending(scanErr error) {
	c.pending.Range(func(k, v any) bool {
		ch := v.(chan *envelope)
		// Synthesise a JSON-RPC error response so the pending
		// Request sees a regular jsonRPCError, not a sentinel.
		errMsg := "client closed"
		if scanErr != nil {
			errMsg = "read loop ended: " + scanErr.Error()
		}
		ch <- &envelope{Error: &jsonRPCError{Code: -1, Message: errMsg}}
		return true
	})
}

// Request sends a JSON-RPC request with the given method+params and
// blocks until the matching response arrives (or Close/read-error
// intervenes). On success, result is unmarshalled from the response
// (pass nil if you don't need it). Errors are returned for both
// JSON-RPC-level errors (envelope.Error) and transport-level failures
// (read loop ended).
func (c *Client) Request(method string, params any, result any) error {
	id := c.nextID.Add(1)
	rawID, err := json.Marshal(id)
	if err != nil {
		return fmt.Errorf("codexserver: marshal id: %w", err)
	}
	paramsJSON, err := marshalParams(params)
	if err != nil {
		return fmt.Errorf("codexserver: marshal params: %w", err)
	}
	env := envelope{
		JSONRPC: "2.0",
		ID:      (*json.RawMessage)(&rawID),
		Method:  method,
		Params:  paramsJSON,
	}
	ch := make(chan *envelope, 1)
	c.pending.Store(idKey(env.ID), ch)
	defer c.pending.Delete(idKey(env.ID))

	if err := c.writeLine(env); err != nil {
		return fmt.Errorf("codexserver: write request: %w", err)
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return fmt.Errorf("codexserver: %s: empty result", method)
		}
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("codexserver: unmarshal result for %s: %w", method, err)
		}
		return nil
	case <-c.closed:
		return fmt.Errorf("codexserver: %s: client closed", method)
	}
}

// Notify sends a JSON-RPC notification (no id, no response expected).
// Use this for fire-and-forget messages like the `initialized`
// notification the App Server expects right after `initialize`.
func (c *Client) Notify(method string, params any) error {
	paramsJSON, err := marshalParams(params)
	if err != nil {
		return fmt.Errorf("codexserver: marshal params: %w", err)
	}
	env := envelope{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	}
	if err := c.writeLine(env); err != nil {
		return fmt.Errorf("codexserver: write notify: %w", err)
	}
	return nil
}

// writeLine serialises env as one JSON object and writes it terminated
// by a newline. Writes are serialised by writeMu so a concurrent
// Request/Notify pair can't interleave bytes and break the line
// framing the server's scanner depends on.
func (c *Client) writeLine(env envelope) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("codexserver: marshal envelope: %w", err)
	}
	data = append(data, '\n')
	if _, err := c.writer.Write(data); err != nil {
		return err
	}
	return nil
}

// idKey turns the envelope's raw id JSON into a stable string key for
// the pending-requests map. Using the raw bytes (rather than the
// decoded int) keeps the lookup correct regardless of how the server
// echoes the id back (numeric, string, or null).
func idKey(raw *json.RawMessage) string {
	if raw == nil {
		return ""
	}
	return string(*raw)
}

// marshalParams is a small wrapper that returns a RawMessage when
// params is already one, otherwise marshals it normally. This lets
// callers pass either a typed struct or an already-encoded payload.
func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(params)
}

