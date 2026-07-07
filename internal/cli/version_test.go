package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandPrintsVersion(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("dotpack version returned error: %v", err)
	}

	got := stdout.String()
	if got == "" {
		t.Fatal("dotpack version produced no output")
	}
	if !strings.Contains(got, "dotpack") {
		t.Errorf("dotpack version output missing tool name: %q", got)
	}
	if !strings.Contains(got, "dev") {
		t.Errorf("dotpack version output missing default version token 'dev': %q", got)
	}
}

func TestResolveVersionUsesLdflagsValueWhenSet(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = "0.1.0"
	if got := resolveVersion(); got != "0.1.0" {
		t.Errorf("resolveVersion() = %q, want %q", got, "0.1.0")
	}
}

func TestResolveVersionFallsBackToDev(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	// Under `go test` the main module build info is empty or "(devel)", so the
	// resolver must fall back to the "dev" sentinel rather than leaking it.
	Version = "dev"
	if got := resolveVersion(); got != "dev" {
		t.Errorf("resolveVersion() = %q, want %q", got, "dev")
	}
}
