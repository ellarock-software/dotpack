// Package skillscanner provisions and describes dotpack's managed
// cisco-ai-skill-scanner runtime — the detector behind the delta skill
// gate (ADR-0016).
//
// It is a peer of internal/skillspector and follows the same shape: a
// pinned upstream, a private virtual environment under DotpackHome, a
// metadata file recording the pin, and a cache-hit path that reuses an
// existing runtime without shelling out. It differs in two ways that
// matter.
//
// First, the pin is a PyPI version rather than a git commit, because
// cisco-ai-skill-scanner is distributed as a wheel.
//
// Second, every subprocess has a deadline. internal/skillspector has no
// timeout anywhere, so a hung pip or a hung scan hangs dotpack forever
// with no output. Provisioning and scanning both take a context here,
// and the exec wrapper sets WaitDelay so a killed process cannot keep
// the call blocked on inherited pipes.
package skillscanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// PackageName and Version pin the detector. Both are recorded in
	// runtime.json and in every baseline, so a pin bump is a visible,
	// reviewable event rather than a silent change in what "approved"
	// means.
	PackageName = "cisco-ai-skill-scanner"
	Version     = "2.0.12"

	// binName is the console script the wheel installs.
	binName = "skill-scanner"
)

// Timeouts. Chosen to be generous enough that a slow-but-working machine
// is never failed, and short enough that a hang surfaces as an error the
// operator can act on.
const (
	venvTimeout    = 2 * time.Minute
	installTimeout = 10 * time.Minute // a cold wheel download pulls a large dependency tree
	verifyTimeout  = 60 * time.Second

	// DefaultScanTimeout bounds one package scan. Matches the 300000ms
	// default of the tool this gate was ported from.
	DefaultScanTimeout = 5 * time.Minute
)

// NoProvisionEnv short-circuits provisioning with an error. It exists so
// the test suite can prove that no test path ever reaches a real pip
// install: TestMain sets it, and any test that forgets to install a fake
// runtime fails loudly instead of silently downloading from PyPI.
const NoProvisionEnv = "DOTPACK_SKILLGATE_NO_PROVISION"

// Runtime is a provisioned detector.
type Runtime struct {
	RootDir      string
	VenvDir      string
	PythonBin    string
	ScannerBin   string
	MetadataPath string
	Metadata     RuntimeMetadata
}

// RuntimeMetadata is the on-disk record of what was provisioned. It is
// also what the delta gate stamps into a baseline as detector_version.
type RuntimeMetadata struct {
	Package        string `json:"package"`
	Version        string `json:"version"`
	InstalledAt    string `json:"installed_at"`
	SelectedPython string `json:"selected_python"`
	VersionOutput  string `json:"version_output"`
}

// DetectorVersion is the identifier recorded in baselines.
func (m RuntimeMetadata) DetectorVersion() string {
	if strings.TrimSpace(m.Package) == "" {
		return PackageName + " " + Version
	}
	return m.Package + " " + m.Version
}

// commandRunner is the test seam. It is declared here rather than in
// internal/cli because that package already has an unrelated interface
// of the same name for lifecycle tasks.
type commandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, dir string, name string, args ...string) (stdout, stderr string, err error)
}

type execRunner struct{}

var runner commandRunner = execRunner{}

func (execRunner) LookPath(file string) (string, error) { return exec.LookPath(file) }

func (execRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	// Without WaitDelay, CommandContext kills the process on deadline but
	// Wait still blocks until every inherited pipe closes. pip spawns
	// children that inherit stdout, so a "timeout" would hang forever —
	// exactly the failure this package exists to prevent.
	cmd.WaitDelay = 5 * time.Second

	out, err := cmd.Output()
	if err == nil {
		return string(out), "", nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), string(exitErr.Stderr), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}

// EnsureRuntime returns a provisioned detector, provisioning it if the
// managed runtime is absent or does not match the pin.
//
// It fails closed in every direction: a missing interpreter, a failed
// install, an unverifiable binary and an unreadable metadata file are
// all errors. The caller blocks the install on any of them, because an
// unscannable package is not an approved package.
func EnsureRuntime(ctx context.Context, dotpackHome string) (Runtime, error) {
	if strings.TrimSpace(dotpackHome) == "" {
		return Runtime{}, fmt.Errorf("DOTPACK_DOTPACK_HOME is required for the %s runtime", PackageName)
	}

	rootDir := filepath.Join(dotpackHome, "skillgate")
	venvDir := filepath.Join(rootDir, "runtime")
	metadataPath := filepath.Join(rootDir, "runtime.json")
	pythonBin := filepath.Join(venvBinDir(venvDir), venvExe("python"))
	scannerBin := filepath.Join(venvBinDir(venvDir), venvExe(binName))

	rt := Runtime{
		RootDir:      rootDir,
		VenvDir:      venvDir,
		PythonBin:    pythonBin,
		ScannerBin:   scannerBin,
		MetadataPath: metadataPath,
	}

	metadata, ok, err := loadRuntimeMetadata(metadataPath)
	if err != nil {
		return Runtime{}, err
	}
	if ok && metadataMatchesPin(metadata) && fileExists(pythonBin) && fileExists(scannerBin) {
		rt.Metadata = metadata
		return rt, nil
	}

	if os.Getenv(NoProvisionEnv) != "" {
		return Runtime{}, fmt.Errorf("%s is set: refusing to provision the %s runtime (a test reached real provisioning)", NoProvisionEnv, PackageName)
	}

	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return Runtime{}, fmt.Errorf("create %s runtime root %s: %w", PackageName, rootDir, err)
	}
	// The existing venv did not match the pin. Remove it rather than
	// upgrading in place: a partially upgraded environment is the one
	// state where the recorded pin and the installed code can disagree.
	if err := os.RemoveAll(venvDir); err != nil {
		return Runtime{}, fmt.Errorf("clear existing %s runtime %s: %w", PackageName, venvDir, err)
	}

	selectedPython, err := createVenv(ctx, rootDir, venvDir)
	if err != nil {
		return Runtime{}, withInstallPrompt(dotpackHome, err)
	}

	installCtx, cancelInstall := context.WithTimeout(ctx, installTimeout)
	defer cancelInstall()
	_, stderr, err := runner.Run(installCtx, rootDir, pythonBin,
		"-m", "pip", "install", "--no-input", "--disable-pip-version-check",
		PackageName+"=="+Version)
	if err != nil {
		return Runtime{}, withInstallPrompt(dotpackHome, fmt.Errorf("install %s runtime: %w\n%s", PackageName, annotateTimeout(installCtx, err), strings.TrimSpace(stderr)))
	}

	verifyCtx, cancelVerify := context.WithTimeout(ctx, verifyTimeout)
	defer cancelVerify()
	versionStdout, versionStderr, err := runner.Run(verifyCtx, rootDir, scannerBin, "--version")
	if err != nil {
		return Runtime{}, withInstallPrompt(dotpackHome, fmt.Errorf("verify %s runtime: %w\n%s", PackageName, annotateTimeout(verifyCtx, err), strings.TrimSpace(versionStderr)))
	}

	// Confirm the binary we are about to trust is the version we pinned.
	// Without this a renamed console script or a resolver that produced a
	// different build surfaces later as an opaque "detector produced no
	// report", which reads like a broken package rather than a broken
	// runtime.
	trimmedVersion := strings.TrimSpace(versionStdout)
	if !strings.Contains(trimmedVersion, Version) {
		return Runtime{}, withInstallPrompt(dotpackHome, fmt.Errorf(
			"verify %s runtime: %s reported %q, which does not contain the pinned version %s",
			PackageName, scannerBin, trimmedVersion, Version))
	}

	metadata = RuntimeMetadata{
		Package:        PackageName,
		Version:        Version,
		InstalledAt:    time.Now().UTC().Format(time.RFC3339),
		SelectedPython: selectedPython,
		VersionOutput:  trimmedVersion,
	}
	if err := writeRuntimeMetadata(metadataPath, metadata); err != nil {
		return Runtime{}, err
	}

	rt.Metadata = metadata
	return rt, nil
}

// annotateTimeout makes a deadline failure legible. exec reports a
// context kill as "signal: killed", which tells the operator nothing.
func annotateTimeout(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timed out: %w", err)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return fmt.Errorf("canceled: %w", err)
	}
	return err
}

func createVenv(ctx context.Context, rootDir, venvDir string) (string, error) {
	var failures []string
	for _, candidate := range []string{"python3.13", "python3.12", "python3.11", "python3", "python"} {
		if _, err := runner.LookPath(candidate); err != nil {
			failures = append(failures, candidate+": not found")
			continue
		}
		venvCtx, cancel := context.WithTimeout(ctx, venvTimeout)
		_, stderr, err := runner.Run(venvCtx, rootDir, candidate, "-m", "venv", venvDir)
		cancel()
		if err != nil {
			msg := strings.TrimSpace(stderr)
			if msg == "" {
				msg = annotateTimeout(venvCtx, err).Error()
			}
			failures = append(failures, candidate+": "+msg)
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("create %s runtime: no compatible Python interpreter succeeded (%s)", PackageName, strings.Join(failures, "; "))
}

func withInstallPrompt(dotpackHome string, err error) error {
	return fmt.Errorf("%w\n\nPass this prompt to an LLM agent to install the managed skill-scanner dependency:\n\n%s", err, installPrompt(dotpackHome))
}

func installPrompt(dotpackHome string) string {
	runtimeRoot := filepath.Join(dotpackHome, "skillgate")
	return fmt.Sprintf(`Work in the current shell environment.

Goal: make dotpack's managed %s runtime valid under %s.

Constraints:
- Do not install %s globally.
- Use dotpack's managed runtime path only.
- Fix only the prerequisites dotpack needs, such as Python 3 with venv/pip and network access to PyPI.
- Keep the upstream pin exactly at %s==%s.

Steps:
1. Check whether one of these interpreters exists and supports venv: python3.13, python3.12, python3.11, python3, python.
2. Fix any missing prerequisites needed for dotpack to create a virtual environment and run pip.
3. Re-run the same dotpack command that failed so dotpack can provision the runtime itself.
4. Verify that:
   - %s/runtime.json exists
   - the managed %s binary under %s reports version %s
   - the original dotpack command now succeeds

If the machine has no network access, the operator can fall back to
"--skill-gate skillspector" or bypass a specific package with
"--skill-bypass-security <name>". Both are explicit and both are reported.

Report exact commands run, exact outputs, and whether dotpack reused or recreated the runtime.`,
		PackageName, runtimeRoot, PackageName, PackageName, Version,
		runtimeRoot, binName, runtimeRoot, Version)
}

func loadRuntimeMetadata(path string) (RuntimeMetadata, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RuntimeMetadata{}, false, nil
		}
		return RuntimeMetadata{}, false, fmt.Errorf("read %s runtime metadata %s: %w", PackageName, path, err)
	}
	var metadata RuntimeMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return RuntimeMetadata{}, false, fmt.Errorf("parse %s runtime metadata %s: %w", PackageName, path, err)
	}
	return metadata, true, nil
}

func writeRuntimeMetadata(path string, metadata RuntimeMetadata) error {
	raw, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s runtime metadata: %w", PackageName, err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s runtime metadata %s: %w", PackageName, path, err)
	}
	return nil
}

func metadataMatchesPin(metadata RuntimeMetadata) bool {
	return metadata.Package == PackageName && metadata.Version == Version
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func venvBinDir(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts")
	}
	return filepath.Join(venvDir, "bin")
}

func venvExe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
