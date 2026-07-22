package subthread

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// TestSkills_Lookup covers the registry's three observable contracts in
// one table: the bundled cdxa-spawn skill resolves with non-trivial
// content and the required frontmatter fields, and an unknown name
// surfaces ErrUnknownSkill with the name in the message and a nil body.
// A separate TestSkills_AllEntriesConsistent sweep catches the "future
// skill added with empty content" failure mode.
func TestSkills_Lookup(t *testing.T) {
	tests := []struct {
		name      string   // subtest name
		skill     string   // skill name passed to Lookup
		wantErr   bool     // whether Lookup should return an error
		minLen    int      // minimum body length when wantErr is false
		wantParts []string // substrings the body must contain when wantErr is false
	}{
		{
			name:      "cdxa-spawn resolves with non-trivial content",
			skill:     "cdxa-spawn",
			minLen:    200, // skill content is non-trivial, well above 200 bytes
			wantParts: []string{"name: cdxa-spawn", "description:"},
		},
		{
			name:    "unknown returns ErrUnknownSkill with name in message",
			skill:   "does-not-exist",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := Lookup(tt.skill)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Lookup(%q): err = nil, want error", tt.skill)
				}
				if !errors.Is(err, ErrUnknownSkill) {
					t.Errorf("err = %v, want errors.Is ErrUnknownSkill", err)
				}
				if !strings.Contains(err.Error(), tt.skill) {
					t.Errorf("err.Error() = %q, want substring %q", err.Error(), tt.skill)
				}
				if body != nil {
					t.Errorf("body = %v, want nil for unknown skill", body)
				}
				return
			}
			if err != nil {
				t.Fatalf("Lookup(%q): %v", tt.skill, err)
			}
			if len(body) < tt.minLen {
				t.Errorf("len(body) = %d, want >= %d (skill content is non-trivial)", len(body), tt.minLen)
			}
			for _, want := range tt.wantParts {
				if !bytes.Contains(body, []byte(want)) {
					t.Errorf("body missing required substring %q", want)
				}
			}
		})
	}
}

// TestSkills_AllEntriesConsistent sweeps the embedded registry to catch
// the "future skill added but embed dropped it" failure mode: every entry
// must be non-empty and carry a YAML name: frontmatter field. If a new
// skill lands in skills/ with empty bytes, this test fails loudly instead
// of the registry silently returning an empty body from Lookup.
func TestSkills_AllEntriesConsistent(t *testing.T) {
	if len(Skills) == 0 {
		t.Fatal("Skills map is empty; expected at least one embedded skill")
	}
	for name, body := range Skills {
		if len(body) == 0 {
			t.Errorf("Skills[%q] has empty body", name)
		}
		if !bytes.Contains(body, []byte("name:")) {
			t.Errorf("Skills[%q] missing name: frontmatter", name)
		}
	}
}

// TestSkills_CDXASpawnContentInvariants asserts that the embedded
// cdxa-spawn markdown retains the four load-bearing structural
// invariants from issue #53 §"The prompt envelope (literal)" and the
// frozen cdxa exit-code contract from ADR 0003 / ADR 0004. If any of
// these drift, downstream consumers (the parent prompt that reads the
// leaf's last assistant message, the shell poll loops that branch on
// exit codes) break silently — the cdxa-spawn skill's own prose flags
// this with "The envelope is byte-stable. Do not rewrite, reword, or
// 'improve' it. Sibling sanity tests assert its presence; downstream
// consumers ... depend on the exact phrasing." This test is the sibling
// it advertises.
//
// The test sits alongside TestSkills_Lookup and TestSkills_AllEntriesConsistent
// so the registry's full contract is asserted in one file: the embed
// wiring, the lookup mechanism, and the content shape.
func TestSkills_CDXASpawnContentInvariants(t *testing.T) {
	body, ok := Skills["cdxa-spawn"]
	if !ok {
		t.Fatal(`Skills["cdxa-spawn"] missing — embedded registry has no cdxa-spawn entry`)
	}

	// Substring invariants: the prose must literally contain each of
	// these byte sequences. The source of each is named in the label
	// so a failure points the reader at the right ADR / issue section.
	substringInvariants := []struct {
		label string
		want  string
	}{
		{
			// Prompt envelope, issue #53 §"The prompt envelope
			// (literal)" — every spawned subthread's prompt
			// contains this line verbatim. The leaf rule
			// (forbidden from cdxa spawn) is prompt-enforced via
			// this exact line, so a reword breaks the depth-1 cap.
			label: "envelope/IDENTITY-leaf",
			want:  "IDENTITY: leaf",
		},
		{
			// YAML frontmatter `name` field, consumed by both
			// the install side (cdxa skills) and any agent
			// loader that resolves the skill by name.
			label: "frontmatter/name",
			want:  "name: cdxa-spawn",
		},
		{
			// YAML frontmatter `description` field — paired
			// with name above; required for the agent's
			// skill-discovery UI to show the skill at all.
			label: "frontmatter/description",
			want:  "description:",
		},
		{
			// Output contract clause, issue #53 §"Output
			// contract". The parent reads ONLY the leaf's
			// last assistant message; the prose must call that
			// out by name so a leaf that buries its summary
			// under tool-call chatter is itself in violation of
			// the contract it was given.
			label: "output-contract/last-assistant-message",
			want:  "last assistant message",
		},
	}
	for _, inv := range substringInvariants {
		t.Run(inv.label, func(t *testing.T) {
			if !bytes.Contains(body, []byte(inv.want)) {
				t.Errorf("body missing required substring %q (invariant %s)", inv.want, inv.label)
			}
		})
	}

	// Exit-code table invariants, ADR 0003 / ADR 0004 (frozen
	// contract). The skill encodes the four values (0 = done,
	// 1 = operational error, 2 = still working, 3 = gone) as a
	// markdown bullet list of the exact shape:
	//
	//   - `0` — a completed turn is available; ...
	//   - `2` — still working; sleep and poll again. ...
	//   - `3` — thread unknown or gone without collectable output. ...
	//   - `1` — operational error (sqlite unreadable, ...). ...
	//
	// A bare substring check for "1" is too permissive (it also
	// appears in "exits `1`", "1 message", the inline shell case
	// statement, and elsewhere) — so this loop matches the exact
	// bullet shape on a per-line basis. If the table is rewritten
	// as prose, this assertion will fail loudly and prompt the
	// author to update the test alongside the table shape.
	exitLine := regexp.MustCompile(`^\s*-\s*` + "`" + `(\d)` + "`" + `\s*—`)
	seen := map[string]bool{}
	for _, line := range bytes.Split(body, []byte("\n")) {
		if m := exitLine.FindSubmatch(line); m != nil {
			seen[string(m[1])] = true
		}
	}
	for _, code := range []string{"0", "2", "3", "1"} {
		t.Run("exit-code/"+code, func(t *testing.T) {
			if !seen[code] {
				t.Errorf("exit-code table missing bullet entry for code %q (expected a line matching `- \\`%s\\` — ...`)", code, code)
			}
		})
	}
}
