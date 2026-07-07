package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/orchestrator"
	"github.com/ellarock-software/dotpack/internal/resource"
)

func TestGitHubSourceParsingValidationAndCacheHelpers(t *testing.T) {
	src, ok, err := parseGitHubSource("github:Owner/repo.git@feature/ref")
	if err != nil || !ok {
		t.Fatalf("parse github shorthand = %+v ok=%v err=%v", src, ok, err)
	}
	if src.owner != "Owner" || src.repo != "repo" || src.ref != "feature/ref" || src.cloneURL() != "https://github.com/Owner/repo.git" {
		t.Fatalf("parsed github source = %+v", src)
	}
	src, ok, err = parseGitHubSource("https://github.com/Owner/repo#main")
	if err != nil || !ok || src.ref != "main" {
		t.Fatalf("parse github URL = %+v ok=%v err=%v", src, ok, err)
	}
	if _, ok, err := parseGitHubSource("local/path"); ok || err != nil {
		t.Fatalf("non-github source ok=%v err=%v; want false,nil", ok, err)
	}
	for _, input := range []string{
		"github:Owner",
		"github:Bad!/repo",
		"github:Owner/repo@",
		"github:Owner/repo@bad..ref",
		"https://github.com/Owner/repo@old#new",
	} {
		if _, _, err := parseGitHubSource(input); err == nil {
			t.Fatalf("parseGitHubSource(%q) expected error", input)
		}
	}

	segment := sanitizeCacheSegment("///")
	if segment != "ref" {
		t.Fatalf("sanitize empty-ish segment = %q; want ref", segment)
	}
	long := sanitizeCacheSegment(strings.Repeat("a", 80))
	if len(long) != 48 {
		t.Fatalf("long sanitized segment len=%d; want 48", len(long))
	}
	cacheDir := githubSourceCacheDir(githubSource{owner: "Owner", repo: "repo", ref: "feature/ref"}, dirs.Dirs{DotpackHome: "/tmp/dp"})
	if !strings.Contains(cacheDir, "feature-ref-") {
		t.Fatalf("cache dir should include sanitized ref: %s", cacheDir)
	}
}

func TestFetchGitHubSourceErrorAndUpdateBranches(t *testing.T) {
	_, err := fetchGitHubSource(githubSource{owner: "o", repo: "r"}, dirs.Dirs{})
	if err == nil || !strings.Contains(err.Error(), "DOTPACK_DOTPACK_HOME") {
		t.Fatalf("fetch without home error = %v; want DOTPACK_DOTPACK_HOME", err)
	}

	tmp := t.TempDir()
	src := githubSource{owner: "o", repo: "r"}
	cache := githubSourceCacheDir(src, dirs.Dirs{DotpackHome: tmp})
	if err := os.MkdirAll(filepath.Join(cache, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir git cache: %v", err)
	}
	old := runGitCommand
	var calls []string
	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	}
	t.Cleanup(func() { runGitCommand = old })
	got, err := fetchGitHubSource(src, dirs.Dirs{DotpackHome: tmp})
	if err != nil {
		t.Fatalf("fetch existing cache: %v", err)
	}
	if got != cache || len(calls) != 2 || !strings.Contains(calls[0], "fetch") || !strings.Contains(calls[1], "pull") {
		t.Fatalf("existing-cache update got=%s calls=%v", got, calls)
	}

	calls = nil
	refSrc := githubSource{owner: "o", repo: "r", ref: "main"}
	refCache := githubSourceCacheDir(refSrc, dirs.Dirs{DotpackHome: tmp})
	if err := os.MkdirAll(filepath.Join(refCache, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir ref cache: %v", err)
	}
	if _, err := fetchGitHubSource(refSrc, dirs.Dirs{DotpackHome: tmp}); err != nil {
		t.Fatalf("fetch ref cache: %v", err)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], "main") || !strings.Contains(calls[1], "checkout") {
		t.Fatalf("ref-cache calls=%v", calls)
	}

	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		return []byte("boom"), errors.New("git failed")
	}
	if _, err := fetchGitHubSource(src, dirs.Dirs{DotpackHome: tmp}); err == nil || !strings.Contains(err.Error(), "fetch cached") {
		t.Fatalf("fetchGitHubSource update error = %v; want fetch cached", err)
	}
	if err := updateGitHubCache(cache, src); err == nil || !strings.Contains(err.Error(), "fetch cached") {
		t.Fatalf("updateGitHubCache fetch error = %v; want fetch cached", err)
	}
	if err := checkoutGitHubRef(refCache, refSrc); err == nil || !strings.Contains(err.Error(), "fetch ref") {
		t.Fatalf("checkoutGitHubRef error = %v; want fetch ref", err)
	}

	blockedHome := t.TempDir()
	mustWriteTestFile(t, filepath.Join(blockedHome, "cache"), "not a directory")
	if _, err := fetchGitHubSource(githubSource{owner: "o", repo: "blocked-cache"}, dirs.Dirs{DotpackHome: blockedHome}); err == nil || !strings.Contains(err.Error(), "clear stale cache") {
		t.Fatalf("fetchGitHubSource blocked cache err=%v; want clear stale cache", err)
	}

	cloneFail := githubSource{owner: "o", repo: "clone-fail"}
	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		return []byte("clone failed"), errors.New("clone failed")
	}
	if _, err := fetchGitHubSource(cloneFail, dirs.Dirs{DotpackHome: tmp}); err == nil || !strings.Contains(err.Error(), "clone o/clone-fail") {
		t.Fatalf("fetchGitHubSource clone error = %v; want clone failure", err)
	}

	checkoutFail := githubSource{owner: "o", repo: "checkout-fail", ref: "main"}
	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "fetch --depth 1 origin main") {
			return []byte("fetch failed"), errors.New("fetch failed")
		}
		return nil, nil
	}
	if _, err := fetchGitHubSource(checkoutFail, dirs.Dirs{DotpackHome: tmp}); err == nil || !strings.Contains(err.Error(), "fetch ref") {
		t.Fatalf("fetchGitHubSource checkout error = %v; want fetch ref", err)
	}
}

func TestResolveRootsLayoutOverridesDirsAndDiscoveryHelpers(t *testing.T) {
	project := t.TempDir()
	agentsRoot := filepath.Join(project, ".agents")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nb\n")
	root, err := resolveAgentsRoot(project)
	if err != nil || root != agentsRoot {
		t.Fatalf("resolve project .agents = %q err=%v; want %q", root, err, agentsRoot)
	}
	root, err = resolveAgentsRoot(agentsRoot)
	if err != nil || root != agentsRoot {
		t.Fatalf("resolve direct .agents = %q err=%v; want %q", root, err, agentsRoot)
	}
	file := filepath.Join(project, "not-dir")
	mustWriteTestFile(t, file, "x")
	if _, err := resolveAgentsRoot(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("resolveAgentsRoot file error = %v; want not a directory", err)
	}
	if _, err := resolveAgentsRoot(filepath.Join(project, "missing")); err == nil || !strings.Contains(err.Error(), "stat --from") {
		t.Fatalf("resolveAgentsRoot missing error = %v; want stat --from", err)
	}
	if root, err := resolveSourceRoot(project); err != nil || root == "" {
		t.Fatalf("resolveSourceRoot dir = %q err=%v", root, err)
	}

	overrides, has, err := parseSourceLayoutOverrides(sourceLayoutOptions{
		kindPaths:    []string{"skill=public-skills", "hook=public-hooks"},
		agentsPath:   "public-agents",
		commandsPath: "public-commands",
	})
	if err != nil || !has || overrides[resource.KindSkill] != "public-skills" || overrides[resource.KindAgent] != "public-agents" {
		t.Fatalf("parse overrides = %#v has=%v err=%v", overrides, has, err)
	}
	for _, opts := range []sourceLayoutOptions{
		{kindPaths: []string{"skill"}},
		{kindPaths: []string{"unknown=path"}},
	} {
		if _, _, err := parseSourceLayoutOverrides(opts); err == nil {
			t.Fatalf("parseSourceLayoutOverrides(%+v) expected error", opts)
		}
	}

	t.Setenv("DOTPACK_PROJECT_HOME", project)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	d, target, err := dirsForTarget(project)
	if err != nil || target != project || d.ProjectHome != project {
		t.Fatalf("dirsForTarget = %+v %q %v", d, target, err)
	}
	if _, _, err := dirsForTarget(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("dirsForTarget file error = %v; want not directory", err)
	}
	t.Setenv("DOTPACK_PROJECT_HOME", filepath.Join(project, "missing-project"))
	if _, _, err := dirsForTarget(""); err == nil || !strings.Contains(err.Error(), "DOTPACK_PROJECT_HOME") {
		t.Fatalf("dirsForTarget bad env project err=%v; want DOTPACK_PROJECT_HOME", err)
	}
	if targetRootForScope(adapter.ScopeProject, d) != project || targetRootForScope(adapter.ScopeUser, d) != "" {
		t.Fatalf("targetRootForScope returned unexpected values")
	}
	if inferCanonicalRoot(filepath.Join(agentsRoot, "skills", "s", "SKILL.md")) != agentsRoot {
		t.Fatalf("inferCanonicalRoot failed")
	}
}

func TestScanCanonicalDestinationsAndCopyIfNeeded(t *testing.T) {
	target := t.TempDir()
	mustWriteTestFile(t, filepath.Join(target, ".claude", "skills", "s", "SKILL.md"), "skill")
	mustWriteTestFile(t, filepath.Join(target, ".claude", "skills", "s", "references", "guide.md"), "guide")
	mustWriteTestFile(t, filepath.Join(target, ".gemini", "skills", "g", "SKILL.md"), "g")
	mustWriteTestFile(t, filepath.Join(target, ".antigravity", "skills", "a", "SKILL.md"), "a")
	mustWriteTestFile(t, filepath.Join(target, ".agents", "skills", "c", "SKILL.md"), "c")
	mustWriteTestFile(t, filepath.Join(target, ".claude", "agents", "agent.md"), "agent")
	mustWriteTestFile(t, filepath.Join(target, ".claude", "agents", "ignore.txt"), "ignore")
	if err := os.MkdirAll(filepath.Join(target, ".claude", "agents", "nested-dir"), 0o755); err != nil {
		t.Fatalf("mkdir flat nested dir: %v", err)
	}
	mustWriteTestFile(t, filepath.Join(target, ".gemini", "commands", "cmd.toml"), "prompt = 'x'\n")
	mustWriteTestFile(t, filepath.Join(target, ".codex", "rules", "rule.md"), "rule")

	observed, err := scanMaterializedFiles(target)
	if err != nil {
		t.Fatalf("scanMaterializedFiles: %v", err)
	}
	if len(observed) < 8 {
		t.Fatalf("observed %d files, want at least 8: %#v", len(observed), observed)
	}

	agentsRoot := filepath.Join(t.TempDir(), ".agents")
	cases := []struct {
		obs  orchestrator.FileObservation
		want string
		ok   bool
	}{
		{orchestrator.FileObservation{Path: "/x/SKILL.md", RelPath: "", Kind: "skill", Name: "s"}, filepath.Join(agentsRoot, "skills", "s", "SKILL.md"), true},
		{orchestrator.FileObservation{Path: "/x/ref.md", RelPath: "references/ref.md", Kind: "skill", Name: "s"}, filepath.Join(agentsRoot, "skills", "s", "references", "ref.md"), true},
		{orchestrator.FileObservation{Path: "/x/a.md", Kind: "agent", Name: "a"}, filepath.Join(agentsRoot, "agents", "a.md"), true},
		{orchestrator.FileObservation{Path: "/x/r.md", Kind: "rule", Name: "r"}, filepath.Join(agentsRoot, "rules", "r.md"), true},
		{orchestrator.FileObservation{Path: "/x/r.toml", Kind: "rule", Name: "r"}, "", false},
		{orchestrator.FileObservation{Path: "/x/c.toml", Kind: "command", Name: "c"}, filepath.Join(agentsRoot, "commands", "c.toml"), true},
		{orchestrator.FileObservation{Path: "/x/c.exe", Kind: "command", Name: "c"}, "", false},
		{orchestrator.FileObservation{Path: "/x/a.toml", Kind: "agent", Name: "a"}, "", false},
		{orchestrator.FileObservation{Path: "/x/u.md", Kind: "unknown", Name: "u"}, "", false},
	}
	for _, tc := range cases {
		got, ok := canonicalDestination(agentsRoot, tc.obs)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("canonicalDestination(%+v) = %q,%v; want %q,%v", tc.obs, got, ok, tc.want, tc.ok)
		}
	}

	src := filepath.Join(t.TempDir(), "src.txt")
	dst := filepath.Join(t.TempDir(), "dst.txt")
	mustWriteTestFile(t, src, "one")
	changed, err := copyIfNeeded(src, dst, false)
	if err != nil || !changed {
		t.Fatalf("copy missing dst = changed %v err %v; want changed", changed, err)
	}
	changed, err = copyIfNeeded(src, dst, false)
	if err != nil || changed {
		t.Fatalf("copy identical = changed %v err %v; want unchanged", changed, err)
	}
	mustWriteTestFile(t, src, "two")
	if _, err := copyIfNeeded(src, dst, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("copy differing without force error = %v; want --force", err)
	}
	changed, err = copyIfNeeded(src, dst, true)
	if err != nil || !changed {
		t.Fatalf("copy differing with force = changed %v err %v; want changed", changed, err)
	}
	if _, err := copyIfNeeded(filepath.Join(t.TempDir(), "missing"), dst, false); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("copy missing source error = %v; want read", err)
	}
}

func TestPlansAndInstallCanonicalEntryHelpers(t *testing.T) {
	agentsHome, dotpackHome := setupCodexEnv(t)
	project := t.TempDir()
	d := dirs.Dirs{
		AgentsHome:  agentsHome,
		CodexHome:   filepath.Join(t.TempDir(), "codex"),
		GeminiHome:  filepath.Join(t.TempDir(), "gemini"),
		ProjectHome: project,
		DotpackHome: dotpackHome,
	}
	store := manifestStoreForTest(t, dotpackHome)
	entry := canonicalEntry{
		Kind: resource.KindSkill,
		Path: filepath.Join(t.TempDir(), "SKILL.md"),
		Resource: &resource.Skill{
			Name:        "helper-skill",
			Description: "d",
			Body:        "body",
		},
	}
	plans, unsupported, err := plansForEntry(entry, "agents-cli", adapter.ScopeUser, d)
	if err != nil || unsupported || len(plans) != 1 {
		t.Fatalf("plansForEntry umbrella skill = len %d unsupported %v err %v", len(plans), unsupported, err)
	}
	if _, unsupported, err := plansForEntry(canonicalEntry{Kind: resource.KindCommand, Resource: &resource.Command{Name: "x", Prompt: "p"}}, "unknown", adapter.ScopeUser, d); err == nil || unsupported {
		t.Fatalf("plansForEntry unknown agent err=%v unsupported=%v; want error", err, unsupported)
	}

	result, unsupported, err := installCanonicalEntry(entry, "agents-cli", adapter.ScopeUser, true, false, filepath.Dir(entry.Path), "", d, store)
	if err != nil || unsupported {
		t.Fatalf("installCanonicalEntry umbrella = unsupported %v err %v", unsupported, err)
	}
	if result.Record.Agent != "agents-cli" {
		t.Fatalf("umbrella install record = %+v; want agents-cli", result.Record)
	}
}

func TestExecCommandRunnerAndLifecycleInstallCandidates(t *testing.T) {
	runner := execCommandRunner{}
	if _, err := runner.LookPath("definitely-not-a-dotpack-test-binary"); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("LookPath missing err = %v; want exec.ErrNotFound", err)
	}
	if err := runner.Run("definitely-not-a-dotpack-test-binary"); err == nil {
		t.Fatalf("Run missing command expected error")
	}

	fake := &fakeCommandRunner{
		lookPathResults: map[string][]lookPathResult{
			"tool":      {{err: exec.ErrNotFound}, {path: "/bin/tool"}},
			"installer": {{path: "/bin/installer"}},
		},
	}
	withFakeLifecycleRunner(t, fake)
	path, err := ensureLifecycleBinary(lifecycleBinary{
		Name: "tool",
		Install: lifecycleInstaller{Candidates: []lifecycleCommand{{
			Command: "installer",
			Args:    []string{"install", "tool"},
		}}},
	})
	if err != nil || path != "/bin/tool" {
		t.Fatalf("ensureLifecycleBinary install candidate path=%q err=%v", path, err)
	}
	if len(fake.runs) != 1 || fake.runs[0] != "/bin/installer install tool" {
		t.Fatalf("installer runs = %v", fake.runs)
	}
}

func manifestStoreForTest(t *testing.T, dotpackHome string) *manifest.Store {
	t.Helper()
	return manifest.NewStore(filepath.Join(dotpackHome, "installs.yaml"))
}
