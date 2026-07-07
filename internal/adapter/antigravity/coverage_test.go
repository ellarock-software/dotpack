package antigravity

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
				Matcher:    "Bash",
				Extensions: map[string]any{"async": true, "once": true},
				Hooks: []resource.HookSpec{{
					Type:          "command",
					Command:       "/bin/true",
					Timeout:       2,
					HasTimeout:    true,
					StatusMessage: "checking",
					Env:           map[string]string{"A": "B"},
					Extensions:    map[string]any{"name": "named-hook", "description": "desc"},
				}},
			}},
		}},
	}
}

func mcpFixture() *resource.MCPServer {
	return (&resource.MCPServer{Name: "github", Command: "npx", Args: []string{}, Env: map[string]string{"TOKEN": "x"}}).
		WithExtensions(map[string]any{"type": "stdio"})
}

func TestPlanCoversFileDropAndConfigFragments(t *testing.T) {
	d := dirs.Dirs{AntigravityHome: t.TempDir(), ProjectHome: t.TempDir()}
	a := New(d)

	cases := []struct {
		name      string
		res       resource.Resource
		scope     adapter.Scope
		wantFile  string
		wantMerge string
	}{
		{"skill", &resource.Skill{Name: "skill", Description: "d", Body: "b"}, adapter.ScopeUser, filepath.Join(d.AntigravityHome, "skills", "skill", "SKILL.md"), ""},
		{"agent", &resource.Agent{Name: "agent", Description: "d", Model: "m", Tools: []string{"Read"}, Body: "body"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".antigravity", "agents", "agent.md"), ""},
		{"rule", &resource.Rule{Name: "rule", Body: "rule body"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".antigravity", "rules", "rule.md"), ""},
		{"command", &resource.Command{Name: "cmd", Description: "d", Prompt: "run"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".antigravity", "commands", "cmd.md"), ""},
		{"memory", (&resource.Memory{Body: "remember"}).WithName("ANTIGRAVITY.md"), adapter.ScopeProject, filepath.Join(d.ProjectHome, "ANTIGRAVITY.md"), ""},
		{"mcp-user", mcpFixture(), adapter.ScopeUser, "", filepath.Join(d.AntigravityHome, "settings.json")},
		{"mcp-project", mcpFixture(), adapter.ScopeProject, "", filepath.Join(d.ProjectHome, ".antigravity", "settings.json")},
		{"hook-user", hookFixture(), adapter.ScopeUser, "", filepath.Join(d.AntigravityHome, "settings.json")},
		{"hook-project", hookFixture(), adapter.ScopeProject, "", filepath.Join(d.ProjectHome, ".antigravity", "settings.json")},
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
				if tc.name == "hook-user" && plan.MergedKeys[0].Path != "$.hooks.BeforeTool" {
					t.Fatalf("antigravity hook event not rewritten: %+v", plan.MergedKeys[0])
				}
			}
		})
	}
}

func TestPlanUnsupportedKindAndEmitErrors(t *testing.T) {
	a := New(dirs.Dirs{AntigravityHome: t.TempDir(), ProjectHome: t.TempDir()})
	if _, err := a.Plan(fakeResource{kind: resource.Kind("unknown")}, adapter.ScopeUser); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("unsupported error = %v", err)
	}
	if _, err := emitMCPServerAntigravity(&resource.Skill{Name: "no"}); err == nil {
		t.Fatal("emitMCPServerAntigravity should reject wrong resource type")
	}
	if _, err := emitHookAntigravity(&resource.Skill{Name: "no"}); err == nil {
		t.Fatal("emitHookAntigravity should reject wrong resource type")
	}
	if _, err := emitHookAntigravity(&resource.Hook{Name: "empty"}); err == nil || !strings.Contains(err.Error(), "no events") {
		t.Fatalf("empty hook error = %v", err)
	}
}

func TestAntigravityHookEventAndGeneratedNames(t *testing.T) {
	h := &resource.Hook{
		Name: "guard",
		Events: []resource.EventBinding{
			{Event: "PostToolUse", Bindings: []resource.Binding{{Hooks: []resource.HookSpec{{Type: "command", Command: "$CLAUDE_PROJECT_DIR/a"}}}}},
			{Event: "Stop", Bindings: []resource.Binding{{Hooks: []resource.HookSpec{{Type: "command", Command: "${CLAUDE_PROJECT_DIR}/b"}}}}},
		},
	}
	frags, err := emitHookAntigravity(h)
	if err != nil {
		t.Fatalf("emitHookAntigravity: %v", err)
	}
	if len(frags) != 2 {
		t.Fatalf("frags = %+v; want 2", frags)
	}
	if frags[0].Path != "$.hooks.AfterTool" || frags[1].Path != "$.hooks.Stop" {
		t.Fatalf("event rewrite paths = %+v", frags)
	}
	first := frags[0].Value.(map[string]any)["hooks"].([]any)[0].(map[string]any)
	second := frags[1].Value.(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if first["name"] != "guard-1" || second["name"] != "guard-2" {
		t.Fatalf("generated names = %v, %v; want guard-1/guard-2", first["name"], second["name"])
	}
	if strings.Contains(first["command"].(string), "CLAUDE_PROJECT_DIR") || strings.Contains(second["command"].(string), "CLAUDE_PROJECT_DIR") {
		t.Fatalf("commands should rewrite Claude project placeholder: %#v %#v", first, second)
	}
}

func TestAntigravityMCPAndHookExtensionEdgeBranches(t *testing.T) {
	mcp := (&resource.MCPServer{Name: "remote", URL: "https://example.com"}).
		WithExtensions(map[string]any{
			"url":     "https://ignored.example.com",
			"type":    "http",
			"unknown": true,
		})
	frags, err := emitMCPServerAntigravity(mcp)
	if err != nil {
		t.Fatalf("emitMCPServerAntigravity: %v", err)
	}
	value := frags[0].Value.(map[string]any)
	if value["url"] != "https://example.com" || value["type"] != "http" {
		t.Fatalf("mcp value = %#v; want typed URL and kept type extension", value)
	}
	if _, ok := value["unknown"]; ok {
		t.Fatalf("unknown mcp extension should be dropped: %#v", value)
	}

	h := &resource.Hook{
		Name: "single",
		Events: []resource.EventBinding{{
			Event: "PreToolUse",
			Bindings: []resource.Binding{{
				Extensions: map[string]any{"async": true, "unknown": true},
				Hooks: []resource.HookSpec{{
					Type:       "command",
					Command:    "echo hi",
					Extensions: map[string]any{"command": "ignored", "description": "desc", "unknown": true},
				}},
			}},
		}},
	}
	frags, err = emitHookAntigravity(h)
	if err != nil {
		t.Fatalf("emitHookAntigravity: %v", err)
	}
	hook := frags[0].Value.(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if hook["name"] != "single" || hook["description"] != "desc" || hook["async"] != true {
		t.Fatalf("single hook value = %#v; want generated name and kept extensions", hook)
	}
	if hook["command"] == "ignored" {
		t.Fatalf("hook extension must not overwrite command: %#v", hook)
	}
	if _, ok := hook["unknown"]; ok {
		t.Fatalf("unknown hook extension should be dropped: %#v", hook)
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
		{"skill-user", &resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeUser, "AntigravityHome"},
		{"skill-project", &resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeProject, "ProjectHome"},
		{"mcp-user", mcpFixture(), adapter.ScopeUser, "AntigravityHome"},
		{"mcp-project", mcpFixture(), adapter.ScopeProject, "ProjectHome"},
		{"hook-user", hookFixture(), adapter.ScopeUser, "AntigravityHome"},
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
