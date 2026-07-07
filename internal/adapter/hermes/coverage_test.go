package hermes

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

func mcpFixture() *resource.MCPServer {
	return (&resource.MCPServer{
		Name:    "github",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-github"},
		Env:     map[string]string{"GITHUB_TOKEN": "x"},
	}).WithExtensions(map[string]any{
		"enabled":                      true,
		"timeout":                      30,
		"connect_timeout":              10,
		"supports_parallel_tool_calls": true,
		"headers":                      map[string]any{"Authorization": "Bearer x"},
		"auth":                         "oauth",
		"sampling":                     map[string]any{"enabled": true},
		"ssl_verify":                   false,
		"client_cert":                  "/tmp/client.pem",
		"client_key":                   "/tmp/client.key",
		"tools": map[string]any{
			"include":   []any{"list_issues"},
			"resources": false,
			"prompts":   false,
		},
	})
}

func hookFixture() *resource.Hook {
	return &resource.Hook{
		Name: "guard",
		Events: []resource.EventBinding{{
			Event: "PreToolUse",
			Bindings: []resource.Binding{{
				Matcher: "Bash",
				Hooks: []resource.HookSpec{{
					Type:       "command",
					Command:    "python hook.py --root $CLAUDE_PROJECT_DIR && echo ok",
					Timeout:    12,
					HasTimeout: true,
					Env:        map[string]string{"OACB_TIER": "gold"},
				}},
			}},
		}},
	}
}

func TestPlanCoversSupportedKinds(t *testing.T) {
	d := dirs.Dirs{HermesHome: t.TempDir(), ProjectHome: t.TempDir()}
	a := New(d)

	cases := []struct {
		name      string
		res       resource.Resource
		scope     adapter.Scope
		wantFile  string
		wantMerge string
	}{
		{"skill-user", &resource.Skill{Name: "skill", Description: "d", Body: "b"}, adapter.ScopeUser, filepath.Join(d.HermesHome, "skills", "skill", "SKILL.md"), ""},
		{"memory-user", (&resource.Memory{Body: "remember"}).WithName("AGENTS.md"), adapter.ScopeUser, filepath.Join(d.HermesHome, "SOUL.md"), ""},
		{"memory-project-hermes", (&resource.Memory{Body: "remember"}).WithName(".hermes.md"), adapter.ScopeProject, filepath.Join(d.ProjectHome, ".hermes.md"), ""},
		{"memory-project-fallback", (&resource.Memory{Body: "remember"}).WithName("GEMINI.md"), adapter.ScopeProject, filepath.Join(d.ProjectHome, "AGENTS.md"), ""},
		{"mcp-user", mcpFixture(), adapter.ScopeUser, "", filepath.Join(d.HermesHome, "config.yaml")},
		{"hook-user", hookFixture(), adapter.ScopeUser, "", filepath.Join(d.HermesHome, "config.yaml")},
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

func TestProjectOnlyAndUnsupportedKindsErrorClearly(t *testing.T) {
	a := New(dirs.Dirs{HermesHome: t.TempDir(), ProjectHome: t.TempDir()})

	for _, tc := range []struct {
		name  string
		res   resource.Resource
		scope adapter.Scope
		want  string
	}{
		{"skill-project", &resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeProject, "scope"},
		{"mcp-project", mcpFixture(), adapter.ScopeProject, "scope"},
		{"hook-project", hookFixture(), adapter.ScopeProject, "scope"},
		{"agent", &resource.Agent{Name: "a", Description: "d", Body: "b"}, adapter.ScopeUser, "not yet supported"},
		{"rule", &resource.Rule{Name: "r", Body: "b"}, adapter.ScopeUser, "not yet supported"},
		{"command", &resource.Command{Name: "c", Description: "d", Prompt: "run"}, adapter.ScopeUser, "not yet supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.Plan(tc.res, tc.scope); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Plan error = %v; want %q", err, tc.want)
			}
		})
	}
	if _, err := a.Plan(fakeResource{kind: resource.Kind("bogus")}, adapter.ScopeUser); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("unknown kind error = %v", err)
	}
}

func TestEmitMCPServerHermesKeepsHermesExtensionsAndRejectsForeignToolsShape(t *testing.T) {
	frags, err := emitMCPServerHermes(mcpFixture())
	if err != nil {
		t.Fatalf("emitMCPServerHermes: %v", err)
	}
	if len(frags) != 1 || frags[0].Path != "mcp_servers.github" {
		t.Fatalf("frags = %+v; want one mcp_servers.github fragment", frags)
	}
	value := frags[0].Value.(map[string]any)
	for _, key := range []string{"enabled", "timeout", "connect_timeout", "supports_parallel_tool_calls", "headers", "auth", "sampling", "ssl_verify", "client_cert", "client_key", "tools"} {
		if _, ok := value[key]; !ok {
			t.Fatalf("Hermes emit dropped %q: %#v", key, value)
		}
	}

	bad := (&resource.MCPServer{Name: "bad", Command: "npx", Args: []string{"server"}}).WithExtensions(map[string]any{
		"tools": map[string]any{"click": map[string]any{"approval_mode": "approve"}},
	})
	if _, err := emitMCPServerHermes(bad); err == nil || !strings.Contains(err.Error(), "tools") {
		t.Fatalf("foreign tools shape error = %v; want tools-shape rejection", err)
	}
}

func TestEmitHookHermesRewritesCommandsAndRejectsUnsupportedEvents(t *testing.T) {
	frags, err := emitHookHermes(hookFixture())
	if err != nil {
		t.Fatalf("emitHookHermes: %v", err)
	}
	if len(frags) != 1 {
		t.Fatalf("frags = %+v; want 1", frags)
	}
	if frags[0].Path != "hooks.pre_tool_call" || frags[0].Op != adapter.MergedKeyAppend {
		t.Fatalf("fragment = %+v; want hooks.pre_tool_call append", frags[0])
	}
	value := frags[0].Value.(map[string]any)
	if value["matcher"] != "Bash" || value["timeout"] != 12 {
		t.Fatalf("hook value = %#v", value)
	}
	command := value["command"].(string)
	for _, want := range []string{"env OACB_TIER='gold'", "bash -lc", "$(git rev-parse --show-toplevel)"} {
		if !strings.Contains(command, want) {
			t.Fatalf("command = %q; want %q", command, want)
		}
	}
	if strings.Contains(command, "CLAUDE_PROJECT_DIR") {
		t.Fatalf("command should rewrite CLAUDE_PROJECT_DIR: %q", command)
	}

	if _, err := emitHookHermes(&resource.Hook{
		Name: "unsupported",
		Events: []resource.EventBinding{{
			Event:    "Stop",
			Bindings: []resource.Binding{{Hooks: []resource.HookSpec{{Type: "command", Command: "echo hi"}}}},
		}},
	}); err == nil || !strings.Contains(err.Error(), "no native shell-hook event") {
		t.Fatalf("unsupported event error = %v", err)
	}
}
