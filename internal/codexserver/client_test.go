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

// newTestStreams wires two io.Pipes in opposite directions and returns
// (client-side writer, client-side reader, server-side writer,
// server-side reader). The test drives the server side and the client
// uses the client side; a real `codex app-server` is never spawned.
//
//	clientW ──> srvR      (client writes requests, server reads them)
//	srvW ──> clientR      (server writes responses/notifications, client reads)
func newTestStreams(t *testing.T) (clientW, clientR, srvW, srvR io.ReadWriteCloser) {
	t.Helper()
	// Two pipes. pipe1 carries client->server; pipe2 carries
	// server->client. io.Pipe returns (Reader, Writer) so the
	// first return value is the read end.
	p1r, p1w := io.Pipe() // client->server
	p2r, p2w := io.Pipe() // server->client
	return wrapWriter(p1w), wrapReader(p2r), wrapWriter(p2w), wrapReader(p1r)
}

type wrappedWriter struct{ w *io.PipeWriter }
type wrappedReader struct{ r *io.PipeReader }

// writeCloser only implements io.Writer + io.Closer; its Read
// returns io.EOF immediately. Used to wrap the client/server's
// unused half of a pipe.
type writeCloser struct{ w *io.PipeWriter }

func (wc writeCloser) Write(p []byte) (int, error) { return wc.w.Write(p) }
func (wc writeCloser) Read([]byte) (int, error)    { return 0, io.EOF }
func (wc writeCloser) Close() error                { return wc.w.Close() }

func wrapWriter(w *io.PipeWriter) io.ReadWriteCloser { return writeCloser{w: w} }

// readCloser only implements io.Reader + io.Closer; its Write
// returns io.ErrClosedPipe (a sane behaviour for a closed read end).
type readCloser struct{ r *io.PipeReader }

func (rc readCloser) Read(p []byte) (int, error) { return rc.r.Read(p) }
func (rc readCloser) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (rc readCloser) Close() error               { return rc.r.Close() }

func wrapReader(r *io.PipeReader) io.ReadWriteCloser { return readCloser{r: r} }

func TestClient_Request_ReceivesMatchingResponse(t *testing.T) {
	clientW, clientR, srvW, srvR := newTestStreams(t)
	defer clientW.Close()
	defer clientR.Close()
	defer srvW.Close()
	defer srvR.Close()

	c := NewClient(clientR, clientW)
	c.Start()
	defer c.Close()

	// Server side: read the request, write a matching response.
	go func() {
		scan := bufio.NewScanner(srvR)
		scan.Buffer(make([]byte, 0, 1024), jsonRPCMaxLine)
		if !scan.Scan() {
			t.Errorf("server read scan failed: %v", scan.Err())
			return
		}
		var env envelope
		if err := json.Unmarshal(scan.Bytes(), &env); err != nil {
			t.Errorf("server unmarshal: %v", err)
			return
		}
		if env.Method != "ping" {
			t.Errorf("server saw method = %q, want ping", env.Method)
		}
		resp := envelope{JSONRPC: "2.0", ID: env.ID, Result: json.RawMessage(`{"ok":true}`)}
		data, _ := json.Marshal(resp)
		srvW.Write(append(data, '\n'))
	}()

	var got map[string]any
	if err := c.Request("ping", nil, &got); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("result ok = %v, want true", got["ok"])
	}
}

func TestClient_Notify_DoesNotWaitForResponse(t *testing.T) {
	clientW, clientR, _, srvR := newTestStreams(t)
	defer clientW.Close()
	defer clientR.Close()
	defer srvR.Close()
	// Drain the server's read end so the client's Notify write doesn't
	// block on a synchronous io.Pipe with no consumer.
	go func() { _, _ = io.Copy(io.Discard, srvR) }()

	c := NewClient(clientR, clientW)
	c.Start()
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- c.Notify("ready", map[string]any{}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Notify: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Notify blocked beyond 1s — it should be fire-and-forget")
	}
}

func TestClient_Notifications_RoutedToChannel(t *testing.T) {
	clientW, clientR, srvW, srvR := newTestStreams(t)
	defer clientW.Close()
	defer clientR.Close()
	defer srvW.Close()
	defer srvR.Close()

	c := NewClient(clientR, clientW)
	c.Start()
	defer c.Close()

	srvW.Write([]byte(`{"jsonrpc":"2.0","method":"thread/started","params":{"threadId":"t1"}}` + "\n"))
	srvW.Write([]byte("not json at all\n"))
	srvW.Write([]byte(`{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"t1","turn":{"id":"u","status":"completed","items":[]}}}` + "\n"))

	got := []Notification{}
	deadline := time.After(time.Second)
	for len(got) < 2 {
		select {
		case n := <-c.Notifications():
			got = append(got, n)
		case <-deadline:
			t.Fatalf("got %d notifications, want 2 within 1s; got=%+v", len(got), got)
		}
	}
	if got[0].Method != "thread/started" {
		t.Errorf("first notification method = %q, want thread/started", got[0].Method)
	}
	if got[1].Method != "turn/completed" {
		t.Errorf("second notification method = %q, want turn/completed", got[1].Method)
	}
}

func TestClient_Request_PropagatesJSONRPCError(t *testing.T) {
	clientW, clientR, srvW, srvR := newTestStreams(t)
	defer clientW.Close()
	defer clientR.Close()
	defer srvW.Close()
	defer srvR.Close()

	c := NewClient(clientR, clientW)
	c.Start()
	defer c.Close()

	go func() {
		scan := bufio.NewScanner(srvR)
		scan.Buffer(make([]byte, 0, 1024), jsonRPCMaxLine)
		scan.Scan() // discard the request
		srvW.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}` + "\n"))
	}()

	err := c.Request("nope", nil, nil)
	if err == nil {
		t.Fatal("Request returned nil error for a JSON-RPC error response")
	}
	if got := err.Error(); !strings.HasPrefix(got, "jsonrpc") {
		t.Errorf("error = %q, want it to start with the jsonrpc prefix", got)
	}
}

func TestClient_Close_UnblocksPendingRequest(t *testing.T) {
	clientW, clientR, _, srvR := newTestStreams(t)
	defer clientW.Close()
	defer clientR.Close()
	defer srvR.Close()
	// Drain so the Request write doesn't block.
	go func() { _, _ = io.Copy(io.Discard, srvR) }()

	c := NewClient(clientR, clientW)
	c.Start()

	errCh := make(chan error, 1)
	go func() { errCh <- c.Request("hang", nil, nil) }()
	time.Sleep(10 * time.Millisecond)
	c.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Request returned nil error after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Request did not unblock within 1s of Close")
	}
}

func TestClient_Request_ResultDecodeErrorIsWrapped(t *testing.T) {
	clientW, clientR, srvW, srvR := newTestStreams(t)
	defer clientW.Close()
	defer clientR.Close()
	defer srvW.Close()
	defer srvR.Close()

	c := NewClient(clientR, clientW)
	c.Start()
	defer c.Close()

	go func() {
		scan := bufio.NewScanner(srvR)
		scan.Buffer(make([]byte, 0, 1024), jsonRPCMaxLine)
		scan.Scan()
		srvW.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"not-a-number"}` + "\n"))
	}()

	var n int
	err := c.Request("bad", nil, &n)
	if err == nil {
		t.Fatal("Request returned nil error for a malformed result")
	}
}

func TestClient_Request_WritesAreSerialised(t *testing.T) {
	var buf safeBuffer
	writer := writerFunc(func(p []byte) (int, error) { return buf.Write(p) })
	_, _, srvW, srvR := newTestStreams(t)
	defer srvW.Close()

	c := NewClient(srvR, writer)
	c.Start()
	defer c.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); c.Request("a", nil, nil) }()
	go func() { defer wg.Done(); c.Request("b", nil, nil) }()

	time.Sleep(50 * time.Millisecond)
	lines := splitLines(buf.String())
	if len(lines) != 2 {
		t.Fatalf("expected 2 well-formed lines, got %d: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var env envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Errorf("line %d (%q) failed to parse as a JSON-RPC envelope: %v", i, line, err)
		}
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, b := range []byte(s) {
		if b == '\n' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
		} else {
			cur += string(b)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
