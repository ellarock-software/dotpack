package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/adapter/registry"
	skillgateregistry "github.com/ellarock-software/dotpack/internal/skillgate/registry"

	// Registers every shipped skill gate (ADR-0016). One blank import;
	// adding a gate touches no CLI core.
	_ "github.com/ellarock-software/dotpack/internal/skillgate/all"
)

// init validates the skill-gate registry once every gate package's own
// init() has run. Go initialises imported packages first, so by the time
// this executes the registry is fully populated. A registry without its
// default gate is a build-time mistake and must fail at process start,
// not at the first install.
func init() {
	if err := skillgateregistry.Validate(); err != nil {
		panic(err)
	}
}

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
materialize a full canonical .agents tree.

Skill-bearing workflows run a mandatory security gate before dotpack
reads or materializes any skill. The default gate is %q, which gates on
CHANGE: a package is approved at a reviewed state with
"dotpack approve-skill", and only findings that are NEW since that
approval block. Registered gates: %s. Select one with --skill-gate or
$DOTPACK_SKILL_GATE; dotpack never reads gate selection from the package
being installed. Skill-bearing commands also accept the explicit,
repeatable --skill-bypass-security <name> flag for invocation-local
exceptions.

Use scan-skills / baseline-skills to inspect or manage the SkillSpector
scan surface directly. Use import to convert a native host tree into
.agents; import currently supports Claude Code input.`,
			shippedAdaptersLine(), skillgateregistry.DefaultName(),
			strings.Join(skillgateregistry.Names(), ", ")),
		Example: `  dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent antigravity-cli --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent agents-cli --scope project
  dotpack scan-skills .agents --baseline-dir ./.dotpack/skillspector/baselines
  dotpack scan-skills .agents --skill-bypass-security legacy-skill
  dotpack import claude-code . --out .`,
		SilenceUsage:  true,
		SilenceErrors: true,

		// Capture the operator's gate selection before any subcommand
		// runs. PersistentPreRunE is inherited, so every subcommand gets
		// it, and the value is resolved from the flag and the environment
		// only -- never from a source tree.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return resolveSelectedSkillGate(cmd)
		},
	}
	addSkillGateFlag(root)
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
	root.AddCommand(newApproveSkillCmd())
	return root
}
