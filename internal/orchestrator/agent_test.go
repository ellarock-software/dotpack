package orchestrator_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/adapter/claudecode"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/manifest"
	"github.com/ellarock/dotpack/internal/orchestrator"
	"github.com/ellarock/dotpack/internal/resource"
)

func TestInstall_AgentToClaudeCode_WritesFileAndRecordsManifest(t *testing.T) {
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	ag := &resource.Agent{
		Name:        "code-reviewer",
		Description: "Use when reviewing PRs",
		Model:       "sonnet",
		Tools:       []string{"Read", "Grep"},
		Body:        "system prompt\n",
	}
	res, err := orch.Install(ag, adapter.ScopeUser, orchestrator.InstallOptions{Source: "file:///fake/code-reviewer.md"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	want := filepath.Join(d.ClaudeHome, "agents", "code-reviewer.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected agent file at %s: %v", want, err)
	}
	if res.Record.ID != "claude-code:agent:code-reviewer" {
		t.Errorf("Record.ID: got %q, want %q", res.Record.ID, "claude-code:agent:code-reviewer")
	}
	if res.Record.Kind != "agent" {
		t.Errorf("Record.Kind: got %q, want %q", res.Record.Kind, "agent")
	}
	// Critical: TargetDir MUST be empty for agents — agents/ is shared.
	if res.Record.TargetDir != "" {
		t.Errorf("Record.TargetDir: got %q, want empty (agents/ is shared)", res.Record.TargetDir)
	}
}

func TestUninstall_Agent_RemovesFileButLeavesSharedAgentsDir(t *testing.T) {
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	ag := &resource.Agent{Name: "reviewer", Description: "d", Body: "b"}
	res, err := orch.Install(ag, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	agentsDir := filepath.Join(d.ClaudeHome, "agents")

	uninstall, err := orch.Uninstall(res.Record.ID)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if _, err := os.Stat(filepath.Join(agentsDir, "reviewer.md")); !os.IsNotExist(err) {
		t.Errorf("agent file should be removed; got %v", err)
	}
	if _, err := os.Stat(agentsDir); err != nil {
		t.Errorf("shared agents/ directory must survive uninstall; got %v", err)
	}
	if uninstall.TargetDirRemoved {
		t.Errorf("UninstallResult.TargetDirRemoved must be false for agents (shared dir)")
	}
}

func TestUninstall_Agent_PreservesSiblingAgent(t *testing.T) {
	// Pre-existing user agent in the shared agents/ dir survives the
	// uninstall of a dotpack-installed sibling. Belt-and-braces around
	// the TargetDir-empty contract: even if a future refactor reintroduces
	// the os.Remove, ENOTEMPTY would still preserve this — but the
	// test exists to lock both layers.
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	ag := &resource.Agent{Name: "dp-managed", Description: "d", Body: "b"}
	res, err := orch.Install(ag, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	sibling := filepath.Join(d.ClaudeHome, "agents", "user-authored.md")
	if err := os.WriteFile(sibling, []byte("---\nname: user-authored\n---\n"), 0o644); err != nil {
		t.Fatalf("write sibling: %v", err)
	}

	if _, err := orch.Uninstall(res.Record.ID); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Errorf("user-authored sibling agent must survive; got %v", err)
	}
}

func TestList_MixedSkillAndAgent_PreservesSlotOrder(t *testing.T) {
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	skill := &resource.Skill{Name: "s1", Description: "d", Body: "b"}
	ag := &resource.Agent{Name: "a1", Description: "d", Body: "b"}
	if _, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"}); err != nil {
		t.Fatalf("Install skill: %v", err)
	}
	if _, err := orch.Install(ag, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"}); err != nil {
		t.Fatalf("Install agent: %v", err)
	}

	records, err := orch.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records): got %d, want 2", len(records))
	}
	if records[0].Kind != "skill" || records[1].Kind != "agent" {
		t.Errorf("slot order: got [%s, %s], want [skill, agent]", records[0].Kind, records[1].Kind)
	}
}

// fakeUnnamedResource is a Resource that does NOT implement Named —
// stands in for memory / mcp-server (whose identity is filename / JSON
// key, not a `name:` field) until those kinds land. Used to pin that
// resourceName errors cleanly instead of panicking.
type fakeUnnamedResource struct{}

func (fakeUnnamedResource) Kind() resource.Kind     { return resource.Kind("fake-unnamed") }
func (fakeUnnamedResource) Extensions() map[string]any { return nil }

// fakeAdapter wraps a real adapter but plans an empty install for any
// Resource — lets us drive Install with a non-Named resource without
// touching the schema package or any claudecode-specific dispatch.
type fakeAdapter struct{}

func (fakeAdapter) HostID() string                        { return "fake-host" }
func (fakeAdapter) Capabilities() adapter.KindCapabilityMatrix { return nil }
func (fakeAdapter) Plan(r resource.Resource, _ adapter.Scope) (adapter.InstallPlan, error) {
	return adapter.InstallPlan{}, nil
}

func TestInstall_ResourceWithoutNamed_ReturnsErrorNotPanic(t *testing.T) {
	// When memory / mcp-server land, they will NOT implement Named
	// (identity is filename or JSON-object key). resourceName must
	// return a useful error in that case, NOT panic — the CLI must
	// print something better than a Go runtime stack trace.
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, fakeAdapter{}, mf)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Install must return error, not panic; got panic: %v", r)
		}
	}()

	_, err := orch.Install(fakeUnnamedResource{}, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err == nil {
		t.Fatal("expected error from Install with non-Named resource, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "fake-unnamed") || !strings.Contains(msg, "Named") {
		t.Errorf("error must name the unsupported kind and the Named interface; got %q", msg)
	}
}

func TestInstall_AgentWithGeminiOnlyExtensionIsLossyOnClaudeCode(t *testing.T) {
	// Discriminating §8 test (per advisor): `temperature` is bound to
	// gemini_agent_runtime_overrides (gemini-cli only). Install onto
	// claude-code without --allow-lossy must fail with LossyError.
	// Different concept than skill's allowed-tools; same §8 algorithm
	// — confirms the schema-driven design generalises across kinds with
	// no per-kind branching in the orchestrator.
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	ag := (&resource.Agent{Name: "g", Description: "d", Body: "b"}).
		WithExtensions(map[string]any{"temperature": 0.5})
	_, err := orch.Install(ag, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err == nil {
		t.Fatal("expected LossyError, got nil")
	}
	var le *orchestrator.LossyError
	if !errors.As(err, &le) {
		t.Fatalf("expected *orchestrator.LossyError, got %T: %v", err, err)
	}
	found := false
	for _, r := range le.Reasons {
		if r.FieldPath == "temperature" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected temperature in LossyError.Reasons; got %+v", le.Reasons)
	}
}
