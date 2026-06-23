package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeImporterRunCopiesSettingsHooksAndSkipsRuntimeState(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, ".claude")
	agents := filepath.Join(tmp, ".agents")
	mustWrite(t, filepath.Join(claude, "agents", "helper.md"), ".claude/helper\n")
	mustWrite(t, filepath.Join(claude, "judge", "state", "skip.json"), "skip")
	mustWrite(t, filepath.Join(claude, "node_modules", "skip.js"), "skip")
	mustWrite(t, filepath.Join(claude, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\n.claude\n")
	mustWrite(t, filepath.Join(claude, "skills", "s", ".DS_Store"), "skip")
	mustWrite(t, filepath.Join(claude, "settings.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo .claude"}]}]}}`)

	imp := claudeImporter{claudeRoot: claude, agentsRoot: agents, force: false, written: map[string]struct{}{}}
	if err := imp.run(); err != nil {
		t.Fatalf("import run: %v", err)
	}
	for _, path := range []string{
		filepath.Join(agents, "agents", "claude", "helper.md"),
		filepath.Join(agents, "skills", "s", "SKILL.md"),
		filepath.Join(agents, "config", "claude-code.settings.json"),
		filepath.Join(agents, "hooks", "registry.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected imported file %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(agents, "judge", "state", "skip.json")); !os.IsNotExist(err) {
		t.Fatalf("judge/state should be skipped, stat err=%v", err)
	}
	if got := readFile(t, filepath.Join(agents, "skills", "s", "SKILL.md")); !strings.Contains(got, ".agents") || strings.Contains(got, ".claude") {
		t.Fatalf("skill path refs not rewritten:\n%s", got)
	}
}

func TestClaudeImporterCopySymlinkSamePathAndWriteErrors(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, ".claude")
	agents := filepath.Join(tmp, ".agents")
	imp := claudeImporter{claudeRoot: claude, agentsRoot: agents, written: map[string]struct{}{}}

	src := filepath.Join(claude, "src.txt")
	mustWrite(t, src, "x")
	sameLink := filepath.Join(claude, "same-link.txt")
	if err := os.Symlink(src, sameLink); err != nil {
		t.Fatalf("symlink same: %v", err)
	}
	if err := imp.copyMaybe(src, sameLink); err != nil {
		t.Fatalf("copyMaybe same resolved path should no-op: %v", err)
	}

	link := filepath.Join(claude, "link.txt")
	if err := os.Symlink(src, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := imp.copyMaybe(link, filepath.Join(agents, "linked.txt")); err != nil {
		t.Fatalf("copyMaybe symlink: %v", err)
	}
	if got := readFile(t, filepath.Join(agents, "linked.txt")); got != "x" {
		t.Fatalf("copied symlink content = %q", got)
	}

	existing := filepath.Join(agents, "generated.json")
	mustWrite(t, existing, "{}")
	if err := imp.writeGeneratedJSON(existing, []byte("{}")); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("writeGeneratedJSON existing err=%v; want refusing", err)
	}
	parentFile := filepath.Join(agents, "parent-file")
	mustWrite(t, parentFile, "x")
	imp.force = true
	if err := imp.writeGeneratedJSON(filepath.Join(parentFile, "child.json"), []byte("{}")); err == nil || !strings.Contains(err.Error(), "mkdir") {
		t.Fatalf("writeGeneratedJSON parent file err=%v; want mkdir", err)
	}
	if err := imp.copyFile(filepath.Join(claude, "missing.txt"), filepath.Join(agents, "missing.txt"), 0o644); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("copyFile missing err=%v; want read", err)
	}
}
