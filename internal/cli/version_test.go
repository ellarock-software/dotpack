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
