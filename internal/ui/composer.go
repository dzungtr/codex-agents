package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Actions are the side-effecting operations the list screen can trigger.
// The ui package stays decoupled from process execution and codex-specific
// invocation knowledge (that's internal/codexlaunch and internal/tmuxstatus)
// — callers (cmd/codex-agents) wire these up and hand them to WithActions.
// A zero-value Actions makes the composer submit and row-Enter into no-ops,
// which keeps the model usable (and every existing test passing) without
// any action wired.
type Actions struct {
	// Launch starts a brand-new codex thread for task under the chosen
	// profile. Returns a Cmd that eventually yields a ThreadLaunchedMsg
	// (success) or ThreadLaunchErrorMsg (failure).
	Launch func(task, profile string) tea.Cmd

	// Attach attaches an alive thread's tmux session, or (for a closed
	// thread) spawns `codex resume` into a managed session first. Returns
	// a Cmd that eventually yields AttachDoneMsg or ThreadLaunchErrorMsg.
	Attach func(row Row) tea.Cmd

	// Refresh reloads the thread list, typically after AttachDoneMsg
	// (tmux detach returns control to the cockpit with "a refreshed
	// list", per PRD #1's List behavior table). Returns a Cmd that yields
	// RowsRefreshedMsg.
	Refresh func() tea.Cmd
}

// ThreadLaunchedMsg reports a successful composer launch. Row is inserted
// at the top of the list, since a just-launched thread is by definition
// the most recent.
type ThreadLaunchedMsg struct{ Row Row }

// ThreadLaunchErrorMsg reports a failed Launch or Attach action, shown as a
// transient status line.
type ThreadLaunchErrorMsg struct{ Err error }

// AttachDoneMsg reports that an attach/resume subprocess returned control
// to the cockpit (the user detached from tmux).
type AttachDoneMsg struct{}

// RowsRefreshedMsg carries a freshly reloaded row set that replaces the
// model's current rows outright.
type RowsRefreshedMsg struct{ Rows []Row }

// composerProfiles is the fixed profile cycle offered by the composer's
// `@` key, per PRD #1's Launch semantics table (each corresponds to
// $CODEX_HOME/<name>.config.toml). general-agentic is first/default: a
// detached launch implies an unattended posture.
var composerProfiles = []string{"general-agentic", "design-session", "review"}

func (m Model) composerProfile() string {
	return composerProfiles[m.composerProfileIdx]
}

// handleComposerKey processes a key while the composer is focused. It
// never touches list navigation/filter state.
func (m Model) handleComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.composerFocused = false
		m.composerTask = ""
		m.composerProfileIdx = 0
	case tea.KeyEnter:
		task := strings.TrimSpace(m.composerTask)
		profile := m.composerProfile()
		m.composerFocused = false
		m.composerTask = ""
		m.composerProfileIdx = 0
		if task != "" && m.actions.Launch != nil {
			return m, m.actions.Launch(task, profile)
		}
	case tea.KeyBackspace:
		if runes := []rune(m.composerTask); len(runes) > 0 {
			m.composerTask = string(runes[:len(runes)-1])
		}
	case tea.KeySpace:
		m.composerTask += " "
	case tea.KeyRunes:
		if string(msg.Runes) == "@" {
			m.composerProfileIdx = (m.composerProfileIdx + 1) % len(composerProfiles)
		} else {
			m.composerTask += string(msg.Runes)
		}
	}
	return m, nil
}
