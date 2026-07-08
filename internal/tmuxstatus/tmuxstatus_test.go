package tmuxstatus

import "testing"

func TestSessionName(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"0123456789abcdef", "cxa-01234567"},
		{"short", "cxa-short"},
		{"", "cxa-"},
	}
	for _, tt := range tests {
		if got := SessionName(tt.id); got != tt.want {
			t.Errorf("SessionName(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

// TestStatusFor_DerivationMatrix exercises the event x tmux-liveness matrix
// from PRD #1 / issue #4: tmux alive + turn in progress = working; tmux
// alive + turn ended = waiting; no tmux session = closed — and a dead
// session always reads as closed even with stale "turn ended" event
// history (turnEnded=true), since a killed session can't be waiting on
// anything.
func TestStatusFor_DerivationMatrix(t *testing.T) {
	live := NewLiveSet([]string{SessionName("thread-alive-123"), "cxa-other99"})

	tests := []struct {
		name      string
		threadID  string
		turnEnded bool
		want      Status
	}{
		{"alive + turn in progress = working", "thread-alive-123", false, StatusWorking},
		{"alive + turn ended = waiting", "thread-alive-123", true, StatusWaiting},
		{"dead + turn in progress = closed", "thread-closed-456", false, StatusClosed},
		{"dead + stale turn-ended event = still closed", "thread-closed-456", true, StatusClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StatusFor(tt.threadID, live, tt.turnEnded); got != tt.want {
				t.Errorf("StatusFor(%q, live, %v) = %v, want %v", tt.threadID, tt.turnEnded, got, tt.want)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	if StatusWorking.String() != "working" {
		t.Errorf("StatusWorking.String() = %q, want working", StatusWorking.String())
	}
	if StatusWaiting.String() != "waiting" {
		t.Errorf("StatusWaiting.String() = %q, want waiting", StatusWaiting.String())
	}
	if StatusClosed.String() != "closed" {
		t.Errorf("StatusClosed.String() = %q, want closed", StatusClosed.String())
	}
}

// ListLiveSessions must degrade gracefully (no error, empty result) whether
// tmux is missing entirely or installed-but-serverless — this test
// environment has no tmux binary at all, which exercises that path.
func TestListLiveSessions_DegradesWithoutError(t *testing.T) {
	sessions, err := ListLiveSessions()
	if err != nil {
		t.Fatalf("ListLiveSessions returned an error instead of degrading: %v", err)
	}
	_ = sessions // may be nil/empty depending on whether tmux is present; both are fine.
}
