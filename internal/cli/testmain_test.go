package cli

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	orig := runPostInstallLifecycle
	runPostInstallLifecycle = func(string) error { return nil }
	code := m.Run()
	runPostInstallLifecycle = orig
	os.Exit(code)
}
