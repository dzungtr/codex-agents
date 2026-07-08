// Package tmuxstatus derives a codex thread's base open/closed status from
// tmux session liveness. It knows nothing about codex's own data (that's
// internal/codexstate) — only about the cxa-<thread-id-prefix> session
// naming convention and how to ask tmux which sessions are alive.
//
// Slice #2 only needs open (session alive) vs closed (no session); the
// working/waiting split is a later slice (PRD #1, #4) layered on top of
// this liveness check via a notify-hook event feed.
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

// Status is a thread's liveness-derived state.
type Status int

const (
	// StatusClosed means no tmux session is alive for the thread.
	StatusClosed Status = iota
	// StatusOpen means the thread's tmux session is alive.
	StatusOpen
)

func (s Status) String() string {
	if s == StatusOpen {
		return "open"
	}
	return "closed"
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

// StatusFor derives a thread's status from its ID and the current live set.
func StatusFor(threadID string, live LiveSet) Status {
	if _, ok := live[SessionName(threadID)]; ok {
		return StatusOpen
	}
	return StatusClosed
}
