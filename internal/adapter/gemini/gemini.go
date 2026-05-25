// Package gemini wires the gemini-cli per-host Policy into the deep
// filedrop adapter. The implementation (canPassThrough,
// encode/reencode for skill + agent, layout-driven path resolution,
// addScalar atomicity) lives in internal/adapter/filedrop per
// architecture review card #1; this package's job is to declare the
// host-specific data and expose New(d).
//
// Skill paths: <GeminiHome>/skills/<name>/SKILL.md (user) or
// <ProjectHome>/.gemini/skills/<name>/SKILL.md (project).
// Agent paths: <GeminiHome>/agents/<name>.md (user) or
// <ProjectHome>/.gemini/agents/<name>.md (project). Skills nest;
// agents are flat.
//
// Convergence note: per schema/skill.yaml ecosystem_notes, gemini-cli
// ALSO reads the shared `~/.agents/skills/` path for the skill kind.
// Codex WRITES to that shared path; gemini-cli deliberately writes to
// its host-specific GeminiHome/skills/ instead so `--agent gemini-cli`
// and `--agent codex` produce distinct manifest-tracked targets. The
// gemini-cli runtime still picks up the codex-installed skill via its
// read-side convergence. The future `--agent agents-cli` umbrella
// flag (ADR-0016 §1) will special-case writing to ~/.agents/skills/
// once for both hosts.
package gemini

import (
	"fmt"

	"github.com/ellarock/dotpack/internal/adapter/filedrop"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
)

// hostID is the dotpack adapter HostID for gemini-cli. MUST match the
// `host:` strings in schema/*.yaml aliases — schema.HostKeepsExtension
// compares on string equality.
const hostID = "gemini-cli"

// userRoot returns GeminiHome with the host-specific missing-dir error.
func userRoot(d dirs.Dirs) (string, error) {
	if d.GeminiHome == "" {
		return "", fmt.Errorf("gemini-cli: user scope requires dirs.GeminiHome to be set")
	}
	return d.GeminiHome, nil
}

// Policy is the gemini-cli per-host data the filedrop module dispatches
// on. Tools shape is YAML array (Gemini convention) — the inverse of
// claudecode's comma-string coercion.
var Policy = filedrop.Policy{
	HostID: hostID,
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot:      userRoot,
			ProjectSubdir: ".gemini",
			KindDir:       "skills",
			Nested:        true,
			NestedFile:    "SKILL.md",
		},
		resource.KindAgent: {
			UserRoot:      userRoot,
			ProjectSubdir: ".gemini",
			KindDir:       "agents",
			Nested:        false,
		},
	},
	AgentToolsShape: filedrop.ToolsYAMLArray,
}

// New constructs the gemini-cli adapter, wiring the package-level
// Policy with the given Dirs.
func New(d dirs.Dirs) *filedrop.Adapter { return filedrop.New(d, Policy) }
