package antigravity_test

import (
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter/antigravity"
	"github.com/ellarock-software/dotpack/internal/dirs"
)

func TestAntigravity_HostID(t *testing.T) {
	// Schema (schema/agent.yaml, schema/mcp-server.yaml, schema/hook.yaml)
	// declares aliases with `host: antigravity-cli`. The adapter's HostID MUST
	// match the schema string exactly — LossyExtensions matches on equal,
	// so a mismatch silently flips antigravity-native concepts to lossy on
	// their own host. The CLI flag mirrors the HostID for symmetry with
	// `--agent claude-code`.
	a := antigravity.New(dirs.Dirs{AntigravityHome: t.TempDir()})
	if got := a.HostID(); got != "antigravity-cli" {
		t.Errorf("HostID: got %q, want %q", got, "antigravity-cli")
	}
}
