package skillgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSeverityAtLeastOrdersTheKnownScale(t *testing.T) {
	cases := []struct {
		have, floor string
		want        bool
	}{
		{"CRITICAL", "HIGH", true},
		{"HIGH", "HIGH", true},
		{"MEDIUM", "HIGH", false},
		{"LOW", "HIGH", false},
		{"INFO", "HIGH", false},
		{"INFO", "INFO", true},
		{"critical", "high", true},
		{"  High  ", "HIGH", true},
	}
	for _, tc := range cases {
		if got := SeverityAtLeast(tc.have, tc.floor); got != tc.want {
			t.Errorf("SeverityAtLeast(%q, %q) = %v, want %v", tc.have, tc.floor, got, tc.want)
		}
	}
}

// An unrecognised severity must never block. The detector's own enum
// includes SAFE, which is explicitly not a finding, and a scanner
// upgrade that invents a label must not start blocking installs on it.
func TestSeverityAtLeastTreatsUnknownAsNeverBlocking(t *testing.T) {
	for _, unknown := range []string{"SAFE", "", "SEV0", "BLOCKER", "none"} {
		if SeverityAtLeast(unknown, "HIGH") {
			t.Errorf("SeverityAtLeast(%q, \"HIGH\") = true, want false: unknown severities must not block", unknown)
		}
	}
	if SeverityAtLeast("CRITICAL", "NOT_A_SEVERITY") {
		t.Error("an unknown floor must not admit anything")
	}
}

func TestSanitizeCommandProducesASafePathSegment(t *testing.T) {
	cases := map[string]string{
		"install":           "install",
		"install-all":       "install-all",
		"sync_back":         "sync_back",
		"sync back":         "sync-back",
		"../../etc/passwd":  "etc-passwd",
		"/absolute/path":    "absolute-path",
		"":                  "skill-gate",
		"///":               "skill-gate",
		"drop;rm -rf ~":     "drop-rm--rf",
		"Install-All-v2":    "Install-All-v2",
		"trailing-dashes--": "trailing-dashes",
	}
	for in, want := range cases {
		if got := SanitizeCommand(in); got != want {
			t.Errorf("SanitizeCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

// A command name is interpolated into a filesystem path, so it must
// never be able to escape the run root.
func TestSanitizeCommandCannotTraverse(t *testing.T) {
	for _, hostile := range []string{"../..", "a/../../b", `..\..\win`, "./."} {
		got := SanitizeCommand(hostile)
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Errorf("SanitizeCommand(%q) = %q, which can still traverse", hostile, got)
		}
	}
}

func TestTimestampSlugIsUTCAndSortable(t *testing.T) {
	// A non-UTC input must still render as UTC, or run directories from
	// machines in different zones sort incoherently.
	loc := time.FixedZone("UTC+5", 5*60*60)
	got := TimestampSlug(time.Date(2026, 8, 1, 5, 0, 0, 0, loc))
	if want := "20260801T000000Z"; got != want {
		t.Fatalf("TimestampSlug = %q, want %q", got, want)
	}
}

func TestRunDirIsCreatedUnderTheGatesOwnRoot(t *testing.T) {
	home := t.TempDir()
	dir, err := RunDir(home, "skillgate", "install")
	if err != nil {
		t.Fatalf("RunDir: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("run directory %s was not created: %v", dir, err)
	}
	wantRoot := filepath.Join(home, "skillgate", "runs")
	if !strings.HasPrefix(dir, wantRoot+string(filepath.Separator)) {
		t.Errorf("run directory %s is not under %s", dir, wantRoot)
	}
	if !strings.HasSuffix(dir, "-install") {
		t.Errorf("run directory %s does not carry the command suffix", dir)
	}
}

// Two gates must not share a run root, or one gate's artifacts overwrite
// the other's when both are exercised on the same machine.
func TestRunDirSeparatesGates(t *testing.T) {
	home := t.TempDir()
	a, err := RunDir(home, "skillgate", "install")
	if err != nil {
		t.Fatalf("RunDir(skillgate): %v", err)
	}
	b, err := RunDir(home, "skillspector", "install")
	if err != nil {
		t.Fatalf("RunDir(skillspector): %v", err)
	}
	if filepath.Dir(a) == filepath.Dir(b) {
		t.Errorf("gates share a run root: %s and %s", a, b)
	}
}

func TestRunDirSanitizesTheCommand(t *testing.T) {
	home := t.TempDir()
	dir, err := RunDir(home, "skillgate", "../../escape")
	if err != nil {
		t.Fatalf("RunDir: %v", err)
	}
	wantRoot := filepath.Join(home, "skillgate", "runs")
	if !strings.HasPrefix(filepath.Clean(dir), wantRoot+string(filepath.Separator)) {
		t.Fatalf("run directory %s escaped %s", dir, wantRoot)
	}
}
