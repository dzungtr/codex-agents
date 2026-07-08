package codexlaunch

import (
	"fmt"

	"github.com/dzungtr/codex-agents/internal/agentstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
)

// QuickReply delivers a one-line reply into threadID's codex composer via
// tmux send-keys (PRD #1's List behavior -> Quick reply row / issue #6),
// then clears the thread's recorded last-turn-event so its status
// derivation (tmuxstatus.StatusFor) reads back as StatusWorking instead of
// staying stuck on StatusWaiting until codex's own notify hook eventually
// fires again. This closes the gap the Statuses slice (#4) flagged:
// last_turn_event was write-only from the hook's side and nothing ever
// cleared it.
//
// Delivery is intentionally the cheap path the PRD calls for: two plain
// tmux send-keys calls (literal text via tmuxstatus.SendKeysArgs, then a
// separate Enter keypress via tmuxstatus.SendEnterArgs — see those
// functions' doc comments for why they can't be combined into one call),
// with no delivery confirmation and no retries. Callers are expected to
// have already excluded closed threads (a dead tmux session has nothing to
// send keys to); QuickReply itself doesn't re-check liveness.
func (l *Launcher) QuickReply(threadID, text string) error {
	session := tmuxstatus.SessionName(threadID)

	if err := l.Tmux.Run(tmuxstatus.SendKeysArgs(session, text)); err != nil {
		return fmt.Errorf("codexlaunch: send reply text: %w", err)
	}
	if err := l.Tmux.Run(tmuxstatus.SendEnterArgs(session)); err != nil {
		return fmt.Errorf("codexlaunch: send reply enter: %w", err)
	}

	if err := agentstate.UpdateLastTurnEvent(l.StatePath, threadID, ""); err != nil {
		return fmt.Errorf("codexlaunch: clear last turn event: %w", err)
	}
	return nil
}
