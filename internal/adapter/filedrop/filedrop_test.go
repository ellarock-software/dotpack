package filedrop_test

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/adapter/filedrop"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
)

// The filedrop module is the deep impl that the three per-host adapter
// shells (claudecode, gemini, codex) delegate to. These tests pin the
// MODULE'S CONTRACT against synthetic policies — the per-host packages
// keep their integration tests, which exercise the same code paths
// end-to-end with real policies.

// skillOnlyPolicy is a fixture: a host that supports only the skill kind.
// Used to pin the kind-not-supported error path without dragging in
// real schema data. UserRoot is a no-op resolver — tests using this
// fixture don't exercise targetPath (they check error dispatch and HostID,
// both upstream of path computation).
var skillOnlyPolicy = filedrop.Policy{
	HostID: "test-host",
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot: func(d dirs.Dirs) (string, error) { return "", nil },
		},
	},
}

// Policy validation tests: a misconfigured Policy is a programmer bug
// (a future host author forgetting a required field). filedrop.New
// panics on detection so the bug surfaces at process start (when
// buildAdapter runs), not on a user's first install. Three invariants
// pinned: HostID required, UserRoot required per Layout, AgentToolsShape
// required when KindAgent in Layouts.

func TestFiledrop_New_EmptyHostID_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic; got nil")
		}
		if !strings.Contains(fmt.Sprint(r), "HostID") {
			t.Errorf("panic message should name the missing field; got: %v", r)
		}
	}()
	filedrop.New(dirs.Dirs{}, filedrop.Policy{
		HostID:  "",
		Layouts: map[resource.Kind]filedrop.Layout{},
	})
}

func TestFiledrop_New_NilUserRoot_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic; got nil")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "UserRoot") {
			t.Errorf("panic message should name the nil field; got: %v", r)
		}
		if !strings.Contains(msg, "test-host") {
			t.Errorf("panic message should name the host; got: %v", r)
		}
	}()
	filedrop.New(dirs.Dirs{}, filedrop.Policy{
		HostID: "test-host",
		Layouts: map[resource.Kind]filedrop.Layout{
			resource.KindSkill: {
				// UserRoot intentionally nil — the bug being pinned.
				KindDir: "skills",
			},
		},
	})
}

func TestFiledrop_New_AgentLayoutWithoutToolsShape_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic; got nil")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "AgentToolsShape") {
			t.Errorf("panic message should name AgentToolsShape; got: %v", r)
		}
		if !strings.Contains(msg, "test-host") {
			t.Errorf("panic message should name the host; got: %v", r)
		}
	}()
	filedrop.New(dirs.Dirs{}, filedrop.Policy{
		HostID: "test-host",
		Layouts: map[resource.Kind]filedrop.Layout{
			resource.KindAgent: {
				UserRoot: func(d dirs.Dirs) (string, error) { return "", nil },
				KindDir:  "agents",
				Nested:   false,
			},
		},
		// AgentToolsShape intentionally unset — the bug being pinned.
	})
}

func TestFiledrop_HostID_ReturnsPolicyHostID(t *testing.T) {
	// The Adapter interface owes HostID; filedrop reads it from Policy.
	a := filedrop.New(dirs.Dirs{}, filedrop.Policy{HostID: "test-host"})
	if got := a.HostID(); got != "test-host" {
		t.Errorf("HostID = %q; want %q", got, "test-host")
	}
}

func TestFiledrop_Plan_PathAndTargetDir_LayoutDriven(t *testing.T) {
	// The path-computation contract: Layout fields drive target shape.
	// Four cases pin the matrix of (scope × nested):
	//   user + nested:   <UserRoot>/<KindDir>/<name>/<NestedFile>  + TargetDir set
	//   user + flat:     <UserRoot>/<KindDir>/<name>.md            + TargetDir empty
	//   project + nested: <ProjectHome>/<ProjectSubdir>/<KindDir>/<name>/<NestedFile> + TargetDir set
	//   project + flat:  <ProjectHome>/<ProjectSubdir>/<KindDir>/<name>.md           + TargetDir empty
	//
	// The Nested=true → TargetDir set / Nested=false → TargetDir empty
	// rule is load-bearing: orchestrator.Uninstall reclaims TargetDir
	// with one os.Remove when empty; reclaiming a shared dir like
	// agents/ would delete sibling resources.
	userRoot := t.TempDir()
	projectHome := t.TempDir()

	nestedLayout := filedrop.Layout{
		UserRoot:      func(d dirs.Dirs) (string, error) { return userRoot, nil },
		ProjectSubdir: ".test",
		KindDir:       "skills",
		Nested:        true,
		NestedFile:    "SKILL.md",
	}
	flatLayout := filedrop.Layout{
		UserRoot:      func(d dirs.Dirs) (string, error) { return userRoot, nil },
		ProjectSubdir: ".test",
		KindDir:       "agents",
		Nested:        false,
	}
	p := filedrop.Policy{
		HostID: "test-host",
		Layouts: map[resource.Kind]filedrop.Layout{
			resource.KindSkill: nestedLayout,
			resource.KindAgent: flatLayout,
		},
		// KindAgent in Layouts requires AgentToolsShape — pin a value
		// so the New() validation accepts the policy. The path test
		// doesn't exercise tools encoding, so either shape works.
		AgentToolsShape: filedrop.ToolsCommaString,
	}
	a := filedrop.New(dirs.Dirs{ProjectHome: projectHome}, p)

	cases := []struct {
		name         string
		res          resource.Resource
		scope        adapter.Scope
		wantPath     string
		wantTargetIs string // "set" | "empty"
	}{
		{
			name:         "skill_user_nested",
			res:          &resource.Skill{Name: "my-skill", Description: "d", Body: "b"},
			scope:        adapter.ScopeUser,
			wantPath:     filepath.Join(userRoot, "skills", "my-skill", "SKILL.md"),
			wantTargetIs: "set",
		},
		{
			name:         "skill_project_nested",
			res:          &resource.Skill{Name: "my-skill", Description: "d", Body: "b"},
			scope:        adapter.ScopeProject,
			wantPath:     filepath.Join(projectHome, ".test", "skills", "my-skill", "SKILL.md"),
			wantTargetIs: "set",
		},
		{
			name:         "agent_user_flat",
			res:          &resource.Agent{Name: "my-agent", Description: "d", Body: "b"},
			scope:        adapter.ScopeUser,
			wantPath:     filepath.Join(userRoot, "agents", "my-agent.md"),
			wantTargetIs: "empty",
		},
		{
			name:         "agent_project_flat",
			res:          &resource.Agent{Name: "my-agent", Description: "d", Body: "b"},
			scope:        adapter.ScopeProject,
			wantPath:     filepath.Join(projectHome, ".test", "agents", "my-agent.md"),
			wantTargetIs: "empty",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := a.Plan(c.res, c.scope)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if len(plan.Files) != 1 {
				t.Fatalf("len(Files)=%d; want 1", len(plan.Files))
			}
			if plan.Files[0].Path != c.wantPath {
				t.Errorf("Files[0].Path = %q; want %q", plan.Files[0].Path, c.wantPath)
			}
			switch c.wantTargetIs {
			case "set":
				want := filepath.Dir(c.wantPath)
				if plan.TargetDir != want {
					t.Errorf("TargetDir = %q; want %q (Nested layout sets it)", plan.TargetDir, want)
				}
			case "empty":
				if plan.TargetDir != "" {
					t.Errorf("TargetDir = %q; want empty (flat layout leaves it empty so shared dirs aren't reclaimed)", plan.TargetDir)
				}
			}
		})
	}
}

func TestFiledrop_PlanAgent_ToolsShape_DrivesEmitShape(t *testing.T) {
	// The tools-shape parametrization is the one genuinely per-host emit
	// divergence: claudecode emits tools as a comma-separated string
	// (Claude convention); gemini emits as a YAML array (Gemini
	// convention). After Card 1, that divergence is data on the Policy,
	// not duplicated code. Pin both arms: same agent + same Layout,
	// only Policy.AgentToolsShape differs → emit byte content differs
	// in the expected direction.
	tmpDir := t.TempDir()
	agentLayout := filedrop.Layout{
		UserRoot: func(d dirs.Dirs) (string, error) { return tmpDir, nil },
		KindDir:  "agents",
		Nested:   false,
	}
	ag := &resource.Agent{
		Name:        "tools-shape-fixture",
		Description: "d",
		Body:        "b",
		Tools:       []string{"Read", "Write"},
	}

	commaPolicy := filedrop.Policy{
		HostID:          "comma-host",
		Layouts:         map[resource.Kind]filedrop.Layout{resource.KindAgent: agentLayout},
		AgentToolsShape: filedrop.ToolsCommaString,
	}
	yamlPolicy := filedrop.Policy{
		HostID:          "yaml-host",
		Layouts:         map[resource.Kind]filedrop.Layout{resource.KindAgent: agentLayout},
		AgentToolsShape: filedrop.ToolsYAMLArray,
	}

	commaPlan, err := filedrop.New(dirs.Dirs{}, commaPolicy).Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("commaPolicy Plan: %v", err)
	}
	yamlPlan, err := filedrop.New(dirs.Dirs{}, yamlPolicy).Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("yamlPolicy Plan: %v", err)
	}

	// CommaString: single-line "tools: Read, Write" — the Claude form.
	if !bytes.Contains(commaPlan.Files[0].Content, []byte("tools: Read, Write")) {
		t.Errorf("CommaString shape: expected `tools: Read, Write` in emit; got:\n%s", commaPlan.Files[0].Content)
	}
	// CommaString must NOT also emit YAML array sequence markers — would
	// signal the policy was ignored and both shapes co-emitted.
	if bytes.Contains(commaPlan.Files[0].Content, []byte("- Read")) {
		t.Errorf("CommaString shape: should not emit sequence markers; got:\n%s", commaPlan.Files[0].Content)
	}
	// YAMLArray: multi-line "tools:\n    - Read\n    - Write" — sequence
	// markers present, "Read, Write" comma form absent.
	if !bytes.Contains(yamlPlan.Files[0].Content, []byte("- Read")) ||
		!bytes.Contains(yamlPlan.Files[0].Content, []byte("- Write")) {
		t.Errorf("YAMLArray shape: expected sequence entries `- Read` + `- Write`; got:\n%s", yamlPlan.Files[0].Content)
	}
	if bytes.Contains(yamlPlan.Files[0].Content, []byte("Read, Write")) {
		t.Errorf("YAMLArray shape: should not emit comma form; got:\n%s", yamlPlan.Files[0].Content)
	}
}

func TestFiledrop_PlanCommand_ReencodesMarkdownForGeminiTOML(t *testing.T) {
	tmpDir := t.TempDir()
	cmd, err := resource.ParseCommand([]byte(`---
description: Demo command
---
Run demo.
`))
	if err != nil {
		t.Fatalf("ParseCommand: %v", err)
	}
	cmd.WithName("demo")

	a := filedrop.New(dirs.Dirs{}, filedrop.Policy{
		HostID: "gemini-cli",
		Layouts: map[resource.Kind]filedrop.Layout{
			resource.KindCommand: {
				UserRoot: func(d dirs.Dirs) (string, error) { return tmpDir, nil },
				KindDir:  "commands",
				FlatExt:  ".toml",
			},
		},
		AgentToolsShape: filedrop.ToolsYAMLArray,
	})

	plan, err := a.Plan(cmd, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got, want := plan.Files[0].Path, filepath.Join(tmpDir, "commands", "demo.toml"); got != want {
		t.Fatalf("target path = %q; want %q", got, want)
	}
	content := string(plan.Files[0].Content)
	if strings.HasPrefix(content, "---") {
		t.Fatalf("Gemini command must be TOML, got markdown:\n%s", content)
	}
	if !strings.Contains(content, "prompt =") || !strings.Contains(content, "Run demo.") {
		t.Fatalf("Gemini TOML command missing prompt:\n%s", content)
	}
}

func TestFiledrop_PlanCommand_ReencodesTOMLForMarkdownHosts(t *testing.T) {
	tmpDir := t.TempDir()
	cmd, err := resource.ParseCommand([]byte(`description = "Demo command"
prompt = "Run demo."
`))
	if err != nil {
		t.Fatalf("ParseCommand: %v", err)
	}
	cmd.WithName("demo")

	a := filedrop.New(dirs.Dirs{}, filedrop.Policy{
		HostID: "claude-code",
		Layouts: map[resource.Kind]filedrop.Layout{
			resource.KindCommand: {
				UserRoot: func(d dirs.Dirs) (string, error) { return tmpDir, nil },
				KindDir:  "commands",
			},
		},
		AgentToolsShape: filedrop.ToolsCommaString,
	})

	plan, err := a.Plan(cmd, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got, want := plan.Files[0].Path, filepath.Join(tmpDir, "commands", "demo.md"); got != want {
		t.Fatalf("target path = %q; want %q", got, want)
	}
	content := string(plan.Files[0].Content)
	if !strings.HasPrefix(content, "---\n") {
		t.Fatalf("Markdown host command must have YAML frontmatter:\n%s", content)
	}
	if !strings.Contains(content, "Run demo.") {
		t.Fatalf("Markdown host command missing body prompt:\n%s", content)
	}
}

func TestFiledrop_PlanMemory_UsesHostNativeFilename(t *testing.T) {
	tmpDir := t.TempDir()
	memory := (&resource.Memory{Body: "# Memory\n", Raw: []byte("# Memory\n")}).WithName("AGENTS.md")

	cases := []struct {
		host string
		want string
	}{
		{host: "claude-code", want: "CLAUDE.md"},
		{host: "gemini-cli", want: "GEMINI.md"},
		{host: "antigravity-cli", want: "ANTIGRAVITY.md"},
		{host: "codex", want: "AGENTS.md"},
	}

	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			a := filedrop.New(dirs.Dirs{}, filedrop.Policy{
				HostID: c.host,
				Layouts: map[resource.Kind]filedrop.Layout{
					resource.KindMemory: {
						UserRoot:     func(d dirs.Dirs) (string, error) { return tmpDir, nil },
						PreserveName: true,
					},
				},
			})
			plan, err := a.Plan(memory, adapter.ScopeUser)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if got, want := plan.Files[0].Path, filepath.Join(tmpDir, c.want); got != want {
				t.Fatalf("target path = %q; want %q", got, want)
			}
		})
	}
}

func TestFiledrop_Plan_KindNotInLayouts_ReturnsTypedError(t *testing.T) {
	// Generalisation of codex's "kind agent not yet supported": missing
	// Layout entry = unsupported kind. Error must name the host and the
	// kind so the CLI can render an actionable message.
	a := filedrop.New(dirs.Dirs{}, skillOnlyPolicy)
	_, err := a.Plan(&resource.Agent{Name: "x", Description: "d", Body: "b"}, adapter.ScopeUser)
	if err == nil {
		t.Fatal("Plan should error when Layouts has no entry for the resource's kind; got nil")
	}
	if !strings.Contains(err.Error(), "test-host") {
		t.Errorf("error should name the host; got: %v", err)
	}
	if !strings.Contains(err.Error(), `kind "agent" not yet supported`) {
		t.Errorf("error should name the unsupported kind; got: %v", err)
	}
}
