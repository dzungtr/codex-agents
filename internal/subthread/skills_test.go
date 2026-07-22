package subthread

import (
	"bytes"
	"errors"
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
