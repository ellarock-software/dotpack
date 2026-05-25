package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestInstall_MCPServerOnClaudeCode_ProjectScope_FreshFile is the tracer
// bullet for hook + mcp-server kinds per ADR-0016 §5–§7 + §9. Smallest
// meaningful vertical slice: one kind (mcp-server) on one host
// (claude-code), JSON-only (no TOML), one scope (project), fresh file
// (no existing .mcp.json). Forces all the architectural elements:
//
//   - resource.MCPServer parser/validator + ADR §7 transport oneOf
//   - configfrag sibling adapter module (per advisor: not file-drop)
//   - claudecode shell as Kind-dispatcher (filedrop + configfrag)
//   - Plan returns MergedKeyWrite values (no Files for fragment kinds)
//   - Orchestrator: read-modify-write the target JSON file with the
//     plan's merged keys + persist them into manifest record
//   - Manifest: MergedKeys as []{File, Path} struct, not "file#path"
//     string (advisor pushback on the placeholder shape)
//   - Reader.Uninstall un-merges JSON keys (format-aware, no adapter —
//     advisor pushback on ADR §9's "requires adapter" future-note)
//
// What this test pins:
//
//  1. `dotpack install <src> --agent claude-code --kind mcp-server
//     --scope project` writes a .mcp.json at ProjectHome with the
//     resource's data under $.mcpServers.<name>.
//  2. Manifest record has ID claude-code:mcp-server:<name>, kind
//     mcp-server, merged_keys: [{file, path}] (structured, not string),
//     no files (config-fragment install owns no files).
//  3. Uninstall by ID removes the key from .mcp.json AND removes the
//     manifest record. The file itself stays (sibling entries may
//     exist; even in fresh-file case dotpack does not delete .mcp.json
//     because we don't own it).
//
// Scope choice: project, not user. Per schema/mcp-server.yaml claude
// reads .mcp.json (project) and ~/.claude.json (user); user-scope's
// canonical path is HOME-relative ($HOME/.claude.json), which is awkward
// in tests because $HOME isn't t.TempDir() and the dirs package
// currently has no HomeDir field. Project scope is cleaner for the
// tracer (just ProjectHome/.mcp.json) and is the dominant case for
// .mcp.json anyway. User scope is additive once the dirs.Dirs surface
// for "the directory holding .claude.json" is decided.
//
// Test will fail RED until tasks 2–10 land.
func TestInstall_MCPServerOnClaudeCode_ProjectScope_FreshFile(t *testing.T) {
	claudeHome := t.TempDir()
	projectHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "claude-code",
		"--kind", "mcp-server",
		"--scope", "project",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, stdout.String())
	}

	got := stdout.String()
	if !strings.Contains(got, "Installed claude-code:mcp-server:github") {
		t.Errorf("expected success message naming the umbrella ID; got %q", got)
	}

	mcpPath := filepath.Join(projectHome, ".mcp.json")
	raw, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse .mcp.json: %v\nfile content:\n%s", err, raw)
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if servers == nil {
		t.Fatalf("expected mcpServers map at root; got %s", raw)
	}
	entry, _ := servers["github"].(map[string]any)
	if entry == nil {
		t.Fatalf("expected mcpServers.github entry; got %s", raw)
	}
	if got := entry["command"]; got != "npx" {
		t.Errorf("mcpServers.github.command = %v; want npx", got)
	}
	args, _ := entry["args"].([]any)
	if len(args) != 2 || args[0] != "-y" {
		t.Errorf("mcpServers.github.args = %v; want [-y, @modelcontextprotocol/server-github]", args)
	}

	// Manifest record check — MergedKeys is the structured shape, not a
	// `"file#path"` string. The orchestrator persists (File, Path) so
	// uninstall can un-merge without re-deriving the file from the host
	// or the schema.
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
	if rec.ID != "claude-code:mcp-server:github" {
		t.Errorf("record ID = %q; want claude-code:mcp-server:github", rec.ID)
	}
	if rec.Kind != "mcp-server" {
		t.Errorf("record Kind = %q; want mcp-server", rec.Kind)
	}
	if rec.Agent != "claude-code" {
		t.Errorf("record Agent = %q; want claude-code", rec.Agent)
	}
	if len(rec.Files) != 0 {
		t.Errorf("config-fragment install must not claim files; got %v", rec.Files)
	}
	if len(rec.MergedKeys) != 1 {
		t.Fatalf("expected 1 merged_keys entry; got %d (%v)", len(rec.MergedKeys), rec.MergedKeys)
	}
	if rec.MergedKeys[0].File != mcpPath {
		t.Errorf("merged_keys[0].file = %q; want %q", rec.MergedKeys[0].File, mcpPath)
	}
	if rec.MergedKeys[0].Path != "$.mcpServers.github" {
		t.Errorf("merged_keys[0].path = %q; want $.mcpServers.github", rec.MergedKeys[0].Path)
	}

	// Uninstall removes the key from .mcp.json (format-aware un-merge in
	// Reader.Uninstall per advisor's refinement of ADR §9) and removes
	// the manifest record. The .mcp.json file itself MAY stay or be
	// removed when its mcpServers map empties; this test only pins that
	// $.mcpServers.github is gone after uninstall.
	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "claude-code:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if raw2, err := os.ReadFile(mcpPath); err == nil {
		var parsed2 map[string]any
		if jerr := json.Unmarshal(raw2, &parsed2); jerr == nil {
			if servers, ok := parsed2["mcpServers"].(map[string]any); ok {
				if _, exists := servers["github"]; exists {
					t.Errorf("expected $.mcpServers.github removed after uninstall; got %s", raw2)
				}
			}
		}
	}

	// Manifest record gone.
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
