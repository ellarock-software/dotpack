package skillspector

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	RepoURL = "https://github.com/NVIDIA/SkillSpector.git"
	Commit  = "ac6b41b7a28b7b3d9001e43fbbf710d4267d5a7c"
	Version = "2.3.5"
)

type Runtime struct {
	RootDir         string
	VenvDir         string
	PythonBin       string
	SkillSpectorBin string
	MetadataPath    string
	Metadata        RuntimeMetadata
}

type RuntimeMetadata struct {
	Repo           string `json:"repo"`
	Commit         string `json:"commit"`
	Version        string `json:"version"`
	InstalledAt    string `json:"installed_at"`
	SelectedPython string `json:"selected_python"`
	VersionOutput  string `json:"version_output"`
}

type commandRunner interface {
	LookPath(file string) (string, error)
	Run(dir string, name string, args ...string) (string, string, error)
}

type execRunner struct{}

var runner commandRunner = execRunner{}

func (execRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (execRunner) Run(dir string, name string, args ...string) (string, string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return string(out), "", nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), string(exitErr.Stderr), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}

func EnsureRuntime(dotpackHome string) (Runtime, error) {
	if strings.TrimSpace(dotpackHome) == "" {
		return Runtime{}, fmt.Errorf("DOTPACK_DOTPACK_HOME is required for SkillSpector runtime")
	}

	rootDir := filepath.Join(dotpackHome, "skillspector")
	venvDir := filepath.Join(rootDir, "runtime")
	metadataPath := filepath.Join(rootDir, "runtime.json")
	pythonBin := filepath.Join(venvBinDir(venvDir), venvPythonName())
	skillSpectorBin := filepath.Join(venvBinDir(venvDir), venvSkillSpectorName())

	rt := Runtime{
		RootDir:         rootDir,
		VenvDir:         venvDir,
		PythonBin:       pythonBin,
		SkillSpectorBin: skillSpectorBin,
		MetadataPath:    metadataPath,
	}

	if metadata, ok, err := loadRuntimeMetadata(metadataPath); err != nil {
		return Runtime{}, err
	} else if ok && metadataMatchesPin(metadata) && fileExists(pythonBin) && fileExists(skillSpectorBin) {
		rt.Metadata = metadata
		return rt, nil
	}

	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return Runtime{}, fmt.Errorf("create SkillSpector runtime root %s: %w", rootDir, err)
	}
	if err := os.RemoveAll(venvDir); err != nil {
		return Runtime{}, fmt.Errorf("clear existing SkillSpector runtime %s: %w", venvDir, err)
	}

	selectedPython, err := createVenv(rootDir, venvDir)
	if err != nil {
		return Runtime{}, withInstallPrompt(dotpackHome, err)
	}

	if _, stderr, err := runner.Run(rootDir, pythonBin, "-m", "pip", "install", "--upgrade", fmt.Sprintf("git+%s@%s", RepoURL, Commit)); err != nil {
		return Runtime{}, withInstallPrompt(dotpackHome, fmt.Errorf("install SkillSpector runtime: %w\n%s", err, strings.TrimSpace(stderr)))
	}
	versionStdout, versionStderr, err := runner.Run(rootDir, skillSpectorBin, "--version")
	if err != nil {
		return Runtime{}, withInstallPrompt(dotpackHome, fmt.Errorf("verify SkillSpector runtime: %w\n%s", err, strings.TrimSpace(versionStderr)))
	}

	metadata := RuntimeMetadata{
		Repo:           RepoURL,
		Commit:         Commit,
		Version:        Version,
		InstalledAt:    time.Now().UTC().Format(time.RFC3339),
		SelectedPython: selectedPython,
		VersionOutput:  strings.TrimSpace(versionStdout),
	}
	if err := writeRuntimeMetadata(metadataPath, metadata); err != nil {
		return Runtime{}, err
	}

	rt.Metadata = metadata
	return rt, nil
}

func withInstallPrompt(dotpackHome string, err error) error {
	return fmt.Errorf("%w\n\nPass this prompt to an LLM agent to install the managed SkillSpector dependency:\n\n%s", err, installPrompt(dotpackHome))
}

func installPrompt(dotpackHome string) string {
	runtimeRoot := filepath.Join(dotpackHome, "skillspector")
	return fmt.Sprintf(`Work in the current shell environment.

Goal: make dotpack's managed SkillSpector runtime valid under %s.

Constraints:
- Do not install SkillSpector globally.
- Use dotpack's managed runtime path only.
- Fix only the prerequisites dotpack needs, such as Python 3 with venv/pip and git access.
- Keep the upstream pin exactly at repo %s commit %s.

Steps:
1. Check whether one of these interpreters exists and supports venv: python3.13, python3.12, python3.11, python3, python.
2. Fix any missing prerequisites needed for dotpack to create a virtual environment and run pip.
3. Re-run the same dotpack command that failed so dotpack can provision the runtime itself.
4. Verify that:
   - %s/runtime.json exists
   - the managed skillspector binary under %s reports its version
   - the original dotpack command now succeeds

Report exact commands run, exact outputs, and whether dotpack reused or recreated the runtime.`, runtimeRoot, RepoURL, Commit, runtimeRoot, runtimeRoot)
}

func createVenv(rootDir, venvDir string) (string, error) {
	var failures []string
	for _, candidate := range []string{"python3.13", "python3.12", "python3.11", "python3", "python"} {
		if _, err := runner.LookPath(candidate); err != nil {
			failures = append(failures, candidate+": not found")
			continue
		}
		if _, stderr, err := runner.Run(rootDir, candidate, "-m", "venv", venvDir); err != nil {
			msg := strings.TrimSpace(stderr)
			if msg == "" {
				msg = err.Error()
			}
			failures = append(failures, candidate+": "+msg)
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("create SkillSpector runtime: no compatible Python interpreter succeeded (%s)", strings.Join(failures, "; "))
}

func loadRuntimeMetadata(path string) (RuntimeMetadata, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RuntimeMetadata{}, false, nil
		}
		return RuntimeMetadata{}, false, fmt.Errorf("read SkillSpector runtime metadata %s: %w", path, err)
	}
	var metadata RuntimeMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return RuntimeMetadata{}, false, fmt.Errorf("parse SkillSpector runtime metadata %s: %w", path, err)
	}
	return metadata, true, nil
}

func writeRuntimeMetadata(path string, metadata RuntimeMetadata) error {
	raw, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode SkillSpector runtime metadata: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write SkillSpector runtime metadata %s: %w", path, err)
	}
	return nil
}

func metadataMatchesPin(metadata RuntimeMetadata) bool {
	return metadata.Repo == RepoURL && metadata.Commit == Commit && metadata.Version == Version
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

func venvPythonName() string {
	if runtime.GOOS == "windows" {
		return "python.exe"
	}
	return "python"
}

func venvSkillSpectorName() string {
	if runtime.GOOS == "windows" {
		return "skillspector.exe"
	}
	return "skillspector"
}
