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

	// GeminiHome is the root of Gemini CLI's user config, e.g.
	// ~/.gemini. The gemini-cli adapter writes user-scope skills to
	// GeminiHome/skills/<name>/SKILL.md and agents to
	// GeminiHome/agents/<name>.md. The ~/.agents/ convergence path
	// (shared with codex) is reserved for the future agents-cli
	// umbrella flag per ADR-0016 §1, not claimed by the per-host
	// adapter.
	GeminiHome string

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
// All three env vars, when set, are normalised to absolute paths
// (filepath.Abs against CWD at FromEnv time). A relative env value
// silently breaks across chdir — install would write into one tree,
// `dotpack list` from a different CWD would look at a different tree.
// Slice 2 task #2 fixed this for DOTPACK_PROJECT_HOME (commit 42ec230);
// slice 3 task #5 extends it to ClaudeHome / DotpackHome, where `list`
// and `uninstall` are the first features to actively bite the relative
// case (slice 1's install ran once per CWD-session, so the bug stayed
// latent).
//
// Existence rules diverge by role:
//   - DOTPACK_PROJECT_HOME MUST exist as a directory — we READ from it
//     (project-scope installs are anchored there). Relative or
//     nonexistent values silently defeated the "manifest paths are
//     absolute" invariant when accepted verbatim. CWD-fallback path is
//     exempt: Getwd by definition returns an existing directory.
//   - DOTPACK_CLAUDE_HOME / DOTPACK_DOTPACK_HOME are dotpack-managed
//     WRITE targets; install MkdirAll's their trees on first use, so
//     FromEnv must tolerate a not-yet-existing path. Normalise to
//     absolute, do not stat.
//
// ProjectHome CWD fallback (hostile-review #5 from 42ec230): when unset,
// falls back to os.Getwd(). Getwd failure is tolerated here (ProjectHome
// left empty) so user-scope installs from a deleted CWD still succeed;
// ScopeProject installs error at the adapter when they actually need
// the value.
func FromEnv() (Dirs, error) {
	d := Dirs{
		ClaudeHome:  os.Getenv("DOTPACK_CLAUDE_HOME"),
		GeminiHome:  os.Getenv("DOTPACK_GEMINI_HOME"),
		DotpackHome: os.Getenv("DOTPACK_DOTPACK_HOME"),
		ProjectHome: os.Getenv("DOTPACK_PROJECT_HOME"),
	}

	if d.ClaudeHome == "" || d.GeminiHome == "" || d.DotpackHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Dirs{}, fmt.Errorf("resolve $HOME: %w", err)
		}
		if d.ClaudeHome == "" {
			d.ClaudeHome = filepath.Join(home, ".claude")
		}
		if d.GeminiHome == "" {
			d.GeminiHome = filepath.Join(home, ".gemini")
		}
		if d.DotpackHome == "" {
			d.DotpackHome = filepath.Join(home, ".dotpack")
		}
	}

	// Normalise write-target env vars to absolute. No existence
	// requirement — install creates these on first use.
	if abs, err := filepath.Abs(d.ClaudeHome); err == nil {
		d.ClaudeHome = abs
	} else {
		return Dirs{}, fmt.Errorf("DOTPACK_CLAUDE_HOME=%q: resolve abs: %w", d.ClaudeHome, err)
	}
	if abs, err := filepath.Abs(d.GeminiHome); err == nil {
		d.GeminiHome = abs
	} else {
		return Dirs{}, fmt.Errorf("DOTPACK_GEMINI_HOME=%q: resolve abs: %w", d.GeminiHome, err)
	}
	if abs, err := filepath.Abs(d.DotpackHome); err == nil {
		d.DotpackHome = abs
	} else {
		return Dirs{}, fmt.Errorf("DOTPACK_DOTPACK_HOME=%q: resolve abs: %w", d.DotpackHome, err)
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
