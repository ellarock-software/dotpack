package cli

import (
	"os"
	"testing"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillscanner"
)

func TestMain(m *testing.M) {
	orig := runPostInstallLifecycle
	origSkillScan := mandatorySkillScan
	runPostInstallLifecycle = func(string) error { return nil }
	mandatorySkillScan = func(string, skillScanSelection, dirs.Dirs) error { return nil }

	// Belt and braces against a test reaching a real detector. The stub
	// above covers the funnel; this covers anything that bypasses it, so
	// a test that would otherwise pip-install from PyPI fails loudly
	// instead of quietly adding a network dependency to the test job.
	if err := os.Setenv(skillscanner.NoProvisionEnv, "1"); err != nil {
		panic(err)
	}

	code := m.Run()
	runPostInstallLifecycle = orig
	mandatorySkillScan = origSkillScan
	os.Exit(code)
}

// useSkillGate pins the gate for one test. Tests that exercise a
// specific gate's behaviour must say which gate they mean rather than
// relying on the default, which is a product decision that can change.
//
// Both the environment variable and the package variable are set. The
// environment covers tests that execute the root command, whose
// PersistentPreRunE re-resolves the selection and would otherwise
// overwrite the package variable; the package variable covers tests that
// call the funnel directly and never run that hook.
func useSkillGate(t *testing.T, name string) {
	t.Helper()
	t.Setenv(skillGateEnv, name)
	prev := selectedSkillGate
	selectedSkillGate = name
	t.Cleanup(func() { selectedSkillGate = prev })
}
