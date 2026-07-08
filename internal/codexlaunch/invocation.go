// Package codexlaunch is the sole owner of codex-specific launch
// knowledge: how to build a `codex` command line (profile/model flags),
// where a new thread's worktree goes, and how to turn that into a detached
// tmux invocation. Nothing outside this package should know codex's CLI
// flags or the `.worktrees/<branch>` convention.
package codexlaunch

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
}

// NewThreadArgs builds the codex invocation for a brand-new thread:
// `codex -p <profile> [-c model=<model>] "<task>"`. Model is layered on
// top of the profile only when explicitly set; an untouched model pill
// means "use the profile's default".
func NewThreadArgs(spec NewThreadSpec) []string {
	profile := spec.Profile
	if profile == "" {
		profile = DefaultProfile
	}
	args := []string{"codex", "-p", profile}
	if spec.Model != "" {
		args = append(args, "-c", "model="+spec.Model)
	}
	return append(args, spec.Task)
}

// ResumeArgs builds the codex invocation for resuming an existing thread by
// ID: `codex resume <id>`.
func ResumeArgs(threadID string) []string {
	return []string{"codex", "resume", threadID}
}
