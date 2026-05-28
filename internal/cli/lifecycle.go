package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed lifecycle_tasks.yaml
var defaultLifecycleConfig []byte

var runPostInstallLifecycle = func(agentName string) error {
	return runLifecyclePhase(lifecyclePhasePostInstall, agentName)
}

var lifecycleRunner commandRunner = execCommandRunner{}

const lifecyclePhasePostInstall = "post-install"

type commandRunner interface {
	LookPath(file string) (string, error)
	Run(name string, args ...string) error
}

type execCommandRunner struct{}

func (execCommandRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (execCommandRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text != "" {
			return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, text)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

type lifecycleDefinition struct {
	Version int             `yaml:"version"`
	Tasks   []lifecycleTask `yaml:"tasks"`
}

type lifecycleTask struct {
	Name      string             `yaml:"name"`
	Phase     string             `yaml:"phase"`
	AppliesTo lifecycleAppliesTo `yaml:"applies_to"`
	Ensure    lifecycleEnsure    `yaml:"ensure"`
	Run       []lifecycleCommand `yaml:"run"`
	Verify    []lifecycleCommand `yaml:"verify"`
	Failure   string             `yaml:"failure"`
}

type lifecycleAppliesTo struct {
	Agents []string `yaml:"agents"`
}

type lifecycleEnsure struct {
	Binaries []lifecycleBinary `yaml:"binaries"`
}

type lifecycleBinary struct {
	Name    string             `yaml:"name"`
	Env     string             `yaml:"env"`
	Install lifecycleInstaller `yaml:"install"`
}

type lifecycleInstaller struct {
	Candidates []lifecycleCommand `yaml:"candidates"`
}

type lifecycleCommand struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

func loadLifecycleDefinition() (lifecycleDefinition, error) {
	var def lifecycleDefinition
	if err := yaml.Unmarshal(defaultLifecycleConfig, &def); err != nil {
		return lifecycleDefinition{}, err
	}
	if def.Version != 1 {
		return lifecycleDefinition{}, fmt.Errorf("unsupported lifecycle config version %d", def.Version)
	}
	return def, nil
}

func runLifecyclePhase(phase, agentName string) error {
	def, err := loadLifecycleDefinition()
	if err != nil {
		return fmt.Errorf("load lifecycle tasks: %w", err)
	}

	for _, task := range def.Tasks {
		if !task.AppliesTo.matches(phase, agentName, task.Phase) {
			continue
		}
		if err := runLifecycleTask(task); err != nil {
			if task.failurePolicy() == "fail-open" {
				continue
			}
			return fmt.Errorf("%s: %w", task.Name, err)
		}
	}
	return nil
}

func (a lifecycleAppliesTo) matches(phase, agentName, taskPhase string) bool {
	if taskPhase != phase {
		return false
	}
	if len(a.Agents) == 0 {
		return true
	}
	for _, agent := range a.Agents {
		if agent == agentName {
			return true
		}
	}
	return false
}

func (t lifecycleTask) failurePolicy() string {
	if t.Failure == "" {
		return "fail-closed"
	}
	return t.Failure
}

func runLifecycleTask(task lifecycleTask) error {
	resolved := map[string]string{}
	for _, binary := range task.Ensure.Binaries {
		path, err := ensureLifecycleBinary(binary)
		if err != nil {
			return fmt.Errorf("ensure binary %s: %w", binary.Name, err)
		}
		resolved[binary.Name] = path
	}

	for _, cmd := range task.Run {
		if err := runLifecycleCommand(cmd, resolved); err != nil {
			return fmt.Errorf("run %s: %w", formatLifecycleCommand(cmd), err)
		}
	}
	for _, cmd := range task.Verify {
		if err := runLifecycleCommand(cmd, resolved); err != nil {
			return fmt.Errorf("verify %s: %w", formatLifecycleCommand(cmd), err)
		}
	}
	return nil
}

func ensureLifecycleBinary(binary lifecycleBinary) (string, error) {
	if binary.Name == "" {
		return "", errors.New("binary name is required")
	}
	if binary.Env != "" {
		if configured := strings.TrimSpace(os.Getenv(binary.Env)); configured != "" {
			abs, err := filepath.Abs(configured)
			if err != nil {
				return "", fmt.Errorf("%s=%q: resolve abs: %w", binary.Env, configured, err)
			}
			if _, err := os.Stat(abs); err != nil {
				return "", fmt.Errorf("%s=%q: %w", binary.Env, configured, err)
			}
			return abs, nil
		}
	}

	path, err := lifecycleRunner.LookPath(binary.Name)
	if err == nil {
		return path, nil
	}
	if !errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("find %s on PATH: %w", binary.Name, err)
	}

	var installFailures []string
	for _, candidate := range binary.Install.Candidates {
		installer, err := lifecycleRunner.LookPath(candidate.Command)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				installFailures = append(installFailures, candidate.Command+": not found")
				continue
			}
			return "", fmt.Errorf("find installer %s on PATH: %w", candidate.Command, err)
		}
		if err := lifecycleRunner.Run(installer, candidate.Args...); err != nil {
			installFailures = append(installFailures, fmt.Sprintf("%s failed: %v", candidate.Command, err))
			continue
		}
		path, err := lifecycleRunner.LookPath(binary.Name)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("find %s after %s install: %w", binary.Name, candidate.Command, err)
		}
		installFailures = append(installFailures, candidate.Command+" completed but binary was still not on PATH")
	}

	if len(binary.Install.Candidates) == 0 {
		return "", fmt.Errorf("%s is not installed and no install candidates are declared", binary.Name)
	}
	return "", fmt.Errorf("%s is not installed and no installer candidate succeeded: %s", binary.Name, strings.Join(installFailures, "; "))
}

func runLifecycleCommand(cmd lifecycleCommand, resolved map[string]string) error {
	command := cmd.Command
	if resolvedCommand, ok := resolved[command]; ok {
		command = resolvedCommand
	}
	return lifecycleRunner.Run(command, cmd.Args...)
}

func formatLifecycleCommand(cmd lifecycleCommand) string {
	if len(cmd.Args) == 0 {
		return cmd.Command
	}
	return cmd.Command + " " + strings.Join(cmd.Args, " ")
}
