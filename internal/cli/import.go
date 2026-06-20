package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	var (
		outDir string
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "import <source-agent> <source-path>",
		Short: "Convert a native agent-host tree into .agents",
		Long: `Convert a native agent-host tree into a canonical .agents tree.

Supported today:
  dotpack import claude-code <project-or-.claude-path> --out <project-dir>

The importer copies durable configuration, skips runtime state, and rewrites
repository-local .claude path references to .agents. Host compatibility output
still belongs to dotpack install/reconcile, not to checked-in generated trees.

To translate .agents resources back into .claude, .gemini, .codex/config.toml,
or shared .agents/skills host files, use dotpack install on a specific
resource or dotpack install-all for a full canonical .agents tree.`,
		Example: `  dotpack import claude-code . --out .
  dotpack install .agents/skills/demo/SKILL.md --agent claude-code --scope project
  dotpack install .agents/skills/demo/SKILL.md --agent gemini-cli --scope project
  dotpack install .agents/skills/demo/SKILL.md --agent codex --scope project`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(cmd, args[0], args[1], outDir, force)
		},
	}

	cmd.Flags().StringVar(&outDir, "out", ".", "Output project directory that will receive .agents")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing files in .agents")
	return cmd
}

func runImport(cmd *cobra.Command, sourceAgent, sourcePath, outDir string, force bool) error {
	if sourceAgent != "claude-code" {
		return fmt.Errorf("import: source agent %q not supported yet (supported: claude-code)", sourceAgent)
	}

	claudeRoot, err := resolveClaudeImportRoot(sourcePath)
	if err != nil {
		return err
	}
	outAbs, err := filepath.Abs(outDir)
	if err != nil {
		return fmt.Errorf("import: resolve --out: %w", err)
	}
	agentsRoot := filepath.Join(outAbs, ".agents")

	imp := claudeImporter{
		claudeRoot: claudeRoot,
		agentsRoot: agentsRoot,
		force:      force,
		written:    map[string]struct{}{},
	}
	if err := imp.run(); err != nil {
		return err
	}

	paths := make([]string, 0, len(imp.written))
	for path := range imp.written {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	cmd.Printf("Imported claude-code configuration into %s\n", agentsRoot)
	for _, path := range paths {
		cmd.Printf("  wrote %s\n", path)
	}
	return nil
}

func resolveClaudeImportRoot(sourcePath string) (string, error) {
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("import: resolve source path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("import: source path %s: %w", sourcePath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("import: source path %s is not a directory", sourcePath)
	}
	if filepath.Base(abs) == ".claude" {
		return abs, nil
	}
	nested := filepath.Join(abs, ".claude")
	if st, err := os.Stat(nested); err == nil && st.IsDir() {
		return nested, nil
	}
	return "", fmt.Errorf("import: %s is neither a .claude directory nor a project containing .claude", sourcePath)
}

type claudeImporter struct {
	claudeRoot string
	agentsRoot string
	force      bool
	written    map[string]struct{}
}

type claudeImportMapping struct {
	src string
	dst string
}

var claudeImportMappings = []claudeImportMapping{
	{src: "agents", dst: filepath.Join("agents", "claude")},
	{src: "commands", dst: filepath.Join("commands", "claude")},
	{src: "hooks", dst: "hooks"},
	{src: "judge", dst: "judge"},
	{src: "lib", dst: "lib"},
	{src: "mem0", dst: "mem0"},
	{src: "model-bridge", dst: "model-bridge"},
	{src: "oracle-db", dst: "oracle-db"},
	{src: "prompts", dst: "prompts"},
	{src: "rules", dst: filepath.Join("rules", "claude")},
	{src: "scripts", dst: "scripts"},
	{src: "skills", dst: "skills"},
	{src: "teams", dst: "teams"},
	{src: "telemetry", dst: "telemetry"},
	{src: "templates", dst: "templates"},
	{src: "tests", dst: "tests"},
}

var claudeRootFiles = map[string]string{
	"approval-fallback.mjs":  "approval-fallback.mjs",
	"CLAUDE.md":              "agents.md",
	"heartbeat-writer.mjs":   "heartbeat-writer.mjs",
	"metrics-collector.mjs":  "metrics-collector.mjs",
	"metrics-schema.json":    "metrics-schema.json",
	"review-tiers.json":      "review-tiers.json",
	"session-correlator.mjs": "session-correlator.mjs",
	"watchdog.mjs":           "watchdog.mjs",
	"worktree-manager.mjs":   "worktree-manager.mjs",
}

func (i *claudeImporter) run() error {
	for _, mapping := range claudeImportMappings {
		if err := i.copyMaybe(filepath.Join(i.claudeRoot, mapping.src), filepath.Join(i.agentsRoot, mapping.dst)); err != nil {
			return err
		}
	}
	for src, dst := range claudeRootFiles {
		if err := i.copyMaybe(filepath.Join(i.claudeRoot, src), filepath.Join(i.agentsRoot, dst)); err != nil {
			return err
		}
	}
	if err := i.importSettings(); err != nil {
		return err
	}
	return nil
}

func (i *claudeImporter) copyMaybe(src, dst string) error {
	info, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("import: stat %s: %w", src, err)
	}
	if sameResolvedPath(src, dst) {
		return nil
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return i.copyTree(src, dst)
	}
	return i.copyFile(src, dst, info.Mode().Perm())
}

func (i *claudeImporter) copyTree(src, dst string) error {
	rootInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("import: stat %s: %w", src, err)
	}
	if !rootInfo.IsDir() {
		return i.copyFile(src, dst, rootInfo.Mode().Perm())
	}

	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if shouldSkipClaudeImportPath(rel, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return i.copyFile(path, target, info.Mode().Perm())
	})
}

func (i *claudeImporter) copyFile(src, dst string, mode fs.FileMode) error {
	if shouldSkipClaudeImportFile(filepath.Base(src)) {
		return nil
	}
	if !i.force {
		if _, err := os.Lstat(dst); err == nil {
			return fmt.Errorf("import: refusing to overwrite %s (pass --force)", dst)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("import: stat target %s: %w", dst, err)
		}
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("import: read %s: %w", src, err)
	}
	if shouldRewriteClaudeImportFile(src) {
		raw = rewriteClaudePathRefs(raw)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("import: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, raw, normalizeFileMode(mode)); err != nil {
		return fmt.Errorf("import: write %s: %w", dst, err)
	}
	i.written[dst] = struct{}{}
	return nil
}

func (i *claudeImporter) importSettings() error {
	src := filepath.Join(i.claudeRoot, "settings.json")
	raw, err := os.ReadFile(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("import: read %s: %w", src, err)
	}
	raw = rewriteClaudePathRefs(raw)

	configDst := filepath.Join(i.agentsRoot, "config", "claude-code.settings.json")
	if err := i.writeGeneratedJSON(configDst, raw); err != nil {
		return err
	}

	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("import: parse %s: %w", src, err)
	}
	hooks, ok := settings["hooks"]
	if !ok {
		return nil
	}
	wrapper := map[string]json.RawMessage{"hooks": hooks}
	hookRaw, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return fmt.Errorf("import: encode hooks registry: %w", err)
	}
	hookRaw = append(hookRaw, '\n')
	return i.writeGeneratedJSON(filepath.Join(i.agentsRoot, "hooks", "registry.json"), hookRaw)
}

func (i *claudeImporter) writeGeneratedJSON(dst string, raw []byte) error {
	if !i.force {
		if _, err := os.Lstat(dst); err == nil {
			return fmt.Errorf("import: refusing to overwrite %s (pass --force)", dst)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("import: stat target %s: %w", dst, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("import: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		return fmt.Errorf("import: write %s: %w", dst, err)
	}
	i.written[dst] = struct{}{}
	return nil
}

func sameResolvedPath(src, dst string) bool {
	srcReal, srcErr := filepath.EvalSymlinks(src)
	dstReal, dstErr := filepath.EvalSymlinks(dst)
	if srcErr != nil || dstErr != nil {
		return false
	}
	srcAbs, srcErr := filepath.Abs(srcReal)
	dstAbs, dstErr := filepath.Abs(dstReal)
	return srcErr == nil && dstErr == nil && srcAbs == dstAbs
}

func shouldSkipClaudeImportPath(rel string, entry fs.DirEntry) bool {
	name := entry.Name()
	if shouldSkipClaudeImportFile(name) {
		return true
	}
	if entry.IsDir() {
		switch name {
		case ".claude", "checkpoints", "conversations", "coverage", "hook-execution-ledger.local", "jobs", "lemons", "logs", "node_modules", "projects", "raw-v8-coverage", "research-gate", "scratchpad", "state", "test-enforcement", "verdicts", "worktrees":
			return true
		}
	}
	normalized := filepath.ToSlash(rel)
	if strings.HasPrefix(normalized, "judge/state/") ||
		strings.HasPrefix(normalized, "judge/logs/") ||
		strings.HasPrefix(normalized, "judge/verdicts/") ||
		strings.HasPrefix(normalized, "judge/lemons/") {
		return true
	}
	return false
}

func shouldSkipClaudeImportFile(name string) bool {
	return name == ".DS_Store" ||
		strings.Contains(name, ".local.") ||
		strings.HasSuffix(name, ".lock") ||
		strings.HasSuffix(name, ".ledger")
}

func shouldRewriteClaudeImportFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".md", ".mjs", ".js", ".ts", ".sh", ".py", ".toml", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

var claudePathRefRE = regexp.MustCompile(`\.claude([^A-Za-z0-9_-]|$)`)

func rewriteClaudePathRefs(raw []byte) []byte {
	return claudePathRefRE.ReplaceAll(raw, []byte(".agents$1"))
}

func normalizeFileMode(mode fs.FileMode) fs.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}
