// Package subthread's skills registry is the compile-time source of truth
// for installed-skill content (PRD #53 "Source of truth"): every .md file
// under the adjacent skills/ directory is embedded into the binary via
// //go:embed, exposed as the Skills map, and resolved by name through
// Lookup. Adding a new skill is purely a file change under skills/ — no Go
// edits — because the embed picks up every .md at build time and the
// registry is built by reading the embed, not by maintaining a hard-coded
// list of names.
//
// The registry is consumed by #56 (the cdxa skills subcommand, which
// surfaces installed-skill metadata to the user) and #57 (the sanity test
// that verifies the bundled content matches what codex itself sees on
// disk). Neither this file nor its tests touch cmd/cdxa; they only wire
// the embed into something addressable.
package subthread

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed skills/*.md
var skillsFS embed.FS

// ErrUnknownSkill is the sentinel returned (wrapped) by Lookup when name is
// not in the registry. The wrapped message includes the requested name so
// the caller can surface it as a JSON error in #56 without rebuilding the
// string. Callers that want to branch on "unknown" should use
// errors.Is(err, ErrUnknownSkill), not string matching on .Error().
var ErrUnknownSkill = errors.New("cdxa: unknown skill")

// Skills is the registry of installed skill markdown, keyed by skill name
// (the filename stem under skills/, e.g. "cdxa-spawn"). Values are the raw
// file bytes — callers are expected to parse the YAML frontmatter
// themselves if they need structured fields like `description`.
//
// The map is built once at init time from the embedded skills/ directory;
// it is safe for concurrent reads (the map is never written to after
// init).
var Skills = mustLoadSkills()

// Lookup returns the markdown bytes for the named skill, or ErrUnknownSkill
// wrapped with the requested name if no such skill is installed. Unknown
// names reflect "not in the bundle" (the embed has no such file) rather
// than a transient filesystem state — the embed is read at build time.
func Lookup(name string) ([]byte, error) {
	body, ok := Skills[name]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownSkill, name)
	}
	return body, nil
}

// mustLoadSkills reads every *.md directly under the embedded skills/
// directory and returns them keyed by filename stem. It panics on the
// (unreachable in practice) failure modes of reading from an in-memory
// embed so a missing or unreadable file at build time surfaces as a build
// error rather than a silent empty registry at runtime.
func mustLoadSkills() map[string][]byte {
	out := make(map[string][]byte)
	entries, err := fs.ReadDir(skillsFS, "skills")
	if err != nil {
		panic(fmt.Sprintf("cdxa: read embedded skills: %v", err))
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, err := skillsFS.ReadFile("skills/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("cdxa: read embedded skill %s: %v", e.Name(), err))
		}
		out[strings.TrimSuffix(e.Name(), ".md")] = body
	}
	return out
}
