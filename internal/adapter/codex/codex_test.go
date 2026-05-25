package codex_test

import (
	"strings"
	"testing"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/adapter/codex"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
)

func TestCodex_HostID(t *testing.T) {
	// Schema (schema/agent.yaml does NOT have codex, but schema/hook.yaml,
	// schema/mcp-server.yaml, schema/memory.yaml all declare aliases with
	// `host: codex`) — the adapter's HostID MUST match those strings
	// verbatim or LossyExtensions will silently flip codex-native
	// concepts to lossy on the codex adapter's own host. schema/schema.go
	// Alias docstring is the load-bearing contract.
	a := codex.New(dirs.Dirs{AgentsHome: t.TempDir()})
	if got := a.HostID(); got != "codex" {
		t.Errorf("HostID: got %q, want %q", got, "codex")
	}
}

func TestCodex_Capabilities_SkillNative_AgentUnsupported(t *testing.T) {
	// Skill on codex is native: codex CLI loads skills from
	// ~/.agents/skills/<name>/SKILL.md per developers.openai.com/codex/skills.
	// Agent on codex is declared Unsupported EXPLICITLY (not left absent
	// and relying on iota's zero value): schema/agent.yaml makes zero
	// mentions of codex, OpenAI Codex CLI docs document no subagent
	// loading directory analogous to .claude/agents/ or .gemini/agents/.
	// The explicit declaration makes "deliberately decided not to
	// support" visible — distinguishable from "nobody thought about it
	// yet." Behaviour-form assertion (caps[KindAgent] == Unsupported)
	// rather than presence-form (`, has := caps[...]; has`) so a future
	// rephrasing of the declaration (e.g. via a helper that emits the
	// same value) doesn't trip a false negative.
	a := codex.New(dirs.Dirs{AgentsHome: t.TempDir()})
	caps := a.Capabilities()
	if got := caps[resource.KindSkill]; got != adapter.Native {
		t.Errorf("Capabilities[skill]: got %v, want Native", got)
	}
	if got := caps[resource.KindAgent]; got != adapter.Unsupported {
		t.Errorf("Capabilities[agent]: got %v, want Unsupported (codex CLI documents no native agent loading directory)", got)
	}
}

func TestCodex_Plan_AgentKind_ReturnsUnsupportedError(t *testing.T) {
	// Adapter-level enforcement of Capabilities[agent] absent. Even if a
	// future CLI bug routes an agent resource to the codex adapter, Plan
	// must return a structured error rather than silently writing the
	// agent to some fictional path. The error mentions both the host
	// ("codex") and the kind ("agent") so the CLI surfaces an actionable
	// message.
	a := codex.New(dirs.Dirs{AgentsHome: t.TempDir()})
	ag := &resource.Agent{Name: "x", Description: "d", Body: "b"}
	_, err := a.Plan(ag, adapter.ScopeUser)
	if err == nil {
		t.Fatal("expected error when planning an agent on codex (kind unsupported), got nil")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("error must name the host (codex); got %v", err)
	}
	if !strings.Contains(err.Error(), "agent") {
		t.Errorf("error must name the unsupported kind (agent); got %v", err)
	}
}
