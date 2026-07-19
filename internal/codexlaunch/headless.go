package codexlaunch

// HeadlessLaunchResult is the subset of LaunchResult a headless caller
// (cdxa spawn, via internal/subthread) needs: the launched thread's
// identity, the tmux session codex is now running in, and the workspace it
// runs in. It deliberately mirrors LaunchResult's identity fields without
// exposing the full struct — a headless caller never drives the cockpit UI,
// so it has no use for the row-rendering fields the composer consumes.
type HeadlessLaunchResult struct {
	ThreadID     string
	SessionName  string
	WorktreePath string
	Branch       string
	InPlace      bool
	Profile      string
}

// HeadlessLaunch launches a brand-new codex thread the same way Launch
// does — same worktree-per-thread resolution, same detached tmux session,
// same notify-hook chaining, same state.json bookkeeping — but returns only
// the thread's identity. This is the entry point internal/subthread calls
// from cdxa spawn (ADR 0003 decision 2): the launch itself is unchanged
// from the cockpit's interactive path; the only difference is that the
// caller is a headless CLI, not the bubbletea program.
//
// req.Profile passes through unchanged: an empty string is honoured as
// "launch with codex's own default" exactly as Launch does. Profile
// defaulting to general-agentic is the caller's responsibility
// (internal/subthread.Spawn), not this method's.
func (l *Launcher) HeadlessLaunch(req LaunchRequest) (HeadlessLaunchResult, error) {
	res, err := l.Launch(req)
	if err != nil {
		return HeadlessLaunchResult{}, err
	}
	return HeadlessLaunchResult{
		ThreadID:     res.ThreadID,
		SessionName:  res.SessionName,
		WorktreePath: res.WorktreePath,
		Branch:       res.Branch,
		InPlace:      res.InPlace,
		Profile:      res.Profile,
	}, nil
}
