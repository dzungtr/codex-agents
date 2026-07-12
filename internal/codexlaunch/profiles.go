package codexlaunch

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// profileConfigSuffix is the per-profile file suffix codex looks for
// inside $CODEX_HOME. A profile named <name> lives at
// $CODEX_HOME/<name>.config.toml.
const profileConfigSuffix = ".config.toml"

// DiscoverProfiles scans codexHome for `<name>.config.toml` files and
// returns the alphabetically-sorted list of profile names they define.
//
// The composer's "default profile" is whichever name ends up at index
// 0 — there is no special-cased name like "general-agentic" promoted
// to the front. If no profiles exist on disk the returned slice is
// empty (not nil) and the caller is expected to launch with no `-p`
// flag, letting codex use its own default.
//
// A missing codexHome directory is not an error: a fresh `~/.codex`
// without any custom profiles is a valid state. Other filesystem
// errors (permission denied, etc.) are wrapped and returned so callers
// can log and proceed with an empty list.
func DiscoverProfiles(codexHome string) ([]string, error) {
	if codexHome == "" {
		return []string{}, nil
	}
	entries, err := os.ReadDir(codexHome)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("codexlaunch: discover profiles in %s: %w", codexHome, err)
	}

	var profiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, profileConfigSuffix) {
			continue
		}
		profiles = append(profiles, strings.TrimSuffix(name, profileConfigSuffix))
	}
	sort.Strings(profiles)
	if profiles == nil {
		return []string{}, nil
	}
	return profiles, nil
}
