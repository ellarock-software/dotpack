package cli

import "github.com/spf13/cobra"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "dotpack",
		Short:         "Package manager for AI-agent resources",
		Long:          "dotpack installs schema-and-template-conformant resources (skills, agents, commands, memories, hooks, mcp-servers) into agent hosts via per-host adapters.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}
