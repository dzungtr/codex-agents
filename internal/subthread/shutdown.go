// Package subthread: Shutdown is the deep module behind the `cdxa shutdown`
// command (ADR 0007). It performs the headless lifecycle close: gate on
// rollout completion, stop the derived tmux session, then ask codex to
// soft-archive its own record. It composes four narrow boundaries:
//
//   - StateProvider, LivenessProvider — reused from Output (the existing
//     codex state/turn provider, plus the existing tmux liveness reader).
//   - SessionStopper — a narrow seam that targets the derived tmux session
//     and treats an already-missing target as clean.
//   - CodexArchiver — a narrow seam that invokes codex's sanctioned
//     `codex archive` shell-out and never writes codex's sqlite/jsonl.
//
// The Output / Send decision-logic patterns are reused directly (Result +
// Status + exit-code mapping in cmd/cdxa); Shutdown only adds the two
// teardown steps and the ordering rules. The exit-code contract itself
// (0 archived / 2 working / 3 gone / 1 operational) is shared with all
// other cdxa commands and lives in cmd/cdxa — Shutdown returns a typed
// Status, never an exit code.
package subthread

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// ShutdownStatus is the outcome of a Shutdown call, mapped by cmd/cdxa to
// the ADR 0007 / ADR 0003 exit-code contract:
//
//	ShutdownArchived — exit 0 (tmux stopped and codex record archived)
//	ShutdownWorking  — exit 2 (a turn is in progress or a session is live
//	                        with no completed turn; no mutation)
//	ShutdownGone     — exit 3 (unknown, already archived, or dead before
//	                        any completed turn; no mutation)
//
// A fourth case — operational failure during teardown — returns
// ErrOperational (exit 1) instead of a Status, since the parent must
// distinguish "still retryable" from a clean refusal.
type ShutdownStatus int

const (
	// ShutdownArchived means both lifecycle targets are clean: the tmux
	// session has been stopped and codex's record has been archived.
	ShutdownArchived ShutdownStatus = iota
	// ShutdownWorking means shutdown was refused because the rollout is
	// still in flight (a turn is in progress) or the thread has no
	// completed turn yet but its tmux session is alive. No side effects
	// occurred — the parent should keep polling cdxa output.
	ShutdownWorking
	// ShutdownGone means the thread is unknown, already archived by codex,
	// or died before producing a completed turn. No side effects
	// occurred — the parent's cleanup loop can stop.
	ShutdownGone
)

// String returns the lowercase status name used in the JSON output object
// ("archived", "working", "gone") — the same vocabulary ADR 0007 freezes.
func (s ShutdownStatus) String() string {
	switch s {
	case ShutdownArchived:
		return "archived"
	case ShutdownWorking:
		return "working"
	case ShutdownGone:
		return "gone"
	default:
		return "unknown"
	}
}

// ShutdownResult is what Shutdown returns: the resolved Status plus the
// thread id it operated on. The JSON shape cdxa prints is exactly
// {"status","thread_id"} (ADR 0007 decision 1).
type ShutdownResult struct {
	Status   ShutdownStatus
	ThreadID string
}

// SessionStopper stops a thread's tmux session, treating an already-missing
// target as clean success so retries are idempotent (ADR 0007 decision 5).
// Production code wires listerRunnerSessionStopper; tests inject a fake so
// the kill/archive ordering can be exercised without a real tmux server.
type SessionStopper interface {
	Stop(threadID string) error
}

// CodexArchiver soft-archives a codex thread by invoking codex's own
// `codex archive` shell-out (ADR 0007 decision 4). It must never write
// codex's sqlite/jsonl directly; a non-nil error means codex refused the
// archive and the parent should retry. Production code wires
// execCodexArchiver; tests inject a fake so the success/failure branching
// is exercisable without a real codex binary.
type CodexArchiver interface {
	Archive(threadID string) error
}

// listerRunnerSessionStopper is the production SessionStopper: probe
// tmuxstatus.ListLiveSessions for the derived session name and only run
// tmuxstatus.KillSessionArgs when the session is present. An already-missing
// target returns nil so a crashed-but-cleanly-stopped subthread reports
// archived instead of an opaque failure (ADR 0007 decision 5).
//
// When the lister itself fails (no PATH, weird tmux state), Shutdown falls
// through to the kill, which surfaces the failure as ErrOperational —
// matching ADR 0007 decision 3's "real tmux failure prevents archive" rule.
type listerRunnerSessionStopper struct {
	lister tmuxstatus.Lister
	runner tmuxstatus.Runner
}

func (s *listerRunnerSessionStopper) Stop(threadID string) error {
	session := tmuxstatus.SessionName(threadID)
	if s.lister != nil {
		if names, err := s.lister(); err == nil {
			live := tmuxstatus.NewLiveSet(names)
			if _, ok := live[session]; !ok {
				return nil
			}
		}
	}
	return s.runner.Run(tmuxstatus.KillSessionArgs(session))
}

// execCodexArchiver is the production CodexArchiver: invoke `codex archive
// <threadID>` and surface a non-zero exit as an error. Returns nil on a
// clean exit (codex 0). ADR 0007 decision 4: this is the only write path
// to codex's persisted state — never touch codex's sqlite/jsonl directly
// from subthread.
type execCodexArchiver struct{}

func (execCodexArchiver) Archive(threadID string) error {
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("codex not on PATH: %w", err)
	}
	cmd := exec.Command(codexPath, "archive", threadID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			return fmt.Errorf("codex archive %s: %w", threadID, err)
		}
		return fmt.Errorf("codex archive %s: %w: %s", threadID, err, trimmed)
	}
	return nil
}

// DefaultSessionStopper is the production SessionStopper, probing the live
// tmux session list before issuing kill-session. It has no mutable state
// and is safe for concurrent use.
func DefaultSessionStopper() SessionStopper {
	return &listerRunnerSessionStopper{
		lister: tmuxstatus.ListLiveSessions,
		runner: tmuxstatus.ExecRunner{},
	}
}

// DefaultCodexArchiver is the production CodexArchiver, shelling out to
// `codex archive`. It has no mutable state and is safe for concurrent use.
func DefaultCodexArchiver() CodexArchiver { return execCodexArchiver{} }

// Shutdown is the headless lifecycle close for a subthread (ADR 0007).
// It resolves threadID through the existing codex state provider, gates on
// rollout completion (≥1 completed turn, none in progress), stops the
// derived tmux session when alive (idempotent on missing), then asks codex
// to soft-archive its own record. The command-level mapping to
// exit 0 / 2 / 3 / 1 lives in cmd/cdxa; Shutdown returns a typed
// ShutdownResult or a wrapped ErrOperational.
//
// Decision matrix (ADR 0007 decisions 2 & 3):
//
//   - thread not found (ErrThreadNotFound)         → ShutdownGone
//   - thread hidden in cockpit state.json           → ShutdownGone
//   - 0 completed turns, tmux session alive         → ShutdownWorking
//   - 0 completed turns, tmux session dead          → ShutdownGone
//   - ≥1 completed turns, latest turn in progress   → ShutdownWorking
//   - ≥1 completed turns, no in-progress turn       → stop tmux, then
//                                                     archive codex, then
//                                                     ShutdownArchived
//
// Teardown ordering (ADR 0007 decision 3): the tmux kill is attempted
// first. A real tmux failure stops the sequence — codex archive is not
// called — so the record still identifies the live lifecycle. After a
// successful tmux stop, codex archive is attempted; a codex failure is
// returned as ErrOperational, but the tmux session is already gone so a
// retry can finish the record transition.
//
// codexHome is $CODEX_HOME (or its ~/.codex default); the caller resolves
// it so this package has no filesystem-root knowledge of its own. statePath
// is the cockpit's state.json (agentstate.DefaultPath); an empty value
// skips the hidden-thread check, matching Output's degraded posture.
func Shutdown(
	state StateProvider,
	live LivenessProvider,
	sessions SessionStopper,
	archiver CodexArchiver,
	codexHome, statePath, threadID string,
) (ShutdownResult, error) {
	result := ShutdownResult{ThreadID: threadID}

	thread, err := state.FindThread(codexHome, threadID)
	if err != nil {
		if errors.Is(err, codexstate.ErrThreadNotFound) {
			return ShutdownResult{Status: ShutdownGone, ThreadID: threadID}, nil
		}
		return result, fmt.Errorf("%w: find thread %q: %v", ErrOperational, threadID, err)
	}

	// Match Output's posture (issue #60): a thread the cockpit already
	// archived via its Hidden bookkeeping but that still resolves through
	// codex's jsonl fallback must read as gone here too. Best-effort — a
	// missing or unreadable state.json doesn't block shutdown.
	if threadIsHidden(statePath, threadID) {
		return ShutdownResult{Status: ShutdownGone, ThreadID: threadID}, nil
	}

	turns, err := state.ReadTurns(thread.RolloutPath)
	if err != nil {
		return result, fmt.Errorf("%w: read turns for %q: %v", ErrOperational, threadID, err)
	}

	// Completion gate: ≥1 completed turn AND no in-progress turn (ADR 0007
	// decision 2). A live tmux session with no completed turn is a first
	// turn still in flight — working, not eligible (mirrors Output's
	// distinction; clarification comment on issue #103 ratified this).
	if len(turns.Completed) == 0 {
		if live != nil && live(threadID) {
			return ShutdownResult{Status: ShutdownWorking, ThreadID: threadID}, nil
		}
		return ShutdownResult{Status: ShutdownGone, ThreadID: threadID}, nil
	}
	if turns.InProgress {
		return ShutdownResult{Status: ShutdownWorking, ThreadID: threadID}, nil
	}

	// Eligible: stop tmux, then archive codex. A real tmux failure
	// short-circuits before the archive so the record still resolves to a
	// live process and the parent can retry; a codex failure surfaces
	// after tmux is gone so the next call can finish the record cleanup
	// without re-killing an already-stopped session.
	if err := sessions.Stop(threadID); err != nil {
		return result, fmt.Errorf("%w: stop session for %q: %v", ErrOperational, threadID, err)
	}
	if err := archiver.Archive(threadID); err != nil {
		return result, fmt.Errorf("%w: archive %q: %v", ErrOperational, threadID, err)
	}

	return ShutdownResult{Status: ShutdownArchived, ThreadID: threadID}, nil
}
