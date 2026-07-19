// Package subthread implements cdxa spawn's orchestration: launch a new
// codex thread headlessly (via internal/codexlaunch), then poll codex's
// sqlite until that thread registers, returning codex's own thread id.
//
// This is the headless counterpart to the cockpit's interactive launch
// path (ADR 0003 decision 2): the launch itself is identical — same
// worktree-per-thread resolution, same detached tmux session named
// cxa-<prefix>, same notify-hook chaining — but the caller is a parent
// codex thread (cdxa spawn) rather than the bubbletea program, and the
// return value is a resolvable thread id rather than a cockpit row.
//
// Issue #28 concurrently adds Output() to this package; Spawn() is additive
// and disjoint from it. The two share no state: Output resolves an existing
// thread's turn/message by reading codex's rollout file; Spawn produces a
// new thread and blocks only until codex's own records know about it.
package subthread

import (
	"errors"
	"fmt"
	"time"
)

// defaultProfile is the profile cdxa spawn uses when --profile is omitted
// (issue #29 acceptance criteria: "Default profile is general-agentic when
// --profile is omitted"). This matches the cockpit composer's documented
// default (ADR 0001 decision 5).
const defaultProfile = "general-agentic"

// DefaultPollInterval is the delay between registration polls when a
// Spawner's PollInterval is zero. Bounded to a value that catches a
// registration within a few polls of codex's own write latency without
// hammering its sqlite: codex registers a freshly started thread within
// roughly a second in practice, and a sub-second poll keeps the perceived
// spawn latency close to codex's own startup latency.
const DefaultPollInterval = 500 * time.Millisecond

// DefaultRegistrationWait bounds how long Spawn blocks waiting for the
// freshly launched thread to appear in codex's sqlite (ADR 0003 decision 4:
// "the brief block at spawn time … is the price of returning a real,
// resolvable id instead of a promise"). Tuned to comfortably exceed
// codex's own startup-to-registration latency on a warm machine while
// keeping a genuine failure (codex never started, tmux session died)
// surfacing inside a minute.
const DefaultRegistrationWait = 30 * time.Second

// ErrRegistrationTimeout is returned by Spawn when the launched thread did
// not appear in codex's sqlite within the Spawner's RegistrationWait. The
// tmux session may still be starting up; the caller reports this as a
// JSON error object and exit 1 (ADR 0003 decision 2).
var ErrRegistrationTimeout = errors.New("subthread: thread did not register in codex's state before timeout")

// Launcher launches a new codex thread headlessly and returns its thread
// id. Production code wires *codexlaunch.Launcher; tests inject a fake so
// the launch-then-poll loop can be exercised without a real tmux server or
// git repo.
type Launcher interface {
	HeadlessLaunch(task, profile string) (string, error)
}

// Registrar reports whether codex already knows about a thread id (i.e. it
// has appeared in codex's sqlite or jsonl fallback). Production code wires
// a thin adapter over codexstate.ThreadRegistered; tests inject a fake that
// scripts successive false-then-true responses to model codex's
// startup-to-registration latency.
type Registrar interface {
	ThreadRegistered(threadID string) (bool, error)
}

// Spawner owns the launch-then-poll-until-registered loop. The zero value
// is not usable: Launch and Registered are required. PollInterval and
// RegistrationWait default when zero (see the Default* constants); Sleep
// defaults to time.Sleep and exists as an injection point for deterministic
// tests.
type Spawner struct {
	Launch           Launcher
	Registered       Registrar
	PollInterval     time.Duration
	RegistrationWait time.Duration
	Sleep            func(time.Duration)
}

func (s *Spawner) pollInterval() time.Duration {
	if s.PollInterval > 0 {
		return s.PollInterval
	}
	return DefaultPollInterval
}

func (s *Spawner) registrationWait() time.Duration {
	if s.RegistrationWait > 0 {
		return s.RegistrationWait
	}
	return DefaultRegistrationWait
}

func (s *Spawner) sleep() func(time.Duration) {
	if s.Sleep != nil {
		return s.Sleep
	}
	return time.Sleep
}

// Spawn launches a new codex thread running task under profile, then polls
// codex's sqlite until that thread registers (bounded by RegistrationWait),
// returning codex's own thread id. An empty profile defaults to
// general-agentic (issue #29).
//
// The launch is the cockpit's standard worktree-per-thread launch (ADR 0001
// decision 4): a fresh worktree at <repo-root>/.worktrees/<slug>, a
// detached tmux session named cxa-<prefix>, the notify-hook wrapper
// chained. The block until registration is the contract from ADR 0003
// decision 4 — the returned id is immediately resolvable via codex's own
// data, so cdxa output (#28) and the cockpit list see the same thread.
//
// Failure modes:
//   - launch error (tmux refused to start the session, codex binary
//     missing, worktree creation failed): returned wrapped, no polling.
//   - registration timeout: ErrRegistrationTimeout (possibly wrapped if a
//     registrar error preceded the final timeout check).
//   - registrar error: propagated; Spawn does not retry past a genuine
//     sqlite/jsonl read failure.
func (s *Spawner) Spawn(task, profile string) (string, error) {
	if profile == "" {
		profile = defaultProfile
	}

	threadID, err := s.Launch.HeadlessLaunch(task, profile)
	if err != nil {
		return "", fmt.Errorf("subthread: launch: %w", err)
	}

	deadline := time.Now().Add(s.registrationWait())
	sleep := s.sleep()
	interval := s.pollInterval()

	for {
		// Check first, then sleep: codex may have already registered the
		// thread by the time Launch returned (the liveness poll inside
		// codexlaunch already waited through codex's startup window), so
		// the common case is one check, zero sleeps.
		registered, err := s.Registered.ThreadRegistered(threadID)
		if err != nil {
			return "", fmt.Errorf("subthread: check registration of %s: %w", threadID, err)
		}
		if registered {
			return threadID, nil
		}

		if !time.Now().Before(deadline) {
			return "", ErrRegistrationTimeout
		}

		sleep(interval)
	}
}
