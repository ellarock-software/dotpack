package skillspector

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	lookups map[string]string
	calls   []fakeCall
	results map[string]fakeResult
}

type fakeCall struct {
	Dir  string
	Name string
	Args []string
}

type fakeResult struct {
	stdout string
	stderr string
	err    error
}

func (f *fakeRunner) LookPath(file string) (string, error) {
	if path, ok := f.lookups[file]; ok {
		return path, nil
	}
	return "", errors.New("not found")
}

func (f *fakeRunner) Run(dir string, name string, args ...string) (string, string, error) {
	f.calls = append(f.calls, fakeCall{Dir: dir, Name: name, Args: append([]string(nil), args...)})
	key := name + " " + strings.Join(args, " ")
	if res, ok := f.results[key]; ok {
		return res.stdout, res.stderr, res.err
	}
	return "", "", nil
}

func TestEnsureRuntimeReusesMatchingMetadata(t *testing.T) {
	dotpackHome := t.TempDir()
	rootDir := filepath.Join(dotpackHome, "skillspector")
	venvDir := filepath.Join(rootDir, "runtime")
	binDir := venvBinDir(venvDir)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, venvPythonName()), []byte("python"), 0o755); err != nil {
		t.Fatalf("write python: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, venvSkillSpectorName()), []byte("skillspector"), 0o755); err != nil {
		t.Fatalf("write skillspector: %v", err)
	}
	metadata := RuntimeMetadata{
		Repo:           RepoURL,
		Commit:         Commit,
		Version:        Version,
		InstalledAt:    "2026-07-04T00:00:00Z",
		SelectedPython: "python3.12",
		VersionOutput:  "skillspector 2.3.5",
	}
	if err := writeRuntimeMetadata(filepath.Join(rootDir, "runtime.json"), metadata); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	oldRunner := runner
	runner = &fakeRunner{}
	t.Cleanup(func() { runner = oldRunner })

	rt, err := EnsureRuntime(dotpackHome)
	if err != nil {
		t.Fatalf("EnsureRuntime: %v", err)
	}
	if rt.Metadata.Version != Version || rt.Metadata.SelectedPython != "python3.12" {
		t.Fatalf("runtime metadata = %+v", rt.Metadata)
	}
}

func TestEnsureRuntimeProvisionWritesMetadata(t *testing.T) {
	dotpackHome := t.TempDir()
	oldRunner := runner
	fr := &fakeRunner{
		lookups: map[string]string{
			"python3.12": "/usr/bin/python3.12",
		},
		results: map[string]fakeResult{},
	}
	runner = fr
	t.Cleanup(func() { runner = oldRunner })

	rootDir := filepath.Join(dotpackHome, "skillspector")
	venvDir := filepath.Join(rootDir, "runtime")
	pythonBin := filepath.Join(venvBinDir(venvDir), venvPythonName())
	skillSpectorBin := filepath.Join(venvBinDir(venvDir), venvSkillSpectorName())
	fr.results["python3.12 -m venv "+venvDir] = fakeResult{}
	fr.results[pythonBin+" -m pip install --upgrade git+"+RepoURL+"@"+Commit] = fakeResult{}
	fr.results[skillSpectorBin+" --version"] = fakeResult{stdout: "skillspector 2.3.5\n"}

	rt, err := EnsureRuntime(dotpackHome)
	if err != nil {
		t.Fatalf("EnsureRuntime: %v", err)
	}
	if rt.Metadata.Repo != RepoURL || rt.Metadata.Commit != Commit || rt.Metadata.Version != Version {
		t.Fatalf("runtime metadata = %+v", rt.Metadata)
	}
	if rt.Metadata.SelectedPython != "python3.12" {
		t.Fatalf("selected python = %q", rt.Metadata.SelectedPython)
	}
	raw, err := os.ReadFile(filepath.Join(rootDir, "runtime.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if !strings.Contains(string(raw), RepoURL) || !strings.Contains(string(raw), Commit) {
		t.Fatalf("runtime metadata missing pin:\n%s", string(raw))
	}
	if len(fr.calls) != 3 {
		t.Fatalf("runner calls = %d, want 3", len(fr.calls))
	}
}

func TestEnsureRuntimeProvisionFailureIncludesLLMAgentPrompt(t *testing.T) {
	dotpackHome := t.TempDir()
	oldRunner := runner
	runner = &fakeRunner{}
	t.Cleanup(func() { runner = oldRunner })

	_, err := EnsureRuntime(dotpackHome)
	if err == nil {
		t.Fatal("EnsureRuntime should fail without a usable python interpreter")
	}
	for _, want := range []string{
		"Pass this prompt to an LLM agent",
		filepath.Join(dotpackHome, "skillspector"),
		RepoURL,
		Commit,
		"Do not install SkillSpector globally.",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("EnsureRuntime error missing %q:\n%s", want, err)
		}
	}
}
