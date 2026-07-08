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

func TestStatusFor(t *testing.T) {
	live := NewLiveSet([]string{SessionName("thread-alive-123"), "cxa-other99"})

	if got := StatusFor("thread-alive-123", live); got != StatusOpen {
		t.Errorf("StatusFor(alive) = %v, want StatusOpen", got)
	}
	if got := StatusFor("thread-closed-456", live); got != StatusClosed {
		t.Errorf("StatusFor(closed) = %v, want StatusClosed", got)
	}
}

func TestStatusString(t *testing.T) {
	if StatusOpen.String() != "open" {
		t.Errorf("StatusOpen.String() = %q, want open", StatusOpen.String())
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
