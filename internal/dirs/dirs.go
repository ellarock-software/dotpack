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

	// ProjectHome is the root of the user's current project, used by
	// scope=project installs (e.g. ProjectHome/.claude/skills/<name>/
	// for claude-code). Resolved at FromEnv time from
	// DOTPACK_PROJECT_HOME or os.Getwd(), so the install plan's paths
	// are absolute and survive uninstall/list invocations from a
	// different CWD.
	ProjectHome string
}

// FromEnv resolves Dirs from the user's environment, with overrides for
// tests: DOTPACK_CLAUDE_HOME / DOTPACK_DOTPACK_HOME / DOTPACK_PROJECT_HOME
// (the *_HOME suffixes are deliberately verbose so they're never confused
// with PATH-like things). Returns an error if HOME cannot be resolved AND
// no overrides are set.
//
// ProjectHome rules (slice 2 task #2 hardening, post hostile-review):
//   - DOTPACK_PROJECT_HOME, when set, is normalised to an absolute path
//     (filepath.Abs against CWD) and MUST exist as a directory. Relative
//     or nonexistent values silently defeated the "manifest paths are
//     absolute" invariant when accepted verbatim.
//   - When unset, falls back to os.Getwd(). Getwd failure is tolerated
//     here (ProjectHome left empty) so user-scope installs from a
//     deleted CWD still succeed; ScopeProject installs error at the
//     adapter when they actually need the value.
func FromEnv() (Dirs, error) {
	d := Dirs{
		ClaudeHome:  os.Getenv("DOTPACK_CLAUDE_HOME"),
		DotpackHome: os.Getenv("DOTPACK_DOTPACK_HOME"),
		ProjectHome: os.Getenv("DOTPACK_PROJECT_HOME"),
	}

	if d.ClaudeHome == "" || d.DotpackHome == "" {
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
	}

	if d.ProjectHome != "" {
		abs, err := filepath.Abs(d.ProjectHome)
		if err != nil {
			return Dirs{}, fmt.Errorf("DOTPACK_PROJECT_HOME=%q: resolve abs: %w", d.ProjectHome, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return Dirs{}, fmt.Errorf("DOTPACK_PROJECT_HOME=%q: %w", d.ProjectHome, err)
		}
		if !info.IsDir() {
			return Dirs{}, fmt.Errorf("DOTPACK_PROJECT_HOME=%q: not a directory", d.ProjectHome)
		}
		d.ProjectHome = abs
	} else if cwd, err := os.Getwd(); err == nil {
		d.ProjectHome = cwd
	}
	return d, nil
}
