package subthread

import "time"

// SetTestPollInterval shrinks the wait loop's poll cadence for tests. It is
// exported so the cmd/cdxa --wait tests can shrink it too (the internal
// tests flip the unexported pollInterval directly). RestoreTestPollDefaults
// undoes it.
func SetTestPollInterval(d time.Duration) { pollInterval = d }

// SetTestSleep replaces the wait loop's sleep with fn for tests, so the loop
// doesn't burn real wall-clock time. RestoreTestPollDefaults undoes it.
func SetTestSleep(fn func(time.Duration)) { sleep = fn }

// RestoreTestPollDefaults restores the production poll cadence and sleep. It
// captures the originals the first time it's called, so it's safe to pair
// with SetTestPollInterval/SetTestSleep even after multiple swaps.
func RestoreTestPollDefaults() {
	sleep = time.Sleep
	pollInterval = 200 * time.Millisecond
}
