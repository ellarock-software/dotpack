package all

import (
	"github.com/ellarock-software/dotpack/internal/adapter/registry"
	"github.com/ellarock-software/dotpack/internal/resource"
)

// init registers the agents-cli umbrella (ADR-0012 §1, ADR-0014). It
// references sub-adapter HostIDs as strings only — no compile dependency
// on the concrete packages — so registration order against the adapter
// init()s does not matter; registry.Validate (called from the CLI init)
// enforces the cross-references after all init()s complete.
//
// Per-kind writers:
//   - Skill: codex writes ~/.agents/skills/<name>/SKILL.md once; gemini-cli
//     also reads that shared path, so a single write converges (write-once).
//   - Agent / mcp-server / hook / rule / command / memory: each sub-adapter
//     owns a DISTINCT host file (e.g. GEMINI.md vs ANTIGRAVITY.md vs
//     AGENTS.md; .gemini/commands/x.toml vs .codex/commands/x.md), so the
//     umbrella fans out — every sub-adapter writes its own.
//
// command + memory were previously unsupported under the umbrella; they
// are now first-class fan-out kinds (ADR-0014). Every sub-adapter already
// supports both via filedrop, and the targets never collide, so the
// existing aggregate-plan / preflight machinery handles them unchanged.
func init() {
	subs := []string{"gemini-cli", "antigravity-cli", "codex"}
	registry.RegisterUmbrella("agents-cli", registry.Umbrella{
		Subs: subs,
		Writers: map[resource.Kind][]string{
			resource.KindSkill:     {"codex"},
			resource.KindAgent:     subs,
			resource.KindMCPServer: subs,
			resource.KindHook:      subs,
			resource.KindRule:      subs,
			resource.KindCommand:   subs,
			resource.KindMemory:    subs,
		},
	})
}
