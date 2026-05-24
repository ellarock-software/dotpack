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
