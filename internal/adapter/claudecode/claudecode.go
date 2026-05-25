// Package claudecode wires the claude-code per-host Policy into the
// deep filedrop adapter. The implementation (canPassThrough,
// encode/reencode for skill + agent, layout-driven path resolution,
// addScalar atomicity) lives in internal/adapter/filedrop per
// architecture review card #1; this package's job is to declare the
// host-specific data and expose New(d).
//
// Skill paths: <ClaudeHome>/skills/<name>/SKILL.md (user) or
// <ProjectHome>/.claude/skills/<name>/SKILL.md (project).
// Agent paths: <ClaudeHome>/agents/<name>.md (user) or
// <ProjectHome>/.claude/agents/<name>.md (project). Skills nest (own
// the per-name subdir); agents are flat (shared dir, not reclaimed by
// uninstall).
package claudecode

import (
	"fmt"

	"github.com/ellarock/dotpack/internal/adapter/filedrop"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
)

// hostID is the dotpack adapter HostID for claude-code. MUST match
// the `host:` strings in schema/*.yaml aliases — schema.HostKeepsExtension
// compares on string equality, so a mismatch silently flips claude-code's
// native concepts to lossy on its own adapter.
const hostID = "claude-code"

// userRoot returns ClaudeHome with the host-specific missing-dir error.
// Shared by the skill + agent Layouts so the message format is one
// string in one place.
func userRoot(d dirs.Dirs) (string, error) {
	if d.ClaudeHome == "" {
		return "", fmt.Errorf("claude-code: user scope requires dirs.ClaudeHome to be set")
	}
	return d.ClaudeHome, nil
}

// Policy is the claude-code per-host data the filedrop module dispatches
// on. Exported (not unexported `policy`) so future cross-adapter
// machinery — agents-cli umbrella (ADR-0016 §1), batch capability
// queries — can read it without importing through New(d).
var Policy = filedrop.Policy{
	HostID: hostID,
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot:      userRoot,
			ProjectSubdir: ".claude",
			KindDir:       "skills",
			Nested:        true,
			NestedFile:    "SKILL.md",
		},
		resource.KindAgent: {
			UserRoot:      userRoot,
			ProjectSubdir: ".claude",
			KindDir:       "agents",
			Nested:        false,
		},
	},
	AgentToolsShape: filedrop.ToolsCommaString,
}

// New constructs the claude-code adapter, wiring the package-level
// Policy with the given Dirs. Returns the filedrop adapter directly —
// callers route through adapter.Adapter, so the concrete type is
// transparent.
func New(d dirs.Dirs) *filedrop.Adapter { return filedrop.New(d, Policy) }
