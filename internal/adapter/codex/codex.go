// Package codex wires the codex CLI per-host Policy into the deep
// filedrop adapter. The implementation lives in
// internal/adapter/filedrop per architecture review card #1; this
// package's job is to declare the host-specific data and expose New(d).
//
// Codex supports skill only — there is no native agent loading
// directory documented by the codex CLI. The absence of resource.KindAgent
// from Policy.Layouts is the canonical declaration of that: the
// filedrop module returns "kind agent not yet supported" for any
// Plan(KindAgent) call, and Capabilities() returns Unsupported (via
// CapabilityLevel's iota zero value) without a separate explicit entry.
// Agent support would be added by appending a KindAgent Layout entry
// + setting AgentToolsShape; that requires the codex CLI to document
// a native agent loading directory analogous to .claude/agents/.
//
// Skill paths: <AgentsHome>/skills/<name>/SKILL.md (user) or
// <ProjectHome>/.agents/skills/<name>/SKILL.md (project). Per
// developers.openai.com/codex/skills, AgentsHome is codex's only
// documented native skill root. Gemini CLI ALSO reads ~/.agents/skills/
// as a convergence path, but the gemini-cli adapter writes to its
// host-specific path so `--agent codex` and `--agent gemini-cli` don't
// collide here today. The future `--agent agents-cli` umbrella flag
// (ADR-0016 §1) will write to AgentsHome/skills/ as its write-once
// convergence; collision handling is owned by that umbrella's CLI-flag-
// to-adapter-set special case when it lands.
package codex

import (
	"fmt"

	"github.com/ellarock/dotpack/internal/adapter/filedrop"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
)

// hostID is the dotpack adapter HostID for codex. MUST match the
// `host:` strings in schema/*.yaml aliases — schema.HostKeepsExtension
// compares on string equality.
const hostID = "codex"

// userRoot returns AgentsHome with the host-specific missing-dir error.
// Codex has no CodexHome (~/.codex/skills/ is not documented by OpenAI),
// so a future reader debugging "where does codex write?" sees the actual
// env var they need (DOTPACK_AGENTS_HOME).
func userRoot(d dirs.Dirs) (string, error) {
	if d.AgentsHome == "" {
		return "", fmt.Errorf("codex: user scope requires dirs.AgentsHome to be set")
	}
	return d.AgentsHome, nil
}

// Policy is the codex per-host data the filedrop module dispatches on.
// Skill only — agent is absent from Layouts (data-driven equivalent of
// the old Capabilities matrix's explicit KindAgent: Unsupported entry).
// AgentToolsShape is intentionally left at zero value (ToolsShapeUnused)
// because no agent Layout exists; the filedrop encoder errors if Plan
// is called for KindAgent before that branch is reached.
var Policy = filedrop.Policy{
	HostID: hostID,
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot:      userRoot,
			ProjectSubdir: ".agents",
			KindDir:       "skills",
			Nested:        true,
			NestedFile:    "SKILL.md",
		},
	},
}

// New constructs the codex adapter, wiring the package-level Policy
// with the given Dirs.
func New(d dirs.Dirs) *filedrop.Adapter { return filedrop.New(d, Policy) }
