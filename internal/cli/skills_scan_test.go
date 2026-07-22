package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillspector"
)

func TestResolveSkillScanSelectionSupportsSingleCanonicalAndCustomRoots(t *testing.T) {
	projectRoot, agentsRoot := writeCanonicalSkill(t, "alpha", "alpha body\n")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "skills", "beta", "SKILL.md"), "---\nname: beta\ndescription: beta\n---\nbeta body\n")

	single, err := resolveSkillScanSelection(filepath.Join(agentsRoot, "skills", "alpha", "SKILL.md"), sourceLayoutOptions{}, nil, false, "HEAD", dirs.Dirs{})
	if err != nil {
		t.Fatalf("resolve single skill: %v", err)
	}
	if len(single.Targets) != 1 || single.Targets[0].Name != "alpha" {
		t.Fatalf("single skill selection = %+v", single)
	}

	canonical, err := resolveSkillScanSelection(projectRoot, sourceLayoutOptions{}, nil, false, "HEAD", dirs.Dirs{})
	if err != nil {
		t.Fatalf("resolve canonical root: %v", err)
	}
	if len(canonical.Targets) != 2 || canonical.Targets[0].Name != "alpha" || canonical.Targets[1].Name != "beta" {
		t.Fatalf("canonical selection = %+v", canonical)
	}

	customRoot := t.TempDir()
	mustWriteTestFile(t, filepath.Join(customRoot, "skills", "custom-skill", "SKILL.md"), "---\nname: custom-skill\ndescription: custom\n---\nbody\n")
	custom, err := resolveSkillScanSelection(customRoot, sourceLayoutOptions{skillsPath: "skills"}, nil, false, "HEAD", dirs.Dirs{})
	if err != nil {
		t.Fatalf("resolve custom root: %v", err)
	}
	if len(custom.Targets) != 1 || custom.Targets[0].Name != "custom-skill" {
		t.Fatalf("custom selection = %+v", custom)
	}
}

func TestResolveSkillScanSelectionRejectsNonSkillOverrides(t *testing.T) {
	_, err := resolveSkillScanSelection(".", sourceLayoutOptions{kindPaths: []string{"agent=agents"}}, nil, false, "HEAD", dirs.Dirs{})
	if err == nil || !strings.Contains(err.Error(), "only accepts skill discovery overrides") {
		t.Fatalf("non-skill override err=%v; want skill discovery override error", err)
	}
}

func TestResolveSkillScanSelectionChangedOnlyUsesGitDiff(t *testing.T) {
	projectRoot, agentsRoot := writeCanonicalSkill(t, "alpha", "alpha body\n")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "skills", "beta", "SKILL.md"), "---\nname: beta\ndescription: beta\n---\nbeta body\n")

	old := runGitCommand
	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "rev-parse --show-toplevel":
			return []byte(projectRoot + "\n"), nil
		case "diff --name-only --diff-filter=ACMRTUXB HEAD -- .agents/skills":
			return []byte(".agents/skills/beta/SKILL.md\n"), nil
		case "ls-files --others --exclude-standard -- .agents/skills":
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected git args: %v", args)
		}
	}
	t.Cleanup(func() { runGitCommand = old })

	selection, err := resolveSkillScanSelection(projectRoot, sourceLayoutOptions{}, nil, true, "HEAD", dirs.Dirs{})
	if err != nil {
		t.Fatalf("resolve changed skills: %v", err)
	}
	if len(selection.Targets) != 1 || selection.Targets[0].Name != "beta" {
		t.Fatalf("changed selection = %+v", selection)
	}
}

func TestScanSkillsCommandFailsOnMissingBaseline(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "good-skill", "body\n")
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	prepareFakeSkillSpectorRuntime(t, dotpackHome)

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"scan-skills", agentsRoot, "--baseline-dir", filepath.Join(t.TempDir(), "baselines")})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing baseline file for good-skill") {
		t.Fatalf("scan-skills missing baseline err=%v output=%s", err, stdout.String())
	}
}

func TestScanSkillsCommandGatesOnFindingsAndHonorsReportOnly(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "bad-skill", "body\n")
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	prepareFakeSkillSpectorRuntime(t, dotpackHome)

	var gated bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&gated)
	cmd.SetErr(&gated)
	cmd.SetArgs([]string{"scan-skills", agentsRoot})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "gate failed") {
		t.Fatalf("scan-skills gating err=%v output=%s", err, gated.String())
	}
	if !strings.Contains(gated.String(), "SkillSpector scan: FAIL") {
		t.Fatalf("scan-skills output missing FAIL summary:\n%s", gated.String())
	}

	var reportOnly bytes.Buffer
	cmd = NewRootCmd()
	cmd.SetOut(&reportOnly)
	cmd.SetErr(&reportOnly)
	cmd.SetArgs([]string{"scan-skills", agentsRoot, "--report-only"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan-skills --report-only: %v\n%s", err, reportOnly.String())
	}
	if !strings.Contains(reportOnly.String(), "Gate mode: report-only") {
		t.Fatalf("scan-skills report-only output missing mode:\n%s", reportOnly.String())
	}
}

func TestScanSkillsReportOnlyAcceptsScannerFindingExitCode(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "exit-one-skill", "body\n")
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	prepareFakeSkillSpectorRuntime(t, dotpackHome)

	var reportOnly bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&reportOnly)
	cmd.SetErr(&reportOnly)
	cmd.SetArgs([]string{"scan-skills", agentsRoot, "--report-only"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan-skills --report-only should parse a report written before exit 1: %v\n%s", err, reportOnly.String())
	}
	if !strings.Contains(reportOnly.String(), "Gate mode: report-only") {
		t.Fatalf("scan-skills report-only output missing mode:\n%s", reportOnly.String())
	}

	var gated bytes.Buffer
	cmd = NewRootCmd()
	cmd.SetOut(&gated)
	cmd.SetErr(&gated)
	cmd.SetArgs([]string{"scan-skills", agentsRoot})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "gate failed") {
		t.Fatalf("scan-skills should enforce parsed findings after exit 1, err=%v output=%s", err, gated.String())
	}
}

func TestScanSkillsCommandWritesJSONAndSARIFOutputs(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "good-skill", "body\n")
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	prepareFakeSkillSpectorRuntime(t, dotpackHome)

	jsonOutput := filepath.Join(t.TempDir(), "skills.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"scan-skills", agentsRoot, "--format", "json", "--output", jsonOutput})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan-skills json: %v", err)
	}
	jsonRaw, err := os.ReadFile(jsonOutput)
	if err != nil {
		t.Fatalf("read json output: %v", err)
	}
	for _, want := range []string{`"skills_scanned": 1`, `"skill": "good-skill"`} {
		if !strings.Contains(string(jsonRaw), want) {
			t.Fatalf("json output missing %q:\n%s", want, string(jsonRaw))
		}
	}

	sarifOutput := filepath.Join(t.TempDir(), "skills.sarif")
	cmd = NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"scan-skills", agentsRoot, "--format", "sarif", "--output", sarifOutput})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan-skills sarif: %v", err)
	}
	sarifRaw, err := os.ReadFile(sarifOutput)
	if err != nil {
		t.Fatalf("read sarif output: %v", err)
	}
	for _, want := range []string{`"version": "2.1.0"`, `"ruleId": "good-skill"`} {
		if !strings.Contains(string(sarifRaw), want) {
			t.Fatalf("sarif output missing %q:\n%s", want, string(sarifRaw))
		}
	}
}

func TestBaselineSkillsCommandWritesPerSkillBaselines(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "good-skill", "body\n")
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	prepareFakeSkillSpectorRuntime(t, dotpackHome)

	baselineDir := filepath.Join(t.TempDir(), "baselines")
	summaryPath := filepath.Join(t.TempDir(), "baseline-summary.json")
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"baseline-skills", agentsRoot, "--baseline-dir", baselineDir, "--output", summaryPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("baseline-skills: %v\n%s", err, stdout.String())
	}
	baselineRaw, err := os.ReadFile(filepath.Join(baselineDir, "good-skill.yaml"))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if !strings.Contains(string(baselineRaw), "Accepted SkillSpector finding after review.") {
		t.Fatalf("baseline missing default reason:\n%s", string(baselineRaw))
	}
	summaryRaw, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read baseline summary: %v", err)
	}
	if !strings.Contains(string(summaryRaw), `"command": "baseline-skills"`) {
		t.Fatalf("baseline summary missing command:\n%s", string(summaryRaw))
	}
}

func prepareFakeSkillSpectorRuntime(t *testing.T, dotpackHome string) {
	t.Helper()
	rootDir := filepath.Join(dotpackHome, "skillspector")
	venvDir := filepath.Join(rootDir, "runtime")
	binDir := filepath.Join(venvDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake runtime: %v", err)
	}
	pythonPath := filepath.Join(binDir, "python")
	if err := os.WriteFile(pythonPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	scriptPath := filepath.Join(binDir, "skillspector")
	script := `#!/bin/sh
set -eu
if [ "$#" -eq 0 ]; then
  exit 1
fi
if [ "$1" = "--version" ]; then
  echo "skillspector 2.3.5"
  exit 0
fi
cmd="$1"
shift
case "$cmd" in
  baseline)
    skill_dir="$1"
    shift
    out=""
    reason=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --output)
          out="$2"
          shift 2
          ;;
        --reason)
          reason="$2"
          shift 2
          ;;
        *)
          shift
          ;;
      esac
    done
    cat >"$out" <<EOF
accepted_findings:
  - id: SK001
    reason: ${reason}
EOF
    ;;
  scan)
    skill_dir="$1"
    shift
    out=""
    format="json"
    baseline=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --format)
          format="$2"
          shift 2
          ;;
        --output)
          out="$2"
          shift 2
          ;;
        --baseline)
          baseline="$2"
          shift 2
          ;;
        --show-suppressed|--no-llm)
          shift
          ;;
        *)
          shift
          ;;
      esac
    done
    name="$(basename "$skill_dir")"
    if [ "$format" = "json" ]; then
      if { [ "$name" = "bad-skill" ] || [ "$name" = "exit-one-skill" ]; } && [ -z "$baseline" ]; then
        cat >"$out" <<EOF
{"risk_assessment":{"score":7,"recommendation":"REVIEW"},"issues":[{"id":"SK001"}],"suppressed_count":0}
EOF
      else
        cat >"$out" <<EOF
{"risk_assessment":{"score":0,"recommendation":"SAFE"},"issues":[],"suppressed_count":1}
EOF
      fi
    else
      cat >"$out" <<EOF
{"runs":[{"tool":{"driver":{"name":"SkillSpector"}},"results":[{"ruleId":"$name"}]}]}
EOF
    fi
    if [ "$name" = "exit-one-skill" ] && [ -z "$baseline" ]; then
      exit 1
    fi
    ;;
  *)
    exit 1
    ;;
esac
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake skillspector: %v", err)
	}
	metadata := skillspector.RuntimeMetadata{
		Repo:           skillspector.RepoURL,
		Commit:         skillspector.Commit,
		Version:        skillspector.Version,
		InstalledAt:    "2026-07-04T00:00:00Z",
		SelectedPython: "python3.12",
		VersionOutput:  "skillspector 2.3.5",
	}
	raw := fmt.Sprintf("{\n  \"repo\": %q,\n  \"commit\": %q,\n  \"version\": %q,\n  \"installed_at\": %q,\n  \"selected_python\": %q,\n  \"version_output\": %q\n}\n",
		metadata.Repo,
		metadata.Commit,
		metadata.Version,
		metadata.InstalledAt,
		metadata.SelectedPython,
		metadata.VersionOutput,
	)
	if err := os.WriteFile(filepath.Join(rootDir, "runtime.json"), []byte(raw), 0o644); err != nil {
		t.Fatalf("write runtime metadata: %v", err)
	}
}
