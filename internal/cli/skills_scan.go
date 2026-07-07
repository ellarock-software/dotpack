package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/internal/skillspector"
)

const defaultBaselineReason = "Accepted SkillSpector finding after review."

type scanSkillsOptions struct {
	layout      sourceLayoutOptions
	baseRef     string
	baselineDir string
	format      string
	outputPath  string
	skillNames  []string
	changed     bool
	reportOnly  bool
}

type baselineSkillsOptions struct {
	layout      sourceLayoutOptions
	baseRef     string
	baselineDir string
	outputPath  string
	reason      string
	skillNames  []string
	changed     bool
}

type skillScanSelection struct {
	SourceRoot string
	SkillRoot  string
	Targets    []skillScanTarget
}

type skillScanTarget struct {
	Name         string
	SkillDir     string
	SkillFile    string
	RelativePath string
}

type skillScanCommandOutput struct {
	Summary skillScanSummary         `json:"summary"`
	Results []skillScanResultPayload `json:"results"`
}

type skillScanSummary struct {
	GeneratedAt     string                         `json:"generated_at"`
	Command         string                         `json:"command"`
	SourceRoot      string                         `json:"source_root"`
	SkillRoot       string                         `json:"skill_root"`
	BaseRef         string                         `json:"base_ref,omitempty"`
	BaselineDir     string                         `json:"baseline_dir,omitempty"`
	Runtime         skillspector.RuntimeMetadata   `json:"runtime"`
	OutputFormat    string                         `json:"output_format"`
	GateMode        string                         `json:"gate_mode"`
	GatePass        bool                           `json:"gate_pass"`
	ReportOnly      bool                           `json:"report_only"`
	SkillsScanned   int                            `json:"skills_scanned"`
	IssueCount      int                            `json:"issue_count"`
	SuppressedCount int                            `json:"suppressed_count"`
	MaxScore        float64                        `json:"max_score"`
	Pass            bool                           `json:"pass"`
	RunDirectory    string                         `json:"run_directory"`
	SelectedSkills  []skillScanTargetSummary       `json:"selected_skills"`
	FailingSkills   []skillScanFailingSkillSummary `json:"failing_skills"`
}

type skillScanTargetSummary struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type skillScanFailingSkillSummary struct {
	Name           string   `json:"name"`
	Path           string   `json:"path"`
	Score          float64  `json:"score"`
	Recommendation string   `json:"recommendation"`
	Issues         []string `json:"issues"`
}

type skillScanResultPayload struct {
	Skill          string          `json:"skill"`
	SkillPath      string          `json:"skill_path"`
	ReportPath     string          `json:"report_path"`
	BaselinePath   string          `json:"baseline_path,omitempty"`
	IssueCount     int             `json:"issue_count"`
	Suppressed     int             `json:"suppressed_count"`
	Score          float64         `json:"score"`
	Recommendation string          `json:"recommendation"`
	Report         json.RawMessage `json:"report"`
}

type skillSpectorReport struct {
	RiskAssessment struct {
		Score          float64 `json:"score"`
		Recommendation string  `json:"recommendation"`
	} `json:"risk_assessment"`
	Issues []struct {
		ID string `json:"id"`
	} `json:"issues"`
	SuppressedCount int `json:"suppressed_count"`
}

type baselineSkillsOutput struct {
	GeneratedAt string                       `json:"generated_at"`
	Command     string                       `json:"command"`
	SourceRoot  string                       `json:"source_root"`
	SkillRoot   string                       `json:"skill_root"`
	BaseRef     string                       `json:"base_ref,omitempty"`
	BaselineDir string                       `json:"baseline_dir"`
	Runtime     skillspector.RuntimeMetadata `json:"runtime"`
	Skills      []baselineSkillResult        `json:"skills"`
}

type baselineSkillResult struct {
	Name         string `json:"name"`
	SkillPath    string `json:"skill_path"`
	BaselinePath string `json:"baseline_path"`
}

func newScanSkillsCmd() *cobra.Command {
	var opts scanSkillsOptions
	cmd := &cobra.Command{
		Use:   "scan-skills [source]",
		Short: "Run static SkillSpector scans against dotpack skills",
		Long: `Scan one skill or a skill catalog with NVIDIA SkillSpector.

scan-skills accepts:
  - a skill directory containing SKILL.md
  - a direct SKILL.md path
  - a canonical .agents tree
  - a project root containing .agents
  - a non-canonical root when paired with --skills-path or --kind-path skill=...

The scan runtime is provisioned under DOTPACK_DOTPACK_HOME, pinned to a
specific upstream repo commit/version, and reused across runs via persisted
runtime metadata.

Static-only mode is the default. scan-skills gates by default: the command
returns non-zero when unsuppressed findings remain. Pass --report-only to keep
the findings in the output while returning zero.`,
		Example: `  dotpack scan-skills /path/to/project/.agents
  dotpack scan-skills /path/to/project --skills-path skills --format sarif --output /tmp/skills.sarif
  dotpack scan-skills /path/to/project/.agents --baseline-dir /path/to/project/.dotpack/skillspector/baselines
  dotpack scan-skills /path/to/project/.agents --changed --base origin/main --skill reviewer`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := "."
			if len(args) == 1 {
				source = args[0]
			}
			return runScanSkills(cmd, source, opts)
		},
	}
	addSkillScanLayoutFlags(cmd, &opts.layout)
	cmd.Flags().StringVar(&opts.baseRef, "base", "HEAD", "Base git ref for --changed selection")
	cmd.Flags().StringVar(&opts.baselineDir, "baseline-dir", "", "Apply per-skill baseline files from this directory")
	cmd.Flags().StringVar(&opts.format, "format", "terminal", "Aggregate output format (terminal|json|markdown|sarif)")
	cmd.Flags().StringVar(&opts.outputPath, "output", "", "Write aggregate output to this path")
	cmd.Flags().StringArrayVar(&opts.skillNames, "skill", nil, "Limit the run to a named skill; repeatable")
	cmd.Flags().BoolVar(&opts.changed, "changed", false, "Only scan changed skills in the enclosing git checkout")
	cmd.Flags().BoolVar(&opts.reportOnly, "report-only", false, "Report findings without failing the command")
	return cmd
}

func newBaselineSkillsCmd() *cobra.Command {
	var opts baselineSkillsOptions
	cmd := &cobra.Command{
		Use:   "baseline-skills [source]",
		Short: "Generate per-skill SkillSpector baselines",
		Long: `Generate one SkillSpector baseline file per selected skill.

The source selection rules match scan-skills. Baseline files are written as
<baseline-dir>/<skill-name>.yaml and can be applied later through
dotpack scan-skills --baseline-dir ...`,
		Example: `  dotpack baseline-skills /path/to/project/.agents --baseline-dir /path/to/project/.dotpack/skillspector/baselines
  dotpack baseline-skills /path/to/project --skills-path skills --baseline-dir /tmp/skill-baselines --changed --base origin/main`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := "."
			if len(args) == 1 {
				source = args[0]
			}
			return runBaselineSkills(cmd, source, opts)
		},
	}
	addSkillScanLayoutFlags(cmd, &opts.layout)
	cmd.Flags().StringVar(&opts.baseRef, "base", "HEAD", "Base git ref for --changed selection")
	cmd.Flags().StringVar(&opts.baselineDir, "baseline-dir", "", "Directory where per-skill baseline YAML files will be written")
	cmd.Flags().StringVar(&opts.outputPath, "output", "", "Write a JSON summary to this path")
	cmd.Flags().StringVar(&opts.reason, "reason", defaultBaselineReason, "Reason written into generated baseline entries")
	cmd.Flags().StringArrayVar(&opts.skillNames, "skill", nil, "Limit the run to a named skill; repeatable")
	cmd.Flags().BoolVar(&opts.changed, "changed", false, "Only baseline changed skills in the enclosing git checkout")
	if err := cmd.MarkFlagRequired("baseline-dir"); err != nil {
		panic(fmt.Sprintf("baseline-skills: mark baseline-dir required: %v", err))
	}
	return cmd
}

func addSkillScanLayoutFlags(cmd *cobra.Command, opts *sourceLayoutOptions) {
	cmd.Flags().StringArrayVar(&opts.kindPaths, "kind-path", nil, "Override the skill discovery path as skill=path")
	cmd.Flags().StringVar(&opts.skillsPath, "skills-path", "", "Override the skill discovery path relative to the source root")
}

func runScanSkills(cmd *cobra.Command, source string, opts scanSkillsOptions) error {
	if err := validateSkillScanFormat(opts.format); err != nil {
		return err
	}
	d, err := dirs.FromEnv()
	if err != nil {
		return err
	}
	selection, err := resolveSkillScanSelection(source, opts.layout, opts.skillNames, opts.changed, opts.baseRef, d)
	if err != nil {
		return err
	}
	if len(selection.Targets) == 0 {
		cmd.Println("scan-skills: no skills selected")
		return nil
	}
	runtimeInfo, err := ensureSkillSpectorRuntime(d.DotpackHome)
	if err != nil {
		return fmt.Errorf("ensure SkillSpector runtime: %w", err)
	}
	runDir, err := createSkillSpectorRunDir(d.DotpackHome, "scan-skills")
	if err != nil {
		return err
	}
	baselineDir, err := resolveOptionalAbsPath(opts.baselineDir)
	if err != nil {
		return fmt.Errorf("resolve --baseline-dir: %w", err)
	}
	results, sarifPaths, err := runSkillScans(selection.Targets, runDir, baselineDir, opts.format, runtimeInfo)
	if err != nil {
		return err
	}
	aggregate := buildSkillScanOutput("scan-skills", selection, results, runDir, baselineDir, opts.baseRef, opts.changed, opts.format, opts.reportOnly, runtimeInfo.Metadata)
	outputPath, err := resolveAggregateOutputPath(opts.outputPath, runDir, "scan-skills", opts.format)
	if err != nil {
		return err
	}
	if err := writeSkillScanOutput(opts.format, outputPath, aggregate, sarifPaths); err != nil {
		return err
	}
	if opts.format == "terminal" {
		printSkillScanTerminalSummary(cmd, aggregate, outputPath)
	} else {
		cmd.Println(outputPath)
	}
	if !opts.reportOnly && !aggregate.Summary.Pass {
		return fmt.Errorf("scan-skills gate failed: unsuppressed findings remain")
	}
	return nil
}

func runBaselineSkills(cmd *cobra.Command, source string, opts baselineSkillsOptions) error {
	d, err := dirs.FromEnv()
	if err != nil {
		return err
	}
	selection, err := resolveSkillScanSelection(source, opts.layout, opts.skillNames, opts.changed, opts.baseRef, d)
	if err != nil {
		return err
	}
	if len(selection.Targets) == 0 {
		cmd.Println("baseline-skills: no skills selected")
		return nil
	}
	runtimeInfo, err := ensureSkillSpectorRuntime(d.DotpackHome)
	if err != nil {
		return fmt.Errorf("ensure SkillSpector runtime: %w", err)
	}
	baselineDir, err := resolveRequiredAbsPath(opts.baselineDir)
	if err != nil {
		return fmt.Errorf("resolve --baseline-dir: %w", err)
	}
	if err := os.MkdirAll(baselineDir, 0o755); err != nil {
		return fmt.Errorf("create baseline directory %s: %w", baselineDir, err)
	}
	out := baselineSkillsOutput{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Command:     "baseline-skills",
		SourceRoot:  selection.SourceRoot,
		SkillRoot:   selection.SkillRoot,
		BaselineDir: baselineDir,
		Runtime:     runtimeInfo.Metadata,
	}
	if opts.changed {
		out.BaseRef = opts.baseRef
	}
	for _, target := range selection.Targets {
		baselinePath := filepath.Join(baselineDir, target.Name+".yaml")
		if err := runSkillSpector(runtimeInfo.SkillSpectorBin, "baseline", target.SkillDir, "--no-llm", "--output", baselinePath, "--reason", opts.reason); err != nil {
			return err
		}
		out.Skills = append(out.Skills, baselineSkillResult{
			Name:         target.Name,
			SkillPath:    target.RelativePath,
			BaselinePath: baselinePath,
		})
	}
	sort.Slice(out.Skills, func(i, j int) bool { return out.Skills[i].Name < out.Skills[j].Name })
	if opts.outputPath != "" {
		outputPath, err := resolveRequiredAbsPath(opts.outputPath)
		if err != nil {
			return fmt.Errorf("resolve --output: %w", err)
		}
		raw, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("encode baseline summary: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			return fmt.Errorf("create output parent %s: %w", filepath.Dir(outputPath), err)
		}
		if err := os.WriteFile(outputPath, append(raw, '\n'), 0o644); err != nil {
			return fmt.Errorf("write baseline summary %s: %w", outputPath, err)
		}
	}
	cmd.Printf("baseline-skills complete: generated=%d baseline_dir=%s\n", len(out.Skills), baselineDir)
	for _, item := range out.Skills {
		cmd.Printf("  wrote %s for %s\n", item.BaselinePath, item.Name)
	}
	return nil
}

func validateSkillScanFormat(format string) error {
	switch format {
	case "terminal", "json", "markdown", "sarif":
		return nil
	default:
		return fmt.Errorf("unsupported --format %q (supported: terminal, json, markdown, sarif)", format)
	}
}

func resolveSkillScanSelection(source string, layout sourceLayoutOptions, skillNames []string, changed bool, baseRef string, d dirs.Dirs) (skillScanSelection, error) {
	overridePath, hasOverride, err := parseSkillPathOverride(layout)
	if err != nil {
		return skillScanSelection{}, err
	}
	resolvedSource, err := resolveSkillScanInput(source, d)
	if err != nil {
		return skillScanSelection{}, err
	}
	if !hasOverride {
		if selection, ok, err := resolveSingleSkillSelection(resolvedSource); err != nil {
			return skillScanSelection{}, err
		} else if ok {
			return filterSkillScanSelection(selection, skillNames, changed, baseRef)
		}
	}

	sourceRoot, skillRoot, err := resolveSkillScanRoots(resolvedSource, overridePath, hasOverride)
	if err != nil {
		return skillScanSelection{}, err
	}
	targets, err := discoverSkillScanTargets(sourceRoot, skillRoot)
	if err != nil {
		return skillScanSelection{}, err
	}
	return filterSkillScanSelection(skillScanSelection{
		SourceRoot: sourceRoot,
		SkillRoot:  skillRoot,
		Targets:    targets,
	}, skillNames, changed, baseRef)
}

func parseSkillPathOverride(layout sourceLayoutOptions) (string, bool, error) {
	overrides, has, err := parseSourceLayoutOverrides(layout)
	if err != nil {
		return "", false, err
	}
	if !has {
		return "", false, nil
	}
	for kind := range overrides {
		if kind != resource.KindSkill {
			return "", false, fmt.Errorf("scan-skills only accepts skill discovery overrides (unsupported kind %q)", kind)
		}
	}
	return overrides[resource.KindSkill], true, nil
}

func resolveSkillScanInput(source string, d dirs.Dirs) (string, error) {
	if source == "" {
		source = "."
	}
	if cached, ok, err := resolveMaybeRemoteSource(source, d); err != nil {
		return "", err
	} else if ok {
		return cached, nil
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve source %q: %w", source, err)
	}
	return abs, nil
}

func resolveSingleSkillSelection(source string) (skillScanSelection, bool, error) {
	info, err := os.Stat(source)
	if err != nil {
		return skillScanSelection{}, false, fmt.Errorf("stat source %s: %w", source, err)
	}
	var skillFile string
	switch {
	case !info.IsDir() && filepath.Base(source) == "SKILL.md":
		skillFile = source
	case info.IsDir() && pathIsRegularFile(filepath.Join(source, "SKILL.md")):
		skillFile = filepath.Join(source, "SKILL.md")
	default:
		return skillScanSelection{}, false, nil
	}
	target, err := buildSkillScanTarget(skillFile, inferSingleSkillSourceRoot(skillFile))
	if err != nil {
		return skillScanSelection{}, false, err
	}
	return skillScanSelection{
		SourceRoot: inferSingleSkillSourceRoot(skillFile),
		SkillRoot:  filepath.Dir(skillFile),
		Targets:    []skillScanTarget{target},
	}, true, nil
}

func inferSingleSkillSourceRoot(skillFile string) string {
	if canonical := inferCanonicalRoot(skillFile); canonical != "" {
		return canonical
	}
	skillDir := filepath.Dir(skillFile)
	return filepath.Dir(skillDir)
}

func resolveSkillScanRoots(source, overridePath string, hasOverride bool) (string, string, error) {
	if hasOverride {
		root, err := resolveSourceRoot(source)
		if err != nil {
			return "", "", err
		}
		path := overridePath
		if path == "" {
			path = defaultCanonicalKindPaths(true)[resource.KindSkill]
		}
		if filepath.IsAbs(path) {
			return root, filepath.Clean(path), nil
		}
		return root, filepath.Join(root, path), nil
	}
	agentsRoot, err := resolveAgentsRoot(source)
	if err != nil {
		return "", "", err
	}
	return agentsRoot, filepath.Join(agentsRoot, "skills"), nil
}

func discoverSkillScanTargets(sourceRoot, skillRoot string) ([]skillScanTarget, error) {
	targets := make([]skillScanTarget, 0)
	seen := map[string]string{}
	if err := walkDirect(skillRoot, func(path string, entry fs.DirEntry) error {
		if entry.IsDir() || filepath.Base(path) != "SKILL.md" {
			return nil
		}
		target, err := buildSkillScanTarget(path, sourceRoot)
		if err != nil {
			return err
		}
		if prior, ok := seen[target.Name]; ok {
			return fmt.Errorf("duplicate skill name %q at %s and %s", target.Name, prior, target.SkillDir)
		}
		seen[target.Name] = target.SkillDir
		targets = append(targets, target)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets, nil
}

func buildSkillScanTarget(skillFile, sourceRoot string) (skillScanTarget, error) {
	skillDir := filepath.Dir(skillFile)
	name := filepath.Base(skillDir)
	if res, err := loadResource(resource.KindSkill, skillFile); err == nil {
		if skill, ok := res.(*resource.Skill); ok && strings.TrimSpace(skill.Name) != "" {
			name = skill.Name
		}
	}
	rel, err := filepath.Rel(sourceRoot, skillDir)
	if err != nil {
		return skillScanTarget{}, fmt.Errorf("relative path for %s: %w", skillDir, err)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = filepath.Base(skillDir)
	}
	return skillScanTarget{
		Name:         name,
		SkillDir:     skillDir,
		SkillFile:    skillFile,
		RelativePath: rel,
	}, nil
}

func filterSkillScanSelection(selection skillScanSelection, skillNames []string, changed bool, baseRef string) (skillScanSelection, error) {
	targets, err := filterSkillTargetsByName(selection.Targets, skillNames)
	if err != nil {
		return skillScanSelection{}, err
	}
	if changed {
		targets, err = filterChangedSkillTargets(selection.SkillRoot, targets, baseRef)
		if err != nil {
			return skillScanSelection{}, err
		}
	}
	selection.Targets = targets
	return selection, nil
}

func filterSkillTargetsByName(targets []skillScanTarget, skillNames []string) ([]skillScanTarget, error) {
	if len(skillNames) == 0 {
		return targets, nil
	}
	byName := map[string]skillScanTarget{}
	for _, target := range targets {
		byName[target.Name] = target
	}
	selected := make([]skillScanTarget, 0, len(skillNames))
	missing := make([]string, 0)
	for _, name := range skillNames {
		target, ok := byName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		selected = append(selected, target)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("unknown skill name(s): %s", strings.Join(missing, ", "))
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })
	return selected, nil
}

func filterChangedSkillTargets(skillRoot string, targets []skillScanTarget, baseRef string) ([]skillScanTarget, error) {
	if len(targets) == 0 {
		return targets, nil
	}
	gitRoot, err := resolveGitTopLevel(skillRoot)
	if err != nil {
		return nil, fmt.Errorf("--changed requires a git checkout enclosing %s: %w", skillRoot, err)
	}
	relRoot, err := filepath.Rel(gitRoot, skillRoot)
	if err != nil {
		return nil, fmt.Errorf("relative git path for %s: %w", skillRoot, err)
	}
	changedPaths, err := collectChangedGitPaths(gitRoot, relRoot, baseRef)
	if err != nil {
		return nil, err
	}
	selected := make([]skillScanTarget, 0, len(targets))
	for _, target := range targets {
		if skillTargetChanged(target, changedPaths) {
			selected = append(selected, target)
		}
	}
	return selected, nil
}

func resolveGitTopLevel(path string) (string, error) {
	out, err := runGitCommand(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func collectChangedGitPaths(gitRoot, relativeRoot, baseRef string) ([]string, error) {
	rootArg := filepath.ToSlash(relativeRoot)
	diffArgs := []string{"diff", "--name-only", "--diff-filter=ACMRTUXB", baseRef}
	untrackedArgs := []string{"ls-files", "--others", "--exclude-standard"}
	if rootArg != "." && rootArg != "" {
		diffArgs = append(diffArgs, "--", rootArg)
		untrackedArgs = append(untrackedArgs, "--", rootArg)
	}
	diffOut, err := runGitCommand(gitRoot, diffArgs...)
	if err != nil {
		return nil, fmt.Errorf("collect changed skills from git diff: %w", err)
	}
	untrackedOut, err := runGitCommand(gitRoot, untrackedArgs...)
	if err != nil {
		return nil, fmt.Errorf("collect changed skills from git ls-files: %w", err)
	}
	seen := map[string]struct{}{}
	paths := make([]string, 0)
	for _, raw := range append(parseGitPathList(diffOut), parseGitPathList(untrackedOut)...) {
		abs := filepath.Clean(filepath.Join(gitRoot, filepath.FromSlash(raw)))
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		paths = append(paths, abs)
	}
	sort.Strings(paths)
	return paths, nil
}

func parseGitPathList(raw []byte) []string {
	lines := strings.Split(string(raw), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func skillTargetChanged(target skillScanTarget, changedPaths []string) bool {
	for _, changed := range changedPaths {
		if pathHasPrefix(changed, target.SkillDir) {
			return true
		}
	}
	return false
}

func pathHasPrefix(path, prefix string) bool {
	path = filepath.Clean(path)
	prefix = filepath.Clean(prefix)
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(os.PathSeparator))
}

type skillScanResult struct {
	Target       skillScanTarget
	BaselinePath string
	ReportPath   string
	Report       skillSpectorReport
	RawReport    json.RawMessage
}

func runSkillScans(targets []skillScanTarget, runDir, baselineDir, format string, runtimeInfo skillspector.Runtime) ([]skillScanResult, []string, error) {
	results := make([]skillScanResult, 0, len(targets))
	sarifPaths := make([]string, 0, len(targets))
	for _, target := range targets {
		jsonPath := filepath.Join(runDir, target.Name+".json")
		baselinePath := ""
		if baselineDir != "" {
			baselinePath = filepath.Join(baselineDir, target.Name+".yaml")
			if !pathIsRegularFile(baselinePath) {
				return nil, nil, fmt.Errorf("missing baseline file for %s: %s", target.Name, baselinePath)
			}
		}
		args := []string{"scan", target.SkillDir, "--no-llm", "--format", "json", "--output", jsonPath}
		if baselinePath != "" {
			args = append(args, "--baseline", baselinePath, "--show-suppressed")
		}
		if err := runSkillSpector(runtimeInfo.SkillSpectorBin, args...); err != nil {
			return nil, nil, err
		}
		raw, err := os.ReadFile(jsonPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read SkillSpector report %s: %w", jsonPath, err)
		}
		var report skillSpectorReport
		if err := json.Unmarshal(raw, &report); err != nil {
			return nil, nil, fmt.Errorf("parse SkillSpector report %s: %w", jsonPath, err)
		}
		if format == "sarif" {
			sarifPath := filepath.Join(runDir, target.Name+".sarif")
			sarifArgs := []string{"scan", target.SkillDir, "--no-llm", "--format", "sarif", "--output", sarifPath}
			if baselinePath != "" {
				sarifArgs = append(sarifArgs, "--baseline", baselinePath, "--show-suppressed")
			}
			if err := runSkillSpector(runtimeInfo.SkillSpectorBin, sarifArgs...); err != nil {
				return nil, nil, err
			}
			sarifPaths = append(sarifPaths, sarifPath)
		}
		results = append(results, skillScanResult{
			Target:       target,
			BaselinePath: baselinePath,
			ReportPath:   jsonPath,
			Report:       report,
			RawReport:    append(json.RawMessage(nil), raw...),
		})
	}
	return results, sarifPaths, nil
}

func runSkillSpector(binary string, args ...string) error {
	cmd := exec.Command(binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return fmt.Errorf("%s %s: %w", binary, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s: %w\n%s", binary, strings.Join(args, " "), err, detail)
	}
	return nil
}

func buildSkillScanOutput(command string, selection skillScanSelection, results []skillScanResult, runDir, baselineDir, baseRef string, changed bool, format string, reportOnly bool, metadata skillspector.RuntimeMetadata) skillScanCommandOutput {
	out := skillScanCommandOutput{
		Summary: skillScanSummary{
			GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
			Command:      command,
			SourceRoot:   selection.SourceRoot,
			SkillRoot:    selection.SkillRoot,
			BaselineDir:  baselineDir,
			Runtime:      metadata,
			OutputFormat: format,
			GateMode:     "gate",
			ReportOnly:   reportOnly,
			RunDirectory: runDir,
		},
		Results: make([]skillScanResultPayload, 0, len(results)),
	}
	if reportOnly {
		out.Summary.GateMode = "report-only"
	}
	if changed {
		out.Summary.BaseRef = baseRef
	}
	for _, result := range results {
		item := skillScanResultPayload{
			Skill:          result.Target.Name,
			SkillPath:      result.Target.RelativePath,
			ReportPath:     result.ReportPath,
			BaselinePath:   result.BaselinePath,
			IssueCount:     len(result.Report.Issues),
			Suppressed:     result.Report.SuppressedCount,
			Score:          result.Report.RiskAssessment.Score,
			Recommendation: result.Report.RiskAssessment.Recommendation,
			Report:         result.RawReport,
		}
		out.Results = append(out.Results, item)
		out.Summary.SelectedSkills = append(out.Summary.SelectedSkills, skillScanTargetSummary{
			Name: result.Target.Name,
			Path: result.Target.RelativePath,
		})
		out.Summary.SkillsScanned++
		out.Summary.IssueCount += item.IssueCount
		out.Summary.SuppressedCount += item.Suppressed
		if item.Score > out.Summary.MaxScore {
			out.Summary.MaxScore = item.Score
		}
		if item.IssueCount > 0 {
			ids := make([]string, 0, len(result.Report.Issues))
			for _, issue := range result.Report.Issues {
				ids = append(ids, issue.ID)
			}
			out.Summary.FailingSkills = append(out.Summary.FailingSkills, skillScanFailingSkillSummary{
				Name:           result.Target.Name,
				Path:           result.Target.RelativePath,
				Score:          item.Score,
				Recommendation: item.Recommendation,
				Issues:         ids,
			})
		}
	}
	sort.Slice(out.Results, func(i, j int) bool { return out.Results[i].Skill < out.Results[j].Skill })
	sort.Slice(out.Summary.SelectedSkills, func(i, j int) bool { return out.Summary.SelectedSkills[i].Name < out.Summary.SelectedSkills[j].Name })
	sort.Slice(out.Summary.FailingSkills, func(i, j int) bool { return out.Summary.FailingSkills[i].Name < out.Summary.FailingSkills[j].Name })
	out.Summary.Pass = out.Summary.IssueCount == 0
	out.Summary.GatePass = out.Summary.Pass
	return out
}

func createSkillSpectorRunDir(dotpackHome, command string) (string, error) {
	root := filepath.Join(dotpackHome, "skillspector", "runs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create SkillSpector run directory %s: %w", root, err)
	}
	dir := filepath.Join(root, timestampSlug(time.Now())+"-"+command)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create SkillSpector run directory %s: %w", dir, err)
	}
	return dir, nil
}

func timestampSlug(now time.Time) string {
	return now.UTC().Format("20060102T150405Z")
}

func resolveAggregateOutputPath(requested, runDir, command, format string) (string, error) {
	if requested != "" {
		return resolveRequiredAbsPath(requested)
	}
	ext := "txt"
	switch format {
	case "json", "terminal":
		ext = "json"
	case "markdown":
		ext = "md"
	case "sarif":
		ext = "sarif"
	}
	return filepath.Join(runDir, command+"-aggregate."+ext), nil
}

func resolveOptionalAbsPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	return resolveRequiredAbsPath(path)
}

func resolveRequiredAbsPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func writeSkillScanOutput(format, outputPath string, aggregate skillScanCommandOutput, sarifPaths []string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create output parent %s: %w", filepath.Dir(outputPath), err)
	}
	switch format {
	case "terminal", "json":
		raw, err := json.MarshalIndent(aggregate, "", "  ")
		if err != nil {
			return fmt.Errorf("encode SkillSpector aggregate output: %w", err)
		}
		if err := os.WriteFile(outputPath, append(raw, '\n'), 0o644); err != nil {
			return fmt.Errorf("write SkillSpector aggregate output %s: %w", outputPath, err)
		}
	case "markdown":
		if err := os.WriteFile(outputPath, []byte(renderSkillScanMarkdown(aggregate)), 0o644); err != nil {
			return fmt.Errorf("write SkillSpector markdown output %s: %w", outputPath, err)
		}
	case "sarif":
		raw, err := json.MarshalIndent(mergeSARIFReports(sarifPaths), "", "  ")
		if err != nil {
			return fmt.Errorf("encode SkillSpector SARIF output: %w", err)
		}
		if err := os.WriteFile(outputPath, append(raw, '\n'), 0o644); err != nil {
			return fmt.Errorf("write SkillSpector SARIF output %s: %w", outputPath, err)
		}
	default:
		return fmt.Errorf("unsupported SkillSpector output format %q", format)
	}
	return nil
}

func renderSkillScanMarkdown(aggregate skillScanCommandOutput) string {
	var b strings.Builder
	b.WriteString("# SkillSpector Scan\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n", aggregate.Summary.GeneratedAt)
	fmt.Fprintf(&b, "- Skills scanned: %d\n", aggregate.Summary.SkillsScanned)
	fmt.Fprintf(&b, "- Unsuppressed issues: %d\n", aggregate.Summary.IssueCount)
	fmt.Fprintf(&b, "- Suppressed findings: %d\n", aggregate.Summary.SuppressedCount)
	fmt.Fprintf(&b, "- Gate mode: %s\n", aggregate.Summary.GateMode)
	fmt.Fprintf(&b, "- Gate result: %t\n\n", aggregate.Summary.GatePass)
	b.WriteString("## Skills\n\n")
	for _, result := range aggregate.Results {
		fmt.Fprintf(&b, "- `%s`: score %.0f, recommendation %s, issues %d, suppressed %d\n", result.Skill, result.Score, result.Recommendation, result.IssueCount, result.Suppressed)
	}
	return b.String()
}

func mergeSARIFReports(paths []string) map[string]any {
	runs := make([]any, 0)
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var payload struct {
			Runs []any `json:"runs"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}
		runs = append(runs, payload.Runs...)
	}
	return map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs":    runs,
	}
}

func pathIsRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func printSkillScanTerminalSummary(cmd *cobra.Command, aggregate skillScanCommandOutput, outputPath string) {
	status := "PASS"
	if !aggregate.Summary.Pass {
		status = "FAIL"
	}
	cmd.Printf("SkillSpector scan: %s\n", status)
	cmd.Printf("Source root: %s\n", aggregate.Summary.SourceRoot)
	cmd.Printf("Skills scanned: %d\n", aggregate.Summary.SkillsScanned)
	cmd.Printf("Unsuppressed issues: %d\n", aggregate.Summary.IssueCount)
	cmd.Printf("Suppressed findings: %d\n", aggregate.Summary.SuppressedCount)
	cmd.Printf("Gate mode: %s\n", aggregate.Summary.GateMode)
	cmd.Printf("Aggregate output: %s\n", outputPath)
	for _, result := range aggregate.Results {
		cmd.Printf("- %s: score %.0f, recommendation %s, issues %d, suppressed %d\n", result.Skill, result.Score, result.Recommendation, result.IssueCount, result.Suppressed)
	}
}
