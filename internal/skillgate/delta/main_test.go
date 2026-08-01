package delta

import (
	"context"
	"os"
	"testing"

	"github.com/ellarock-software/dotpack/internal/skillscanner"
)

// TestMain guards the whole package against reaching a real detector.
//
// Both belts are deliberate. The seam makes Run cheap; the environment
// variable makes any code path that bypasses the seam fail loudly rather
// than silently pip-installing from PyPI, which is how this suite first
// went from milliseconds to a minute per run.
func TestMain(m *testing.M) {
	if err := os.Setenv(skillscanner.NoProvisionEnv, "1"); err != nil {
		panic(err)
	}
	ensureRuntime = func(context.Context, string) (skillscanner.Runtime, error) {
		return fakeRuntime(), nil
	}
	os.Exit(m.Run())
}
