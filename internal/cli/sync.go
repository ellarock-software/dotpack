package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/orchestrator"
	"github.com/ellarock-software/dotpack/internal/resource"
)

type canonicalEntry struct {
	Kind     resource.Kind
	Path     string
	Resource resource.Resource
}

type sourceLayoutOptions struct {
	kindPaths      []string
	skillsPath     string
	agentsPath     string
	rulesPath      string
	commandsPath   string
	mcpServersPath string
	hooksPath      string
}

type sourceLayout struct {
	root  string
	paths map[resource.Kind]string
}

func newInventoryCmd() *cobra.Command {
	var fromRoot, targetRoot, agentName, scopeName string
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "Classify materialized host file outputs",
		Long: `Scan a target project's materialized host file outputs and classify them
against dotpack's manifest and, optionally, a canonical .agents tree.

Statuses:
  tracked               manifest owns the file and bytes match
  drifted               manifest owns the file but bytes changed
  missing               manifest owns the file but it is absent
  canonical-untracked   file is not in manifest but matches canonical output
  foreign-untracked     file is not in manifest and does not match canonical output`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInventory(cmd, fromRoot, targetRoot, agentName, scopeName)
		},
	}
	cmd.Flags().StringVar(&fromRoot, "from", "", "Canonical .agents tree or project containing .agents")
	cmd.Flags().StringVar(&targetRoot, "target", "", "Target project root (defaults to DOTPACK_PROJECT_HOME or CWD)")
	cmd.Flags().StringVar(&agentName, "agent", "agents-cli", "Target host adapter for canonical comparison")
	cmd.Flags().StringVar(&scopeName, "scope", "project", "Install scope for canonical comparison (project|user)")
	return cmd
}

func newSyncBackCmd() *cobra.Command {
	var fromRoot, targetRoot string
	var force bool
	cmd := &cobra.Command{
		Use:   "sync-back",
		Short: "Copy changed materialized file outputs back into canonical .agents",
		Long: `Copy drifted or untracked materialized file outputs from a target project
back into a canonical .agents tree. This command handles file-drop resources
(skills, markdown agents, rules, and commands). Config-fragment sync-back from
settings/config files remains an importer concern.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncBack(cmd, fromRoot, targetRoot, force)
		},
	}
	cmd.Flags().StringVar(&fromRoot, "from", "", "Canonical .agents tree or project containing .agents")
	cmd.Flags().StringVar(&targetRoot, "target", "", "Target project root (defaults to DOTPACK_PROJECT_HOME or CWD)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite canonical files when materialized output differs")
	return cmd
}

func newResetMaterializedCmd() *cobra.Command {
	var fromRoot, targetRoot string
	var includeUntracked bool
	cmd := &cobra.Command{
		Use:   "reset-materialized",
		Short: "Remove materialized host outputs for a target",
		Long: `Remove materialized host outputs for a target project.

Manifest-owned file outputs are removed and manifest-owned config fragments are
unmerged. With --include-untracked, dotpack also removes known file-drop host
outputs in the target that have no manifest claim. Untracked merged settings
entries are left in place and will surface as install collisions until an
importer can promote them safely.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResetMaterialized(cmd, fromRoot, targetRoot, includeUntracked)
		},
	}
	cmd.Flags().StringVar(&fromRoot, "from", "", "Canonical .agents tree or project containing .agents")
	cmd.Flags().StringVar(&targetRoot, "target", "", "Target project root (defaults to DOTPACK_PROJECT_HOME or CWD)")
	cmd.Flags().BoolVar(&includeUntracked, "include-untracked", false, "Also remove scanned file-drop outputs not claimed by the manifest")
	return cmd
}

func newInstallAllCmd() *cobra.Command {
	var fromRoot, targetRoot, agentName, scopeName string
	var allowLossy, force, runLifecycle bool
	var layoutOpts sourceLayoutOptions
	cmd := &cobra.Command{
		Use:   "install-all",
		Short: "Install every supported resource from a source layout",
		Long: `Install every supported resource discovered under a source layout
into a target host/project. Unsupported kinds for the selected --agent are
reported as skipped rather than treated as fatal.

With no layout flags, --from keeps the canonical behavior: it resolves to a
.agents directory and discovers skills/, agents/, rules/, commands/,
mcp-servers/, and hooks/ inside it. Pass --kind-path kind=path, or the
per-kind aliases, to discover resources from non-canonical layouts such as
root-level skills/. Custom paths are relative to --from unless absolute.

By default, install-all only materializes host files. Pass --run-lifecycle to
run optional post-install lifecycle tasks after a non-empty batch.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstallAll(cmd, fromRoot, targetRoot, agentName, scopeName, layoutOpts, allowLossy, force, runLifecycle)
		},
	}
	cmd.Flags().StringVar(&fromRoot, "from", "", "Source root; resolves to canonical .agents unless layout flags are used")
	cmd.Flags().StringVar(&targetRoot, "target", "", "Target project root (defaults to DOTPACK_PROJECT_HOME or CWD)")
	cmd.Flags().StringVar(&agentName, "agent", "agents-cli", "Target host adapter")
	cmd.Flags().StringVar(&scopeName, "scope", "project", "Install scope (project|user)")
	addSourceLayoutFlags(cmd, &layoutOpts)
	cmd.Flags().BoolVar(&allowLossy, "allow-lossy", false, "Proceed even if an adapter cannot honour all source fields")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing untracked files at install targets")
	cmd.Flags().BoolVar(&runLifecycle, "run-lifecycle", false, "Run optional post-install lifecycle tasks after a non-empty batch")
	return cmd
}

func addSourceLayoutFlags(cmd *cobra.Command, opts *sourceLayoutOptions) {
	cmd.Flags().StringArrayVar(&opts.kindPaths, "kind-path", nil, "Override a discovery path as kind=path; repeatable for skill, agent, rule, command, mcp-server, hook")
	cmd.Flags().StringVar(&opts.skillsPath, "skills-path", "", "Override skill discovery path relative to --from")
	cmd.Flags().StringVar(&opts.agentsPath, "agents-path", "", "Override agent discovery path relative to --from")
	cmd.Flags().StringVar(&opts.rulesPath, "rules-path", "", "Override rule discovery path relative to --from")
	cmd.Flags().StringVar(&opts.commandsPath, "commands-path", "", "Override command discovery path relative to --from")
	cmd.Flags().StringVar(&opts.mcpServersPath, "mcp-servers-path", "", "Override mcp-server discovery path relative to --from")
	cmd.Flags().StringVar(&opts.hooksPath, "hooks-path", "", "Override hook discovery path relative to --from")
}

func runInventory(cmd *cobra.Command, fromRoot, targetRoot, agentName, scopeName string) error {
	d, target, err := dirsForTarget(targetRoot)
	if err != nil {
		return err
	}
	scope, err := parseScope(scopeName)
	if err != nil {
		return err
	}
	observed, err := scanMaterializedFiles(target)
	if err != nil {
		return err
	}

	var expected []orchestrator.ExpectedFile
	if fromRoot != "" {
		agentsRoot, err := resolveAgentsRoot(fromRoot)
		if err != nil {
			return err
		}
		expected, err = expectedFilesFromCanonical(agentsRoot, agentName, scope, d)
		if err != nil {
			return err
		}
	}

	reader := manifestReader(d)
	items, err := reader.InventoryFiles(observed, expected, target)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		cmd.Println("no materialized file outputs")
		return nil
	}
	for _, item := range items {
		line := fmt.Sprintf("%s\t%s", item.Status, item.Path)
		if item.RecordID != "" {
			line += "\trecord=" + item.RecordID
		}
		if item.Source != "" {
			line += "\tsource=" + item.Source
		}
		cmd.Println(line)
	}
	return nil
}

func runSyncBack(cmd *cobra.Command, fromRoot, targetRoot string, force bool) error {
	agentsRoot, err := resolveAgentsRoot(fromRoot)
	if err != nil {
		return err
	}
	d, target, err := dirsForTarget(targetRoot)
	if err != nil {
		return err
	}
	observed, err := scanMaterializedFiles(target)
	if err != nil {
		return err
	}
	items, err := manifestReader(d).InventoryFiles(observed, nil, target)
	if err != nil {
		return err
	}

	written := 0
	skipped := 0
	for _, item := range items {
		if item.Status != orchestrator.InventoryDrifted && item.Status != orchestrator.InventoryForeignUntracked && item.Status != orchestrator.InventoryCanonicalUntracked {
			continue
		}
		obs := itemToObservation(item)
		dst, ok := canonicalDestination(agentsRoot, obs)
		if !ok {
			skipped++
			cmd.Printf("skipped %s (no safe canonical file-drop mapping)\n", item.Path)
			continue
		}
		changed, err := copyIfNeeded(item.Path, dst, force)
		if err != nil {
			return err
		}
		if changed {
			written++
			cmd.Printf("synced %s -> %s\n", item.Path, dst)
		} else {
			skipped++
			cmd.Printf("kept %s (canonical already matches)\n", dst)
		}
	}
	cmd.Printf("sync-back complete: wrote=%d skipped=%d\n", written, skipped)
	return nil
}

func runResetMaterialized(cmd *cobra.Command, fromRoot, targetRoot string, includeUntracked bool) error {
	var canonicalRoot string
	if fromRoot != "" {
		resolved, err := resolveAgentsRoot(fromRoot)
		if err != nil {
			return err
		}
		canonicalRoot = resolved
	}
	d, target, err := dirsForTarget(targetRoot)
	if err != nil {
		return err
	}
	observed, err := scanMaterializedFiles(target)
	if err != nil {
		return err
	}
	result, err := manifestReader(d).ResetMaterialized(orchestrator.ResetOptions{
		TargetRoot:       target,
		CanonicalRoot:    canonicalRoot,
		IncludeUntracked: includeUntracked,
		ObservedFiles:    observed,
	})
	if err != nil {
		return err
	}
	for _, res := range result.Uninstalled {
		cmd.Printf("reset %s\n", res.Record.ID)
		for _, p := range res.RemovedPaths {
			cmd.Printf("  removed %s\n", p)
		}
	}
	for _, p := range result.RemovedUntracked {
		cmd.Printf("  removed untracked %s\n", p)
	}
	if len(result.Uninstalled) == 0 && len(result.RemovedUntracked) == 0 {
		cmd.Println("nothing to reset")
	}
	return nil
}

func runInstallAll(cmd *cobra.Command, fromRoot, targetRoot, agentName, scopeName string, layoutOpts sourceLayoutOptions, allowLossy, force, runLifecycle bool) error {
	layout, err := resolveSourceLayout(fromRoot, layoutOpts)
	if err != nil {
		return err
	}
	d, target, err := dirsForTarget(targetRoot)
	if err != nil {
		return err
	}
	scope, err := parseScope(scopeName)
	if err != nil {
		return err
	}
	entries, err := discoverCanonicalResources(layout)
	if err != nil {
		return err
	}
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))

	installed := 0
	skipped := 0
	for _, entry := range entries {
		result, unsupported, err := installCanonicalEntry(entry, agentName, scope, allowLossy, force, layout.root, target, d, mf)
		if unsupported {
			skipped++
			cmd.Printf("skipped %s (%s unsupported on %s)\n", entry.Path, entry.Kind, agentName)
			continue
		}
		if err != nil {
			return err
		}
		installed++
		cmd.Printf("installed %s\n", result.Record.ID)
	}
	if installed > 0 && runLifecycle {
		if err := runPostInstallLifecycle(agentName); err != nil {
			return fmt.Errorf("installed %d resources, but post-install lifecycle failed: %w", installed, err)
		}
	}
	cmd.Printf("install-all complete: installed=%d skipped=%d\n", installed, skipped)
	return nil
}

func resolveSourceLayout(input string, opts sourceLayoutOptions) (sourceLayout, error) {
	overrides, hasOverrides, err := parseSourceLayoutOverrides(opts)
	if err != nil {
		return sourceLayout{}, err
	}
	if !hasOverrides {
		agentsRoot, err := resolveAgentsRoot(input)
		if err != nil {
			return sourceLayout{}, err
		}
		return sourceLayout{root: agentsRoot, paths: defaultCanonicalKindPaths(false)}, nil
	}

	root, err := resolveSourceRoot(input)
	if err != nil {
		return sourceLayout{}, err
	}
	paths := defaultCanonicalKindPaths(true)
	for kind, path := range overrides {
		paths[kind] = path
	}
	return sourceLayout{root: root, paths: paths}, nil
}

func resolveAgentsRoot(input string) (string, error) {
	if input == "" {
		input = "."
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve --from: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat --from %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--from %s is not a directory", abs)
	}
	if filepath.Base(abs) == ".agents" {
		return abs, nil
	}
	nested := filepath.Join(abs, ".agents")
	if st, err := os.Stat(nested); err == nil && st.IsDir() {
		return nested, nil
	}
	return "", fmt.Errorf("--from %s is neither a .agents directory nor a project containing .agents", abs)
}

func resolveSourceRoot(input string) (string, error) {
	if input == "" {
		input = "."
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve --from: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat --from %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--from %s is not a directory", abs)
	}
	return abs, nil
}

func defaultCanonicalKindPaths(withAgentsPrefix bool) map[resource.Kind]string {
	prefix := ""
	if withAgentsPrefix {
		prefix = ".agents"
	}
	join := func(path string) string {
		if prefix == "" {
			return path
		}
		return filepath.Join(prefix, path)
	}
	return map[resource.Kind]string{
		resource.KindSkill:     join("skills"),
		resource.KindAgent:     join("agents"),
		resource.KindRule:      join("rules"),
		resource.KindCommand:   join("commands"),
		resource.KindMCPServer: join("mcp-servers"),
		resource.KindHook:      join("hooks"),
	}
}

func parseSourceLayoutOverrides(opts sourceLayoutOptions) (map[resource.Kind]string, bool, error) {
	overrides := map[resource.Kind]string{}
	add := func(kind resource.Kind, path string) error {
		if strings.TrimSpace(path) == "" {
			return nil
		}
		switch kind {
		case resource.KindSkill, resource.KindAgent, resource.KindRule, resource.KindCommand, resource.KindMCPServer, resource.KindHook:
			overrides[kind] = path
			return nil
		default:
			return fmt.Errorf("unsupported --kind-path kind %q (supported: skill, agent, rule, command, mcp-server, hook)", kind)
		}
	}
	for _, raw := range opts.kindPaths {
		key, val, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(val) == "" {
			return nil, false, fmt.Errorf("--kind-path must be kind=path (got %q)", raw)
		}
		if err := add(resource.Kind(strings.TrimSpace(key)), strings.TrimSpace(val)); err != nil {
			return nil, false, err
		}
	}
	for _, pair := range []struct {
		kind resource.Kind
		path string
	}{
		{resource.KindSkill, opts.skillsPath},
		{resource.KindAgent, opts.agentsPath},
		{resource.KindRule, opts.rulesPath},
		{resource.KindCommand, opts.commandsPath},
		{resource.KindMCPServer, opts.mcpServersPath},
		{resource.KindHook, opts.hooksPath},
	} {
		if err := add(pair.kind, pair.path); err != nil {
			return nil, false, err
		}
	}
	return overrides, len(overrides) > 0, nil
}

func dirsForTarget(targetRoot string) (dirs.Dirs, string, error) {
	d, err := dirs.FromEnv()
	if err != nil {
		return dirs.Dirs{}, "", err
	}
	if targetRoot != "" {
		abs, err := filepath.Abs(targetRoot)
		if err != nil {
			return dirs.Dirs{}, "", fmt.Errorf("resolve --target: %w", err)
		}
		st, err := os.Stat(abs)
		if err != nil {
			return dirs.Dirs{}, "", fmt.Errorf("stat --target %s: %w", abs, err)
		}
		if !st.IsDir() {
			return dirs.Dirs{}, "", fmt.Errorf("--target %s is not a directory", abs)
		}
		d.ProjectHome = abs
	}
	if d.ProjectHome == "" {
		return dirs.Dirs{}, "", fmt.Errorf("target project root is unavailable")
	}
	return d, d.ProjectHome, nil
}

func targetRootForScope(scope adapter.Scope, d dirs.Dirs) string {
	if scope == adapter.ScopeProject {
		return d.ProjectHome
	}
	return ""
}

func inferCanonicalRoot(sourcePath string) string {
	path := filepath.Clean(sourcePath)
	for {
		if filepath.Base(path) == ".agents" {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func manifestReader(d dirs.Dirs) *orchestrator.Reader {
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	return orchestrator.NewReader(d, mf)
}

func discoverCanonicalResources(layout sourceLayout) ([]canonicalEntry, error) {
	var out []canonicalEntry
	add := func(kind resource.Kind, path string) error {
		res, err := loadResource(kind, path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}
		out = append(out, canonicalEntry{Kind: kind, Path: path, Resource: res})
		return nil
	}

	if err := walkDirect(layout.kindDir(resource.KindSkill), func(path string, entry fs.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		if filepath.Base(path) == "SKILL.md" {
			return add(resource.KindSkill, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, spec := range []struct {
		kind resource.Kind
		exts map[string]struct{}
	}{
		{kind: resource.KindAgent, exts: map[string]struct{}{".md": {}}},
		{kind: resource.KindRule, exts: map[string]struct{}{".md": {}}},
		{kind: resource.KindCommand, exts: map[string]struct{}{".md": {}, ".toml": {}}},
		{kind: resource.KindMCPServer, exts: map[string]struct{}{".json": {}}},
		{kind: resource.KindHook, exts: map[string]struct{}{".json": {}}},
	} {
		if err := walkDirect(layout.kindDir(spec.kind), func(path string, entry fs.DirEntry) error {
			if entry.IsDir() {
				return nil
			}
			if spec.kind == resource.KindHook && filepath.Base(path) == "registry.json" {
				return nil
			}
			if _, ok := spec.exts[filepath.Ext(path)]; ok {
				return add(spec.kind, path)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (l sourceLayout) kindDir(kind resource.Kind) string {
	path := l.paths[kind]
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(l.root, path)
}

func walkDirect(root string, fn func(path string, entry fs.DirEntry) error) error {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.IsDir() && filepath.Dir(path) != root {
			return filepath.SkipDir
		}
		return fn(path, entry)
	})
}

func expectedFilesFromCanonical(agentsRoot, agentName string, scope adapter.Scope, d dirs.Dirs) ([]orchestrator.ExpectedFile, error) {
	entries, err := discoverCanonicalResources(sourceLayout{root: agentsRoot, paths: defaultCanonicalKindPaths(false)})
	if err != nil {
		return nil, err
	}
	var expected []orchestrator.ExpectedFile
	for _, entry := range entries {
		plans, unsupported, err := plansForEntry(entry, agentName, scope, d)
		if err != nil {
			return nil, err
		}
		if unsupported {
			continue
		}
		for _, plan := range plans {
			for _, fw := range plan.Files {
				expected = append(expected, orchestrator.ExpectedFile{
					Path:   fw.Path,
					Source: entry.Path,
					SHA256: sha256String(fw.Content),
				})
			}
		}
	}
	return expected, nil
}

func installCanonicalEntry(entry canonicalEntry, agentName string, scope adapter.Scope, allowLossy, force bool, canonicalRoot, targetRoot string, d dirs.Dirs, mf *manifest.Store) (orchestrator.InstallResult, bool, error) {
	opts := orchestrator.InstallOptions{
		Source:        "file://" + entry.Path,
		CanonicalRoot: canonicalRoot,
		TargetRoot:    targetRoot,
		AllowLossy:    allowLossy,
		Force:         force,
	}
	if _, ok := umbrellaFactories[agentName]; ok {
		subs, writers, err := buildUmbrella(agentName, d)
		if err != nil {
			return orchestrator.InstallResult{}, false, err
		}
		if _, ok := writers[entry.Kind]; !ok {
			return orchestrator.InstallResult{}, true, nil
		}
		result, err := orchestrator.NewUmbrellaInstaller(d, agentName, subs, writers, mf).Install(entry.Resource, scope, opts)
		return result, isUnsupportedError(err), err
	}
	a, err := buildAdapter(agentName, d)
	if err != nil {
		return orchestrator.InstallResult{}, false, err
	}
	result, err := orchestrator.NewInstaller(d, a, mf).Install(entry.Resource, scope, opts)
	return result, isUnsupportedError(err), err
}

func plansForEntry(entry canonicalEntry, agentName string, scope adapter.Scope, d dirs.Dirs) ([]adapter.InstallPlan, bool, error) {
	if _, ok := umbrellaFactories[agentName]; ok {
		_, writers, err := buildUmbrella(agentName, d)
		if err != nil {
			return nil, false, err
		}
		ws := writers[entry.Kind]
		if len(ws) == 0 {
			return nil, true, nil
		}
		var plans []adapter.InstallPlan
		for _, writer := range ws {
			plan, err := writer.Plan(entry.Resource, scope)
			if err != nil {
				if isUnsupportedError(err) {
					return nil, true, nil
				}
				return nil, false, err
			}
			plans = append(plans, plan)
		}
		return plans, false, nil
	}
	a, err := buildAdapter(agentName, d)
	if err != nil {
		return nil, false, err
	}
	plan, err := a.Plan(entry.Resource, scope)
	if err != nil {
		if isUnsupportedError(err) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return []adapter.InstallPlan{plan}, false, nil
}

func isUnsupportedError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not supported")
}

func scanMaterializedFiles(targetRoot string) ([]orchestrator.FileObservation, error) {
	var out []orchestrator.FileObservation
	addNested := func(agent, kind, root, fileName string) error {
		return walkDirect(root, func(path string, entry fs.DirEntry) error {
			if !entry.IsDir() {
				return nil
			}
			file := filepath.Join(path, fileName)
			if st, err := os.Stat(file); err == nil && !st.IsDir() {
				out = append(out, orchestrator.FileObservation{
					Path:      file,
					TargetDir: path,
					Agent:     agent,
					Kind:      kind,
					Name:      filepath.Base(path),
				})
			}
			return nil
		})
	}
	addFlat := func(agent, kind, root string, exts ...string) error {
		extOK := map[string]struct{}{}
		for _, ext := range exts {
			extOK[ext] = struct{}{}
		}
		return walkDirect(root, func(path string, entry fs.DirEntry) error {
			if entry.IsDir() {
				return nil
			}
			ext := filepath.Ext(path)
			if _, ok := extOK[ext]; !ok {
				return nil
			}
			out = append(out, orchestrator.FileObservation{
				Path:  path,
				Agent: agent,
				Kind:  kind,
				Name:  strings.TrimSuffix(filepath.Base(path), ext),
			})
			return nil
		})
	}

	if err := addNested("claude-code", "skill", filepath.Join(targetRoot, ".claude", "skills"), "SKILL.md"); err != nil {
		return nil, err
	}
	if err := addNested("gemini-cli", "skill", filepath.Join(targetRoot, ".gemini", "skills"), "SKILL.md"); err != nil {
		return nil, err
	}
	if err := addNested("antigravity-cli", "skill", filepath.Join(targetRoot, ".antigravity", "skills"), "SKILL.md"); err != nil {
		return nil, err
	}
	if err := addNested("codex", "skill", filepath.Join(targetRoot, ".agents", "skills"), "SKILL.md"); err != nil {
		return nil, err
	}

	for _, spec := range []struct {
		agent  string
		subdir string
		kind   string
		exts   []string
	}{
		{"claude-code", ".claude/agents", "agent", []string{".md"}},
		{"gemini-cli", ".gemini/agents", "agent", []string{".md"}},
		{"antigravity-cli", ".antigravity/agents", "agent", []string{".md"}},
		{"codex", ".codex/agents", "agent", []string{".toml"}},
		{"claude-code", ".claude/rules", "rule", []string{".md"}},
		{"gemini-cli", ".gemini/rules", "rule", []string{".md"}},
		{"antigravity-cli", ".antigravity/rules", "rule", []string{".md"}},
		{"codex", ".codex/rules", "rule", []string{".md"}},
		{"claude-code", ".claude/commands", "command", []string{".md"}},
		{"gemini-cli", ".gemini/commands", "command", []string{".toml"}},
		{"antigravity-cli", ".antigravity/commands", "command", []string{".md"}},
		{"codex", ".codex/commands", "command", []string{".md"}},
	} {
		if err := addFlat(spec.agent, spec.kind, filepath.Join(targetRoot, filepath.FromSlash(spec.subdir)), spec.exts...); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func canonicalDestination(agentsRoot string, obs orchestrator.FileObservation) (string, bool) {
	ext := filepath.Ext(obs.Path)
	switch obs.Kind {
	case "skill":
		return filepath.Join(agentsRoot, "skills", obs.Name, "SKILL.md"), true
	case "agent":
		if ext != ".md" {
			return "", false
		}
		return filepath.Join(agentsRoot, "agents", obs.Name+".md"), true
	case "rule":
		if ext != ".md" {
			return "", false
		}
		return filepath.Join(agentsRoot, "rules", obs.Name+".md"), true
	case "command":
		if ext != ".md" && ext != ".toml" {
			return "", false
		}
		return filepath.Join(agentsRoot, "commands", obs.Name+ext), true
	default:
		return "", false
	}
}

func itemToObservation(item orchestrator.InventoryItem) orchestrator.FileObservation {
	return orchestrator.FileObservation{
		Path:      item.Path,
		TargetDir: item.TargetDir,
		Agent:     item.Agent,
		Kind:      item.Kind,
		Name:      item.Name,
	}
}

func copyIfNeeded(src, dst string, force bool) (bool, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", src, err)
	}
	if existing, err := os.ReadFile(dst); err == nil {
		if string(existing) == string(raw) {
			return false, nil
		}
		if !force {
			return false, fmt.Errorf("canonical target %s already exists and differs; pass --force to overwrite", dst)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", dst, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", dst, err)
	}
	return true, nil
}

func sha256String(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
