package cli

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleEnsureBinaryEnvAndInstallerErrorBranches(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tool")
	mustWriteTestFile(t, bin, "#!/bin/sh\n")
	t.Setenv("TOOL_BIN", bin)
	if got, err := ensureLifecycleBinary(lifecycleBinary{Name: "tool", Env: "TOOL_BIN"}); err != nil || got != bin {
		t.Fatalf("ensure env binary = %q,%v; want %q", got, err, bin)
	}
	t.Setenv("TOOL_BIN", filepath.Join(t.TempDir(), "missing"))
	if _, err := ensureLifecycleBinary(lifecycleBinary{Name: "tool", Env: "TOOL_BIN"}); err == nil || !strings.Contains(err.Error(), "TOOL_BIN") {
		t.Fatalf("ensure missing env err=%v; want TOOL_BIN", err)
	}
	if _, err := ensureLifecycleBinary(lifecycleBinary{}); err == nil || !strings.Contains(err.Error(), "binary name") {
		t.Fatalf("ensure no name err=%v", err)
	}

	runner := &fakeCommandRunner{
		lookPathResults: map[string][]lookPathResult{
			"tool":      {{err: exec.ErrNotFound}},
			"installer": {{path: "/bin/installer"}, {path: "/bin/installer"}},
		},
		runErrs: map[string]error{"/bin/installer install": errors.New("install failed")},
	}
	withFakeLifecycleRunner(t, runner)
	if _, err := ensureLifecycleBinary(lifecycleBinary{Name: "tool", Install: lifecycleInstaller{Candidates: []lifecycleCommand{{Command: "missing"}, {Command: "installer", Args: []string{"install"}}}}}); err == nil || !strings.Contains(err.Error(), "no installer candidate succeeded") {
		t.Fatalf("ensure installer failures err=%v", err)
	}

	runner = &fakeCommandRunner{lookPathResults: map[string][]lookPathResult{"tool": {{err: errors.New("permission denied")}}}}
	withFakeLifecycleRunner(t, runner)
	if _, err := ensureLifecycleBinary(lifecycleBinary{Name: "tool"}); err == nil || !strings.Contains(err.Error(), "find tool") {
		t.Fatalf("ensure LookPath non-notfound err=%v", err)
	}
}
