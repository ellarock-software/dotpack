package cli

import "github.com/spf13/cobra"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dotpack",
		Short: "Package manager and translator for AI-agent resources",
		Long: `dotpack validates portable AI-agent resources and materializes them
into host-native files.

The main direction is .agents -> host:
  - Claude Code: .claude/skills, .claude/agents, .claude/settings.json, .mcp.json
  - Gemini CLI:  .gemini/skills, .gemini/agents, .gemini/settings.json
  - Codex CLI:   .agents/skills for skills, .codex/config.toml for config
  - agents-cli:  an umbrella target for Gemini CLI + Codex

Use install to translate a resource into a host. Use import to convert a
native host tree into .agents; import currently supports Claude Code input.`,
		Example: `  dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent gemini-cli --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent codex --scope project
  dotpack import claude-code . --out .`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newInstallCmd())
	root.AddCommand(newImportCmd())
	root.AddCommand(newUninstallCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newReconcileCmd())
	root.AddCommand(newPruneCmd())
	return root
}
