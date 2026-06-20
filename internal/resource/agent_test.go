package resource_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/resource"
)

func TestParseAgent_TracerBulletFixture(t *testing.T) {
	path := filepath.Join("testdata", "agents", "dotpack-tracer-agent.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	agent, err := resource.ParseAgent(raw)
	if err != nil {
		t.Fatalf("ParseAgent: %v", err)
	}

	if agent.Name != "dotpack-tracer-agent" {
		t.Errorf("Name: got %q, want %q", agent.Name, "dotpack-tracer-agent")
	}
	if !strings.Contains(agent.Description, "fnord-quasar-tracerphant") {
		t.Errorf("Description should contain trigger phrase; got %q", agent.Description)
	}
	if agent.Model != "sonnet" {
		t.Errorf("Model: got %q, want %q", agent.Model, "sonnet")
	}
	want := []string{"Read", "Write", "Edit"}
	if !reflect.DeepEqual(agent.Tools, want) {
		t.Errorf("Tools: got %v, want %v", agent.Tools, want)
	}
	if !strings.Contains(agent.Body, "ECHO-AGENT-TRACER-7E4F1D8C") {
		t.Errorf("Body should contain sentinel; got %q", agent.Body)
	}
	if len(agent.Extensions()) != 0 {
		t.Errorf("Extensions: got %v, want empty (fixture has only universal-core fields)", agent.Extensions())
	}
}

func TestParseAgent_ToolsAsYAMLArray(t *testing.T) {
	// Gemini convention: tools as a YAML array. ParseAgent normalises to []string
	// internally so adapters can re-emit in the host's preferred shape.
	raw := []byte("---\nname: arr-agent\ndescription: d\ntools:\n  - read_file\n  - grep_search\n---\nbody\n")
	agent, err := resource.ParseAgent(raw)
	if err != nil {
		t.Fatalf("ParseAgent: %v", err)
	}
	want := []string{"read_file", "grep_search"}
	if !reflect.DeepEqual(agent.Tools, want) {
		t.Errorf("Tools: got %v, want %v", agent.Tools, want)
	}
}

func TestParseAgent_ToolsAsCommaString(t *testing.T) {
	raw := []byte("---\nname: str-agent\ndescription: d\ntools: Read, Write,  Edit\n---\nbody\n")
	agent, err := resource.ParseAgent(raw)
	if err != nil {
		t.Fatalf("ParseAgent: %v", err)
	}
	want := []string{"Read", "Write", "Edit"}
	if !reflect.DeepEqual(agent.Tools, want) {
		t.Errorf("Tools: got %v, want %v", agent.Tools, want)
	}
}

func TestParseAgent_CollectsUnknownFieldsAsExtensions(t *testing.T) {
	// `temperature` is a gemini-only field per schema/agent.yaml deliberately_excluded
	// gemini_agent_runtime_overrides. ParseAgent must surface it on Extensions so
	// the orchestrator's §8 lossy detection has data to inspect.
	raw := []byte("---\nname: ext-agent\ndescription: d\ntemperature: 0.5\n---\nbody\n")
	agent, err := resource.ParseAgent(raw)
	if err != nil {
		t.Fatalf("ParseAgent: %v", err)
	}
	if _, ok := agent.Extensions()["temperature"]; !ok {
		t.Fatalf("Extensions[temperature] missing; Extensions=%v", agent.Extensions())
	}
}

func TestParseAgent_RejectsMissingDelimiters(t *testing.T) {
	if _, err := resource.ParseAgent([]byte("no frontmatter\n")); err == nil {
		t.Fatal("expected error for missing opening ---")
	}
	if _, err := resource.ParseAgent([]byte("---\nname: foo\nno closing\n")); err == nil {
		t.Fatal("expected error for missing closing ---")
	}
}

func TestAgent_ImplementsNamedAndResource(t *testing.T) {
	a := &resource.Agent{Name: "x"}
	var r resource.Resource = a
	if r.Kind() != resource.KindAgent {
		t.Errorf("Kind: got %q, want %q", r.Kind(), resource.KindAgent)
	}
	var n resource.Named = a
	if n.ResourceName() != "x" {
		t.Errorf("ResourceName(): got %q, want %q", n.ResourceName(), "x")
	}
}

func TestSkill_ImplementsNamed(t *testing.T) {
	s := &resource.Skill{Name: "y"}
	var n resource.Named = s
	if n.ResourceName() != "y" {
		t.Errorf("ResourceName(): got %q, want %q", n.ResourceName(), "y")
	}
}
