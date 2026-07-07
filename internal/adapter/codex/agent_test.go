package codex_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/adapter/codex"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/pelletier/go-toml/v2"
)

func TestCodex_PlanAgent_UserScope_WritesToCodexHomeAgents(t *testing.T) {
	home := t.TempDir()
	a := codex.New(dirs.Dirs{CodexHome: home})

	agent := &resource.Agent{
		Name:        "test-agent",
		Description: "A test agent",
		Model:       "codex-model",
		Body:        "Do something useful.",
		Tools:       []string{"read_file"},
	}
	plan, err := a.Plan(agent, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if got := len(plan.Files); got != 1 {
		t.Fatalf("len(plan.Files): got %d, want 1", got)
	}

	want := filepath.Join(home, "agents", "test-agent.toml")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}

	// Verify TOML formatting
	content := plan.Files[0].Content
	var decoded map[string]any
	if err := toml.Unmarshal(content, &decoded); err != nil {
		t.Fatalf("failed to parse emitted TOML: %v", err)
	}

	if decoded["name"] != "test-agent" {
		t.Errorf("TOML name: got %v, want %q", decoded["name"], "test-agent")
	}
	if decoded["developer_instructions"] != "Do something useful." {
		t.Errorf("TOML developer_instructions: got %v", decoded["developer_instructions"])
	}
	skills, ok := decoded["skills"].([]any)
	if !ok || len(skills) == 0 || skills[0] != "read_file" {
		t.Errorf("TOML skills: got %v", decoded["skills"])
	}
}

func TestCodex_PlanAgent_ProjectScope_WritesToProjectCodexAgents(t *testing.T) {
	projectHome := t.TempDir()
	a := codex.New(dirs.Dirs{ProjectHome: projectHome})

	agent := &resource.Agent{
		Name: "project-agent",
	}
	plan, err := a.Plan(agent, adapter.ScopeProject)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := filepath.Join(projectHome, ".codex", "agents", "project-agent.toml")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}
}

func TestCodex_PlanAgent_MissingDirsErrors(t *testing.T) {
	a := codex.New(dirs.Dirs{})
	agent := &resource.Agent{Name: "h"}

	_, err := a.Plan(agent, adapter.ScopeUser)
	if err == nil {
		t.Fatal("expected error for user scope with no CodexHome")
	}
	if !strings.Contains(err.Error(), "CodexHome") {
		t.Errorf("error should name CodexHome; got %v", err)
	}

	_, err = a.Plan(agent, adapter.ScopeProject)
	if err == nil {
		t.Fatal("expected error for project scope with no ProjectHome")
	}
	if !strings.Contains(err.Error(), "ProjectHome") {
		t.Errorf("error should name ProjectHome; got %v", err)
	}
}

func TestCodex_PlanAgent_PreservesExtensions(t *testing.T) {
	a := codex.New(dirs.Dirs{CodexHome: t.TempDir()})
	agent := (&resource.Agent{
		Name: "ext-agent",
	}).WithExtensions(map[string]any{
		"sandbox_mode": "full",
	})
	plan, err := a.Plan(agent, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if !bytes.Contains(plan.Files[0].Content, []byte("sandbox_mode = 'full'")) &&
		!bytes.Contains(plan.Files[0].Content, []byte("sandbox_mode = \"full\"")) {
		t.Errorf("codex must preserve extensions in TOML; got:\n%s", string(plan.Files[0].Content))
	}
}
