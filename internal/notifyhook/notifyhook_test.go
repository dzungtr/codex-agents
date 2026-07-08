package notifyhook

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dzungtr/codex-agents/internal/agentstate"
)

func TestWrapperArgsAndParseWrapperArgs_RoundTrip(t *testing.T) {
	args := WrapperArgs("/usr/local/bin/codex-agents", "thread-1", "/home/x/.codex-agents/events.jsonl", []string{"/usr/bin/terminal-notifier", "-title", "codex"})
	want := []string{
		"/usr/local/bin/codex-agents", "notify-hook", "thread-1",
		"/home/x/.codex-agents/events.jsonl",
		"/usr/bin/terminal-notifier\x1f-title\x1fcodex",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("WrapperArgs() = %v, want %v", args, want)
	}

	// codex invokes the program at args[0] with args[1:] plus its own
	// appended JSON payload as the final argument; ParseWrapperArgs works
	// on everything after the "notify-hook" subcommand word.
	payload := `{"type":"agent-turn-complete"}`
	invocation := append(append([]string(nil), args[2:]...), payload)

	threadID, eventsPath, forward, gotPayload, err := ParseWrapperArgs(invocation)
	if err != nil {
		t.Fatalf("ParseWrapperArgs: %v", err)
	}
	if threadID != "thread-1" {
		t.Errorf("threadID = %q, want thread-1", threadID)
	}
	if eventsPath != "/home/x/.codex-agents/events.jsonl" {
		t.Errorf("eventsPath = %q, want /home/x/.codex-agents/events.jsonl", eventsPath)
	}
	if !reflect.DeepEqual(forward, []string{"/usr/bin/terminal-notifier", "-title", "codex"}) {
		t.Errorf("forward = %v, want [/usr/bin/terminal-notifier -title codex]", forward)
	}
	if gotPayload != payload {
		t.Errorf("payload = %q, want %q", gotPayload, payload)
	}
}

func TestWrapperArgs_NoForward_ParsesToNilForward(t *testing.T) {
	args := WrapperArgs("/bin/codex-agents", "thread-2", "/events.jsonl", nil)
	invocation := append(append([]string(nil), args[2:]...), `{}`)

	_, _, forward, _, err := ParseWrapperArgs(invocation)
	if err != nil {
		t.Fatalf("ParseWrapperArgs: %v", err)
	}
	if len(forward) != 0 {
		t.Errorf("expected no forward command, got %v", forward)
	}
}

func TestParseWrapperArgs_WrongArgCountErrors(t *testing.T) {
	if _, _, _, _, err := ParseWrapperArgs([]string{"only-one"}); err == nil {
		t.Fatalf("expected an error for the wrong number of args")
	}
}

func TestAppendEvent_ThenLatestByThread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	t1 := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 7, 8, 12, 5, 0, 0, time.UTC)

	if err := AppendEvent(path, Event{ThreadID: "a", Kind: KindTurnEnded, At: t1}); err != nil {
		t.Fatalf("AppendEvent 1: %v", err)
	}
	if err := AppendEvent(path, Event{ThreadID: "b", Kind: KindTurnEnded, At: t1}); err != nil {
		t.Fatalf("AppendEvent 2: %v", err)
	}
	// A second event for "a": LatestByThread should report this one, not
	// the first.
	if err := AppendEvent(path, Event{ThreadID: "a", Kind: KindTurnEnded, At: t2}); err != nil {
		t.Fatalf("AppendEvent 3: %v", err)
	}

	got, err := LatestByThread(path)
	if err != nil {
		t.Fatalf("LatestByThread: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 threads, got %v", got)
	}
	if !got["a"].At.Equal(t2) {
		t.Errorf("thread a's latest event = %v, want %v", got["a"].At, t2)
	}
	if !got["b"].At.Equal(t1) {
		t.Errorf("thread b's latest event = %v, want %v", got["b"].At, t1)
	}
}

func TestLatestByThread_MissingFileReturnsEmptyMap(t *testing.T) {
	got, err := LatestByThread(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err != nil {
		t.Fatalf("LatestByThread on a missing file returned an error instead of degrading: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty map, got %v", got)
	}
}

func TestLatestByThread_SkipsMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	content := "{\"thread_id\":\"a\",\"kind\":\"turn-ended\",\"at\":\"2026-07-08T12:00:00Z\"}\n" +
		"not valid json at all\n" +
		"{\"thread_id\":\"b\",\"kind\":\"turn-ended\",\"at\":\"2026-07-08T12:00:00Z\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed events file: %v", err)
	}

	got, err := LatestByThread(path)
	if err != nil {
		t.Fatalf("LatestByThread: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected the two well-formed lines to survive, got %v", got)
	}
}

func TestLastTurnEventValue_Format(t *testing.T) {
	ev := Event{ThreadID: "t1", Kind: KindTurnEnded, At: time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)}
	want := "turn-ended@2026-07-08T12:00:00Z"
	if got := LastTurnEventValue(ev); got != want {
		t.Errorf("LastTurnEventValue() = %q, want %q", got, want)
	}
}

// fakeForwardRunner records the argv it was invoked with instead of
// shelling out.
type fakeForwardRunner struct {
	calls [][]string
	err   error
}

func (f *fakeForwardRunner) Run(argv []string) error {
	f.calls = append(f.calls, append([]string(nil), argv...))
	return f.err
}

func TestRun_RecordsEventAndUpdatesStateAndForwards(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	eventsPath := filepath.Join(dir, "events.jsonl")
	if err := agentstate.Upsert(statePath, "thread-1", agentstate.Entry{TmuxSession: "cxa-thread-1", Profile: "general-agentic"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	forward := &fakeForwardRunner{}
	var stderr bytes.Buffer

	Run(&stderr, forward, statePath, "thread-1", eventsPath, []string{"/usr/bin/terminal-notifier", "-title", "codex"}, `{"type":"agent-turn-complete"}`, now)

	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output on a clean run, got %q", stderr.String())
	}

	events, err := LatestByThread(eventsPath)
	if err != nil {
		t.Fatalf("LatestByThread: %v", err)
	}
	if events["thread-1"].Kind != KindTurnEnded || !events["thread-1"].At.Equal(now) {
		t.Fatalf("expected a recorded turn-ended event, got %+v", events["thread-1"])
	}

	st, err := agentstate.Load(statePath)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if want := "turn-ended@2026-07-08T12:00:00Z"; st.Threads["thread-1"].LastTurnEvent != want {
		t.Fatalf("LastTurnEvent = %q, want %q", st.Threads["thread-1"].LastTurnEvent, want)
	}
	if st.Threads["thread-1"].TmuxSession != "cxa-thread-1" {
		t.Fatalf("expected TmuxSession preserved, got %+v", st.Threads["thread-1"])
	}

	if len(forward.calls) != 1 {
		t.Fatalf("expected exactly one forward call, got %v", forward.calls)
	}
	want := []string{"/usr/bin/terminal-notifier", "-title", "codex", `{"type":"agent-turn-complete"}`}
	if !reflect.DeepEqual(forward.calls[0], want) {
		t.Errorf("forward call = %v, want %v", forward.calls[0], want)
	}
}

func TestRun_NoForwardConfigured_SkipsForwarding(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	eventsPath := filepath.Join(dir, "events.jsonl")
	forward := &fakeForwardRunner{}

	Run(&bytes.Buffer{}, forward, statePath, "thread-1", eventsPath, nil, `{}`, time.Now())

	if len(forward.calls) != 0 {
		t.Fatalf("expected no forward call when no forward command is configured, got %v", forward.calls)
	}
}

// TestRun_EventsFileUnwritable_DegradesInsteadOfPanicking exercises the
// "hook unavailable -> degrade to open/closed" contract: if the events
// directory can't be created (e.g. a file sits where the directory should
// be), Run must report the failure on stderr rather than panic or block —
// the cockpit's status derivation then simply sees no event for the
// thread, i.e. reads it as StatusWorking (open) rather than crashing.
func TestRun_EventsFileUnwritable_DegradesInsteadOfPanicking(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	// Create a plain file where AppendEvent needs a directory, so
	// os.MkdirAll fails.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}
	eventsPath := filepath.Join(blocker, "events.jsonl")

	var stderr bytes.Buffer
	Run(&stderr, &fakeForwardRunner{}, statePath, "thread-1", eventsPath, nil, `{}`, time.Now())

	if stderr.Len() == 0 {
		t.Fatalf("expected the failure to be reported on stderr")
	}
}
