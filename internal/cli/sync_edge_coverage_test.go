package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/resource"
)

func TestDefaultGitRunnerAndGitHubErrorBranches(t *testing.T) {
	if out, err := runGitCommand("", "--version"); err != nil || !strings.Contains(string(out), "git version") {
		t.Fatalf("runGitCommand git --version out=%q err=%v", out, err)
	}
	if _, err := runGitCommand("", "definitely-not-a-real-dotpack-subcommand"); err == nil || !strings.Contains(err.Error(), "git definitely-not-a-real-dotpack-subcommand") {
		t.Fatalf("runGitCommand bad subcommand err=%v; want wrapped git command", err)
	}
	if _, _, err := parseGitHubSource("https://github.com/%zz"); err == nil || !strings.Contains(err.Error(), "parse URL") {
		t.Fatalf("parse bad github URL err=%v; want parse URL", err)
	}
	if _, err := parseGitHubOwnerRepoRef("owner/repo!"); err == nil || !strings.Contains(err.Error(), "invalid repo") {
		t.Fatalf("invalid repo err=%v; want invalid repo", err)
	}
	if got := sanitizeCacheSegment("aA0._-!"); got != "aA0._" {
		t.Fatalf("sanitizeCacheSegment = %q; want aA0._", got)
	}

	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "cache")
	mustWriteTestFile(t, blocker, "not a dir")
	old := runGitCommand
	runGitCommand = func(workDir string, args ...string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { runGitCommand = old })
	if _, err := fetchGitHubSource(githubSource{owner: "o", repo: "r"}, dirsWithDotpackHome(tmp)); err == nil || !strings.Contains(err.Error(), "clear stale cache") {
		t.Fatalf("fetchGitHubSource stale cache err=%v; want clear stale cache", err)
	}

	calls := 0
	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		calls++
		if calls == 2 {
			return []byte("checkout failed"), errors.New("checkout failed")
		}
		return nil, nil
	}
	if err := checkoutGitHubRef(tmp, githubSource{owner: "o", repo: "r", ref: "main"}); err == nil || !strings.Contains(err.Error(), "checkout ref") {
		t.Fatalf("checkoutGitHubRef checkout err=%v; want checkout ref", err)
	}
}

func dirsWithDotpackHome(path string) dirs.Dirs {
	return dirs.Dirs{DotpackHome: path}
}

func TestRunInventoryWithCanonicalSourceAndResetRemovals(t *testing.T) {
	d := setDotpackEnvForSyncTest(t)
	agentsRoot := filepath.Join(t.TempDir(), ".agents")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nbody\n")
	materialized := filepath.Join(d.ProjectHome, ".agents", "skills", "s", "SKILL.md")
	mustWriteTestFile(t, materialized, "---\nname: s\ndescription: d\n---\nbody\n")

	cmd, out := newBufferedTestCmd()
	if err := runInventory(cmd, agentsRoot, d.ProjectHome, "agents-cli", "project"); err != nil {
		t.Fatalf("runInventory canonical: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "canonical-untracked") || !strings.Contains(got, "source=") {
		t.Fatalf("inventory output missing canonical source:\n%s", got)
	}

	store := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	rec := manifest.Record{
		ID:            "agents-cli:skill:s",
		Scope:         string(adapter.ScopeProject),
		TargetRoot:    d.ProjectHome,
		CanonicalRoot: agentsRoot,
		Files:         []string{materialized},
		TargetDir:     filepath.Dir(materialized),
	}
	if err := store.Upsert(rec); err != nil {
		t.Fatalf("Upsert reset record: %v", err)
	}
	cmd, out = newBufferedTestCmd()
	if err := runResetMaterialized(cmd, agentsRoot, d.ProjectHome, false); err != nil {
		t.Fatalf("runResetMaterialized removal: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "reset agents-cli:skill:s") || !strings.Contains(got, "removed") {
		t.Fatalf("reset output missing removal:\n%s", got)
	}
	if _, err := os.Stat(materialized); !os.IsNotExist(err) {
		t.Fatalf("materialized file should be removed, stat err=%v", err)
	}
}

func TestRunInstallAllAndSourceLayoutErrorBranches(t *testing.T) {
	d := setDotpackEnvForSyncTest(t)
	targetFile := filepath.Join(t.TempDir(), "target-file")
	mustWriteTestFile(t, targetFile, "x")
	cmd := &cobra.Command{}
	if err := runInstallAll(cmd, t.TempDir(), targetFile, "agents-cli", "user", sourceLayoutOptions{}, true, false, false); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("runInstallAll target file err=%v; want not directory", err)
	}

	source := t.TempDir()
	mustWriteTestFile(t, filepath.Join(source, ".agents", "skills", "bad", "SKILL.md"), "not frontmatter\n")
	if err := runInstallAll(cmd, source, d.ProjectHome, "agents-cli", "bad-scope", sourceLayoutOptions{}, true, false, false); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("runInstallAll bad scope err=%v; want scope", err)
	}
	if err := runInstallAll(cmd, source, d.ProjectHome, "agents-cli", "user", sourceLayoutOptions{}, true, false, false); err == nil || !strings.Contains(err.Error(), "load") {
		t.Fatalf("runInstallAll discover err=%v; want load", err)
	}

	if _, err := resolveSourceLayout(source, sourceLayoutOptions{kindPaths: []string{"bad"}}, d); err == nil || !strings.Contains(err.Error(), "--kind-path") {
		t.Fatalf("resolveSourceLayout bad override err=%v; want --kind-path", err)
	}
	if _, err := resolveSourceLayout(filepath.Join(t.TempDir(), "missing"), sourceLayoutOptions{}, d); err == nil || !strings.Contains(err.Error(), "stat --from") {
		t.Fatalf("resolveSourceLayout missing canonical err=%v; want stat --from", err)
	}
	if _, err := resolveSourceLayout(filepath.Join(t.TempDir(), "missing"), sourceLayoutOptions{skillsPath: "skills"}, d); err == nil || !strings.Contains(err.Error(), "stat --from") {
		t.Fatalf("resolveSourceLayout missing override root err=%v; want stat --from", err)
	}
}

func TestPlansInstallCanonicalAndDiscoveryErrorBranches(t *testing.T) {
	d := dirsForSyncLayoutTest(t)
	badSkill := filepath.Join(t.TempDir(), "skills", "bad", "SKILL.md")
	mustWriteTestFile(t, badSkill, "bad")
	if _, err := discoverCanonicalResources(sourceLayout{root: filepath.Dir(filepath.Dir(badSkill)), paths: map[resource.Kind]string{resource.KindSkill: "skills"}}); err == nil || !strings.Contains(err.Error(), "load") {
		t.Fatalf("discover bad skill err=%v; want load", err)
	}

	entry := canonicalEntry{Kind: resource.KindAgent, Path: "/tmp/a.md", Resource: &resource.Agent{Name: "a", Description: "d", Body: "b"}}
	missingHomes := dirsWithDotpackHome(d.DotpackHome)
	missingHomes.AgentsHome = d.AgentsHome
	missingHomes.CodexHome = d.CodexHome
	missingHomes.ProjectHome = d.ProjectHome
	if _, unsupported, err := plansForEntry(entry, "agents-cli", adapter.ScopeUser, missingHomes); err == nil || unsupported {
		t.Fatalf("plansForEntry umbrella writer error err=%v unsupported=%v; want non-unsupported error", err, unsupported)
	}
	unknownKind := canonicalEntry{Kind: resource.Kind("unknown"), Path: "/tmp/u", Resource: syncFakeResource{kind: resource.Kind("unknown")}}
	if _, unsupported, err := plansForEntry(unknownKind, "claude-code", adapter.ScopeUser, d); err == nil || unsupported {
		t.Fatalf("plansForEntry unknown single adapter err=%v unsupported=%v; want normal error", err, unsupported)
	}

	store := manifestStoreForTest(t, d.DotpackHome)
	if _, unsupported, err := installCanonicalEntry(entry, "agents-cli", adapter.ScopeUser, true, false, "", "", missingHomes, store); err == nil || unsupported {
		t.Fatalf("installCanonicalEntry writer error err=%v unsupported=%v; want error", err, unsupported)
	}
}

type syncFakeResource struct{ kind resource.Kind }

func (r syncFakeResource) Kind() resource.Kind        { return r.kind }
func (r syncFakeResource) Extensions() map[string]any { return nil }

func TestScanMaterializedFilesAllFlatVariantsAndCopyErrors(t *testing.T) {
	target := t.TempDir()
	for _, spec := range []struct {
		path string
		body string
	}{
		{".claude/agents/claude-agent.md", "a"},
		{".gemini/agents/gemini-agent.md", "a"},
		{".antigravity/agents/ag-agent.md", "a"},
		{".codex/agents/codex-agent.toml", "name = 'a'\n"},
		{".claude/rules/claude-rule.md", "r"},
		{".gemini/rules/gemini-rule.md", "r"},
		{".antigravity/rules/ag-rule.md", "r"},
		{".codex/rules/codex-rule.md", "r"},
		{".claude/commands/claude-cmd.md", "c"},
		{".gemini/commands/gemini-cmd.toml", "prompt = 'c'\n"},
		{".antigravity/commands/ag-cmd.md", "c"},
		{".codex/commands/codex-cmd.md", "c"},
	} {
		mustWriteTestFile(t, filepath.Join(target, filepath.FromSlash(spec.path)), spec.body)
	}
	mustWriteTestFile(t, filepath.Join(target, ".claude", "skills", "bad-primary", "SKILL.md", "child"), "not file")
	observed, err := scanMaterializedFiles(target)
	if err != nil {
		t.Fatalf("scanMaterializedFiles: %v", err)
	}
	if len(observed) != 12 {
		t.Fatalf("observed %d files, want 12 flat variants: %+v", len(observed), observed)
	}

	src := filepath.Join(t.TempDir(), "src.txt")
	mustWriteTestFile(t, src, "x")
	dstDir := t.TempDir()
	if _, err := copyIfNeeded(src, dstDir, true); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("copyIfNeeded dst dir err=%v; want read", err)
	}
	parentFile := filepath.Join(t.TempDir(), "parent")
	mustWriteTestFile(t, parentFile, "x")
	if _, err := copyIfNeeded(src, filepath.Join(parentFile, "child"), true); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("copyIfNeeded parent file err=%v; want read", err)
	}
}

func TestSyncCommandConstructorsWireRunE(t *testing.T) {
	for _, cmd := range []*cobra.Command{newInventoryCmd(), newSyncBackCmd(), newResetMaterializedCmd(), newInstallAllCmd()} {
		if cmd.RunE == nil || cmd.Args == nil {
			t.Fatalf("%s missing RunE/Args", cmd.Use)
		}
	}
}

func TestRunSyncBackAndInventoryManifestErrors(t *testing.T) {
	d := setDotpackEnvForSyncTest(t)
	agentsRoot := filepath.Join(t.TempDir(), ".agents")
	if err := os.MkdirAll(agentsRoot, 0o755); err != nil {
		t.Fatalf("mkdir agents root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d.DotpackHome, "installs.yaml"), []byte("installs: ["), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	cmd := &cobra.Command{}
	if err := runInventory(cmd, "", d.ProjectHome, "agents-cli", "user"); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Fatalf("runInventory manifest err=%v; want load manifest", err)
	}
	if err := runSyncBack(cmd, agentsRoot, d.ProjectHome, false); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Fatalf("runSyncBack manifest err=%v; want load manifest", err)
	}
}

func TestSyncCommandAdditionalErrorAndSkipBranches(t *testing.T) {
	d := setDotpackEnvForSyncTest(t)
	cmd, out := newBufferedTestCmd()

	targetFile := filepath.Join(t.TempDir(), "not-dir")
	mustWriteTestFile(t, targetFile, "x")
	if err := runInventory(cmd, "", targetFile, "agents-cli", "project"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("runInventory target file err=%v; want not a directory", err)
	}

	agentsRoot := filepath.Join(t.TempDir(), ".agents")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nbody\n")
	if err := runInventory(cmd, agentsRoot, d.ProjectHome, "missing-agent", "project"); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("runInventory expected-files err=%v; want unknown agent", err)
	}

	tracked := filepath.Join(d.ProjectHome, ".claude", "rules", "tracked.md")
	mustWriteTestFile(t, tracked, "tracked")
	store := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	if err := store.Upsert(manifest.Record{
		ID:         "claude-code:rule:tracked",
		Kind:       "rule",
		Agent:      "claude-code",
		Scope:      string(adapter.ScopeProject),
		TargetRoot: d.ProjectHome,
		Files:      []string{tracked},
		FileClaims: []manifest.FileClaim{{Path: tracked, SHA256: sha256String([]byte("tracked"))}},
	}); err != nil {
		t.Fatalf("Upsert tracked: %v", err)
	}
	out.Reset()
	if err := runSyncBack(cmd, agentsRoot, d.ProjectHome, false); err != nil {
		t.Fatalf("runSyncBack tracked skip: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "wrote=0 skipped=0") {
		t.Fatalf("tracked-only sync-back output = %q; want zero summary", got)
	}

	if err := os.WriteFile(filepath.Join(d.DotpackHome, "installs.yaml"), []byte("installs: ["), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	if err := runResetMaterialized(cmd, "", d.ProjectHome, false); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Fatalf("runResetMaterialized manifest err=%v; want load manifest", err)
	}

	source := t.TempDir()
	mustWriteTestFile(t, filepath.Join(source, ".agents", "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nbody\n")
	if err := os.WriteFile(filepath.Join(d.DotpackHome, "installs.yaml"), []byte("installs: []\n"), 0o644); err != nil {
		t.Fatalf("reset manifest: %v", err)
	}
	if err := runInstallAll(cmd, source, d.ProjectHome, "unknown-agent", "project", sourceLayoutOptions{}, true, false, false); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("runInstallAll unknown agent err=%v; want unknown agent", err)
	}
}

func TestSyncBackAndResetEarlyErrorBranches(t *testing.T) {
	d := setDotpackEnvForSyncTest(t)
	cmd, _ := newBufferedTestCmd()

	if err := runSyncBack(cmd, filepath.Join(t.TempDir(), "missing"), d.ProjectHome, false); err == nil || !strings.Contains(err.Error(), "stat --from") {
		t.Fatalf("runSyncBack missing from err=%v; want stat --from", err)
	}

	agentsRoot := filepath.Join(t.TempDir(), ".agents")
	if err := os.MkdirAll(agentsRoot, 0o755); err != nil {
		t.Fatalf("mkdir agents root: %v", err)
	}
	targetFile := filepath.Join(t.TempDir(), "target-file")
	mustWriteTestFile(t, targetFile, "x")
	if err := runSyncBack(cmd, agentsRoot, targetFile, false); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("runSyncBack target file err=%v; want not a directory", err)
	}
	if err := runResetMaterialized(cmd, filepath.Join(t.TempDir(), "missing"), d.ProjectHome, false); err == nil || !strings.Contains(err.Error(), "stat --from") {
		t.Fatalf("runResetMaterialized missing from err=%v; want stat --from", err)
	}
	if err := runResetMaterialized(cmd, "", targetFile, false); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("runResetMaterialized target file err=%v; want not a directory", err)
	}
}

func TestSyncDiscoveryRootAndWalkAdditionalBranches(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	if _, err := resolveAgentsRoot(""); err == nil || !strings.Contains(err.Error(), "neither") {
		t.Fatalf("resolveAgentsRoot empty err=%v; want neither", err)
	}
	if root, err := resolveSourceRoot(""); err != nil || root != tmp {
		t.Fatalf("resolveSourceRoot empty = %q,%v; want cwd", root, err)
	}

	root := filepath.Join(tmp, ".agents")
	mustWriteTestFile(t, filepath.Join(root, "skills", "s", "README.md"), "ignore")
	mustWriteTestFile(t, filepath.Join(root, "commands", "ignore.txt"), "ignore")
	entries, err := discoverCanonicalResources(sourceLayout{root: root, paths: defaultCanonicalKindPaths(false)})
	if err != nil {
		t.Fatalf("discover ignored files: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("discover ignored entries = %+v; want none", entries)
	}

	badSkill := filepath.Join(root, "skills", "bad", "SKILL.md")
	mustWriteTestFile(t, badSkill, "not frontmatter\n")
	if _, err := expectedFilesFromCanonical(root, "agents-cli", adapter.ScopeUser, dirsForSyncLayoutTest(t)); err == nil || !strings.Contains(err.Error(), "load") {
		t.Fatalf("expectedFilesFromCanonical bad skill err=%v; want load", err)
	}

	parentFile := filepath.Join(t.TempDir(), "parent-file")
	mustWriteTestFile(t, parentFile, "x")
	if err := walkDirect(filepath.Join(parentFile, "child"), func(path string, entry os.DirEntry) error { return nil }); err == nil {
		t.Fatal("walkDirect through file parent expected stat error")
	}

	if _, _, err := dirsForTarget(filepath.Join(t.TempDir(), "missing-target")); err == nil || !strings.Contains(err.Error(), "stat --target") {
		t.Fatalf("dirsForTarget missing target err=%v; want stat --target", err)
	}

	plans, unsupported, err := plansForEntry(
		canonicalEntry{Kind: resource.KindSkill, Path: badSkill, Resource: &resource.Skill{Name: "s", Description: "d", Body: "b"}},
		"agents-cli",
		adapter.ScopeUser,
		dirs.Dirs{CodexHome: t.TempDir(), GeminiHome: t.TempDir(), ProjectHome: t.TempDir(), DotpackHome: t.TempDir()},
	)
	if err == nil || unsupported || plans != nil {
		t.Fatalf("plansForEntry missing AgentsHome plans=%v unsupported=%v err=%v; want non-unsupported error", plans, unsupported, err)
	}
}
