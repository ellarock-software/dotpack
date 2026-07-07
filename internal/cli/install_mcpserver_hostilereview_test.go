package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Regression tests for hostile-review findings #1–#4 on the mcp-server
// + configfrag slice. Each test pins an invariant whose absence would
// reintroduce the original finding.

// Finding #1: ParseMCPServer must preserve explicit "args": [] (empty
// non-nil slice) — otherwise the validator's nil-vs-empty distinction
// rejects what the validator docstring promises to accept.
func TestInstall_MCPServerOnClaudeCode_ExplicitEmptyArgsAccepted(t *testing.T) {
	projectHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

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
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "claude-code", "--kind", "mcp-server", "--scope", "project",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install with explicit empty args should succeed; got %v", err)
	}

	raw, _ := os.ReadFile(filepath.Join(projectHome, ".mcp.json"))
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	entry := parsed["mcpServers"].(map[string]any)["noargs"].(map[string]any)
	args, ok := entry["args"].([]any)
	if !ok {
		t.Fatalf("args should be present in emit; got %v", entry)
	}
	if len(args) != 0 {
		t.Errorf("args should be empty (preserved from source); got %v", args)
	}
}

// Finding #2: writeJSON must preserve an existing file's mode rather
// than relaxing to 0o644 — .mcp.json can carry inlined credentials per
// schema/mcp-server.yaml's ecosystem_notes.
//
// Skipped on Windows: Go's os.Stat returns synthetic permission bits
// that don't reflect filesystem state for that platform.
func TestInstall_MCPServerOnClaudeCode_PreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode preservation is POSIX-specific")
	}
	projectHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	mcpPath := filepath.Join(projectHome, ".mcp.json")
	// Seed the file with 0o600 (secrets-bearing convention) and one
	// pre-existing entry we don't own. The install merges into it.
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{"linear":{"command":"npx","args":[]}}}`), 0o600); err != nil {
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
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	st, err := os.Stat(mcpPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode should be preserved at 0o600 after install; got %o", got)
	}
}

// Finding #3: symlink targets must NOT be silently followed-and-replaced
// at the merged-key path. preflightMergedKeyCollisions surfaces the
// symlink as a CollisionError; without --force the install refuses.
func TestInstall_MCPServerOnClaudeCode_SymlinkTargetRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows; not exercised on this platform")
	}
	projectHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	// Real .mcp.json elsewhere; project root's .mcp.json is a symlink.
	realDir := t.TempDir()
	realFile := filepath.Join(realDir, "canonical.mcp.json")
	if err := os.WriteFile(realFile, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("seed real: %v", err)
	}
	link := filepath.Join(projectHome, ".mcp.json")
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatalf("symlink: %v", err)
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
		t.Fatal("expected CollisionError refusing to replace symlink; got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should name the symlink refusal; got %v", err)
	}

	// Symlink survives the refusal.
	st, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink should survive refusal; got mode %v", st.Mode())
	}
}

// Finding #4: uninstall must tolerate a manually-corrupted parent
// (e.g., user nulled out $.mcpServers between install and uninstall).
// The merged-key delete walks past the non-map intermediate as a
// no-op rather than refusing — matching the "drift on uninstall is
// intentional" principle documented on Reader.Uninstall.
func TestUninstall_MCPServerOnClaudeCode_TolerantOfNulledParent(t *testing.T) {
	projectHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

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

	// User manually corrupts the file (nulls out the parent).
	mcpPath := filepath.Join(projectHome, ".mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":null}`), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	// Uninstall must succeed despite the user's corruption — the leaf
	// is effectively gone, manifest record cleanup proceeds.
	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "claude-code:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall must tolerate user-corrupted parent; got %v", err)
	}
}
