package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootWithNoArgsShowsUsage(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("dotpack with no args returned error: %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "Usage:") {
		t.Errorf("dotpack usage output missing 'Usage:' header: %q", got)
	}
	if !strings.Contains(got, "version") {
		t.Errorf("dotpack usage missing 'version' subcommand: %q", got)
	}
	if !strings.Contains(got, "agent") {
		t.Errorf("dotpack usage missing domain tagline (expected 'agent'): %q", got)
	}
}
