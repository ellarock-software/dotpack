package cli

import (
	"os"
	"testing"

	"github.com/ellarock-software/dotpack/internal/dirs"
)

func TestMain(m *testing.M) {
	orig := runPostInstallLifecycle
	origSkillScan := mandatorySkillScan
	runPostInstallLifecycle = func(string) error { return nil }
	mandatorySkillScan = func(string, skillScanSelection, dirs.Dirs) error { return nil }
	code := m.Run()
	runPostInstallLifecycle = orig
	mandatorySkillScan = origSkillScan
	os.Exit(code)
}
