// Package tmuxstatus derives a codex thread's status from tmux session
// liveness, plus (as of slice #4) whether its last known turn ended. It
// knows nothing about codex's own data (that's internal/codexstate) or the
// notify-hook event feed's shape (that's internal/notifyhook) — only about
// the cxa-<thread-id-prefix> session naming convention, how to ask tmux
// which sessions are alive, and the plain derivation matrix (PRD #1 / issue
// #4): tmux alive + turn in progress = working; tmux alive + turn ended =
// waiting; no tmux session = closed, regardless of any stale event history.
package tmuxstatus

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// sessionPrefix is the tmux session name prefix used for cockpit-managed
// codex threads: cxa-<first idPrefixLen chars of the thread id>.
const sessionPrefix = "cxa-"

// idPrefixLen is how many characters of a thread ID are used to build its
// tmux session name. This is a provisional choice for slice #2; the Launch
// slice (#3) is the producer of record for the final naming rule (PRD #1
// Handoffs table) and may formalize/replace it.
const idPrefixLen = 8

// SessionName returns the tmux session name for a codex thread ID.
func SessionName(threadID string) string {
	n := idPrefixLen
	if len(threadID) < n {
		n = len(threadID)
	}
	return sessionPrefix + threadID[:n]
}

// Status is a thread's liveness+turn-event-derived state, per PRD #1's List
// behavior -> Statuses row.
type Status int

const (
	// StatusClosed means no tmux session is alive for the thread. This
	// always wins over any turn-event history: a dead session can't be
	// "waiting" on anything.
	StatusClosed Status = iota
	// StatusWorking means the thread's tmux session is alive and its last
	// known turn is still in progress (no turn-ended event seen, or none
	// available — see StatusFor's degraded-mode note).
	StatusWorking
	// StatusWaiting means the thread's tmux session is alive and its last
	// turn ended, so it needs user input.
	StatusWaiting
)

func (s Status) String() string {
	switch s {
	case StatusWorking:
		return "working"
	case StatusWaiting:
		return "waiting"
	default:
		return "closed"
	}
}

// Lister lists the names of currently-alive tmux sessions. Production code
// uses ListLiveSessions (shells out to `tmux list-sessions`); tests inject a
// fake so they don't depend on a real tmux server being available.
type Lister func() ([]string, error)

// ListLiveSessions shells out to `tmux list-sessions -F '#{session_name}'`.
// If tmux isn't installed or no server is running, that's treated as "zero
// live sessions" rather than an error — a personal machine without tmux (or
// with tmux not yet started) should simply show every thread as closed.
func ListLiveSessions() ([]string, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// tmux exits non-zero (e.g. "no server running") when there are
			// no sessions; that's not a cockpit-level error.
			return nil, nil
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			// tmux isn't installed at all; treat as "nothing is alive".
			return nil, nil
		}
		return nil, err
	}

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	sessions := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			sessions = append(sessions, l)
		}
	}
	return sessions, nil
}

// LiveSet is a set of live tmux session names, as returned by a Lister.
type LiveSet map[string]struct{}

// NewLiveSet builds a LiveSet from a slice of session names.
func NewLiveSet(names []string) LiveSet {
	set := make(LiveSet, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return set
}

// StatusFor derives a thread's status from its ID, the current live tmux
// session set, and turnEnded — whether the notify-hook event feed's latest
// record for this thread says its last turn ended (internal/notifyhook is
// the producer; callers pass false when no event has ever been recorded,
// which is also exactly what "hook unavailable" degrades to: every alive
// thread reads as StatusWorking, i.e. plain open/closed with a "working"
// label, per PRD #1's Launch semantics -> Status hook row).
//
// A dead tmux session is always StatusClosed regardless of turnEnded: stale
// event history from a session that's since been killed must not read as
// "waiting".
func StatusFor(threadID string, live LiveSet, turnEnded bool) Status {
	if _, ok := live[SessionName(threadID)]; !ok {
		return StatusClosed
	}
	if turnEnded {
		return StatusWaiting
	}
	return StatusWorking
}
