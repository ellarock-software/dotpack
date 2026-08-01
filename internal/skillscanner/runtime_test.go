package skillscanner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeCall struct {
	Dir  string
	Name string
	Args []string
}

type fakeResult struct {
	Stdout string
	Stderr string
	Err    error
	// Delay blocks the call, so a test can exercise a real deadline.
	Delay time.Duration
}

type fakeRunner struct {
	calls    []fakeCall
	results  map[string]fakeResult
	missing  map[string]bool
	fallback fakeResult
}

func (f *fakeRunner) LookPath(file string) (string, error) {
	if f.missing[file] {
		return "", errors.New("not found")
	}
	return "/usr/bin/" + file, nil
}

func (f *fakeRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, string, error) {
	f.calls = append(f.calls, fakeCall{Dir: dir, Name: name, Args: args})
	key := name + " " + strings.Join(args, " ")
	res, ok := f.results[key]
	if !ok {
		res = f.fallback
	}
	if res.Delay > 0 {
		select {
		case <-time.After(res.Delay):
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	return res.Stdout, res.Stderr, res.Err
}

func useFakeRunner(t *testing.T, f *fakeRunner) {
	t.Helper()
	prev := runner
	runner = f
	t.Cleanup(func() { runner = prev })
}

// allowProvisioning clears the global no-provision guard for tests that
// deliberately exercise the provisioning path through a fake runner.
func allowProvisioning(t *testing.T) {
	t.Helper()
	t.Setenv(NoProvisionEnv, "")
}

func writeFakeRuntime(t *testing.T, home string, metadata RuntimeMetadata) {
	t.Helper()
	root := filepath.Join(home, "skillgate")
	binDir := filepath.Join(root, "runtime", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"python", binName} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	raw, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtime.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write runtime.json: %v", err)
	}
}

func TestEnsureRuntimeReusesAMatchingRuntimeWithoutShellingOut(t *testing.T) {
	home := t.TempDir()
	writeFakeRuntime(t, home, RuntimeMetadata{
		Package: PackageName, Version: Version,
		InstalledAt: "2026-08-01T00:00:00Z", SelectedPython: "python3.13",
		VersionOutput: binName + " " + Version,
	})
	f := &fakeRunner{}
	useFakeRunner(t, f)

	rt, err := EnsureRuntime(context.Background(), home)
	if err != nil {
		t.Fatalf("EnsureRuntime: %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("cache hit still shelled out: %+v", f.calls)
	}
	if rt.Metadata.Version != Version {
		t.Errorf("metadata version = %q, want %q", rt.Metadata.Version, Version)
	}
	if !strings.HasSuffix(rt.ScannerBin, binName) {
		t.Errorf("ScannerBin = %q, want it to end in %q", rt.ScannerBin, binName)
	}
}

// The whole point of the cache-hit path is that no test and no ordinary
// install touches the network. If the recorded pin differs, dotpack must
// re-provision rather than trust a runtime it did not install.
func TestEnsureRuntimeReprovisionsOnPinMismatch(t *testing.T) {
	allowProvisioning(t)
	home := t.TempDir()
	writeFakeRuntime(t, home, RuntimeMetadata{Package: PackageName, Version: "0.0.1"})

	f := &fakeRunner{fallback: fakeResult{Stdout: binName + " " + Version}}
	useFakeRunner(t, f)

	if _, err := EnsureRuntime(context.Background(), home); err != nil {
		t.Fatalf("EnsureRuntime: %v", err)
	}
	var sawInstall bool
	for _, c := range f.calls {
		if strings.Join(c.Args, " ") == "-m pip install --no-input --disable-pip-version-check "+PackageName+"=="+Version {
			sawInstall = true
		}
	}
	if !sawInstall {
		t.Fatalf("pin mismatch did not re-provision; calls: %+v", f.calls)
	}
}

func TestEnsureRuntimeInstallsThePinnedVersion(t *testing.T) {
	allowProvisioning(t)
	home := t.TempDir()
	f := &fakeRunner{fallback: fakeResult{Stdout: binName + " " + Version}}
	useFakeRunner(t, f)

	if _, err := EnsureRuntime(context.Background(), home); err != nil {
		t.Fatalf("EnsureRuntime: %v", err)
	}

	var installArgs []string
	for _, c := range f.calls {
		if len(c.Args) > 2 && c.Args[0] == "-m" && c.Args[1] == "pip" {
			installArgs = c.Args
		}
	}
	if installArgs == nil {
		t.Fatalf("no pip install call; calls: %+v", f.calls)
	}
	joined := strings.Join(installArgs, " ")
	if !strings.Contains(joined, PackageName+"=="+Version) {
		t.Errorf("pip install did not pin the version: %q", joined)
	}
	// An unpinned or --upgrade install would silently drift the detector
	// and invalidate every baseline fingerprint.
	if strings.Contains(joined, "--upgrade") {
		t.Errorf("pip install uses --upgrade, which defeats the pin: %q", joined)
	}
}

// A wrong console-script name or a resolver that produced a different
// build must fail here, not later as an opaque "no report" block.
func TestEnsureRuntimeRejectsAVersionMismatchFromTheBinary(t *testing.T) {
	allowProvisioning(t)
	home := t.TempDir()
	f := &fakeRunner{fallback: fakeResult{Stdout: binName + " 1.2.3"}}
	useFakeRunner(t, f)

	_, err := EnsureRuntime(context.Background(), home)
	if err == nil {
		t.Fatal("EnsureRuntime accepted a binary reporting the wrong version")
	}
	if !strings.Contains(err.Error(), Version) || !strings.Contains(err.Error(), "1.2.3") {
		t.Errorf("error does not name both versions: %v", err)
	}
}

func TestEnsureRuntimeFailsWithAnInstallPromptWhenNoInterpreterExists(t *testing.T) {
	allowProvisioning(t)
	home := t.TempDir()
	f := &fakeRunner{missing: map[string]bool{
		"python3.13": true, "python3.12": true, "python3.11": true,
		"python3": true, "python": true,
	}}
	useFakeRunner(t, f)

	_, err := EnsureRuntime(context.Background(), home)
	if err == nil {
		t.Fatal("EnsureRuntime succeeded with no interpreter available")
	}
	if !strings.Contains(err.Error(), "Pass this prompt to an LLM agent") {
		t.Errorf("error lacks the remediation prompt: %v", err)
	}
	if !strings.Contains(err.Error(), PackageName+"=="+Version) {
		t.Errorf("remediation prompt does not carry the pin: %v", err)
	}
}

// The defect this package exists to fix: internal/skillspector has no
// timeout, so a hung pip hangs dotpack with no output forever.
func TestEnsureRuntimeSurfacesAnInstallTimeoutRatherThanHanging(t *testing.T) {
	allowProvisioning(t)
	home := t.TempDir()
	f := &fakeRunner{
		results: map[string]fakeResult{
			"-m venv " + filepath.Join(home, "skillgate", "runtime"): {},
		},
		fallback: fakeResult{Delay: time.Hour},
	}
	useFakeRunner(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { _, err := EnsureRuntime(ctx, home); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("EnsureRuntime succeeded despite a hung subprocess")
		}
		if !strings.Contains(err.Error(), "timed out") && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error does not identify a timeout: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("EnsureRuntime hung past the context deadline")
	}
}

func TestEnsureRuntimeRequiresDotpackHome(t *testing.T) {
	if _, err := EnsureRuntime(context.Background(), "  "); err == nil {
		t.Fatal("EnsureRuntime accepted an empty DotpackHome")
	}
}

func TestEnsureRuntimeRefusesToProvisionUnderTheTestGuard(t *testing.T) {
	t.Setenv(NoProvisionEnv, "1")
	home := t.TempDir()
	f := &fakeRunner{fallback: fakeResult{Stdout: binName + " " + Version}}
	useFakeRunner(t, f)

	_, err := EnsureRuntime(context.Background(), home)
	if err == nil {
		t.Fatal("provisioning proceeded despite the no-provision guard")
	}
	if len(f.calls) != 0 {
		t.Fatalf("guard did not short-circuit before shelling out: %+v", f.calls)
	}
}

func TestEnsureRuntimeRejectsUnparseableMetadata(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "skillgate")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtime.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	useFakeRunner(t, &fakeRunner{})

	if _, err := EnsureRuntime(context.Background(), home); err == nil {
		t.Fatal("EnsureRuntime accepted unparseable runtime metadata")
	}
}

func TestDetectorVersionIsStableForBaselines(t *testing.T) {
	m := RuntimeMetadata{Package: PackageName, Version: Version}
	if got, want := m.DetectorVersion(), PackageName+" "+Version; got != want {
		t.Errorf("DetectorVersion() = %q, want %q", got, want)
	}
	// A zero metadata still names the pin, so a baseline written before
	// provisioning metadata exists is not stamped with an empty detector.
	if got := (RuntimeMetadata{}).DetectorVersion(); !strings.Contains(got, Version) {
		t.Errorf("zero-value DetectorVersion() = %q, want it to carry the pin", got)
	}
}
