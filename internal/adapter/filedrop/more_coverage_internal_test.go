package filedrop

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"gopkg.in/yaml.v3"
)

func TestFiledropEncodeAgentAndScalarErrorBranches(t *testing.T) {
	a := &Adapter{policy: Policy{HostID: "test-host"}}
	_, err := a.encodeAgent(&resource.Agent{Name: "a", Description: "d", Body: "b", Tools: []string{"Read"}})
	if err == nil || !strings.Contains(err.Error(), "AgentToolsShape") {
		t.Fatalf("encodeAgent shape error = %v; want AgentToolsShape", err)
	}

	a = New(dirs.Dirs{}, Policy{
		HostID: "claude-code",
		Layouts: map[resource.Kind]Layout{resource.KindAgent: {
			UserRoot: func(d dirs.Dirs) (string, error) { return t.TempDir(), nil },
			KindDir:  "agents",
		}},
		AgentToolsShape: ToolsCommaString,
	})
	plan, err := a.Plan((&resource.Agent{Name: "a", Description: "d", Body: "b"}).WithExtensions(map[string]any{"temperature": 0.1}), adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan agent: %v", err)
	}
	if strings.Contains(string(plan.Files[0].Content), "temperature") {
		t.Fatalf("claude-code agent should drop gemini-only temperature:\n%s", plan.Files[0].Content)
	}

}

func TestFiledropPlanAndTargetPathAdditionalBranches(t *testing.T) {
	tmp := t.TempDir()
	a := New(dirs.Dirs{}, coveragePolicy(tmp))

	_, err := a.Plan(fakeResource{kind: resource.KindSkill}, adapter.ScopeUser)
	if err == nil || !strings.Contains(err.Error(), "no name-derivation path") {
		t.Fatalf("Plan unnamed resource error = %v; want name-derivation path", err)
	}

	_, err = a.Plan(&resource.Skill{
		Name:        "bad-support",
		Description: "d",
		Body:        "body",
		SupportFiles: []resource.SupportFile{
			{RelPath: "../escape.md", Content: []byte("x")},
		},
	}, adapter.ScopeUser)
	if err == nil || !strings.Contains(err.Error(), "skill support file") {
		t.Fatalf("Plan unsafe support file error = %v; want support-file context", err)
	}

	badEncode := &Adapter{dirs: dirs.Dirs{}, policy: Policy{
		HostID: "shape-error",
		Layouts: map[resource.Kind]Layout{resource.KindAgent: {
			UserRoot: func(d dirs.Dirs) (string, error) { return tmp, nil },
			KindDir:  "agents",
		}},
	}}
	_, err = badEncode.Plan(&resource.Agent{Name: "a", Description: "d", Tools: []string{"Read"}, Body: "body"}, adapter.ScopeUser)
	if err == nil || !strings.Contains(err.Error(), "AgentToolsShape") {
		t.Fatalf("Plan encode error = %v; want AgentToolsShape", err)
	}

	target, err := a.targetPath(Layout{
		UserRoot:     func(d dirs.Dirs) (string, error) { return tmp, nil },
		KindDir:      "memory",
		PreserveName: true,
	}, adapter.ScopeUser, "TEAM.md")
	if err != nil {
		t.Fatalf("targetPath preserve name with kind dir: %v", err)
	}
	if got, want := target, filepath.Join(tmp, "memory", "TEAM.md"); got != want {
		t.Fatalf("target path = %q; want %q", got, want)
	}

	target, err = a.targetPath(Layout{
		UserRoot: func(d dirs.Dirs) (string, error) { return tmp, nil },
	}, adapter.ScopeUser, "loose")
	if err != nil {
		t.Fatalf("targetPath flat no kind dir: %v", err)
	}
	if got, want := target, filepath.Join(tmp, "loose.md"); got != want {
		t.Fatalf("target path = %q; want %q", got, want)
	}
}

func TestFiledropReencodeKeepsAndDropsHostExtensions(t *testing.T) {
	tmp := t.TempDir()
	a := New(dirs.Dirs{}, coveragePolicy(tmp))

	skill := (&resource.Skill{
		Name:        "licensed",
		Description: "d",
		License:     "MIT",
		Body:        "body\n",
	}).WithExtensions(map[string]any{
		"keywords":      []string{"one"},
		"allowed-tools": []string{"Read"},
	})
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan skill: %v", err)
	}
	got := string(plan.Files[0].Content)
	for _, want := range []string{"license: MIT", "keywords:", "allowed-tools:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reencoded skill missing %q:\n%s", want, got)
		}
	}

	agent := (&resource.Agent{
		Name:        "agent",
		Description: "d",
		Model:       "sonnet",
		Tools:       []string{"Read"},
		Body:        "body",
	}).WithExtensions(map[string]any{
		"disallowedTools": []string{"Bash"},
		"temperature":     0.3,
	})
	plan, err = a.Plan(agent, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan agent: %v", err)
	}
	got = string(plan.Files[0].Content)
	for _, want := range []string{"model: sonnet", "tools: Read", "disallowedTools:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reencoded agent missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "temperature") {
		t.Fatalf("claude-code agent should drop gemini-only extension:\n%s", got)
	}

	rule := (&resource.Rule{Name: "rule", Body: "body"}).WithExtensions(map[string]any{
		"owner":   "docs",
		"unknown": "drop",
	})
	plan, err = a.Plan(rule, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan rule: %v", err)
	}
	got = string(plan.Files[0].Content)
	if !strings.Contains(got, "owner: docs") || strings.Contains(got, "unknown") {
		t.Fatalf("rule should keep schema metadata and drop unknown extension:\n%s", got)
	}
}

func TestFiledropCommandReencodeShapeBranches(t *testing.T) {
	tmp := t.TempDir()

	comma := New(dirs.Dirs{}, coveragePolicy(tmp))
	plan, err := comma.Plan(&resource.Command{
		Name:         "cmd",
		Description:  "d",
		Model:        "sonnet",
		AllowedTools: []string{"Read", "Write"},
		Prompt:       "run",
	}, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan comma command: %v", err)
	}
	got := string(plan.Files[0].Content)
	if !strings.Contains(got, "allowed-tools: Read, Write") || !strings.Contains(got, "model: sonnet") {
		t.Fatalf("comma command output missing expected fields:\n%s", got)
	}

	arrayPolicy := coveragePolicy(tmp)
	arrayPolicy.AgentToolsShape = ToolsYAMLArray
	array := New(dirs.Dirs{}, arrayPolicy)
	plan, err = array.Plan(&resource.Command{
		Name:         "cmd-array",
		AllowedTools: []string{"Read", "Write"},
		Prompt:       "run",
	}, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan array command: %v", err)
	}
	got = string(plan.Files[0].Content)
	if !strings.Contains(got, "- Read") || !strings.Contains(got, "- Write") {
		t.Fatalf("array command output missing YAML tools:\n%s", got)
	}

	defaultPolicy := coveragePolicy(tmp)
	defaultPolicy.HostID = "custom-host"
	defaultPolicy.AgentToolsShape = ToolsShapeUnused
	delete(defaultPolicy.Layouts, resource.KindAgent)
	defaultShape := New(dirs.Dirs{}, defaultPolicy)
	plan, err = defaultShape.Plan(&resource.Command{
		Name:         "cmd-default",
		AllowedTools: []string{"Read", "Write"},
		Prompt:       "run",
	}, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan default command: %v", err)
	}
	got = string(plan.Files[0].Content)
	if !strings.Contains(got, "allowed-tools: Read, Write") {
		t.Fatalf("default command output missing comma tools:\n%s", got)
	}

	claudeTomlPolicy := coveragePolicy(tmp)
	claudeTomlPolicy.Layouts[resource.KindCommand] = Layout{
		UserRoot: func(d dirs.Dirs) (string, error) { return tmp, nil },
		KindDir:  "commands",
		FlatExt:  ".toml",
	}
	claudeToml := New(dirs.Dirs{}, claudeTomlPolicy)
	rawMarkdown, err := resource.ParseCommand([]byte("---\nname: convert\ndescription: d\nargument-hint: FILE\n---\nrun\n"))
	if err != nil {
		t.Fatalf("ParseCommand markdown: %v", err)
	}
	plan, err = claudeToml.Plan(rawMarkdown.WithName("convert"), adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan raw markdown to TOML: %v", err)
	}
	got = string(plan.Files[0].Content)
	if !strings.Contains(got, "argument-hint: FILE") {
		t.Fatalf("converted command should retain claude extension:\n%s", got)
	}

	geminiPolicy := coveragePolicy(tmp)
	geminiPolicy.HostID = "gemini-cli"
	geminiPolicy.AgentToolsShape = ToolsShapeUnused
	delete(geminiPolicy.Layouts, resource.KindAgent)
	geminiPolicy.Layouts[resource.KindCommand] = Layout{
		UserRoot: func(d dirs.Dirs) (string, error) { return tmp, nil },
		KindDir:  "commands",
		FlatExt:  ".toml",
	}
	gemini := New(dirs.Dirs{}, geminiPolicy)
	rawTOML, err := resource.ParseCommand([]byte("name = 'toml'\nprompt = 'run'\nextra = 'kept'\nallowed-tools = ['Read']\n"))
	if err != nil {
		t.Fatalf("ParseCommand TOML: %v", err)
	}
	plan, err = gemini.Plan(rawTOML.WithName("toml"), adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan TOML reencode: %v", err)
	}
	got = string(plan.Files[0].Content)
	if !strings.Contains(got, "extra = 'kept'") || !strings.Contains(got, "allowed-tools") {
		t.Fatalf("gemini TOML reencode should retain extension map:\n%s", got)
	}
}

func TestFiledropMemoryAndStaleRuleHelperBranches(t *testing.T) {
	project := t.TempDir()
	a := New(dirs.Dirs{ProjectHome: project}, coveragePolicy(t.TempDir()))

	if got := (&Adapter{policy: Policy{HostID: "antigravity-cli"}}).memoryFilename(&resource.Memory{}); got != "ANTIGRAVITY.md" {
		t.Fatalf("antigravity memory filename = %q", got)
	}
	if got := (&Adapter{policy: Policy{HostID: "codex"}}).memoryFilename(&resource.Memory{}); got != "AGENTS.md" {
		t.Fatalf("codex memory filename = %q", got)
	}
	if got := (&Adapter{policy: Policy{HostID: "unknown"}}).memoryFilename(&resource.Memory{}); got != "AGENTS.md" {
		t.Fatalf("unknown memory filename = %q", got)
	}

	if got := a.staleRuleFiles(&resource.Rule{Name: "r"}, "/tmp/target", adapter.ScopeUser); got != nil {
		t.Fatalf("user-scope stale rules = %+v; want nil", got)
	}
	if got := a.staleRuleFiles(&resource.Rule{Name: "r"}, "/tmp/target", adapter.ScopeProject); got != nil {
		t.Fatalf("missing source stale rules = %+v; want nil", got)
	}

	canonicalDir := filepath.Join(project, ".agents", "rules")
	rule := (&resource.Rule{Name: "r"}).WithSourcePath(filepath.Join(canonicalDir, "r.md"))
	target := filepath.Join(canonicalDir, "gemini-cli", "r.md")
	got := a.staleRuleFiles(rule, target, adapter.ScopeProject)
	for _, rm := range got {
		if rm.Path == target {
			t.Fatalf("stale rule cleanup included current target: %+v", got)
		}
	}
	if len(got) == 0 {
		t.Fatal("expected stale rule cleanup candidates")
	}
}

func TestFiledropGeminiCommandFallbackAndMemoryDefault(t *testing.T) {
	tmp := t.TempDir()
	a := New(dirs.Dirs{}, Policy{
		HostID: "gemini-cli",
		Layouts: map[resource.Kind]Layout{
			resource.KindCommand: {UserRoot: func(d dirs.Dirs) (string, error) { return tmp, nil }, KindDir: "commands", FlatExt: ".toml"},
			resource.KindMemory:  {UserRoot: func(d dirs.Dirs) (string, error) { return tmp, nil }, PreserveName: true},
		},
	})
	plan, err := a.Plan(&resource.Command{Name: "cmd", Prompt: "run", AllowedTools: []string{"Read"}}, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan gemini command: %v", err)
	}
	if !strings.Contains(string(plan.Files[0].Content), "allowed-tools") {
		t.Fatalf("gemini command should include allowed-tools array:\n%s", plan.Files[0].Content)
	}
	plan, err = a.Plan(&resource.Memory{Body: "body"}, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan memory: %v", err)
	}
	if !strings.HasSuffix(plan.Files[0].Path, "GEMINI.md") {
		t.Fatalf("gemini memory path = %s; want GEMINI.md", plan.Files[0].Path)
	}
}

func TestFiledropPassThroughAndEncodingErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	a := New(dirs.Dirs{}, coveragePolicy(tmp))

	if pass, err := a.canPassThrough(&resource.Skill{Name: "plain"}); err != nil || !pass {
		t.Fatalf("canPassThrough no extensions = %v,%v; want true,nil", pass, err)
	}
	if pass, err := a.canPassThrough(extFakeResource{kind: resource.Kind("unknown"), ext: map[string]any{"x": "y"}}); err == nil || pass {
		t.Fatalf("canPassThrough unknown schema = %v,%v; want false,error", pass, err)
	}

	if _, err := a.reencodeSkill((&resource.Skill{Name: "s", Description: "d", Body: "b"}).WithExtensions(map[string]any{"keywords": badYAML{}})); err == nil || !strings.Contains(err.Error(), "encode key") {
		t.Fatalf("reencodeSkill func extension err=%v; want encode key", err)
	}
	if _, err := a.encodeAgent((&resource.Agent{Name: "a", Description: "d", Body: "b"}).WithExtensions(map[string]any{"disallowedTools": badYAML{}})); err == nil || !strings.Contains(err.Error(), "encode key") {
		t.Fatalf("encodeAgent func extension err=%v; want encode key", err)
	}
	if _, err := a.reencodeRule((&resource.Rule{Name: "r", Body: "b"}).WithExtensions(map[string]any{"owner": badYAML{}})); err == nil || !strings.Contains(err.Error(), "encode key") {
		t.Fatalf("reencodeRule func extension err=%v; want encode key", err)
	}
	var front []*yaml.Node
	var encodeErr error
	mkAddScalar(&front, &encodeErr)("bad", badYAML{})
	if encodeErr == nil || len(front) != 0 {
		t.Fatalf("mkAddScalar encodeErr=%v front=%d; want error and no appended front matter", encodeErr, len(front))
	}

	projectAdapter := New(dirs.Dirs{ProjectHome: tmp}, coveragePolicy(t.TempDir()))
	outside := (&resource.Rule{Name: "outside"}).WithSourcePath(filepath.Join(tmp, "elsewhere", "outside.md"))
	if got := projectAdapter.staleRuleFiles(outside, filepath.Join(tmp, "target.md"), adapter.ScopeProject); got != nil {
		t.Fatalf("staleRuleFiles outside canonical = %+v; want nil", got)
	}
}

type extFakeResource struct {
	kind resource.Kind
	ext  map[string]any
}

func (f extFakeResource) Kind() resource.Kind        { return f.kind }
func (f extFakeResource) Extensions() map[string]any { return f.ext }

type badYAML struct{}

func (badYAML) MarshalYAML() (any, error) { return nil, errors.New("bad yaml") }
