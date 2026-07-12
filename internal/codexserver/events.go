// Package codexserver is the cockpit's connection to codex's own App
// Server: a single child process running `codex app-server` over stdio,
// speaking JSON-RPC 2.0 to multiplex live conversation events for every
// managed thread (ADR 0002). The package owns all knowledge of that
// protocol — the envelope shape, the event names, the lifecycle
// (initialize -> thread/resume -> ... -> thread/unsubscribe), and the
// process management — so other packages only see a narrow Go API:
//
//	mgr := codexserver.NewManager(codexHome)
//	mgr.Start()                        // spawn the single server
//	mgr.Subscribe(threadID)            // open a live event stream
//	mgr.Unsubscribe(threadID)          // close it
//	for ev := range mgr.Events() { ... } // consume Event values
//	mgr.Stop()                         // kill the server, clean up
//
// Nothing outside this package needs to know that the wire format is
// JSON-RPC, that the events come from a child process, or what specific
// notification methods the server uses for live message/token deltas.
// The same boundary that codexstate (sqlite/jsonl reader) and notifyhook
// (turn-ended event tap) already maintain for the rest of the cockpit's
// codex integration.
package codexserver

import "time"

// EventKind tags a decoded App Server notification with what it means at
// the cockpit's level of abstraction. The manager collapses the server's
// per-item notifications into one of these broad categories before
// forwarding to the UI, so the ui package never sees raw method names.
type EventKind int

const (
	// EventMessageCount is a message-count delta: the manager has observed
	// a new completed agent message item on this thread and is forwarding
	// the new absolute count. The UI patches Thread.MessageCount from
	// Event.MessageCount.
	EventMessageCount EventKind = iota
	// EventTokenCount is a token-usage update: the server reported a new
	// last-turn or total token total. The UI patches Thread.TokenCount
	// from Event.TokenCount.
	EventTokenCount
	// EventTurnCompleted is a turn-end notification (semantically the
	// same trigger as notifyhook's turn-ended). v1 only logs it; the
	// notify hook remains the primary status-derivation source for
	// cockpit-launched threads (ADR 0002 decision 6).
	EventTurnCompleted
)

// Event is one decoded, UI-ready update from the codex App Server, tagged
// with the thread it belongs to. The manager emits one of these per
// relevant notification (after coalescing/throttling per ADR 0002
// open-question 3), so the UI just iterates Manager.Events() and applies
// each one to the matching row.
//
// MessageCount and TokenCount are absolute totals — never deltas. The
// manager maintains the per-thread counters internally so the UI's
// in-place row patch is always a full replacement of the displayed
// value, not a relative adjustment. A value of -1 means "no change for
// this field on this event"; the UI's Update handler must skip fields
// flagged this way (matching codexstate's "unknown" sentinel) rather
// than overwriting a known good value with -1.
type Event struct {
	ThreadID     string
	Kind         EventKind
	MessageCount int
	TokenCount   int
	At           time.Time
}
