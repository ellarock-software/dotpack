package main

import (
	"fmt"
	"os"

	"github.com/ellarock/dotpack/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "dotpack:", err)
		os.Exit(1)
	}
}
