// Package codexlaunch is the sole owner of codex-specific launch
// knowledge: how to build a `codex` command line (profile/model/notify-hook
// flags), where a new thread's worktree goes, and how to turn that into a
// detached tmux invocation. Nothing outside this package should know
// codex's CLI flags or the `.worktrees/<branch>` convention.
package codexlaunch

import "encoding/json"

// NewThreadSpec describes a new codex thread to launch.
type NewThreadSpec struct {
	Profile string // "" means no -p flag; codex uses its own default
	Model   string // "" leaves the profile's own model untouched
	Task    string
	// Notify, when non-empty, is chained as `-c notify=[...]`: the
	// notify-hook wrapper argv built by internal/notifyhook.WrapperArgs
	// (PRD #1's Launch semantics -> Status hook row, issue #4). Empty
	// means no notify hook is configured for this launch — status
	// derivation then degrades to plain tmux-liveness (every alive thread
	// reads as working, never waiting).
	Notify []string
}

// NewThreadArgs builds the codex invocation for a brand-new thread:
// `codex -p <profile> [-c model=<model>] [-c notify=[...]] "<task>"`. Model
// and Notify are each layered on top of the profile only when explicitly
// set.
func NewThreadArgs(spec NewThreadSpec) []string {
	// An empty Profile means "don't pass -p" — let codex pick its own
	// default. The launcher is the single place that decides whether a
	// caller-supplied empty profile is honoured, and it does (see
	// Launcher.Launch: req.Profile passes through unchanged). UI code
	// that wants a specific profile must look it up in the discovered
	// list rather than synthesizing a name.
	args := []string{"codex"}
	if spec.Profile != "" {
		args = append(args, "-p", spec.Profile)
	}
	if spec.Model != "" {
		args = append(args, "-c", "model="+spec.Model)
	}
	if len(spec.Notify) > 0 {
		args = append(args, "-c", "notify="+notifyConfigValue(spec.Notify))
	}
	return append(args, spec.Task)
}

// notifyConfigValue renders argv as a JSON string array, which for the
// plain-ASCII argv this cockpit ever produces (an executable path plus its
// own flags) is also valid TOML array-of-strings syntax — the literal shape
// PRD #1 shows for `-c notify=[…]`.
func notifyConfigValue(argv []string) string {
	data, err := json.Marshal(argv)
	if err != nil {
		// argv is always []string; json.Marshal cannot fail for that type.
		return "[]"
	}
	return string(data)
}

// ResumeArgs builds the codex invocation for resuming an existing thread by
// ID: `codex -p <profile> resume <id>`. An empty profile ("unknown", e.g. a
// thread whose rollout predates profile recording) omits `-p` entirely
// rather than forcing a default that might not match how the thread was
// originally launched.
func ResumeArgs(threadID, profile string) []string {
	if profile == "" {
		return []string{"codex", "resume", threadID}
	}
	return []string{"codex", "-p", profile, "resume", threadID}
}
