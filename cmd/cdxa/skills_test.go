package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dzungtr/codex-agents/internal/subthread"
)

// testSkillName and testSkillContent are the canned inputs every runSkills
// test uses. Using a single name/content pair keeps the table tests focused
// on the install logic (which is what this slice owns) rather than on
// per-skill variation (which is the registry's job, tested in
// internal/subthread).
const (
	testSkillName    = "cdxa-spawn"
	testSkillContent = "---\nname: cdxa-spawn\ndescription: test fixture\n---\n\n# cdxa-spawn (test fixture)\n"
)

// fakeSkillLookup returns a skillLookupFn that resolves the names in
// `known` to their canned bodies and returns subthread.ErrUnknownSkill
// (matching the production subthread.Lookup contract) for everything
// else. Tests use this instead of the real subthread.Lookup so a
// registry change in #55 doesn't ripple into the install-logic tests
// here.
func fakeSkillLookup(known map[string][]byte) skillLookupFn {
	return func(name string) ([]byte, error) {
		if body, ok := known[name]; ok {
			return body, nil
		}
		return nil, fmt.Errorf("%w %q", subthread.ErrUnknownSkill, name)
	}
}

// freshHomeResolver returns a brand-new homeResolverFn (mapping each
// known agent to a fresh t.TempDir()) so each table row gets an
// isolated filesystem. Sharing one resolver across rows would let the
// first fresh-write test write the file, after which every later
// "fresh write" test would see the existing file and report no-op —
// that's not what the table is asserting. The resolver is built per
// row, so each row's "codex" home is a different t.TempDir().
func freshHomeResolver(t *testing.T) homeResolverFn {
	t.Helper()
	homes := map[string]string{
		"codex":  t.TempDir(),
		"claude": t.TempDir(),
		"agents": t.TempDir(),
	}
	return func(agent string) (string, error) {
		if h, ok := homes[agent]; ok {
			return h, nil
		}
		return "", errors.New("unknown agent")
	}
}

// skillsCase is one row of the runSkills table: scripted deps, args,
// the expected exit code, and the JSON shape (success → path/written/
// changed; failure → error string contains wantErrContains). Each row
// exercises one branch of the install contract. The homeFn is
// overridden only when the case needs an error or a "parent is a
// file" filesystem shape; the default is a per-row fresh
// tempHomeResolver so fresh-write cases actually start fresh.
type skillsCase struct {
	name            string
	args            []string
	agent           string // for path assertion; the agent name in tt.args
	knownSkills     map[string][]byte
	homeFn          homeResolverFn // nil → use freshHomeResolver
	preExistingBody []byte         // non-nil → pre-create SKILL.md with this content
	wantCode        int
	wantWritten     *bool
	wantChanged     *bool
	wantErrContains string
}

func boolPtr(b bool) *bool { return &b }

// runCase dispatches one row: applies the row's fixture (pre-create,
// resolver override), captures runSkills' stdout via captureStdout,
// simulates run()'s printError-for-failure-case mapping, and returns
// the JSON-parsed stdout. Splitting the dispatch from the assertions
// keeps each row's fixture setup and assertions readable.
func runCase(t *testing.T, tt skillsCase) (pathObj, errorObj map[string]any) {
	t.Helper()
	homeFn := tt.homeFn
	if homeFn == nil {
		homeFn = freshHomeResolver(t)
	}
	d := deps{
		skillLookup:  fakeSkillLookup(tt.knownSkills),
		homeResolver: homeFn,
	}
	// If the row declares a pre-existing file, create it now so the
	// install step sees the expected "old" state. The path is derived
	// from the same resolver runSkills will use, so it's guaranteed
	// to match the install target.
	if tt.preExistingBody != nil {
		home, err := homeFn(tt.agent)
		if err != nil {
			t.Fatalf("resolve home for pre-existing setup: %v", err)
		}
		path := filepath.Join(home, "skills", testSkillName, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir for pre-existing: %v", err)
		}
		if err := os.WriteFile(path, tt.preExistingBody, 0644); err != nil {
			t.Fatalf("write pre-existing: %v", err)
		}
	}

	var (
		gotCode int
		gotErr  error
	)
	out := captureStdout(t, func() {
		gotCode, gotErr = runSkills(tt.args, d)
		// Simulate run()'s error-mapping for the failure rows: a
		// non-nil err from runSkills means run() will print the JSON
		// error object on stdout and exit 1. We replicate that here
		// so the assertion sees the same stdout shape production
		// callers do. Success rows skip this (gotErr is nil,
		// printError would emit {"error":""} and corrupt the JSON
		// success object).
		if gotErr != nil {
			printError(gotErr)
		}
	})
	if gotCode != tt.wantCode {
		t.Errorf("exit code = %d, want %d (err=%v)", gotCode, tt.wantCode, gotErr)
	}
	// Parse the JSON. For success rows there's exactly one object; for
	// failure rows there's exactly one {"error":...} object. Trim
	// any leading whitespace before unmarshalling.
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(out)))
	dec.UseNumber()
	if err := dec.Decode(&pathObj); err != nil {
		t.Fatalf("parse out %q: %v", out, err)
	}
	_ = errorObj
	return pathObj, nil
}

// pathObj is the success-shape JSON: {"path","written","changed"}. The
// test asserts each field individually so a regression on one field
// doesn't get hidden by another.
type pathObj map[string]any

func TestRunSkills_Table(t *testing.T) {
	cases := []skillsCase{
		{
			name:        "fresh write sets changed=true written=true",
			args:        []string{testSkillName, "--agent", "codex"},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitDone,
			wantWritten: boolPtr(true),
			wantChanged: boolPtr(true),
		},
		{
			name:        "fresh write with --agent=codex (equals form)",
			args:        []string{testSkillName, "--agent=codex"},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitDone,
			wantWritten: boolPtr(true),
			wantChanged: boolPtr(true),
		},
		{
			name:        "fresh write with -agent (single-dash form)",
			args:        []string{testSkillName, "-agent", "codex"},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitDone,
			wantWritten: boolPtr(true),
			wantChanged: boolPtr(true),
		},
		{
			name:        "claude agent lands in ~/.claude/skills/<name>/",
			args:        []string{testSkillName, "--agent", "claude"},
			agent:       "claude",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitDone,
			wantWritten: boolPtr(true),
			wantChanged: boolPtr(true),
		},
		{
			name:        "agents agent lands in ~/.agents/skills/<name>/",
			args:        []string{testSkillName, "--agent", "agents"},
			agent:       "agents",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitDone,
			wantWritten: boolPtr(true),
			wantChanged: boolPtr(true),
		},
		{
			name:        "flag-before-name order: --agent codex cdxa-spawn",
			args:        []string{"--agent", "codex", testSkillName},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitDone,
			wantWritten: boolPtr(true),
			wantChanged: boolPtr(true),
		},
		{
			name:            "no-op when existing file is byte-identical",
			args:            []string{testSkillName, "--agent", "codex"},
			agent:           "codex",
			knownSkills:     map[string][]byte{testSkillName: []byte(testSkillContent)},
			preExistingBody: []byte(testSkillContent),
			wantCode:        exitDone,
			wantWritten:     boolPtr(false),
			wantChanged:     boolPtr(false),
		},
		{
			name:            "overwrite when existing file differs",
			args:            []string{testSkillName, "--agent", "codex"},
			agent:           "codex",
			knownSkills:     map[string][]byte{testSkillName: []byte(testSkillContent)},
			preExistingBody: []byte("STALE CONTENT FROM A PREVIOUS INSTALL"),
			wantCode:        exitDone,
			wantWritten:     boolPtr(true),
			wantChanged:     boolPtr(true),
		},
		{
			name:            "unknown skill name → exit 1 with JSON error",
			args:            []string{"does-not-exist", "--agent", "codex"},
			agent:           "codex",
			knownSkills:     nil, // registry is empty
			wantCode:        exitOperErr,
			wantErrContains: "does-not-exist",
		},
		{
			name:        "unknown agent (production error shape) → exit 1 with agent name in message",
			args:        []string{testSkillName, "--agent", "wat"},
			agent:       "wat",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			homeFn: func(string) (string, error) {
				return "", errors.New(`unknown agent "wat" (want claude|agents|codex)`)
			},
			wantCode:        exitOperErr,
			wantErrContains: "wat",
		},
		{
			name:        "no args → exit 1 usage",
			args:        []string{},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitOperErr,
			wantErrContains: "usage",
		},
		{
			name:        "only --agent, no name → exit 1 usage",
			args:        []string{"--agent", "codex"},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitOperErr,
			wantErrContains: "usage",
		},
		{
			name:        "only name, no --agent → exit 1 --agent required",
			args:        []string{testSkillName},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitOperErr,
			wantErrContains: "--agent",
		},
		{
			name:        "--agent with no value → exit 1",
			args:        []string{testSkillName, "--agent"},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitOperErr,
			wantErrContains: "--agent",
		},
		{
			name:        "unknown flag → exit 1",
			args:        []string{testSkillName, "--wat"},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitOperErr,
			wantErrContains: "unknown flag",
		},
		{
			name:        "empty name positional → exit 1",
			args:        []string{"", "--agent", "codex"},
			agent:       "codex",
			knownSkills: map[string][]byte{testSkillName: []byte(testSkillContent)},
			wantCode:    exitOperErr,
			wantErrContains: "name",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			obj, _ := runCase(t, tt)

			if tt.wantErrContains != "" {
				errMsg, _ := obj["error"].(string)
				if !strings.Contains(errMsg, tt.wantErrContains) {
					t.Errorf("error = %q, want substring %q (full obj: %v)", errMsg, tt.wantErrContains, obj)
				}
				return
			}

			// Success-case assertions: path/written/changed.
			pathRaw, ok := obj["path"].(string)
			if !ok {
				t.Fatalf("path = %v, want string (obj: %v)", obj["path"], obj)
			}
			// Assert the path ends with the deterministic suffix AND
			// lands under the agent's home dir (per-row fresh
			// tempHomeResolver).
			wantSuffix := filepath.Join("skills", testSkillName, "SKILL.md")
			if !strings.HasSuffix(pathRaw, wantSuffix) {
				t.Errorf("path = %q, want suffix %q", pathRaw, wantSuffix)
			}
			// Sanity-check the file actually exists with the expected
			// bytes (the fresh-write cases confirm the install
			// side-effect; the no-op/overwrite cases confirm
			// idempotence).
			if tt.preExistingBody != nil {
				if !bytes.Equal(tt.preExistingBody, []byte(testSkillContent)) {
					// Overwrite case: on-disk must equal the new
					// embedded content.
					onDisk, err := os.ReadFile(pathRaw)
					if err != nil {
						t.Fatalf("read %s: %v", pathRaw, err)
					}
					if !bytes.Equal(onDisk, []byte(testSkillContent)) {
						t.Errorf("overwrite: on-disk = %q, want %q", onDisk, testSkillContent)
					}
				} else {
					// No-op case: on-disk must equal preExistingBody
					// (byte-identical), unchanged.
					onDisk, err := os.ReadFile(pathRaw)
					if err != nil {
						t.Fatalf("read %s: %v", pathRaw, err)
					}
					if !bytes.Equal(onDisk, tt.preExistingBody) {
						t.Errorf("no-op: file drifted: got %q, want %q", onDisk, tt.preExistingBody)
					}
				}
			} else {
				// Fresh-write case: file must exist with the embedded
				// content.
				onDisk, err := os.ReadFile(pathRaw)
				if err != nil {
					t.Fatalf("read %s: %v", pathRaw, err)
				}
				if !bytes.Equal(onDisk, []byte(testSkillContent)) {
					t.Errorf("on-disk = %q, want %q", onDisk, testSkillContent)
				}
			}
			if tt.wantWritten != nil {
				w, _ := obj["written"].(bool)
				if w != *tt.wantWritten {
					t.Errorf("written = %v, want %v", w, *tt.wantWritten)
				}
			}
			if tt.wantChanged != nil {
				c, _ := obj["changed"].(bool)
				if c != *tt.wantChanged {
					t.Errorf("changed = %v, want %v", c, *tt.wantChanged)
				}
			}
		})
	}
}

// TestRunSkills_WriteFailure_MkdirBlocked covers the "parent path is a
// file, not a dir" failure mode: runSkills' os.MkdirAll must surface
// the error so run can map it to exit 1 with a JSON error. The blocker
// is a regular file at the home path, so the skills/<name> child
// can't be created.
func TestRunSkills_WriteFailure_MkdirBlocked(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0644); err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	home := func(string) (string, error) { return blocker, nil }
	d := deps{
		skillLookup:  fakeSkillLookup(map[string][]byte{testSkillName: []byte(testSkillContent)}),
		homeResolver: home,
	}

	out := captureStdout(t, func() {
		code, err := runSkills([]string{testSkillName, "--agent", "codex"}, d)
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	if !strings.Contains(out, "create dir") {
		t.Errorf("stdout = %q, want it to mention the mkdir failure", out)
	}
}

// TestRunSkills_WriteFailure_ReadOnlyDir is a platform-portable
// alternative: a read-only parent dir causes WriteFile to fail. We
// chmod 0500 on the dir, run the install, expect exit 1, then chmod
// back so cleanup works. (Skipped on root because root bypasses 0500.)
func TestRunSkills_WriteFailure_ReadOnlyDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses read-only directory permissions; covered by the file-blocker test")
	}
	home := t.TempDir()
	ro := filepath.Join(home, "ro")
	if err := os.MkdirAll(ro, 0755); err != nil {
		t.Fatalf("mkdir ro: %v", err)
	}
	if err := os.Chmod(ro, 0500); err != nil {
		t.Fatalf("chmod ro: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0755) })

	d := deps{
		skillLookup:  fakeSkillLookup(map[string][]byte{testSkillName: []byte(testSkillContent)}),
		homeResolver: func(string) (string, error) { return ro, nil },
	}

	out := captureStdout(t, func() {
		code, err := runSkills([]string{testSkillName, "--agent", "codex"}, d)
		printError(err)
		if code != exitOperErr {
			t.Errorf("exit code = %d, want %d", code, exitOperErr)
		}
	})
	if !strings.Contains(out, "create dir") && !strings.Contains(out, "write") {
		t.Errorf("stdout = %q, want it to mention the create-dir or write failure", out)
	}
}

// TestResolveAgentHome_Production wires through the real resolveAgentHome
// (no DI override) to confirm the switch statement's three known values
// and the default-error branch all behave as expected. The "codex" case
// honors $CODEX_HOME via resolveCodexHome; the others depend on $HOME,
// so we set it to a temp dir for determinism.
func TestResolveAgentHome_Production(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "") // fall through to $HOME/.codex

	cases := []struct {
		agent   string
		wantSub string // path must end with this substring
		wantErr bool
	}{
		{"codex", ".codex", false},
		{"claude", ".claude", false},
		{"agents", ".agents", false},
		{"wat", "", true},
	}
	for _, tt := range cases {
		t.Run(tt.agent, func(t *testing.T) {
			got, err := resolveAgentHome(tt.agent)
			if tt.wantErr {
				if err == nil {
					t.Errorf("resolveAgentHome(%q): err = nil, want error", tt.agent)
				}
				if !strings.Contains(err.Error(), tt.agent) {
					t.Errorf("err = %q, want it to mention %q", err.Error(), tt.agent)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveAgentHome(%q): %v", tt.agent, err)
			}
			if !strings.HasSuffix(got, tt.wantSub) {
				t.Errorf("resolveAgentHome(%q) = %q, want suffix %q", tt.agent, got, tt.wantSub)
			}
		})
	}
}

// TestResolveAgentHome_CodexEnv verifies that resolveAgentHome("codex")
// honors $CODEX_HOME exactly the way resolveCodexHome() (and codex's own
// CLI) does — the production codex case in resolveAgentHome delegates
// to resolveCodexHome rather than re-implementing the env lookup.
func TestResolveAgentHome_CodexEnv(t *testing.T) {
	want := t.TempDir()
	t.Setenv("CODEX_HOME", want)
	got, err := resolveAgentHome("codex")
	if err != nil {
		t.Fatalf("resolveAgentHome(codex): %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q (honored $CODEX_HOME)", got, want)
	}
}
