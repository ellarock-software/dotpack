package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// setupCodexMCPEnv mirrors setupCodexEnv (codex_test.go) but adds
// DOTPACK_CODEX_HOME — config.toml lives under CodexHome (not
// AgentsHome). The two roots are distinct: AgentsHome is the cross-host
// skill convergence path; CodexHome is codex-specific for config.toml.
func setupCodexMCPEnv(t *testing.T) (codexHome, dotpackHome string) {
	t.Helper()
	codexHome = t.TempDir()
	dotpackHome = t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", codexHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	return codexHome, dotpackHome
}

// TestInstall_MCPServerOnCodex_UserScope_FreshFile is the tracer-bullet
// for the codex mcp-server slice per ADR-0012 §5–§7 + ADR-0010. Smallest
// meaningful vertical slice: one kind (mcp-server) on codex, TOML format,
// user scope, fresh config.toml (no existing file). Forces all new
// architectural elements:
//
//   - pelletier/go-toml/v2 dep (ADR-0012 §4 sanctioned)
//   - orchestrator.applyTOMLMergedKey / unmergeTOMLKey (the format-
//     specific walkers around the format-agnostic map primitives)
//   - codex.configfragPolicy() + emitMCPServerCodex (mirror of claudecode
//     pattern from slice v15; mcp_servers snake_case wrapper key)
//   - DOTPACK_CODEX_HOME env var + dirs.CodexHome field (distinct from
//     AgentsHome — skills converge at AgentsHome/skills/, config.toml
//     is codex-specific at CodexHome)
//
// What this test pins:
//
//  1. `dotpack install <src> --agent codex --kind mcp-server --scope user`
//     writes ~/.codex/config.toml with `[mcp_servers.github]` containing
//     the resource's universal-core fields.
//  2. Manifest record has ID codex:mcp-server:<name>, merged_keys: [{
//     file, path}] with path = "mcp_servers.github" (DOTTED, no `$.`
//     prefix — TOML-native syntax, not JSON-style).
//  3. Uninstall by ID removes mcp_servers.github from config.toml AND
//     the manifest record. Sibling [mcp_servers.<other>] entries (when
//     present in other scenarios below) survive untouched.
func TestInstall_MCPServerOnCodex_UserScope_FreshFile(t *testing.T) {
	codexHome, dotpackHome := setupCodexMCPEnv(t)

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "codex",
		"--kind", "mcp-server",
		"--scope", "user",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, stdout.String())
	}

	got := stdout.String()
	if !strings.Contains(got, "Installed codex:mcp-server:github") {
		t.Errorf("expected success message naming the umbrella ID; got %q", got)
	}

	configPath := filepath.Join(codexHome, "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse config.toml: %v\nfile content:\n%s", err, raw)
	}
	servers, _ := parsed["mcp_servers"].(map[string]any)
	if servers == nil {
		t.Fatalf("expected mcp_servers map at root; got %s", raw)
	}
	entry, _ := servers["github"].(map[string]any)
	if entry == nil {
		t.Fatalf("expected mcp_servers.github entry; got %s", raw)
	}
	if entry["command"] != "npx" {
		t.Errorf("mcp_servers.github.command = %v; want npx", entry["command"])
	}
	args, _ := entry["args"].([]any)
	if len(args) != 2 || args[0] != "-y" || args[1] != "@modelcontextprotocol/server-github" {
		t.Errorf("mcp_servers.github.args = %v; want [-y, @modelcontextprotocol/server-github]", args)
	}
	env, _ := entry["env"].(map[string]any)
	if env == nil || env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "${GITHUB_TOKEN}" {
		t.Errorf("mcp_servers.github.env should pass through verbatim; got %v", env)
	}

	// Manifest record check — MergedKeys path is "mcp_servers.github"
	// (dotted, TOML-native), not "$.mcpServers.github" (JSON-style).
	type mkPersisted struct {
		File string `yaml:"file"`
		Path string `yaml:"path"`
	}
	var manifestRaw struct {
		Installs []struct {
			ID         string        `yaml:"id"`
			Kind       string        `yaml:"kind"`
			Agent      string        `yaml:"agent"`
			Files      []string      `yaml:"files,omitempty"`
			MergedKeys []mkPersisted `yaml:"merged_keys,omitempty"`
		} `yaml:"installs"`
	}
	mr, err := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := yaml.Unmarshal(mr, &manifestRaw); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, mr)
	}
	if len(manifestRaw.Installs) != 1 {
		t.Fatalf("expected 1 install record; got %d (%s)", len(manifestRaw.Installs), mr)
	}
	rec := manifestRaw.Installs[0]
	if rec.ID != "codex:mcp-server:github" {
		t.Errorf("record ID = %q; want codex:mcp-server:github", rec.ID)
	}
	if rec.Kind != "mcp-server" {
		t.Errorf("record Kind = %q; want mcp-server", rec.Kind)
	}
	if rec.Agent != "codex" {
		t.Errorf("record Agent = %q; want codex", rec.Agent)
	}
	if len(rec.Files) != 0 {
		t.Errorf("config-fragment install must not claim files; got %v", rec.Files)
	}
	if len(rec.MergedKeys) != 1 {
		t.Fatalf("expected 1 merged_keys entry; got %d (%v)", len(rec.MergedKeys), rec.MergedKeys)
	}
	if rec.MergedKeys[0].File != configPath {
		t.Errorf("merged_keys[0].file = %q; want %q", rec.MergedKeys[0].File, configPath)
	}
	if rec.MergedKeys[0].Path != "mcp_servers.github" {
		t.Errorf("merged_keys[0].path = %q; want mcp_servers.github (TOML-dotted, no $ prefix)", rec.MergedKeys[0].Path)
	}

	// Uninstall removes the key from config.toml (format-aware un-merge)
	// AND the manifest record.
	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "codex:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if raw2, err := os.ReadFile(configPath); err == nil {
		var parsed2 map[string]any
		if jerr := toml.Unmarshal(raw2, &parsed2); jerr == nil {
			if servers2, ok := parsed2["mcp_servers"].(map[string]any); ok {
				if _, exists := servers2["github"]; exists {
					t.Errorf("expected mcp_servers.github removed after uninstall; got %s", raw2)
				}
			}
		}
	}

	mr2, err := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if err != nil {
		t.Fatalf("read manifest after uninstall: %v", err)
	}
	manifestRaw.Installs = nil
	if err := yaml.Unmarshal(mr2, &manifestRaw); err != nil {
		t.Fatalf("parse manifest after uninstall: %v", err)
	}
	if len(manifestRaw.Installs) != 0 {
		t.Errorf("expected 0 install records after uninstall; got %d", len(manifestRaw.Installs))
	}
}

// TestInstall_MCPServerOnCodex_PreservesCodexSupersetExtensions pins
// the schema/mcp-server.yaml + ADR-0010 promise that codex emit
// preserves the codex superset verbatim. Source has `enabled`,
// `startup_timeout_sec`, `http_headers` (all schema-listed extensions);
// emit must keep them on the value. Also pins the integral-float64 →
// int64 coercion in orchestrator.normalizeForTOML — the JSON-source
// number 30 must render as `30` in TOML, not `30.0` (would be visually
// confusing in a config file a human diffs).
func TestInstall_MCPServerOnCodex_PreservesCodexSupersetExtensions(t *testing.T) {
	codexHome, _ := setupCodexMCPEnv(t)

	src := filepath.Join(t.TempDir(), "codex-rich.mcp.json")
	if err := os.WriteFile(src, []byte(`{
		"mcpServers": {
			"codex-rich": {
				"command": "node",
				"args": ["server.js"],
				"enabled": true,
				"startup_timeout_sec": 30,
				"http_headers": {"Authorization": "Bearer ${TOKEN}"},
				"enabled_tools": ["read", "write"]
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "codex", "--kind", "mcp-server", "--scope", "user",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	asStr := string(raw)
	// Integral float64 → int64 coercion: emit must show `30`, not `30.0`.
	if strings.Contains(asStr, "30.0") {
		t.Errorf("integral number rendered as float; got `30.0` in:\n%s", asStr)
	}
	if !strings.Contains(asStr, "startup_timeout_sec = 30") {
		t.Errorf("expected `startup_timeout_sec = 30` in output; got:\n%s", asStr)
	}

	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	entry := parsed["mcp_servers"].(map[string]any)["codex-rich"].(map[string]any)
	if entry["enabled"] != true {
		t.Errorf("enabled should be passed through verbatim; got %v", entry["enabled"])
	}
	// startup_timeout_sec arrives as int64 from toml.Unmarshal (the
	// coercion's whole point).
	if got, ok := entry["startup_timeout_sec"].(int64); !ok || got != 30 {
		t.Errorf("startup_timeout_sec should be int64(30); got %T %v", entry["startup_timeout_sec"], entry["startup_timeout_sec"])
	}
	headers, _ := entry["http_headers"].(map[string]any)
	if headers == nil || headers["Authorization"] != "Bearer ${TOKEN}" {
		t.Errorf("http_headers must pass through verbatim; got %v", headers)
	}
	tools, _ := entry["enabled_tools"].([]any)
	if len(tools) != 2 || tools[0] != "read" || tools[1] != "write" {
		t.Errorf("enabled_tools must pass through verbatim; got %v", tools)
	}
}

// TestInstall_MCPServerOnCodex_TwoSiblings_Coexist pins that installing
// two mcp-servers into the same config.toml preserves both. The
// orchestrator's read-modify-write merges into the existing TOML's
// mcp_servers table rather than overwriting it.
func TestInstall_MCPServerOnCodex_TwoSiblings_Coexist(t *testing.T) {
	codexHome, _ := setupCodexMCPEnv(t)

	srcA := filepath.Join(t.TempDir(), "alpha.mcp.json")
	if err := os.WriteFile(srcA, []byte(`{"mcpServers":{"alpha":{"command":"a","args":["x"]}}}`), 0o644); err != nil {
		t.Fatalf("write alpha: %v", err)
	}
	srcB := filepath.Join(t.TempDir(), "beta.mcp.json")
	if err := os.WriteFile(srcB, []byte(`{"mcpServers":{"beta":{"command":"b","args":["y"]}}}`), 0o644); err != nil {
		t.Fatalf("write beta: %v", err)
	}

	for _, src := range []string{srcA, srcB} {
		cmd := NewRootCmd()
		cmd.SetOut(io_DiscardWriter())
		cmd.SetErr(io_DiscardWriter())
		cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("install %s: %v", src, err)
		}
	}

	raw, _ := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	servers := parsed["mcp_servers"].(map[string]any)
	if _, ok := servers["alpha"]; !ok {
		t.Errorf("alpha should still be in config.toml after beta install; got:\n%s", raw)
	}
	if _, ok := servers["beta"]; !ok {
		t.Errorf("beta should be in config.toml; got:\n%s", raw)
	}

	// Uninstall alpha; beta survives.
	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "codex:mcp-server:alpha"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall alpha: %v", err)
	}
	raw2, _ := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	var parsed2 map[string]any
	if err := toml.Unmarshal(raw2, &parsed2); err != nil {
		t.Fatalf("parse after uninstall: %v", err)
	}
	servers2 := parsed2["mcp_servers"].(map[string]any)
	if _, ok := servers2["alpha"]; ok {
		t.Errorf("alpha should be gone after uninstall; got:\n%s", raw2)
	}
	if _, ok := servers2["beta"]; !ok {
		t.Errorf("beta must survive alpha's uninstall; got:\n%s", raw2)
	}
}

// TestInstall_MCPServerOnCodex_PreservesUserAuthoredEntry pins the
// claim that codex mcp-server install does NOT clobber a user-authored
// entry already in config.toml. The user's `[mcp_servers.linear]`
// survives an install of `github`.
func TestInstall_MCPServerOnCodex_PreservesUserAuthoredEntry(t *testing.T) {
	codexHome, _ := setupCodexMCPEnv(t)

	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(`[mcp_servers.linear]
command = "linear-bin"
args = ["--token", "${LINEAR_TOKEN}"]
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	raw, _ := os.ReadFile(configPath)
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	servers := parsed["mcp_servers"].(map[string]any)
	linear, _ := servers["linear"].(map[string]any)
	if linear == nil {
		t.Fatalf("user-authored linear must survive install of github; got:\n%s", raw)
	}
	if linear["command"] != "linear-bin" {
		t.Errorf("linear.command corrupted; got %v", linear["command"])
	}
	// User-authored args field survives byte-equivalently. Earlier
	// drafts of this test only checked .command; that left a gap where
	// emit re-wrote linear's args (a different code path) silently.
	linearArgs, _ := linear["args"].([]any)
	if len(linearArgs) != 2 || linearArgs[0] != "--token" || linearArgs[1] != "${LINEAR_TOKEN}" {
		t.Errorf("user-authored linear.args corrupted; got %v", linearArgs)
	}
	if _, ok := servers["github"]; !ok {
		t.Errorf("github should be added; got:\n%s", raw)
	}
}

// TestInstall_MCPServerOnCodex_PreservesFileMode mirrors the claudecode
// hostile-review #2 fix for TOML: a 0o600-mode config.toml carrying
// inlined credentials (per schema/mcp-server.yaml's ecosystem_notes —
// abcdan-style args, arc-kit-style headers) must survive the
// read-modify-write with mode preserved.
func TestInstall_MCPServerOnCodex_PreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode preservation is POSIX-specific")
	}
	codexHome, _ := setupCodexMCPEnv(t)

	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(`[mcp_servers.existing]
command = "existing-bin"
args = []
`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	st, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("config.toml mode should be preserved at 0o600 after install; got %o", got)
	}
}

// TestInstall_MCPServerOnCodex_SymlinkTargetRefused mirrors the
// claudecode hostile-review #3 fix for TOML. The symlink defense lives
// in orchestrator.preflightMergedKeyCollisions and is format-agnostic
// — same code path catches both .mcp.json and config.toml symlinks.
func TestInstall_MCPServerOnCodex_SymlinkTargetRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows; not exercised on this platform")
	}
	codexHome, _ := setupCodexMCPEnv(t)

	realDir := t.TempDir()
	realFile := filepath.Join(realDir, "canonical.toml")
	if err := os.WriteFile(realFile, []byte("# real codex config\n"), 0o644); err != nil {
		t.Fatalf("seed real: %v", err)
	}
	link := filepath.Join(codexHome, "config.toml")
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected CollisionError refusing to replace symlink; got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should name the symlink refusal; got %v", err)
	}

	st, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink should survive refusal; got mode %v", st.Mode())
	}
}

// TestInstall_MCPServerOnCodex_ReinstallSymlinkReplacement_Refused
// pins the slice v16 hostile-review #1 fix carried into TOML. The
// symlink defense in preflightMergedKeyCollisions runs BEFORE the
// same-ID short-circuit, so a re-install of a manifest-claimed codex
// mcp-server into a now-symlinked config.toml refuses rather than
// silently rewriting through the symlink.
//
// Without this test a future refactor that "simplifies" by moving the
// same-ID check earlier would silently regress on TOML — the JSON
// case has explicit hook-slice tests pinning the invariant; codex
// needs symmetric coverage.
func TestInstall_MCPServerOnCodex_ReinstallSymlinkReplacement_Refused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows; not exercised on this platform")
	}
	codexHome, _ := setupCodexMCPEnv(t)

	// First install — succeeds, claims a manifest record.
	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	if err := install.Execute(); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// User replaces config.toml with a symlink to somewhere else
	// between installs. Re-install must refuse rather than silently
	// rewriting through the symlink.
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("remove real file: %v", err)
	}
	realDir := t.TempDir()
	realFile := filepath.Join(realDir, "attacker-target.toml")
	if err := os.WriteFile(realFile, []byte("# attacker-controlled\n"), 0o644); err != nil {
		t.Fatalf("write attacker target: %v", err)
	}
	if err := os.Symlink(realFile, configPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	reinstall := NewRootCmd()
	reinstall.SetOut(io_DiscardWriter())
	reinstall.SetErr(io_DiscardWriter())
	reinstall.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	err := reinstall.Execute()
	if err == nil {
		t.Fatal("expected re-install to refuse symlinked config.toml; got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should name the symlink refusal; got %v", err)
	}

	// Attacker target untouched.
	attackerRaw, _ := os.ReadFile(realFile)
	if string(attackerRaw) != "# attacker-controlled\n" {
		t.Errorf("attacker target was rewritten through symlink; got %q", string(attackerRaw))
	}
}

// TestUninstall_MCPServerOnCodex_TolerantOfNulledParent mirrors the
// claudecode hostile-review #4 tolerance for TOML. The user manually
// removes [mcp_servers] between install and uninstall; uninstall must
// proceed (manifest record cleanup; the leaf is effectively gone).
func TestUninstall_MCPServerOnCodex_TolerantOfNulledParent(t *testing.T) {
	codexHome, _ := setupCodexMCPEnv(t)

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	// User manually rewrites config.toml without the mcp_servers table.
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("# user manually cleared mcp_servers\n"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "codex:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall must tolerate user-cleared parent; got %v", err)
	}
}

// TestUninstall_MCPServerOnCodex_IdempotentUninstall_DoesNotTouchFile
// pins hostile-review #5 from THIS slice. Earlier drafts always rewrote
// config.toml on uninstall even when the entry was already gone — the
// re-marshal would re-sort keys, normalize string quote style, and
// (without the #1 fix) coerce user-authored floats. Even with #1 fixed,
// touching the file for a no-op uninstall is hygiene noise the user sees
// in their diff.
//
// Fix: deleteJSONPath now returns (changed, err); unmergeJSONKey and
// unmergeTOMLKey skip the writeTOML/writeJSON call when changed=false.
//
// This test pins: install, uninstall once (real removal), uninstall the
// same ID again (already gone — but the manifest record is also gone,
// so re-uninstall errors. Instead pin the byte-equality of the file
// across a different scenario: install + manually delete the key via
// file edit + uninstall must leave the file untouched.
func TestUninstall_MCPServerOnCodex_IdempotentUninstall_DoesNotTouchFile(t *testing.T) {
	codexHome, _ := setupCodexMCPEnv(t)

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	// User manually rewrites config.toml: removes our entry but leaves
	// their own untouched. Distinctive whitespace + comment to detect
	// any byte-touch.
	configPath := filepath.Join(codexHome, "config.toml")
	preBytes := []byte(`# user-authored header — must survive
profile = "user-default"

[mcp_servers.linear]
command = "linear-bin"
args   = [   "--verbose"   ]
`)
	if err := os.WriteFile(configPath, preBytes, 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	preStat, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat pre: %v", err)
	}
	preMode := preStat.Mode().Perm()

	// Uninstall the github entry — it's not in the file anymore (user
	// manually removed it). unmergeTOMLKey's deleteJSONPath returns
	// changed=false; writeTOML must be skipped.
	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "codex:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	postBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read post: %v", err)
	}
	if !bytes.Equal(preBytes, postBytes) {
		t.Errorf("idempotent uninstall must not touch file bytes; got diff:\nbefore:\n%s\nafter:\n%s", preBytes, postBytes)
	}

	postStat, _ := os.Stat(configPath)
	if postStat.Mode().Perm() != preMode {
		t.Errorf("idempotent uninstall must not touch file mode; before %o, after %o", preMode, postStat.Mode().Perm())
	}
}

// TestInstall_MCPServerOnCodex_ExplicitEmptyArgsAccepted mirrors the
// claudecode hostile-review #1 fix for TOML emit. Source has `args:
// []` (explicit empty, not absent); emit must preserve the empty
// array. ParseMCPServer's nil-vs-empty distinction (hostile-review #1
// from slice v15) is upstream of the host adapter; this test pins the
// codex emit doesn't drop the field when it's empty.
func TestInstall_MCPServerOnCodex_ExplicitEmptyArgsAccepted(t *testing.T) {
	codexHome, _ := setupCodexMCPEnv(t)

	src := filepath.Join(t.TempDir(), "noargs.mcp.json")
	if err := os.WriteFile(src, []byte(`{
		"mcpServers": {
			"noargs": {
				"command": "noargs-binary",
				"args": []
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install with explicit empty args should succeed; got %v", err)
	}

	raw, _ := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	entry := parsed["mcp_servers"].(map[string]any)["noargs"].(map[string]any)
	args, ok := entry["args"].([]any)
	if !ok {
		t.Fatalf("args should be present in emit (explicit empty preserved); got %v", entry)
	}
	if len(args) != 0 {
		t.Errorf("args should be empty array; got %v", args)
	}
}

// TestInstall_MCPServerOnCodex_PreservesUserAuthoredFloatSyntax pins
// the hostile-review #1 fix from this slice. Earlier drafts ran
// normalizeForTOML on the entire root inside writeTOML, which silently
// demoted a user-authored `version = 1.0` (toml.Unmarshal → float64(1.0))
// to int64(1) on every dotpack-touched write. Direct violation of "we
// don't own this file."
//
// The fix moved normalizeForTOML into applyTOMLMergedKey, applied to
// mk.Value only (the JSON-sourced fragment that needs coercion).
// writeTOML now just marshals the root unchanged — toml.Unmarshal
// already returns int64 for ints and float64 for floats, so the user's
// explicit syntax survives.
//
// Without the fix this test fails at the version assertion: the user
// authored `version = 1.0` (TOML float) and finds `version = 1` (TOML
// int) after dotpack runs.
func TestInstall_MCPServerOnCodex_PreservesUserAuthoredFloatSyntax(t *testing.T) {
	codexHome, _ := setupCodexMCPEnv(t)

	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(`version = 1.0
profile = "default"

[mcp_servers.existing]
command = "x"
args = []
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	raw, _ := os.ReadFile(configPath)
	asStr := string(raw)
	// The killer assertion: `version = 1.0` survives, NOT `version = 1`.
	// go-toml/v2 emits float64(1.0) as `1.0` and int64(1) as `1`; a
	// failed normalizeForTOML escape would show `version = 1`.
	if !strings.Contains(asStr, "version = 1.0") {
		t.Errorf("user-authored `version = 1.0` must survive dotpack write; got:\n%s", asStr)
	}
	if strings.Contains(asStr, "version = 1\n") {
		t.Errorf("user-authored float demoted to int (hostile-review #1 regression); got:\n%s", asStr)
	}

	// Re-parse to confirm type is still float64 (not int64).
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := parsed["version"].(float64); !ok {
		t.Errorf("version field type must remain float64; got %T = %v", parsed["version"], parsed["version"])
	}

	// Uninstall should also not corrupt.
	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "codex:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	raw2, _ := os.ReadFile(configPath)
	if !strings.Contains(string(raw2), "version = 1.0") {
		t.Errorf("user-authored `version = 1.0` must survive uninstall; got:\n%s", raw2)
	}
}

// TestInstall_MCPServerOnCodex_ReinstallReplaces pins that a second
// install of the same resource (same ID) overwrites the existing
// entry rather than refusing. Re-install = uninstall + install
// semantics for Op=Set: the new value replaces in place (no array
// reordering issues here — mcp-server is a single leaf, not a list).
func TestInstall_MCPServerOnCodex_ReinstallReplaces(t *testing.T) {
	codexHome, _ := setupCodexMCPEnv(t)

	srcDir := t.TempDir()
	v1 := filepath.Join(srcDir, "v1.mcp.json")
	if err := os.WriteFile(v1, []byte(`{"mcpServers":{"echo":{"command":"v1-bin","args":["x"]}}}`), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	v2 := filepath.Join(srcDir, "v2.mcp.json")
	if err := os.WriteFile(v2, []byte(`{"mcpServers":{"echo":{"command":"v2-bin","args":["x","y"]}}}`), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}

	for _, src := range []string{v1, v2} {
		cmd := NewRootCmd()
		cmd.SetOut(io_DiscardWriter())
		cmd.SetErr(io_DiscardWriter())
		cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("install %s: %v", src, err)
		}
	}

	raw, _ := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	echo := parsed["mcp_servers"].(map[string]any)["echo"].(map[string]any)
	if echo["command"] != "v2-bin" {
		t.Errorf("re-install should replace; got command=%v in:\n%s", echo["command"], raw)
	}
	args, _ := echo["args"].([]any)
	if len(args) != 2 || args[1] != "y" {
		t.Errorf("re-install should replace args; got %v", args)
	}
}
