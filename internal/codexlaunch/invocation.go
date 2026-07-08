// Package codexlaunch is the sole owner of codex-specific launch
// knowledge: how to build a `codex` command line (profile/model/notify-hook
// flags), where a new thread's worktree goes, and how to turn that into a
// detached tmux invocation. Nothing outside this package should know
// codex's CLI flags or the `.worktrees/<branch>` convention.
package codexlaunch

import "encoding/json"

// DefaultProfile is the composer's default profile pick: a detached launch
// implies an unattended posture (PRD #1, Launch semantics).
const DefaultProfile = "general-agentic"

// KnownProfiles are the profiles the composer's `@` menu offers, per PRD
// #1: each corresponds to $CODEX_HOME/<name>.config.toml.
var KnownProfiles = []string{"general-agentic", "design-session", "review"}

// NewThreadSpec describes a new codex thread to launch.
type NewThreadSpec struct {
	Profile string // "" defaults to DefaultProfile
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
	profile := spec.Profile
	if profile == "" {
		profile = DefaultProfile
	}
	args := []string{"codex", "-p", profile}
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
// ID: `codex resume <id>`.
func ResumeArgs(threadID string) []string {
	return []string{"codex", "resume", threadID}
}
