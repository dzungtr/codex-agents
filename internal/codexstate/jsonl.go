package codexstate

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Rollout jsonl files are a stream of newline-delimited JSON records. This
// package only understands three record shapes, all best-effort:
//
//	{"type":"session_meta","payload":{"id":...,"title":...,"cwd":...,"model":...,"git_branch":...,"profile":...}}
//	{"type":"token_count","payload":{"total_tokens":...}}
//	{"type":"event_msg","payload":{"type":"user_message","message":...}}
//
// Any line that isn't valid JSON, or doesn't match one of these shapes, is
// skipped rather than treated as fatal — this is the "best-effort" jsonl
// path described in ADR 0001.
type jsonlRecord struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CWD       string `json:"cwd"`
	Model     string `json:"model"`
	GitBranch string `json:"git_branch"`
	Profile   string `json:"profile"`
}

type tokenCountPayload struct {
	TotalTokens int `json:"total_tokens"`
}

// eventMsgPayload is the payload shape of an event_msg record whose
// Type == "user_message". Other event_msg subtypes, and other fields on
// this subtype (images, local_images, text_elements), are ignored.
type eventMsgPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// firstMessageMaxRunes caps how much of a thread's first user message is
// retained on Thread.FirstMessage. The field exists to give threads a
// one-line fallback identity, and this parsing runs for every thread on
// every load, so the cap keeps memory use bounded regardless of how long a
// real user message is. Truncation is rune-safe and adds no ellipsis;
// display concerns (e.g. further truncation for a list row) belong to the
// UI layer.
const firstMessageMaxRunes = 500

// truncateRunes returns s truncated to at most max runes. It never splits a
// multi-byte rune. If s already has max runes or fewer, it is returned
// unchanged.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// scanSessionsJSONL walks sessionsDir for *.jsonl rollout files and builds a
// Thread from each one's session_meta record. Used when the sqlite schema
// probe fails. Threads recovered this way have no archived concept (codex's
// archived flag lives only in sqlite) so none are hidden, and Recency falls
// back to the file's modification time.
func scanSessionsJSONL(sessionsDir string) ([]Thread, error) {
	var threads []Thread

	err := filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		t, ok := threadFromRolloutFile(path)
		if !ok {
			return nil
		}
		t.Source = SourceJSONL
		threads = append(threads, t)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return threads, nil
		}
		return nil, err
	}
	return threads, nil
}

// threadFromRolloutFile parses a single rollout jsonl file's session_meta
// (first matching record) and, if present, its last token_count record.
// Returns ok=false if no session_meta record could be found/parsed.
func threadFromRolloutFile(path string) (Thread, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Thread{}, false
	}
	defer f.Close()

	var (
		meta         sessionMetaPayload
		haveMeta     bool
		tokenCount   = -1
		messageCount = 0
		firstMessage string
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "session_meta":
			if !haveMeta {
				var m sessionMetaPayload
				if err := json.Unmarshal(rec.Payload, &m); err == nil && m.ID != "" {
					meta = m
					haveMeta = true
				}
			}
		case "token_count":
			var tc tokenCountPayload
			if err := json.Unmarshal(rec.Payload, &tc); err == nil {
				tokenCount = tc.TotalTokens
			}
		case "event_msg":
			// messageCount counts every event_msg record seen (not just
			// user_message), as a best-effort proxy for "how many messages
			// this conversation has" — the rollout format doesn't expose a
			// narrower "conversation turn" concept than that.
			messageCount++
			if firstMessage == "" {
				var p eventMsgPayload
				if err := json.Unmarshal(rec.Payload, &p); err == nil && p.Type == "user_message" {
					if trimmed := strings.TrimSpace(p.Message); trimmed != "" {
						firstMessage = truncateRunes(trimmed, firstMessageMaxRunes)
					}
				}
			}
		}
	}
	if !haveMeta {
		return Thread{}, false
	}

	recency := time.Now().UTC()
	if info, err := os.Stat(path); err == nil {
		recency = info.ModTime().UTC()
	}

	return Thread{
		ID:           meta.ID,
		Title:        meta.Title,
		CWD:          meta.CWD,
		Model:        meta.Model,
		GitBranch:    meta.GitBranch,
		Archived:     false,
		Recency:      recency,
		RolloutPath:  path,
		Profile:      meta.Profile,
		FirstMessage: firstMessage,
		TokenCount:   tokenCount,
		MessageCount: messageCount,
	}, true
}

// enrichFromSessionFiles fills in Profile and TokenCount for threads sourced
// from sqlite by best-effort parsing their RolloutPath. Threads whose
// rollout file is missing or unparseable keep Profile == "" and
// TokenCount == -1 ("unknown"), which the UI renders as "-".
func enrichFromSessionFiles(threads []Thread, sessionsDir string) {
	_ = sessionsDir // rollout_path from sqlite is already absolute; kept for symmetry/future use.
	for i := range threads {
		if threads[i].RolloutPath == "" {
			continue
		}
		t, ok := threadFromRolloutFile(threads[i].RolloutPath)
		if !ok {
			continue
		}
		threads[i].Profile = t.Profile
		threads[i].TokenCount = t.TokenCount
		threads[i].MessageCount = t.MessageCount
		threads[i].FirstMessage = t.FirstMessage
	}
}
