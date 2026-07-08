package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleReplyKey processes a key while the quick-reply input is focused
// (PRD #1's List behavior -> Quick reply row / issue #6). It mirrors
// handleComposerKey's shape but submits via Actions.QuickReply against the
// thread captured at focus time (m.replyThreadID) rather than a task+profile
// pair, and never touches list navigation/filter state.
func (m Model) handleReplyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.replyFocused = false
		m.replyText = ""
		m.replyThreadID = ""
	case tea.KeyEnter:
		text := strings.TrimSpace(m.replyText)
		threadID := m.replyThreadID
		m.replyFocused = false
		m.replyText = ""
		m.replyThreadID = ""
		if text != "" && m.actions.QuickReply != nil {
			return m, m.actions.QuickReply(threadID, text)
		}
	case tea.KeyBackspace:
		if runes := []rune(m.replyText); len(runes) > 0 {
			m.replyText = string(runes[:len(runes)-1])
		}
	case tea.KeySpace:
		m.replyText += " "
	case tea.KeyRunes:
		m.replyText += string(msg.Runes)
	}
	return m, nil
}
