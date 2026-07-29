package filedrop

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
)

func coveragePolicy(tmp string) Policy {
	layout := func(kindDir string, nested bool, nestedFile string, flatExt string, preserveName bool) Layout {
		return Layout{
			UserRoot:      func(d dirs.Dirs) (string, error) { return tmp, nil },
			ProjectSubdir: ".host",
			KindDir:       kindDir,
			Nested:        nested,
			NestedFile:    nestedFile,
			FlatExt:       flatExt,
			PreserveName:  preserveName,
		}
	}
	return Policy{
		HostID: "claude-code",
		Layouts: map[resource.Kind]Layout{
			resource.KindSkill:   layout("skills", true, "SKILL.md", "", false),
			resource.KindAgent:   layout("agents", false, "", "", false),
			resource.KindRule:    layout("rules", false, "", "", false),
			resource.KindCommand: layout("commands", false, "", "", false),
			resource.KindMemory:  layout("", false, "", "", true),
		},
		AgentToolsShape: ToolsCommaString,
	}
}

func TestPlanSkillSupportFilesCopiesNestedSiblings(t *testing.T) {
	tmp := t.TempDir()
	a := New(dirs.Dirs{}, coveragePolicy(tmp))
	skill := &resource.Skill{
		Name:        "with-support",
		Description: "d",
		Body:        "body\n",
		SupportFiles: []resource.SupportFile{
			{RelPath: "references/guide.md", Content: []byte("guide"), Mode: 0o600},
			{RelPath: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Mode: 0o755},
			{RelPath: "assets/logo.txt", Content: []byte("logo")},
		},
	}

	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Files) != 4 {
		t.Fatalf("len(Files)=%d; want SKILL.md plus three support files", len(plan.Files))
	}
	if got, want := plan.Files[0].Path, filepath.Join(tmp, "skills", "with-support", "SKILL.md"); got != want {
		t.Fatalf("SKILL.md path = %q; want %q", got, want)
	}
	wantPaths := []string{
		filepath.Join(tmp, "skills", "with-support", "assets", "logo.txt"),
		filepath.Join(tmp, "skills", "with-support", "references", "guide.md"),
		filepath.Join(tmp, "skills", "with-support", "scripts", "run.sh"),
	}
	for i, want := range wantPaths {
		fw := plan.Files[i+1]
		if fw.Path != want {
			t.Fatalf("support Files[%d].Path = %q; want %q", i+1, fw.Path, want)
		}
	}
	if got := plan.Files[1].Mode; got != fs.FileMode(0o644) {
		t.Fatalf("default support-file mode = %v; want 0644", got)
	}
	if got := plan.Files[2].Mode; got != fs.FileMode(0o600) {
		t.Fatalf("explicit support-file mode = %v; want 0600", got)
	}
	if got := plan.Files[3].Mode; got != fs.FileMode(0o755) {
		t.Fatalf("executable support-file mode = %v; want 0755", got)
	}
}

func TestCleanSupportRelPathRejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		rel     string
		wantErr string
	}{
		{"", "empty"},
		{filepath.Join(string(filepath.Separator), "tmp", "x"), "absolute"},
		{"..", "escapes"},
		{"../x", "escapes"},
		{"safe/../guide.md", ""},
	}
	for _, tc := range cases {
		t.Run(tc.rel, func(t *testing.T) {
			got, err := cleanSupportRelPath(tc.rel)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("cleanSupportRelPath: %v", err)
				}
				if got != "guide.md" && tc.rel == "safe/../guide.md" {
					t.Fatalf("cleaned path = %q; want guide.md", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v; want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestPlanSkillSupportFilesRejectsDuplicateTargets(t *testing.T) {
	a := New(dirs.Dirs{}, coveragePolicy(t.TempDir()))
	for _, support := range [][]resource.SupportFile{
		{{RelPath: "SKILL.md", Content: []byte("shadow")}},
		{
			{RelPath: "references/one.md", Content: []byte("one")},
			{RelPath: "references/./one.md", Content: []byte("two")},
		},
	} {
		skill := &resource.Skill{Name: "dup", Description: "d", Body: "b", SupportFiles: support}
		_, err := a.Plan(skill, adapter.ScopeUser)
		if err == nil || !strings.Contains(err.Error(), "duplicate skill support file") {
			t.Fatalf("Plan error = %v; want duplicate support-file error", err)
		}
	}
}

func TestTargetPathErrorsForInvalidScopeAndMissingRoots(t *testing.T) {
	a := New(dirs.Dirs{}, coveragePolicy(t.TempDir()))
	_, err := a.Plan(&resource.Skill{Name: "x", Description: "d", Body: "b"}, adapter.ScopeProject)
	if err == nil || !strings.Contains(err.Error(), "ProjectHome") {
		t.Fatalf("project scope without ProjectHome error = %v; want ProjectHome", err)
	}

	_, err = a.Plan(&resource.Skill{Name: "x", Description: "d", Body: "b"}, adapter.Scope("bogus"))
	if err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Fatalf("bogus scope error = %v; want unknown scope", err)
	}

	rootErr := errors.New("missing home")
	bad := New(dirs.Dirs{}, Policy{
		HostID: "bad-home",
		Layouts: map[resource.Kind]Layout{resource.KindSkill: {
			UserRoot:   func(d dirs.Dirs) (string, error) { return "", rootErr },
			KindDir:    "skills",
			Nested:     true,
			NestedFile: "SKILL.md",
		}},
	})
	_, err = bad.Plan(&resource.Skill{Name: "x", Description: "d", Body: "b"}, adapter.ScopeUser)
	if !errors.Is(err, rootErr) {
		t.Fatalf("Plan error = %v; want wrapped root error", err)
	}
}

func TestSkillSourcePassThroughAndSynthesizedReencode(t *testing.T) {
	tmp := t.TempDir()
	a := New(dirs.Dirs{}, coveragePolicy(tmp))
	raw := []byte("---\nname: raw\n\ndescription: d\nkeywords:\n  - one\n---\nbody\n")
	skill, err := resource.ParseSkill(raw)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan pass-through: %v", err)
	}
	if string(plan.Files[0].Content) != string(raw) {
		t.Fatalf("pass-through content changed:\n%s", plan.Files[0].Content)
	}

	other := New(dirs.Dirs{}, func() Policy {
		p := coveragePolicy(tmp)
		p.HostID = "gemini-cli"
		return p
	}())
	claudeOnly := []byte("---\nname: raw\ndescription: d\nallowed-tools: Read\n---\nbody\n")
	parsed, err := resource.ParseSkill(claudeOnly)
	if err != nil {
		t.Fatalf("ParseSkill claude-only: %v", err)
	}
	plan, err = other.Plan(parsed, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan source pass-through: %v", err)
	}
	if string(plan.Files[0].Content) != string(claudeOnly) {
		t.Fatalf("source-backed gemini skill changed:\n%s", plan.Files[0].Content)
	}

	synthesized := (&resource.Skill{Name: "synthetic", Description: "d", Body: "body\n"}).
		WithExtensions(map[string]any{"allowed-tools": "Read"})
	plan, err = other.Plan(synthesized, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan synthesized reencode: %v", err)
	}
	if strings.Contains(string(plan.Files[0].Content), "allowed-tools") {
		t.Fatalf("synthesized gemini skill should drop claude-only field:\n%s", plan.Files[0].Content)
	}
}

func TestRuleCommandAndMemoryEncodingBranches(t *testing.T) {
	tmp := t.TempDir()
	a := New(dirs.Dirs{}, coveragePolicy(tmp))

	ruleRaw := []byte("---\nid: rule-one\nowner: docs\n---\nbody\n")
	rule, err := resource.ParseRule(ruleRaw)
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	rulePlan, err := a.Plan(rule, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan raw rule: %v", err)
	}
	if string(rulePlan.Files[0].Content) != string(ruleRaw) {
		t.Fatalf("rule raw pass-through changed:\n%s", rulePlan.Files[0].Content)
	}

	reencoded := (&resource.Rule{ID: "rule-two", Body: "rule body\n"}).WithExtensions(map[string]any{
		"artifact-type": "rule",
	})
	rulePlan, err = a.Plan(reencoded, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan reencoded rule: %v", err)
	}
	if got := string(rulePlan.Files[0].Content); !strings.Contains(got, "artifact-type: rule") || !strings.Contains(got, "rule body") {
		t.Fatalf("reencoded rule missing metadata/body:\n%s", got)
	}

	cmdRaw := []byte("---\ndescription: d\nallowed-tools: Read, Write\nargument-hint: FILE\n---\nrun\n")
	cmd, err := resource.ParseCommand(cmdRaw)
	if err != nil {
		t.Fatalf("ParseCommand: %v", err)
	}
	cmd.WithName("cmd")
	cmdPlan, err := a.Plan(cmd, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan raw command: %v", err)
	}
	if string(cmdPlan.Files[0].Content) != string(cmdRaw) {
		t.Fatalf("command raw pass-through changed:\n%s", cmdPlan.Files[0].Content)
	}

	memPlan, err := a.Plan(&resource.Memory{Body: "remember", Raw: []byte("raw memory"), Name: "CUSTOM.md"}, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan memory: %v", err)
	}
	if got, want := filepath.Base(memPlan.Files[0].Path), "CLAUDE.md"; got != want {
		t.Fatalf("memory filename = %q; want %q", got, want)
	}
	if string(memPlan.Files[0].Content) != "raw memory" {
		t.Fatalf("memory raw content changed: %q", memPlan.Files[0].Content)
	}
}

func TestGeminiCommandTOMLEncodeAndDefaultMemoryFilename(t *testing.T) {
	tmp := t.TempDir()
	p := coveragePolicy(tmp)
	p.HostID = "gemini-cli"
	p.Layouts[resource.KindCommand] = Layout{
		UserRoot: func(d dirs.Dirs) (string, error) { return tmp, nil },
		KindDir:  "commands",
		FlatExt:  ".toml",
	}
	a := New(dirs.Dirs{}, p)
	cmd := (&resource.Command{
		Name:         "deploy",
		Description:  "d",
		Model:        "model",
		AllowedTools: []string{"Read"},
		Prompt:       "ship it",
	}).WithName("deploy")
	plan, err := a.Plan(cmd, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan command: %v", err)
	}
	got := string(plan.Files[0].Content)
	for _, want := range []string{`prompt = 'ship it'`, `description = 'd'`, `allowed-tools = ['`} {
		if !strings.Contains(got, want) {
			t.Fatalf("gemini TOML command missing %q:\n%s", want, got)
		}
	}

	custom := New(dirs.Dirs{}, func() Policy {
		p := coveragePolicy(tmp)
		p.HostID = "custom-host"
		return p
	}())
	memPlan, err := custom.Plan(&resource.Memory{Body: "body", Name: "TEAM.md"}, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan custom memory: %v", err)
	}
	if got, want := filepath.Base(memPlan.Files[0].Path), "TEAM.md"; got != want {
		t.Fatalf("custom memory filename = %q; want %q", got, want)
	}
}

func TestStaleRuleFilesForProjectCanonicalRules(t *testing.T) {
	project := t.TempDir()
	a := New(dirs.Dirs{ProjectHome: project}, coveragePolicy(t.TempDir()))
	source := filepath.Join(project, ".agents", "rules", "shared-rule.md")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(source, []byte("---\nname: shared-rule\n---\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rule := (&resource.Rule{Name: "shared-rule", Body: "body"}).WithSourcePath(source)
	plan, err := a.Plan(rule, adapter.ScopeProject)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.RemoveFiles) == 0 {
		t.Fatal("expected stale host-specific rule files for canonical project rule")
	}
	for _, rm := range plan.RemoveFiles {
		if rm.Path == source || rm.Path == plan.Files[0].Path {
			t.Fatalf("stale cleanup must not remove source or target: %+v", plan.RemoveFiles)
		}
	}
}

func TestEncodeUnsupportedResourceErrors(t *testing.T) {
	a := New(dirs.Dirs{}, coveragePolicy(t.TempDir()))
	_, err := a.encode(fakeResource{kind: resource.Kind("unknown")})
	if err == nil || !strings.Contains(err.Error(), "no encoder") {
		t.Fatalf("encode error = %v; want no encoder", err)
	}
}

type fakeResource struct{ kind resource.Kind }

func (f fakeResource) Kind() resource.Kind        { return f.kind }
func (f fakeResource) Extensions() map[string]any { return nil }
