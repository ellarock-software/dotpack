package claudecode

import (
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
	return &resource.MCPServer{Name: "github", Command: "npx", Args: []string{"-y", "github"}, Env: map[string]string{"TOKEN": "x"}}
}

func TestPlanCoversFileDropAndConfigFragments(t *testing.T) {
	d := dirs.Dirs{HomeDir: t.TempDir(), ClaudeHome: t.TempDir(), ProjectHome: t.TempDir()}
	a := New(d)

	cases := []struct {
		name      string
		res       resource.Resource
		scope     adapter.Scope
		wantFile  string
		wantMerge string
	}{
		{"skill", &resource.Skill{Name: "skill", Description: "d", Body: "b"}, adapter.ScopeUser, filepath.Join(d.ClaudeHome, "skills", "skill", "SKILL.md"), ""},
		{"agent", &resource.Agent{Name: "agent", Description: "d", Model: "m", Tools: []string{"Read"}, Body: "body"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".claude", "agents", "agent.md"), ""},
		{"rule", &resource.Rule{Name: "rule", Body: "rule body"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".claude", "rules", "rule.md"), ""},
		{"command", &resource.Command{Name: "cmd", Description: "d", Prompt: "run"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".claude", "commands", "cmd.md"), ""},
		{"memory", (&resource.Memory{Body: "remember"}).WithName("CLAUDE.md"), adapter.ScopeProject, filepath.Join(d.ProjectHome, "CLAUDE.md"), ""},
		{"mcp-user", mcpFixture(), adapter.ScopeUser, "", filepath.Join(d.HomeDir, ".claude.json")},
		{"mcp-project", mcpFixture(), adapter.ScopeProject, "", filepath.Join(d.ProjectHome, ".mcp.json")},
		{"hook-user", hookFixture(), adapter.ScopeUser, "", filepath.Join(d.ClaudeHome, "settings.json")},
		{"hook-project", hookFixture(), adapter.ScopeProject, "", filepath.Join(d.ProjectHome, ".claude", "settings.json")},
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
			}
			if tc.wantMerge != "" {
				if len(plan.MergedKeys) == 0 || plan.MergedKeys[0].File != tc.wantMerge {
					t.Fatalf("merge plan = %+v, want file %s", plan.MergedKeys, tc.wantMerge)
				}
			}
		})
	}
}

func TestPlanUnsupportedKind(t *testing.T) {
	a := New(dirs.Dirs{ClaudeHome: t.TempDir(), HomeDir: t.TempDir(), ProjectHome: t.TempDir()})
	_, err := a.Plan(fakeResource{kind: resource.Kind("unknown")}, adapter.ScopeUser)
	if err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("unsupported error = %v", err)
	}
}

func TestEmitErrorsOnWrongResourceAndEmptyHook(t *testing.T) {
	if _, err := emitMCPServer(&resource.Skill{Name: "no"}); err == nil {
		t.Fatal("emitMCPServer should reject wrong resource type")
	}
	if _, err := emitHook(&resource.Skill{Name: "no"}); err == nil {
		t.Fatal("emitHook should reject wrong resource type")
	}
	if _, err := emitHook(&resource.Hook{Name: "empty"}); err == nil || !strings.Contains(err.Error(), "no events") {
		t.Fatalf("empty hook error = %v", err)
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
		{"skill-user", &resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeUser, "ClaudeHome"},
		{"skill-project", &resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeProject, "ProjectHome"},
		{"mcp-user", mcpFixture(), adapter.ScopeUser, "HomeDir"},
		{"mcp-project", mcpFixture(), adapter.ScopeProject, "ProjectHome"},
		{"hook-user", hookFixture(), adapter.ScopeUser, "ClaudeHome"},
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
