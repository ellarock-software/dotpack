package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
)

func setDotpackEnvForSyncTest(t *testing.T) dirs.Dirs {
	t.Helper()
	project := t.TempDir()
	d := dirs.Dirs{
		HomeDir:         t.TempDir(),
		ClaudeHome:      t.TempDir(),
		GeminiHome:      t.TempDir(),
		AntigravityHome: t.TempDir(),
		AgentsHome:      t.TempDir(),
		CodexHome:       t.TempDir(),
		DotpackHome:     t.TempDir(),
		ProjectHome:     project,
	}
	t.Setenv("DOTPACK_USER_HOME", d.HomeDir)
	t.Setenv("DOTPACK_CLAUDE_HOME", d.ClaudeHome)
	t.Setenv("DOTPACK_GEMINI_HOME", d.GeminiHome)
	t.Setenv("DOTPACK_ANTIGRAVITY_HOME", d.AntigravityHome)
	t.Setenv("DOTPACK_AGENTS_HOME", d.AgentsHome)
	t.Setenv("DOTPACK_CODEX_HOME", d.CodexHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", d.DotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", d.ProjectHome)
	return d
}

func newBufferedTestCmd() (*cobra.Command, *bytes.Buffer) {
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	return cmd, &out
}

func TestRunInstallAllLifecycleSkipAndFailureBranches(t *testing.T) {
	d := setDotpackEnvForSyncTest(t)
	sourceProject := t.TempDir()
	mustWriteTestFile(t, filepath.Join(sourceProject, ".agents", "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nbody\n")
	mustWriteTestFile(t, filepath.Join(sourceProject, ".agents", "commands", "deploy.md"), "---\ndescription: d\n---\nrun\n")

	var lifecycleAgents []string
	withPostInstallLifecycle(t, func(agent string) error {
		lifecycleAgents = append(lifecycleAgents, agent)
		return nil
	})

	cmd, out := newBufferedTestCmd()
	if err := runInstallAll(cmd, sourceProject, d.ProjectHome, "agents-cli", "user", sourceLayoutOptions{}, true, false, true); err != nil {
		t.Fatalf("runInstallAll success: %v\n%s", err, out.String())
	}
	got := out.String()
	// command is now a first-class fan-out kind under agents-cli
	// (ADR-0014), so both the skill and the command install — nothing is
	// skipped for an unsupported kind anymore.
	for _, want := range []string{"installed agents-cli:skill:s", "installed agents-cli:command:deploy", "install-all complete: installed=2 skipped=0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("runInstallAll output missing %q:\n%s", want, got)
		}
	}
	if len(lifecycleAgents) != 1 || lifecycleAgents[0] != "agents-cli" {
		t.Fatalf("lifecycle agents = %v; want [agents-cli]", lifecycleAgents)
	}

	emptySource := t.TempDir()
	if err := os.MkdirAll(filepath.Join(emptySource, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir empty .agents: %v", err)
	}
	lifecycleAgents = nil
	cmd, out = newBufferedTestCmd()
	if err := runInstallAll(cmd, emptySource, d.ProjectHome, "agents-cli", "user", sourceLayoutOptions{}, true, false, true); err != nil {
		t.Fatalf("runInstallAll empty: %v", err)
	}
	if len(lifecycleAgents) != 0 {
		t.Fatalf("empty install-all should not run lifecycle, got %v", lifecycleAgents)
	}
	if !strings.Contains(out.String(), "installed=0 skipped=0") {
		t.Fatalf("empty install-all output = %q", out.String())
	}

	failSource := t.TempDir()
	mustWriteTestFile(t, filepath.Join(failSource, ".agents", "skills", "fail", "SKILL.md"), "---\nname: fail\ndescription: d\n---\nbody\n")
	withPostInstallLifecycle(t, func(agent string) error {
		return errors.New("lifecycle failed")
	})
	cmd, out = newBufferedTestCmd()
	err := runInstallAll(cmd, failSource, d.ProjectHome, "agents-cli", "user", sourceLayoutOptions{}, true, false, true)
	if err == nil || !strings.Contains(err.Error(), "installed 1 resources, but post-install lifecycle failed") {
		t.Fatalf("runInstallAll lifecycle error = %v; output:\n%s", err, out.String())
	}
}

func TestCanonicalDiscoveryExpectedFilesAndWalkBranches(t *testing.T) {
	d := dirsForSyncLayoutTest(t)
	source := t.TempDir()
	agentsRoot := filepath.Join(source, ".agents")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nbody\n")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "commands", "deploy.md"), "---\ndescription: d\n---\nrun\n")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "hooks", "registry.json"), `{"hooks":{"PreToolUse":[]}}`)

	expected, err := expectedFilesFromCanonical(agentsRoot, "agents-cli", adapter.ScopeUser, d)
	if err != nil {
		t.Fatalf("expectedFilesFromCanonical: %v", err)
	}
	// agents-cli now fans the command out to each sub-adapter's own file
	// (gemini .toml, antigravity .md, codex .md) in addition to the shared
	// skill write — 1 skill + 3 command files (ADR-0014).
	var skillFiles, commandFiles int
	for _, ef := range expected {
		switch {
		case strings.HasSuffix(ef.Path, filepath.Join("skills", "s", "SKILL.md")):
			skillFiles++
		case strings.Contains(ef.Path, string(filepath.Separator)+"commands"+string(filepath.Separator)):
			commandFiles++
		}
	}
	if skillFiles != 1 || commandFiles != 3 {
		t.Fatalf("expected files = %+v; want 1 skill + 3 command fan-out files", expected)
	}
	if _, err := expectedFilesFromCanonical(agentsRoot, "missing-agent", adapter.ScopeUser, d); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expectedFilesFromCanonical unknown agent err=%v", err)
	}

	layout := sourceLayout{root: source, paths: map[resource.Kind]string{resource.KindSkill: filepath.Join(agentsRoot, "skills")}}
	if got := layout.kindDir(resource.KindSkill); got != filepath.Join(agentsRoot, "skills") {
		t.Fatalf("absolute kindDir = %q", got)
	}

	directRoot := filepath.Join(t.TempDir(), "direct")
	mustWriteTestFile(t, filepath.Join(directRoot, "one.md"), "one")
	mustWriteTestFile(t, filepath.Join(directRoot, "nested", "two.md"), "two")
	mustWriteTestFile(t, filepath.Join(directRoot, "nested", "deeper", "three.md"), "three")
	var visited []string
	if err := walkDirect(directRoot, func(path string, entry os.DirEntry) error {
		visited = append(visited, filepath.Base(path))
		return nil
	}); err != nil {
		t.Fatalf("walkDirect: %v", err)
	}
	gotVisited := strings.Join(visited, ",")
	for _, want := range []string{"one.md", "nested", "two.md"} {
		if !strings.Contains(gotVisited, want) {
			t.Fatalf("walkDirect visited %v; missing %s", visited, want)
		}
	}
	if strings.Contains(gotVisited, "three.md") {
		t.Fatalf("walkDirect visited deep nested file: %v", visited)
	}

	fileSource := filepath.Join(t.TempDir(), "not-dir")
	mustWriteTestFile(t, fileSource, "x")
	if _, err := resolveSourceRoot(fileSource); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("resolveSourceRoot file err=%v; want not directory", err)
	}
	if _, err := resolveSourceRoot(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "stat --from") {
		t.Fatalf("resolveSourceRoot missing err=%v; want stat --from", err)
	}
}

func TestGitHubFetchUpdateErrorAndCloneRefBranches(t *testing.T) {
	tmp := t.TempDir()
	src := githubSource{owner: "o", repo: "r"}
	cache := githubSourceCacheDir(src, dirs.Dirs{DotpackHome: tmp})
	if err := os.MkdirAll(filepath.Join(cache, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	old := runGitCommand
	t.Cleanup(func() { runGitCommand = old })

	calls := 0
	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		calls++
		if calls == 2 {
			return []byte("pull failed"), errors.New("pull failed")
		}
		return nil, nil
	}
	if err := updateGitHubCache(cache, src); err == nil || !strings.Contains(err.Error(), "update cached") {
		t.Fatalf("updateGitHubCache pull error = %v; want update cached", err)
	}

	refSrc := githubSource{owner: "o", repo: "clone", ref: "feature"}
	var sawClone, sawFetchRef, sawCheckout bool
	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case len(args) > 0 && args[0] == "clone":
			sawClone = true
			dest := args[len(args)-1]
			mustWriteTestFile(t, filepath.Join(dest, ".git", "HEAD"), "ref: refs/heads/main\n")
		case strings.Contains(joined, "fetch --depth 1 origin feature"):
			sawFetchRef = true
		case strings.Contains(joined, "checkout --detach FETCH_HEAD"):
			sawCheckout = true
		}
		return nil, nil
	}
	got, err := fetchGitHubSource(refSrc, dirs.Dirs{DotpackHome: tmp})
	if err != nil {
		t.Fatalf("fetchGitHubSource ref clone: %v", err)
	}
	if got == "" || !sawClone || !sawFetchRef || !sawCheckout {
		t.Fatalf("fetch ref clone got=%q clone=%v fetch=%v checkout=%v", got, sawClone, sawFetchRef, sawCheckout)
	}
}
