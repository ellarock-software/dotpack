package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
)

type fakeResource struct{ kind resource.Kind }

func (f fakeResource) Kind() resource.Kind        { return f.kind }
func (f fakeResource) Extensions() map[string]any { return nil }

func hookFixture() *resource.Hook {
	return &resource.Hook{
		Name: "guard",
		Events: []resource.EventBinding{{
			Event: "PreToolUse",
			Bindings: []resource.Binding{{
				Matcher: "Bash",
				Hooks: []resource.HookSpec{{
					Type:          "command",
					Command:       "/bin/true",
					Timeout:       3,
					HasTimeout:    true,
					StatusMessage: "checking",
					Env:           map[string]string{"A": "B"},
				}},
			}},
		}},
	}
}

func mcpFixture() *resource.MCPServer {
	return (&resource.MCPServer{Name: "github", Command: "npx", Args: []string{"-y"}, Env: map[string]string{"TOKEN": "x"}}).
		WithExtensions(map[string]any{"enabled_tools": []any{"issues"}, "startup_timeout_sec": float64(30)})
}

func TestPlanCoversFileDropAgentAndConfigFragments(t *testing.T) {
	d := dirs.Dirs{AgentsHome: t.TempDir(), CodexHome: t.TempDir(), ProjectHome: t.TempDir()}
	a := New(d)

	cases := []struct {
		name      string
		res       resource.Resource
		scope     adapter.Scope
		wantFile  string
		wantMerge string
	}{
		{"skill", &resource.Skill{Name: "skill", Description: "d", Body: "b"}, adapter.ScopeUser, filepath.Join(d.AgentsHome, "skills", "skill", "SKILL.md"), ""},
		{"rule", &resource.Rule{Name: "rule", Body: "rule body"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".codex", "rules", "rule.md"), ""},
		{"command", &resource.Command{Name: "cmd", Description: "d", Prompt: "run"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".codex", "commands", "cmd.md"), ""},
		{"memory", (&resource.Memory{Body: "remember"}).WithName("AGENTS.md"), adapter.ScopeProject, filepath.Join(d.ProjectHome, "AGENTS.md"), ""},
		{"agent-user", &resource.Agent{Name: "agent", Description: "d", Model: "m", Tools: []string{"skill-a"}, Body: "body"}, adapter.ScopeUser, filepath.Join(d.CodexHome, "agents", "agent.toml"), ""},
		{"agent-project", &resource.Agent{Name: "agent", Description: "d", Model: "m", Body: "body"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".codex", "agents", "agent.toml"), ""},
		{"mcp-user", mcpFixture(), adapter.ScopeUser, "", filepath.Join(d.CodexHome, "config.toml")},
		{"mcp-project", mcpFixture(), adapter.ScopeProject, "", filepath.Join(d.ProjectHome, ".codex", "config.toml")},
		{"hook-user", hookFixture(), adapter.ScopeUser, "", filepath.Join(d.CodexHome, "config.toml")},
		{"hook-project", hookFixture(), adapter.ScopeProject, "", filepath.Join(d.ProjectHome, ".codex", "config.toml")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := a.Plan(tc.res, tc.scope)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if tc.wantFile != "" {
				if len(plan.Files) != 1 || plan.Files[0].Path != tc.wantFile {
					t.Fatalf("file plan = %+v, want %s", plan.Files, tc.wantFile)
				}
				if tc.name == "agent-user" && !strings.Contains(string(plan.Files[0].Content), "developer_instructions") {
					t.Fatalf("codex agent TOML missing developer_instructions:\n%s", plan.Files[0].Content)
				}
			}
			if tc.wantMerge != "" {
				if len(plan.MergedKeys) == 0 || plan.MergedKeys[0].File != tc.wantMerge {
					t.Fatalf("merge plan = %+v, want file %s", plan.MergedKeys, tc.wantMerge)
				}
				if strings.HasPrefix(tc.name, "hook") && plan.MergedKeys[0].Path != "hooks.PreToolUse" {
					t.Fatalf("codex hook path = %+v", plan.MergedKeys[0])
				}
			}
		})
	}
}

func TestPlanUnsupportedKindAndAgentErrors(t *testing.T) {
	a := New(dirs.Dirs{AgentsHome: t.TempDir(), CodexHome: t.TempDir(), ProjectHome: t.TempDir()})
	if _, err := a.Plan(fakeResource{kind: resource.Kind("unknown")}, adapter.ScopeUser); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("unsupported error = %v", err)
	}
	if _, err := planAgentCodex(&resource.Skill{Name: "no"}, adapter.ScopeUser, dirs.Dirs{CodexHome: t.TempDir()}); err == nil {
		t.Fatal("planAgentCodex should reject wrong resource type")
	}
	if _, err := planAgentCodex(&resource.Agent{Name: "a"}, adapter.ScopeUser, dirs.Dirs{}); err == nil || !strings.Contains(err.Error(), "CodexHome") {
		t.Fatalf("missing CodexHome error = %v", err)
	}
	if _, err := planAgentCodex(&resource.Agent{Name: "a"}, adapter.ScopeProject, dirs.Dirs{}); err == nil || !strings.Contains(err.Error(), "ProjectHome") {
		t.Fatalf("missing ProjectHome error = %v", err)
	}
	if _, err := planAgentCodex(&resource.Agent{Name: "a"}, adapter.Scope("bad"), dirs.Dirs{CodexHome: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Fatalf("unknown scope error = %v", err)
	}
}

func TestEmitErrorsAndHooksJSONConflict(t *testing.T) {
	if _, err := emitMCPServerCodex(&resource.Skill{Name: "no"}); err == nil {
		t.Fatal("emitMCPServerCodex should reject wrong resource type")
	}
	if _, err := emitHookCodex(&resource.Skill{Name: "no"}); err == nil {
		t.Fatal("emitHookCodex should reject wrong resource type")
	}
	if _, err := emitHookCodex(&resource.Hook{Name: "empty"}); err == nil || !strings.Contains(err.Error(), "no events") {
		t.Fatalf("empty hook error = %v", err)
	}

	codexHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexHome, "hooks.json"), []byte(`{"hooks":{"PreToolUse":[{"command":"x"}]}}`), 0o644); err != nil {
		t.Fatalf("write hooks.json: %v", err)
	}
	a := New(dirs.Dirs{CodexHome: codexHome, AgentsHome: t.TempDir(), ProjectHome: t.TempDir()})
	if _, err := a.Plan(hookFixture(), adapter.ScopeUser); err == nil || !strings.Contains(err.Error(), "hooks.json") {
		t.Fatalf("hooks.json conflict error = %v", err)
	}
}

func TestCodexHooksJSONDefinesHooksShapes(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"missing", "", false},
		{"empty-file", "", false},
		{"empty-hooks-map", `{"hooks":{}}`, false},
		{"hooks-array", `{"hooks":{"PreToolUse":[{"command":"x"}]}}`, true},
		{"top-level-empty-event", `{"PreToolUse":[]}`, false},
		{"top-level-event", `{"PreToolUse":[{"command":"x"}]}`, true},
		{"string-event", `{"Stop":"notify"}`, true},
		{"null-event", `{"Stop":null}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmp, tc.name+".json")
			if tc.name != "missing" {
				if err := os.WriteFile(path, []byte(tc.raw), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			got, err := codexHooksJSONDefinesHooks(path)
			if err != nil {
				t.Fatalf("codexHooksJSONDefinesHooks: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	bad := filepath.Join(tmp, "bad.json")
	if err := os.WriteFile(bad, []byte(`{bad`), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if _, err := codexHooksJSONDefinesHooks(bad); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("bad JSON error = %v", err)
	}
}

func TestPlanMissingRootsReturnHostSpecificErrors(t *testing.T) {
	a := New(dirs.Dirs{})
	cases := []struct {
		name  string
		res   resource.Resource
		scope adapter.Scope
		want  string
	}{
		{"skill-user", &resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeUser, "AgentsHome"},
		{"skill-project", &resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeProject, "ProjectHome"},
		{"rule-user", &resource.Rule{Name: "r", Body: "b"}, adapter.ScopeUser, "CodexHome"},
		{"mcp-user", mcpFixture(), adapter.ScopeUser, "CodexHome"},
		{"mcp-project", mcpFixture(), adapter.ScopeProject, "ProjectHome"},
		{"hook-user", hookFixture(), adapter.ScopeUser, "CodexHome"},
		{"hook-project", hookFixture(), adapter.ScopeProject, "ProjectHome"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.Plan(tc.res, tc.scope); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Plan error = %v; want %s", err, tc.want)
			}
		})
	}
}
