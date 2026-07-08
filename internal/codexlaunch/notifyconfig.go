package codexlaunch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
)

// notifyLineRe matches a single-line `notify = [...]` key in a codex
// profile config.toml. This intentionally does not attempt general TOML
// parsing (multi-line arrays, inline tables, comments inside the array,
// etc.) — only the single-line double-quoted string-array form codex's own
// config generator and hand-edited configs use in practice. Anything more
// exotic is treated the same as "no notify key found".
var notifyLineRe = regexp.MustCompile(`(?m)^\s*notify\s*=\s*(\[[^\n]*\])\s*$`)

// ExistingNotifyCommand reads $CODEX_HOME/<profile>.config.toml (per PRD
// #1's Launch semantics -> Profile row: "Codex profiles =
// $CODEX_HOME/<name>.config.toml") looking for a pre-existing `notify =
// ["...", ...]` key — the user's own notify command, configured before the
// cockpit's launch chains its own wrapper in via `-c notify=[...]` (issue
// #4's Status hook).
//
// Returns nil, never an error: a missing codexHome/profile, a missing
// config file, or a notify key that isn't a simple single-line string
// array all mean the same thing to the caller — "no existing command to
// forward to", so the notify wrapper only records events rather than
// forwarding. This mirrors the PRD's "hook unavailable -> degrade
// gracefully" posture: a lookup failure here must never block a launch.
func ExistingNotifyCommand(codexHome, profile string) []string {
	if codexHome == "" || profile == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(codexHome, profile+".config.toml"))
	if err != nil {
		return nil
	}
	m := notifyLineRe.FindSubmatch(data)
	if m == nil {
		return nil
	}
	var argv []string
	if err := json.Unmarshal(m[1], &argv); err != nil {
		return nil
	}
	return argv
}
