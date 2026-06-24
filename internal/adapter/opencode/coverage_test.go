package opencode

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
	return &resource.MCPServer{Name: "github", Command: "npx", Args: []string{"-y", "gh-mcp"}, Env: map[string]string{"TOKEN": "x"}}
}

// TestPlanCoversSupportedKinds pins OpenCode's PARTIAL matrix: the five
// supported kinds resolve to the expected paths/merge file, across user
// and project scope.
func TestPlanCoversSupportedKinds(t *testing.T) {
	d := dirs.Dirs{OpenCodeHome: t.TempDir(), ProjectHome: t.TempDir()}
	a := New(d)

	cases := []struct {
		name      string
		res       resource.Resource
		scope     adapter.Scope
		wantFile  string
		wantMerge string
	}{
		{"skill", &resource.Skill{Name: "skill", Description: "d", Body: "b"}, adapter.ScopeUser, filepath.Join(d.OpenCodeHome, "skills", "skill", "SKILL.md"), ""},
		{"agent", &resource.Agent{Name: "agent", Description: "d", Model: "m", Tools: []string{"Read"}, Body: "body"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".opencode", "agents", "agent.md"), ""},
		{"command", &resource.Command{Name: "cmd", Description: "d", Prompt: "run"}, adapter.ScopeProject, filepath.Join(d.ProjectHome, ".opencode", "commands", "cmd.md"), ""},
		{"memory", (&resource.Memory{Body: "remember"}).WithName("AGENTS.md"), adapter.ScopeProject, filepath.Join(d.ProjectHome, "AGENTS.md"), ""},
		{"mcp-user", mcpFixture(), adapter.ScopeUser, "", filepath.Join(d.OpenCodeHome, "opencode.json")},
		{"mcp-project", mcpFixture(), adapter.ScopeProject, "", filepath.Join(d.ProjectHome, "opencode.json")},
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
				if !strings.HasPrefix(plan.MergedKeys[0].Path, "$.mcp.") {
					t.Fatalf("opencode mcp path = %q; want $.mcp.<name>", plan.MergedKeys[0].Path)
				}
			}
		})
	}
}

// TestRuleAndHookUnsupported pins that OpenCode's partial matrix surfaces
// the standard structured error for kinds it does not implement — proving
// per-operation support is genuine per-adapter data (ADR-0014), not a
// global constant.
func TestRuleAndHookUnsupported(t *testing.T) {
	a := New(dirs.Dirs{OpenCodeHome: t.TempDir(), ProjectHome: t.TempDir()})
	for _, r := range []resource.Resource{
		&resource.Rule{Name: "r", Body: "x"},
		&resource.Hook{Name: "h", Events: []resource.EventBinding{{Event: "PreToolUse"}}},
	} {
		if _, err := a.Plan(r, adapter.ScopeUser); err == nil || !strings.Contains(err.Error(), "not yet supported") {
			t.Fatalf("kind %s: err = %v; want 'not yet supported'", r.Kind(), err)
		}
	}
	if _, err := a.Plan(fakeResource{kind: resource.Kind("bogus")}, adapter.ScopeUser); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("unknown kind err = %v", err)
	}
}

// TestMCPEmitShape pins OpenCode's distinct $.mcp emit (type + command
// array), and that emit rejects the wrong resource type.
func TestMCPEmitShape(t *testing.T) {
	frags, err := emitMCPServerOpenCode(mcpFixture())
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if len(frags) != 1 {
		t.Fatalf("frags = %d; want 1", len(frags))
	}
	value, ok := frags[0].Value.(map[string]any)
	if !ok {
		t.Fatalf("value type = %T", frags[0].Value)
	}
	if value["type"] != "local" {
		t.Fatalf("type = %v; want local", value["type"])
	}
	cmd, ok := value["command"].([]string)
	if !ok || len(cmd) != 3 || cmd[0] != "npx" {
		t.Fatalf("command = %#v; want [npx -y gh-mcp]", value["command"])
	}

	remote, err := emitMCPServerOpenCode(&resource.MCPServer{Name: "r", URL: "https://x/mcp"})
	if err != nil {
		t.Fatalf("emit remote: %v", err)
	}
	rv := remote[0].Value.(map[string]any)
	if rv["type"] != "remote" || rv["url"] != "https://x/mcp" {
		t.Fatalf("remote value = %#v; want type remote + url", rv)
	}

	if _, err := emitMCPServerOpenCode(&resource.Skill{Name: "no"}); err == nil {
		t.Fatal("emit should reject non-MCPServer resource")
	}
}

// TestDescribeLayoutsExposesFileDropKinds proves the registry-driven scan
// can discover OpenCode's materialized layout without a hard-coded table.
func TestDescribeLayoutsExposesFileDropKinds(t *testing.T) {
	a := New(dirs.Dirs{OpenCodeHome: t.TempDir(), ProjectHome: t.TempDir()})
	got := map[resource.Kind]adapter.KindLayout{}
	for _, l := range a.DescribeLayouts() {
		got[l.Kind] = l
	}
	if s, ok := got[resource.KindSkill]; !ok || !s.Nested || s.KindDir != "skills" {
		t.Fatalf("skill layout = %+v", got[resource.KindSkill])
	}
	if c, ok := got[resource.KindCommand]; !ok || c.Ext != ".md" || c.KindDir != "commands" {
		t.Fatalf("command layout = %+v", got[resource.KindCommand])
	}
	if _, ok := got[resource.KindRule]; ok {
		t.Fatal("rule must not appear in opencode layouts")
	}
}
