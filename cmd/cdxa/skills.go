// Command cdxa skills installs the embedded skill markdown (PRD #53
// + ADR 0004) into an agent's skill folder. The install is a write-only
// action: it never prints skill content to stdout (ADR 0003 decision
// "JSON-only stdout") and reports a tiny JSON object describing what it
// did. Two failure modes map to two distinct outcomes:
//
//   - operational error (mkdir fails, write fails, unknown agent,
//     unknown skill name) → exit 1 + {"error": "..."} on stdout
//   - success (idempotent no-op or fresh/overwrite write) → exit 0
//     + {"path": "...", "written": bool, "changed": bool} on stdout
//
// The exit-code space for this command is intentionally just 0 and 1 —
// the 2/3 codes are reserved for cdxa subthread state machines
// (output/spawn/send) and have no analogue here. There is no "still
// working" state for a file write.
//
// The skill bytes are read once at build time via go:embed (the
// registry added by #55 in internal/subthread) so the installed file is
// always byte-identical to what this binary shipped. Byte-comparing on
// every invocation is what makes "run cdxa skills again after a binary
// upgrade" idempotent: unchanged skills are skipped (changed:false),
// changed skills are silently overwritten (changed:true) — the
// drift-killing property ADR 0004 decision 2 calls out.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dzungtr/codex-agents/internal/subthread"
)

// homeResolverFn maps an agent name (claude|agents|codex) to that
// agent's home directory (the dir whose "skills/" subdir will receive
// the installed SKILL.md). The production resolver reads $CODEX_HOME /
// $HOME; tests inject a t.TempDir()-rooted fake so the suite never
// touches a real agent home. It is a field on deps (DI via the same
// pattern runSpawn uses for its spawner factory), not a package
// global, so the injection is per-call and concurrent-test-safe.
type homeResolverFn func(agent string) (string, error)

// skillLookupFn is the registry seam: production wires subthread.Lookup
// (the function in internal/subthread that reads the embedded
// map[string][]byte), tests inject a fake that returns canned bytes.
// Signature matches subthread.Lookup exactly so newSkillLookupFor can
// hand back the function value with no wrapper.
type skillLookupFn func(name string) ([]byte, error)

// runSkills implements `cdxa skills <name> --agent <claude|agents|codex>`
// (PRD #53 frozen contract + ADR 0004 decisions 1, 2): parses the
// positional <name> and the --agent flag, resolves the agent home,
// looks up the embedded skill bytes, MkdirAll's the install dir,
// byte-compares against any existing SKILL.md, and prints a JSON
// result. Operational errors → (exitOperErr, err); run in main.go maps
// that to exit 1 with a JSON error object on stdout.
//
// Flag parsing mirrors runSpawn's manual scan (the task may come
// before or after the flag, so Go's flag package — which stops at the
// first non-flag — isn't usable). Both `--agent X` and `--agent=X`
// forms are accepted, mirroring how runSpawn handles --profile and
// --workspace. `-agent` (single dash) is accepted too, matching the
// other subcommands' tolerance for the half-spelled flag.
func runSkills(args []string, d deps) (int, error) {
	var agent string
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--agent" || a == "-agent":
			if i+1 >= len(args) {
				return exitOperErr, fmt.Errorf("cdxa skills: --agent requires a value")
			}
			agent = args[i+1]
			i++
		case strings.HasPrefix(a, "--agent=") || strings.HasPrefix(a, "-agent="):
			agent = strings.SplitN(a, "=", 2)[1]
		case strings.HasPrefix(a, "-"):
			return exitOperErr, fmt.Errorf("cdxa skills: unknown flag %q", a)
		default:
			positionals = append(positionals, a)
		}
	}

	if len(positionals) != 1 {
		return exitOperErr, fmt.Errorf("cdxa skills: usage: cdxa skills <name> --agent <claude|agents|codex>")
	}
	name := positionals[0]
	if name == "" {
		return exitOperErr, fmt.Errorf("cdxa skills: name must not be empty")
	}
	if agent == "" {
		return exitOperErr, fmt.Errorf("cdxa skills: --agent is required (want claude|agents|codex)")
	}

	// Resolve agent home (production: real ~/.claude etc.; tests: t.TempDir).
	// An unknown agent surfaces here as a JSON error — same shape as every
	// other operational error in this command, distinct from the
	// "no such skill" lookup failure that fires a few lines below.
	home, err := newHomeResolverFor(d)(agent)
	if err != nil {
		return exitOperErr, fmt.Errorf("cdxa skills: %w", err)
	}

	// Look up the embedded skill bytes. subthread.Lookup returns
	// ErrUnknownSkill (wrapped, with the name in the message) for names
	// the embed has no .md for; the wrapping preserves errors.Is checks
	// for callers that want to branch.
	body, err := newSkillLookupFor(d)(name)
	if err != nil {
		return exitOperErr, fmt.Errorf("cdxa skills: %w", err)
	}

	// Compute the install path: <home>/skills/<name>/SKILL.md. The skill
	// dir (not the parent of <home>) is the dir we MkdirAll, so a fresh
	// install on a brand-new agent home creates both <home>/skills and
	// <home>/skills/<name> in one syscall chain.
	dir := filepath.Join(home, "skills", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return exitOperErr, fmt.Errorf("cdxa skills: create dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "SKILL.md")

	// Byte-compare against existing content (ADR 0004 decision 2:
	// idempotent overwrite). Read the file first; if it doesn't exist,
	// we fall through to the write branch. Any other read error (perm
	// denied, EISDIR, etc.) is an operational error.
	existing, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return exitOperErr, fmt.Errorf("cdxa skills: read existing %s: %w", path, readErr)
	}
	if readErr == nil && bytes.Equal(existing, body) {
		// Identical — skip the write, report both flags false. This is
		// the "drift-killing" no-op: re-running cdxa skills after a
		// binary that shipped the same bytes is a cheap stat.
		fmt.Fprintf(stdout, "{\"path\":%q,\"written\":false,\"changed\":false}\n", path)
		return exitDone, nil
	}
	if err := os.WriteFile(path, body, 0644); err != nil {
		return exitOperErr, fmt.Errorf("cdxa skills: write %s: %w", path, err)
	}
	fmt.Fprintf(stdout, "{\"path\":%q,\"written\":true,\"changed\":true}\n", path)
	return exitDone, nil
}

// resolveAgentHome maps an agent name to that agent's skill-home
// directory. The three supported values follow the convention each
// agent's own installer uses:
//
//   - "codex"  → $CODEX_HOME if set, else ~/.codex (via resolveCodexHome,
//                which honors the same env var codex's own CLI honors).
//   - "claude" → ~/.claude (no env var; matches Claude's own layout).
//   - "agents" → ~/.agents (the harness5 convention; "r0" in
//                SKILL.md skill roots).
//
// Anything else is an error — the caller (runSkills) wraps it in the
// JSON error object on stdout. The two homedir-based cases (claude,
// agents) share the same $HOME lookup; the codex case is the odd one
// out and goes through resolveCodexHome() so the env-var discipline
// stays in one place.
func resolveAgentHome(agent string) (string, error) {
	switch agent {
	case "codex":
		// Reuse resolveCodexHome() in this package — same $CODEX_HOME /
		// ~/.codex fallback as codex's own CLI. Keeping the lookup in
		// one place means a future change to the env-var name (or a
		// future override file) only touches one function.
		return resolveCodexHome()
	case "claude":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve $HOME: %w", err)
		}
		return filepath.Join(home, ".claude"), nil
	case "agents":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve $HOME: %w", err)
		}
		return filepath.Join(home, ".agents"), nil
	default:
		return "", fmt.Errorf("unknown agent %q (want claude|agents|codex)", agent)
	}
}

// newHomeResolverFor returns the deps-injected home resolver when set
// (tests populate d.homeResolver) and the production resolveAgentHome
// otherwise. The indirection is a field on deps rather than a package
// global so it follows the same DI pattern newSpawnerFor/newReplierFor
// use — concurrent tests can each inject their own fake without
// stepping on each other.
func newHomeResolverFor(d deps) homeResolverFn {
	if d.homeResolver != nil {
		return d.homeResolver
	}
	return resolveAgentHome
}

// newSkillLookupFor returns the deps-injected skill lookup when set
// (tests populate d.skillLookup) and the production subthread.Lookup
// otherwise. Mirrors newHomeResolverFor's nil-default pattern. The
// returned function's signature matches subthread.Lookup so callers
// don't have to unwrap a result.
func newSkillLookupFor(d deps) skillLookupFn {
	if d.skillLookup != nil {
		return d.skillLookup
	}
	return subthread.Lookup
}
