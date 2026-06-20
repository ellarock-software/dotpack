package codex_test

import (
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter/codex"
	"github.com/ellarock-software/dotpack/internal/dirs"
)

func TestCodex_HostID(t *testing.T) {
	// Schema (schema/agent.yaml does NOT have codex, but schema/hook.yaml,
	// schema/mcp-server.yaml, schema/memory.yaml all declare aliases with
	// `host: codex`) — the adapter's HostID MUST match those strings
	// verbatim or LossyExtensions will silently flip codex-native
	// concepts to lossy on the codex adapter's own host. schema/schema.go
	// Alias docstring is the load-bearing contract.
	a := codex.New(dirs.Dirs{AgentsHome: t.TempDir()})
	if got := a.HostID(); got != "codex" {
		t.Errorf("HostID: got %q, want %q", got, "codex")
	}
}
