package dirs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock/dotpack/internal/dirs"
)

func TestFromEnv_ProjectHome_FromEnvOverride(t *testing.T) {
	// DOTPACK_PROJECT_HOME is the test override + the explicit knob for
	// agents-cli fan-out / future MCP-driven flows. When set, FromEnv
	// uses it verbatim — no CWD fallback.
	wantProject := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", wantProject)

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if d.ProjectHome != wantProject {
		t.Errorf("ProjectHome: got %q, want %q", d.ProjectHome, wantProject)
	}
}

func TestFromEnv_ProjectHome_FallsBackToCWD(t *testing.T) {
	// When DOTPACK_PROJECT_HOME is unset, FromEnv resolves ProjectHome
	// from os.Getwd(). This is the default production path: the user
	// runs `dotpack install foo.md --scope project` from the project
	// root, and ProjectHome === CWD.
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	os.Unsetenv("DOTPACK_PROJECT_HOME")

	wantCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	// On macOS /tmp is a symlink to /private/tmp; Getwd canonicalises
	// but other surfaces don't. Use filepath.Clean + matching path
	// canonicalisation on both sides.
	if filepath.Clean(d.ProjectHome) != filepath.Clean(wantCWD) {
		t.Errorf("ProjectHome: got %q, want %q (CWD fallback)", d.ProjectHome, wantCWD)
	}
}

func TestFromEnv_ProjectHome_RelativeEnvIsResolvedToAbsolute(t *testing.T) {
	// Hostile-review #1: DOTPACK_PROJECT_HOME accepting a relative path
	// silently defeats slice 2 task #2's "manifest paths are absolute"
	// invariant. Relative env values are normalised to absolute via
	// filepath.Abs (resolved against CWD at FromEnv time).
	wantParent := t.TempDir()
	subdir := "myproj"
	if err := os.Mkdir(filepath.Join(wantParent, subdir), 0o755); err != nil {
		t.Fatalf("setup Mkdir: %v", err)
	}
	t.Chdir(wantParent)
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", subdir) // relative!

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.ProjectHome) {
		t.Errorf("ProjectHome must be absolute after FromEnv normalises env input; got %q", d.ProjectHome)
	}
	wantAbs := filepath.Join(wantParent, subdir)
	if filepath.Clean(d.ProjectHome) != filepath.Clean(wantAbs) {
		t.Errorf("ProjectHome: got %q, want %q (relative env resolved against CWD)", d.ProjectHome, wantAbs)
	}
}

func TestFromEnv_ProjectHome_NonexistentEnvErrors(t *testing.T) {
	// Hostile-review #4: DOTPACK_PROJECT_HOME=/typo/path silently caused
	// MkdirAll to create the typo'd dir tree at install time. Fail loudly
	// at FromEnv: env-provided ProjectHome MUST exist as a directory.
	// CWD-fallback path is exempt — Getwd by definition returns an
	// existing directory.
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", filepath.Join(t.TempDir(), "does", "not", "exist"))

	_, err := dirs.FromEnv()
	if err == nil {
		t.Fatal("expected FromEnv to error on nonexistent DOTPACK_PROJECT_HOME, got nil")
	}
	if !strings.Contains(err.Error(), "DOTPACK_PROJECT_HOME") {
		t.Errorf("error should name the offending env var; got %v", err)
	}
}

func TestFromEnv_ClaudeHome_RelativeEnvIsResolvedToAbsolute(t *testing.T) {
	// Slice 3 hostile-review (#5): the relative-env fix on
	// DOTPACK_PROJECT_HOME (commit 42ec230 #1) never propagated to its
	// siblings. A relative DOTPACK_CLAUDE_HOME silently breaks across
	// chdir — install writes to ./skills/foo, then `dotpack list` from
	// a different CWD looks at a different path. Symmetric fix:
	// filepath.Abs at FromEnv. Existence not required here (install
	// MkdirAll's the tree on first use).
	wantParent := t.TempDir()
	rel := "claude-cfg"
	t.Chdir(wantParent)
	t.Setenv("DOTPACK_CLAUDE_HOME", rel)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.ClaudeHome) {
		t.Errorf("ClaudeHome must be absolute after FromEnv normalises env input; got %q", d.ClaudeHome)
	}
	wantAbs := filepath.Join(wantParent, rel)
	if filepath.Clean(d.ClaudeHome) != filepath.Clean(wantAbs) {
		t.Errorf("ClaudeHome: got %q, want %q (relative env resolved against CWD)", d.ClaudeHome, wantAbs)
	}
}

func TestFromEnv_GeminiHome_RelativeEnvIsResolvedToAbsolute(t *testing.T) {
	// Mirror of TestFromEnv_ClaudeHome_RelativeEnvIsResolvedToAbsolute
	// for DOTPACK_GEMINI_HOME (slice 3 task #7 — second adapter `gemini`).
	// Same class of bug: a relative env value silently breaks across
	// chdir. FromEnv must filepath.Abs.
	wantParent := t.TempDir()
	rel := "gemini-cfg"
	t.Chdir(wantParent)
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", rel)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.GeminiHome) {
		t.Errorf("GeminiHome must be absolute after FromEnv normalises env input; got %q", d.GeminiHome)
	}
	wantAbs := filepath.Join(wantParent, rel)
	if filepath.Clean(d.GeminiHome) != filepath.Clean(wantAbs) {
		t.Errorf("GeminiHome: got %q, want %q", d.GeminiHome, wantAbs)
	}
}

func TestFromEnv_GeminiHome_NonexistentEnvDoesNotError(t *testing.T) {
	// Same write-target tolerance as ClaudeHome — gemini-cli adapter
	// MkdirAll's the tree on first install.
	parent := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", filepath.Join(parent, "does-not-exist-yet"))
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.GeminiHome) {
		t.Errorf("GeminiHome must be absolute; got %q", d.GeminiHome)
	}
}

func TestFromEnv_AntigravityHome_RelativeEnvIsResolvedToAbsolute(t *testing.T) {
	wantParent := t.TempDir()
	rel := "antigravity-cfg"
	t.Chdir(wantParent)
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_ANTIGRAVITY_HOME", rel)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.AntigravityHome) {
		t.Errorf("AntigravityHome must be absolute after FromEnv normalises env input; got %q", d.AntigravityHome)
	}
	wantAbs := filepath.Join(wantParent, rel)
	if filepath.Clean(d.AntigravityHome) != filepath.Clean(wantAbs) {
		t.Errorf("AntigravityHome: got %q, want %q", d.AntigravityHome, wantAbs)
	}
}

func TestFromEnv_AntigravityHome_NonexistentEnvDoesNotError(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_ANTIGRAVITY_HOME", filepath.Join(parent, "does-not-exist-yet"))
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.AntigravityHome) {
		t.Errorf("AntigravityHome must be absolute; got %q", d.AntigravityHome)
	}
}

func TestFromEnv_AgentsHome_RelativeEnvIsResolvedToAbsolute(t *testing.T) {
	// Mirror of TestFromEnv_GeminiHome_RelativeEnvIsResolvedToAbsolute
	// for DOTPACK_AGENTS_HOME (slice 3 task #8 — third adapter `codex`).
	// Codex's only documented native skill path is ~/.agents/skills/
	// (per developers.openai.com/codex/skills), so the codex adapter
	// targets <AgentsHome>/skills/<name>/ for user scope. AgentsHome is
	// shared infrastructure: agents-cli umbrella (ADR-0012 §1) will
	// eventually special-case the same root for write-once convergence.
	// Same class of bug as Claude/Gemini: relative env values silently
	// break across chdir.
	wantParent := t.TempDir()
	rel := "agents-cfg"
	t.Chdir(wantParent)
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", rel)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.AgentsHome) {
		t.Errorf("AgentsHome must be absolute after FromEnv normalises env input; got %q", d.AgentsHome)
	}
	wantAbs := filepath.Join(wantParent, rel)
	if filepath.Clean(d.AgentsHome) != filepath.Clean(wantAbs) {
		t.Errorf("AgentsHome: got %q, want %q", d.AgentsHome, wantAbs)
	}
}

func TestFromEnv_AgentsHome_NonexistentEnvDoesNotError(t *testing.T) {
	// Same write-target tolerance as ClaudeHome / GeminiHome — codex
	// adapter MkdirAll's the .agents/skills/<name>/ tree on first
	// install. Existence at FromEnv time is not required.
	parent := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", filepath.Join(parent, "does-not-exist-yet"))
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.AgentsHome) {
		t.Errorf("AgentsHome must be absolute; got %q", d.AgentsHome)
	}
}

func TestFromEnv_CodexHome_RelativeEnvIsResolvedToAbsolute(t *testing.T) {
	// Mirror of the Agents/Gemini/Claude relative-env fixes for
	// DOTPACK_CODEX_HOME (codex mcp-server slice). CodexHome is distinct
	// from AgentsHome: skills converge at AgentsHome/skills/ (cross-host
	// root), but config.toml is codex-specific and lives at CodexHome.
	// Same class of bug: relative env values silently break across chdir.
	wantParent := t.TempDir()
	rel := "codex-cfg"
	t.Chdir(wantParent)
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", rel)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.CodexHome) {
		t.Errorf("CodexHome must be absolute after FromEnv normalises env input; got %q", d.CodexHome)
	}
	wantAbs := filepath.Join(wantParent, rel)
	if filepath.Clean(d.CodexHome) != filepath.Clean(wantAbs) {
		t.Errorf("CodexHome: got %q, want %q", d.CodexHome, wantAbs)
	}
}

func TestFromEnv_CodexHome_NonexistentEnvDoesNotError(t *testing.T) {
	// Write-target tolerance: codex mcp-server install MkdirAll's
	// CodexHome on first use, so FromEnv must accept a not-yet-existing
	// path. Existence at FromEnv time is not required.
	parent := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", filepath.Join(parent, "does-not-exist-yet"))
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.CodexHome) {
		t.Errorf("CodexHome must be absolute; got %q", d.CodexHome)
	}
}

func TestFromEnv_DotpackHome_RelativeEnvIsResolvedToAbsolute(t *testing.T) {
	// Same class as the ClaudeHome relative-env fix: a relative
	// DOTPACK_DOTPACK_HOME silently breaks list/uninstall after chdir,
	// because the manifest file path is composed at command-invocation
	// time (not install time). Slice 3's `dotpack list` is the first
	// caller where this actively bites.
	wantParent := t.TempDir()
	rel := "dotpack-state"
	t.Chdir(wantParent)
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", rel)

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.DotpackHome) {
		t.Errorf("DotpackHome must be absolute after FromEnv normalises env input; got %q", d.DotpackHome)
	}
	wantAbs := filepath.Join(wantParent, rel)
	if filepath.Clean(d.DotpackHome) != filepath.Clean(wantAbs) {
		t.Errorf("DotpackHome: got %q, want %q (relative env resolved against CWD)", d.DotpackHome, wantAbs)
	}
}

func TestFromEnv_ClaudeHome_NonexistentEnvDoesNotError(t *testing.T) {
	// Unlike DOTPACK_PROJECT_HOME (which MUST exist — we read from it),
	// ClaudeHome / DotpackHome are dotpack-managed write targets. Install
	// MkdirAll's the tree on first use, so FromEnv must tolerate a path
	// that doesn't exist yet — otherwise first-install on a fresh box
	// fails with "no such file" before it ever gets to do the mkdir.
	// Normalise to absolute, don't stat.
	parent := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", filepath.Join(parent, "does-not-exist-yet"))
	t.Setenv("DOTPACK_DOTPACK_HOME", filepath.Join(parent, "also-not-yet"))

	d, err := dirs.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !filepath.IsAbs(d.ClaudeHome) {
		t.Errorf("ClaudeHome must be absolute; got %q", d.ClaudeHome)
	}
	if !filepath.IsAbs(d.DotpackHome) {
		t.Errorf("DotpackHome must be absolute; got %q", d.DotpackHome)
	}
}

func TestFromEnv_ProjectHome_FileInsteadOfDirErrors(t *testing.T) {
	// Hostile-review #4 (partner case): if the path EXISTS but is a
	// regular file (not a directory), FromEnv must still refuse rather
	// than letting writeAtomic produce a confusing "ENOTDIR" later.
	tmp := t.TempDir()
	bogus := filepath.Join(tmp, "i-am-a-file")
	if err := os.WriteFile(bogus, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", bogus)

	_, err := dirs.FromEnv()
	if err == nil {
		t.Fatal("expected FromEnv to error when DOTPACK_PROJECT_HOME points at a file, got nil")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error should mention 'directory'; got %v", err)
	}
}
