package main

import (
	"os"
	"testing"
)

func TestMainRunsVersionCommand(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"dotpack", "version"}
	t.Cleanup(func() { os.Args = oldArgs })

	main()
}
