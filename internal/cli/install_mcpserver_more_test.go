package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstall_MCPServerOnClaudeCode_PreExistingFile_SiblingsPreserved
// pins that merged-key installs do NOT clobber sibling entries when
// .mcp.json already has other manually-added or previously-installed
// servers. The applyJSONMergedKey read-modify-write path must reify
// sibling state.
func TestInstall_MCPServerOnClaudeCode_PreExistingFile_SiblingsPreserved(t *testing.T) {
	claudeHome := t.TempDir()
	projectHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	// Pre-populate .mcp.json with a sibling entry the user added by hand.
	mcpPath := filepath.Join(projectHome, ".mcp.json")
	pre := []byte(`{
  "mcpServers": {
    "linear": {
      "command": "npx",
      "args": ["-y", "@linear/mcp-server"]
    }
  }
}`)
	if err := os.WriteFile(mcpPath, pre, 0o644); err != nil {
		t.Fatalf("seed .mcp.json: %v", err)
	}

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "claude-code", "--kind", "mcp-server", "--scope", "project",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	raw, _ := os.ReadFile(mcpPath)
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	servers := parsed["mcpServers"].(map[string]any)
	if _, ok := servers["linear"]; !ok {
		t.Errorf("sibling 'linear' must survive the install of 'github'; got %s", raw)
	}
	if _, ok := servers["github"]; !ok {
		t.Errorf("github should be present after install; got %s", raw)
	}

	// Uninstall github — linear MUST still be present.
	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "claude-code:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	raw2, _ := os.ReadFile(mcpPath)
	var parsed2 map[string]any
	json.Unmarshal(raw2, &parsed2)
	servers2 := parsed2["mcpServers"].(map[string]any)
	if _, ok := servers2["github"]; ok {
		t.Errorf("github should be gone after uninstall; got %s", raw2)
	}
	if _, ok := servers2["linear"]; !ok {
		t.Errorf("sibling 'linear' must survive uninstall of 'github'; got %s", raw2)
	}
}

// TestInstall_MCPServerOnClaudeCode_UserEditedCollision_RefusedAndForced
// pins both halves of the collision contract for merged keys:
//
//   - Pre-existing .mcp.json with $.mcpServers.<name> NOT owned by any
//     manifest record → refused with CollisionError + --force hint.
//   - --force overrides → install completes and overwrites.
//
// Mirrors the file-collision shape from
// TestInstall_CollisionRefused_BypassedWithForce — the user-facing
// behaviour is the same regardless of whether the collision is a file
// or a config-fragment key.
func TestInstall_MCPServerOnClaudeCode_UserEditedCollision_RefusedAndForced(t *testing.T) {
	claudeHome := t.TempDir()
	projectHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	// User has manually authored a $.mcpServers.github entry with their
	// own data — no manifest record claims it.
	mcpPath := filepath.Join(projectHome, ".mcp.json")
	pre := []byte(`{"mcpServers":{"github":{"command":"echo","args":["user-customised"]}}}`)
	if err := os.WriteFile(mcpPath, pre, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "claude-code", "--kind", "mcp-server", "--scope", "project",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected CollisionError on user-edited entry; got nil")
	}
	if !strings.Contains(err.Error(), "collide") || !strings.Contains(err.Error(), "--force") {
		t.Errorf("CollisionError should describe collision + suggest --force; got %v", err)
	}
	if !strings.Contains(err.Error(), "$.mcpServers.github") {
		t.Errorf("CollisionError should name the colliding path; got %v", err)
	}
	// User's data still on disk (refusal short-circuits before apply).
	raw, _ := os.ReadFile(mcpPath)
	if !bytes.Contains(raw, []byte("user-customised")) {
		t.Errorf("refusal must not have overwritten user data; got %s", raw)
	}

	// --force succeeds and overwrites.
	cmd2 := NewRootCmd()
	cmd2.SetOut(io_DiscardWriter())
	cmd2.SetErr(io_DiscardWriter())
	cmd2.SetArgs([]string{
		"install", src,
		"--agent", "claude-code", "--kind", "mcp-server", "--scope", "project",
		"--force",
	})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("install --force: %v", err)
	}
	raw2, _ := os.ReadFile(mcpPath)
	var parsed map[string]any
	if err := json.Unmarshal(raw2, &parsed); err != nil {
		t.Fatalf("parse after --force: %v\n%s", err, raw2)
	}
	servers := parsed["mcpServers"].(map[string]any)
	entry := servers["github"].(map[string]any)
	if entry["command"] != "npx" {
		t.Errorf("--force should have overwritten with the resource's data; got command=%v", entry["command"])
	}
}

// TestInstall_MCPServerOnClaudeCode_ValidatorRejectsAmbiguousTransport
// pins ADR-0016 §7's discriminated-transport invariant: a source
// declaring both `command` and `url` is structurally ill-formed and
// must be rejected at parse-time, not silently coerced to one or the
// other.
func TestInstall_MCPServerOnClaudeCode_ValidatorRejectsAmbiguousTransport(t *testing.T) {
	claudeHome := t.TempDir()
	projectHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	tmpSrc := filepath.Join(t.TempDir(), "ambiguous.mcp.json")
	if err := os.WriteFile(tmpSrc, []byte(`{
		"mcpServers": {
			"ambiguous": {
				"command": "npx",
				"args": ["-y", "foo"],
				"url": "https://example.com/mcp"
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", tmpSrc,
		"--agent", "claude-code", "--kind", "mcp-server", "--scope", "project",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected validator rejection on command+url ambiguity; got nil")
	}
	if !strings.Contains(err.Error(), "validation") {
		t.Errorf("error should be a validation error; got %v", err)
	}
	if !strings.Contains(err.Error(), "command/url") {
		t.Errorf("validation error should name the transport-discriminator field; got %v", err)
	}

	// No .mcp.json written — validator runs before any adapter call.
	if _, err := os.Stat(filepath.Join(projectHome, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf("validator rejection must not have written .mcp.json; stat err=%v", err)
	}
}

// TestInstall_MCPServerOnClaudeCode_ParserRejectsMultiEntrySource pins
// the "one resource = one server entry" contract from ParseMCPServer.
// A multi-entry .mcp.json fragment is a translator-level concern, not
// a parser-level acceptance.
func TestInstall_MCPServerOnClaudeCode_ParserRejectsMultiEntrySource(t *testing.T) {
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	tmpSrc := filepath.Join(t.TempDir(), "multi.mcp.json")
	if err := os.WriteFile(tmpSrc, []byte(`{
		"mcpServers": {
			"github": {"command": "npx", "args": []},
			"linear": {"command": "npx", "args": []}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", tmpSrc,
		"--agent", "claude-code", "--kind", "mcp-server", "--scope", "project",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected parser rejection on multi-entry source; got nil")
	}
	if !strings.Contains(err.Error(), "one resource = one server") {
		t.Errorf("parser error should name the one-resource-one-server rule; got %v", err)
	}
}
