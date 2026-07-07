package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/adapter/registry"
)

// shippedAdaptersLine renders the currently-registered per-host adapters
// as a comma-separated list, sourced from the registry rather than a
// hard-coded host table (ADR-0014). The product intent is universal
// LLM-tool coverage; this line names the adapters shipped TODAY, so a
// newly onboarded host appears in help automatically.
func shippedAdaptersLine() string {
	return strings.Join(registry.AdapterHostIDs(), ", ")
}

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dotpack",
		Short: "Package manager and translator for AI-agent resources",
		Long: fmt.Sprintf(`dotpack validates portable AI-agent resources and materializes them
into host-native files.

Product intent: universal coverage of LLM coding tools for every
operation (skill, agent, rule, command, memory, mcp-server, hook).
dotpack is built so onboarding a new tool is a self-contained adapter,
not an edit to core switchboards (see ADR-0014 and CONTRIBUTING.md).

Currently shipped adapters: %s
plus the agents-cli umbrella target, which fans out across the
agents-compatible adapters. Per-adapter operation support is intentionally
partial where a host lacks a concept (e.g. opencode has no rule/hook); see
README's support matrix.

The main direction is .agents -> host, e.g.:
  - claude-code: .claude/skills, .claude/agents, .claude/settings.json, .mcp.json
  - codex:       .agents/skills for skills, .codex/config.toml for config
  - hermes:      ~/.hermes/skills for skills, ~/.hermes/config.yaml for config
  - opencode:    .opencode/skills, .opencode/agents, opencode.json for mcp

Use install to translate one resource into a host, or install-all to
materialize a full canonical .agents tree. Skill-bearing workflows run a
mandatory static SkillSpector gate automatically; use scan-skills /
baseline-skills when you want to inspect or manage that same scan surface
directly. Use import to convert a native host tree into .agents; import
currently supports Claude Code input.`, shippedAdaptersLine()),
		Example: `  dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent antigravity-cli --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent agents-cli --scope project
  dotpack scan-skills .agents --baseline-dir ./.dotpack/skillspector/baselines
  dotpack import claude-code . --out .`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newInstallCmd())
	root.AddCommand(newScanSkillsCmd())
	root.AddCommand(newBaselineSkillsCmd())
	root.AddCommand(newImportCmd())
	root.AddCommand(newUninstallCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newReconcileCmd())
	root.AddCommand(newPruneCmd())
	root.AddCommand(newInventoryCmd())
	root.AddCommand(newSyncBackCmd())
	root.AddCommand(newResetMaterializedCmd())
	root.AddCommand(newInstallAllCmd())
	return root
}
