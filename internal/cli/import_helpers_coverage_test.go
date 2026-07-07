package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportHelperErrorAndUtilityBranches(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "file")
	mustWrite(t, file, "x")
	if _, err := resolveClaudeImportRoot(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("resolveClaudeImportRoot file err=%v; want not directory", err)
	}
	if _, err := resolveClaudeImportRoot(filepath.Join(tmp, "missing")); err == nil || !strings.Contains(err.Error(), "source path") {
		t.Fatalf("resolveClaudeImportRoot missing err=%v; want source path", err)
	}
	if _, err := resolveClaudeImportRoot(tmp); err == nil || !strings.Contains(err.Error(), "neither") {
		t.Fatalf("resolveClaudeImportRoot no .claude err=%v; want neither", err)
	}

	if !shouldSkipClaudeImportFile("x.local.json") || !shouldSkipClaudeImportFile("pnpm.lock") || !shouldSkipClaudeImportFile("events.ledger") || shouldSkipClaudeImportFile("keep.md") {
		t.Fatal("shouldSkipClaudeImportFile returned unexpected values")
	}
	for _, path := range []string{"x.json", "x.md", "x.mjs", "x.js", "x.ts", "x.sh", "x.py", "x.toml", "x.yaml", "x.yml"} {
		if !shouldRewriteClaudeImportFile(path) {
			t.Fatalf("shouldRewriteClaudeImportFile(%s)=false; want true", path)
		}
	}
	if shouldRewriteClaudeImportFile("x.png") {
		t.Fatal("png should not be rewritten")
	}
	if normalizeFileMode(0o755) != 0o755 || normalizeFileMode(0o600) != 0o644 {
		t.Fatal("normalizeFileMode returned unexpected values")
	}
	if string(rewriteClaudePathRefs([]byte(".claude/hooks and .claude"))) != ".agents/hooks and .agents" {
		t.Fatal("rewriteClaudePathRefs did not rewrite expected refs")
	}
}

func TestClaudeImporterCopyMaybeAndSettingsBranches(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, ".claude")
	agents := filepath.Join(tmp, ".agents")
	imp := claudeImporter{claudeRoot: claude, agentsRoot: agents, written: map[string]struct{}{}}
	if err := imp.copyMaybe(filepath.Join(claude, "missing"), filepath.Join(agents, "missing")); err != nil {
		t.Fatalf("copyMaybe missing should no-op: %v", err)
	}
	mustWrite(t, filepath.Join(claude, "src.txt"), "x")
	if err := imp.copyTree(filepath.Join(claude, "src.txt"), filepath.Join(agents, "dst.txt")); err != nil {
		t.Fatalf("copyTree file branch: %v", err)
	}
	if got := readFile(t, filepath.Join(agents, "dst.txt")); got != "x" {
		t.Fatalf("copyTree file copied %q", got)
	}
	if sameResolvedPath(filepath.Join(claude, "src.txt"), filepath.Join(agents, "dst.txt")) {
		t.Fatal("different files should not be sameResolvedPath")
	}

	mustWrite(t, filepath.Join(claude, "settings.json"), `{"env":{"A":"B"}}`)
	if err := imp.importSettings(); err != nil {
		t.Fatalf("importSettings without hooks: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agents, "hooks", "registry.json")); !os.IsNotExist(err) {
		t.Fatalf("registry should not be written when settings has no hooks: %v", err)
	}
	imp.force = true
	mustWrite(t, filepath.Join(claude, "settings.json"), `{bad`)
	if err := imp.importSettings(); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("bad settings err=%v; want parse", err)
	}

	if err := imp.run(); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("run should propagate importSettings parse error=%v; want parse", err)
	}

	imp.force = false
	mustWrite(t, filepath.Join(claude, "settings.json"), `{"hooks":{"PreToolUse":[]}}`)
	mustWrite(t, filepath.Join(agents, "config", "claude-code.settings.json"), "{}")
	if err := imp.importSettings(); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("importSettings existing generated config err=%v; want refusing to overwrite", err)
	}

	imp.force = true
	dstDir := filepath.Join(agents, "generated-dir.json")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir generated dir: %v", err)
	}
	if err := imp.writeGeneratedJSON(dstDir, []byte("{}")); err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("writeGeneratedJSON directory err=%v; want write", err)
	}

	for _, rel := range []string{"judge/logs/x.json", "judge/verdicts/x.json", "judge/lemons/x.json"} {
		entry, err := os.Stat(tmp)
		if err != nil {
			t.Fatalf("stat temp dir: %v", err)
		}
		if !shouldSkipClaudeImportPath(rel, fsFileInfoDirEntry{FileInfo: entry}) {
			t.Fatalf("shouldSkipClaudeImportPath(%q)=false; want true", rel)
		}
	}
}

type fsFileInfoDirEntry struct {
	os.FileInfo
}

func (e fsFileInfoDirEntry) Type() os.FileMode          { return e.Mode().Type() }
func (e fsFileInfoDirEntry) Info() (os.FileInfo, error) { return e.FileInfo, nil }
