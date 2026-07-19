package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// TestComposer_Enter_FullFlow_LaunchedRowAppearsInView exercises the
// composer Enter -> Launch cmd -> ThreadLaunchedMsg chain end-to-end at the
// model level (without bubbletea's loop), to lock in the user's mental
// model: typing a task, pressing Enter, and seeing that thread appear at
// the top of the list once the launch resolves. The pre-launch view must
// not contain the launched row yet, and the post-launch view must.
func TestComposer_Enter_FullFlow_LaunchedRowAppearsInView(t *testing.T) {
	var launchedTask string
	actions := Actions{Launch: func(task, profile string) tea.Cmd {
		launchedTask = task
		return func() tea.Msg {
			return ThreadLaunchedMsg{Row: Row{
				Thread: codexstate.Thread{
					ID:        "launched-uuid",
					Title:     task,
					CWD:       "/tmp/some-worktree",
					GitBranch: "fix-bug",
					Profile:   profile,
					Recency:   fixedNow(),
				},
				Status: tmuxstatus.StatusWorking,
			}}
		}
	}}
	m := newFixtureModel().WithActions(actions)
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	for _, r := range "fix bug" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected a non-nil Cmd from submitting the composer")
	}
	if launchedTask != "fix bug" {
		t.Fatalf("Launch called with %q, want fix bug", launchedTask)
	}

	pre := m.View()
	if strings.Contains(pre, "fix bug") {
		t.Fatalf("pre-launch view should not contain composer text 'fix bug', got:\n%s", pre)
	}

	msg := cmd()
	updated2, _ := m.Update(msg)
	m = updated2.(Model)
	post := m.View()
	if !strings.Contains(post, "fix bug") {
		t.Fatalf("post-launch view should contain launched row title 'fix bug', got:\n%s", post)
	}
}
