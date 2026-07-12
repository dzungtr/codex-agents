package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
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

	// QuickReply delivers a one-line reply into threadID's codex composer
	// (PRD #1's List behavior -> Quick reply row / issue #6). The ui
	// package has already excluded closed threads before calling this (see
	// handleKey's "r" case). Returns a Cmd that eventually yields
	// QuickReplySentMsg (success) or ThreadLaunchErrorMsg (failure) — the
	// same generic error message Attach/Launch failures already surface as
	// a transient status line.
	QuickReply func(threadID, text string) tea.Cmd

	// Interrupt stops row's current turn (PRD #1's List behavior ->
	// Interrupt row: thread -> waiting, redirectable; no hard-kill).
	// Returns a Cmd that yields InterruptDoneMsg or ThreadLaunchErrorMsg.
	Interrupt func(row Row) tea.Cmd

	// Archive kills row's tmux session if alive, archives the codex
	// thread (or hides it in the cockpit's own state), and offers
	// worktree removal per PRD #1's List behavior -> Archive row. Returns
	// a Cmd that yields ArchiveDoneMsg or ThreadLaunchErrorMsg.
	Archive func(row Row) tea.Cmd

	// CheckLiveness re-derives threadID's tmux-liveness status shortly
	// after a Launch, so a thread that died moments after its
	// ThreadLaunchedMsg row was optimistically inserted doesn't keep
	// reading as StatusWorking forever. Deliberately narrower than a full
	// Refresh: codex may not have written this thread's own record yet,
	// and Refresh replaces the whole row set from that record — calling it
	// this soon after launch could drop the freshly-launched row outright
	// instead of correcting its status. Returns a Cmd that yields
	// ThreadLivenessMsg.
	CheckLiveness func(threadID string) tea.Cmd
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

// QuickReplySentMsg reports that a quick-reply's tmux send-keys delivery
// completed (fire-and-forget per the PRD's cheap-path mandate — this is not
// a delivery confirmation, just "the tmux commands didn't error").
type QuickReplySentMsg struct{ ThreadID string }

// InterruptDoneMsg reports that the Interrupt (`x`) action successfully
// stopped ThreadID's current turn.
type InterruptDoneMsg struct{ ThreadID string }

// ArchiveDoneMsg reports that the Archive (`a`) action finished: ThreadID's
// row is removed from the list (archived rows disappear, per PRD #1's List
// behavior -> Archive row), and Note is a status-line summary — e.g.
// whether the worktree was removed or kept because it wasn't safe to.
type ArchiveDoneMsg struct {
	ThreadID string
	Note     string
}

// ThreadLivenessMsg carries a freshly re-derived tmux-liveness Status for
// ThreadID (see Actions.CheckLiveness), used to correct a launched row that
// died shortly after its optimistic ThreadLaunchedMsg insert.
type ThreadLivenessMsg struct {
	ThreadID string
	Status   tmuxstatus.Status
}

// composerProfile returns the profile name the composer would launch
// with on Enter right now. It indexes m.composerProfiles, supplied at
// startup via WithProfiles (typically the result of
// codexlaunch.DiscoverProfiles, which scans $CODEX_HOME/*.config.toml).
//
// When the discovered list is empty (no $CODEX_HOME/*.config.toml
// files), this returns ""; the composer pill renders as `[]` and
// the launch goes ahead with no `-p` flag.
func (m Model) composerProfile() string {
	if len(m.composerProfiles) == 0 {
		return ""
	}
	return m.composerProfiles[m.composerProfileIdx%len(m.composerProfiles)]
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
			// No-op on an empty list: pressing @ when there are no
			// profiles on disk shouldn't panic with a divide-by-zero,
			// and there's nothing to cycle through anyway.
			if len(m.composerProfiles) > 0 {
				m.composerProfileIdx = (m.composerProfileIdx + 1) % len(m.composerProfiles)
			}
		} else {
			m.composerTask += string(msg.Runes)
		}
	}
	return m, nil
}
