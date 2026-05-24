package cli

import "github.com/spf13/cobra"

var Version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print dotpack version",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("dotpack %s\n", Version)
		},
	}
}
