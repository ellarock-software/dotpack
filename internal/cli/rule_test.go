package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallRule_ProjectHosts_MaterializeNativeFilesWithoutLossyFlag(t *testing.T) {
	cases := []struct {
		agent     string
		targetRel string
	}{
		{agent: "claude-code", targetRel: filepath.Join(".claude", "rules", "graphify.md")},
		{agent: "gemini-cli", targetRel: filepath.Join(".gemini", "rules", "graphify.md")},
		{agent: "codex", targetRel: filepath.Join(".codex", "rules", "graphify.md")},
	}

	for _, tc := range cases {
		t.Run(tc.agent, func(t *testing.T) {
			projectHome := t.TempDir()
			dotpackHome := t.TempDir()
			t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
			t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

			src := writeRuleFixture(t, filepath.Join(t.TempDir(), "graphify.md"))

			cmd := NewRootCmd()
			cmd.SetOut(io_DiscardWriter())
			cmd.SetErr(io_DiscardWriter())
			cmd.SetArgs([]string{"install", src, "--agent", tc.agent, "--kind", "rule", "--scope", "project"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("install rule on %s without --allow-lossy: %v", tc.agent, err)
			}

			target := filepath.Join(projectHome, tc.targetRel)
			raw, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("read materialized rule %s: %v", target, err)
			}
			got := string(raw)
			for _, want := range []string{
				`artifact-type: "rule"`,
				`host-compatibility: "shared"`,
				"## graphify",
				"graphify update .",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("%s output missing %q; got:\n%s", tc.agent, want, got)
				}
			}
		})
	}
}

func TestInstallRule_AgentsCLI_MaterializesGeminiAndCodexRuleFiles(t *testing.T) {
	projectHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := writeRuleFixture(t, filepath.Join(t.TempDir(), "graphify.md"))

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli", "--kind", "rule", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agents-cli rule install: %v\n%s", err, stdout.String())
	}

	for _, rel := range []string{
		filepath.Join(".gemini", "rules", "graphify.md"),
		filepath.Join(".codex", "rules", "graphify.md"),
	} {
		target := filepath.Join(projectHome, rel)
		if raw, err := os.ReadFile(target); err != nil {
			t.Fatalf("expected agents-cli to write %s: %v", target, err)
		} else if !strings.Contains(string(raw), `artifact-type: "rule"`) {
			t.Fatalf("agents-cli output should preserve rule metadata in %s; got:\n%s", target, raw)
		}
	}

	if got := stdout.String(); !strings.Contains(got, "Installed agents-cli:rule:graphify onto agents-cli") {
		t.Fatalf("install output should use agents-cli manifest identity; got %q", got)
	}
}

func TestInstallRule_RemovesLegacyHostSpecificCanonicalRules(t *testing.T) {
	projectHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := writeRuleFixture(t, filepath.Join(projectHome, ".agents", "rules", "graphify.md"))
	for _, rel := range []string{
		filepath.Join(".agents", "rules", "gemini", "graphify.md"),
		filepath.Join(".agents", "rules", "claude", "graphify.md"),
	} {
		path := filepath.Join(projectHome, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir stale parent: %v", err)
		}
		if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
			t.Fatalf("write stale rule: %v", err)
		}
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install inferred direct .agents/rules/*.md rule: %v", err)
	}

	for _, rel := range []string{
		filepath.Join(".agents", "rules", "gemini", "graphify.md"),
		filepath.Join(".agents", "rules", "claude", "graphify.md"),
	} {
		path := filepath.Join(projectHome, rel)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected stale host-specific canonical rule removed at %s, stat err=%v", path, err)
		}
	}
}

func TestRuleListAndReconcile_ShowSourceAndMissingMaterializedFile(t *testing.T) {
	projectHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := writeRuleFixture(t, filepath.Join(t.TempDir(), "graphify.md"))
	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "rule", "--scope", "project"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install rule: %v", err)
	}

	var listOut bytes.Buffer
	list := NewRootCmd()
	list.SetOut(&listOut)
	list.SetErr(&listOut)
	list.SetArgs([]string{"list"})
	if err := list.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	listText := listOut.String()
	for _, want := range []string{"codex:rule:graphify", "source=file://", src} {
		if !strings.Contains(listText, want) {
			t.Errorf("list output missing %q; got:\n%s", want, listText)
		}
	}

	target := filepath.Join(projectHome, ".codex", "rules", "graphify.md")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove rule output: %v", err)
	}

	var reconcileOut bytes.Buffer
	reconcile := NewRootCmd()
	reconcile.SetOut(&reconcileOut)
	reconcile.SetErr(&reconcileOut)
	reconcile.SetArgs([]string{"reconcile"})
	if err := reconcile.Execute(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := reconcileOut.String()
	for _, want := range []string{"codex:rule:graphify", "missing file", target} {
		if !strings.Contains(got, want) {
			t.Errorf("reconcile output missing %q; got:\n%s", want, got)
		}
	}
}

func writeRuleFixture(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir rule fixture parent: %v", err)
	}
	raw := `---
id: "graphify"
name: "graphify"
artifact-type: "rule"
owner: "shared"
owner/surface: "shared"
purpose: "Shared Graphify knowledge-graph guidance for all LLM tool hosts."
triggers: "Host rule materialization or agent configuration loads this shared rule."
inputs: "User request, agent runtime context, and Graphify artifacts under graphify-out/ when present."
outputs: "Agent instructions for using Graphify reports, wiki pages, graph traversal, and graph updates."
state-read: "graphify-out/GRAPH_REPORT.md, graphify-out/wiki/index.md, and graphify-out/graph.json when present."
state-write: "none"
state-written: "none; agents may run graphify update . after code changes when Graphify is installed."
failure-mode: "fails closed when required metadata is missing; otherwise host runtime handles unsupported content."
registered-in: ".agents/rules/graphify.md"
tests: "npm run artifacts:intro:enforce"
host-compatibility: "shared"
---

## graphify

When a project has a Graphify knowledge graph at ` + "`graphify-out/`" + `, all LLM tool hosts must use it as first-pass architecture context.

Rules:

- Before answering architecture or codebase questions, read ` + "`graphify-out/GRAPH_REPORT.md`" + ` for god nodes and community structure when it exists.
- After modifying code files in a project with Graphify installed, run ` + "`graphify update .`" + ` to keep the graph current.
`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write rule fixture: %v", err)
	}
	return path
}
