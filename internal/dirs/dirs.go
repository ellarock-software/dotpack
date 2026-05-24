// Package dirs centralises the user's filesystem layout that dotpack
// reads from and writes to: where Claude Code stores its config (where
// the claude-code adapter drops skills), and where dotpack persists its
// own manifest. Callers MUST receive a Dirs value from main(); call
// sites must not call os.UserHomeDir() directly. This is the testability
// seam — tests construct a Dirs pointing at t.TempDir() so they never
// touch the real ~/.claude/ or ~/.dotpack/.
package dirs

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dirs holds the resolved filesystem roots dotpack operates against.
type Dirs struct {
	// ClaudeHome is the root of Claude Code's user config, e.g.
	// ~/.claude. The claude-code adapter writes user-scope skills to
	// ClaudeHome/skills/<name>/SKILL.md (per ADR-0009).
	ClaudeHome string

	// DotpackHome is the root of dotpack's own state, e.g. ~/.dotpack.
	// The manifest store writes installs.yaml here (per ADR-0008).
	DotpackHome string
}

// FromEnv resolves Dirs from the user's environment, with overrides for
// tests: DOTPACK_CLAUDE_HOME / DOTPACK_DOTPACK_HOME (the second of those
// is deliberately verbose so it's never confused with PATH-like things).
// Returns an error if HOME cannot be resolved AND no overrides are set.
func FromEnv() (Dirs, error) {
	d := Dirs{
		ClaudeHome:  os.Getenv("DOTPACK_CLAUDE_HOME"),
		DotpackHome: os.Getenv("DOTPACK_DOTPACK_HOME"),
	}
	if d.ClaudeHome != "" && d.DotpackHome != "" {
		return d, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, fmt.Errorf("resolve $HOME: %w", err)
	}
	if d.ClaudeHome == "" {
		d.ClaudeHome = filepath.Join(home, ".claude")
	}
	if d.DotpackHome == "" {
		d.DotpackHome = filepath.Join(home, ".dotpack")
	}
	return d, nil
}
