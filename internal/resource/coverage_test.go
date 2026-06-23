package resource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceParsersCoverCoreAndExtensionShapes(t *testing.T) {
	skill, err := ParseSkill([]byte("---\nname: skill-one\ndescription: d\nlicense: MIT\nkeywords:\n  - docs\n---\nbody\n"))
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if skill.Kind() != KindSkill || skill.ResourceName() != "skill-one" || skill.License != "MIT" || skill.Extensions()["keywords"] == nil {
		t.Fatalf("skill fields not populated: %+v extensions=%v", skill, skill.Extensions())
	}
	skill.WithExtensions(map[string]any{"metadata": "x"})
	if skill.Raw != nil || skill.Extensions()["metadata"] != "x" {
		t.Fatalf("WithExtensions should replace extensions and clear Raw: raw=%q ext=%v", skill.Raw, skill.Extensions())
	}

	agent, err := ParseAgent([]byte("---\nname: agent-one\ndescription: d\nmodel: sonnet\ntools: Read, Write\ntemperature: 0.2\n---\nbody\n"))
	if err != nil {
		t.Fatalf("ParseAgent: %v", err)
	}
	if agent.Kind() != KindAgent || agent.ResourceName() != "agent-one" || len(agent.Tools) != 2 || agent.Extensions()["temperature"] == nil {
		t.Fatalf("agent fields not populated: %+v extensions=%v", agent, agent.Extensions())
	}
	agent.WithExtensions(map[string]any{"x": "y"})
	if agent.Raw != nil || agent.Extensions()["x"] != "y" {
		t.Fatalf("Agent.WithExtensions should clear Raw and set ext")
	}

	rule, err := ParseRule([]byte("---\nid: rule-id\nowner: docs\n---\nbody\n"))
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	rule.WithSourcePath("relative-rule.md")
	rule.WithExtensions(map[string]any{"artifact-type": "rule"})
	if rule.Kind() != KindRule || rule.ResourceName() != "rule-id" || rule.SourcePath == "" || rule.Raw != nil || rule.Extensions()["artifact-type"] != "rule" {
		t.Fatalf("rule fields not populated: %+v extensions=%v", rule, rule.Extensions())
	}

	mdCommand, err := ParseCommand([]byte("---\nname: build\ndescription: d\nallowed-tools:\n  - Read\nargument-hint: TARGET\n---\nRun build.\n"))
	if err != nil {
		t.Fatalf("ParseCommand markdown: %v", err)
	}
	mdCommand.WithName("renamed-build")
	if mdCommand.Kind() != KindCommand || mdCommand.ResourceName() != "renamed-build" || mdCommand.Prompt != "Run build." || mdCommand.Extensions()["argument-hint"] == nil {
		t.Fatalf("markdown command fields not populated: %+v extensions=%v", mdCommand, mdCommand.Extensions())
	}

	tomlCommand, err := ParseCommand([]byte("name = \"deploy\"\nprompt = \"Deploy\"\nallowed-tools = [\"Read\"]\nextra = \"x\"\n"))
	if err != nil {
		t.Fatalf("ParseCommand TOML: %v", err)
	}
	if tomlCommand.ResourceName() != "deploy" || tomlCommand.Prompt != "Deploy" || tomlCommand.Extensions()["extra"] != "x" {
		t.Fatalf("TOML command fields not populated: %+v extensions=%v", tomlCommand, tomlCommand.Extensions())
	}

	memory, err := ParseMemory([]byte("remember this"))
	if err != nil {
		t.Fatalf("ParseMemory: %v", err)
	}
	memory.WithName("AGENTS.md")
	if memory.Kind() != KindMemory || memory.Extensions() != nil || memory.ResourceName() != "AGENTS.md" || memory.Body != "remember this" {
		t.Fatalf("memory fields not populated: %+v", memory)
	}
}

func TestRuleNameFallbackBranches(t *testing.T) {
	if got := (&Rule{Name: "rule-name", ID: "rule-id"}).NameOrID(); got != "rule-name" {
		t.Fatalf("NameOrID with name = %q; want rule-name", got)
	}
	if got := (&Rule{ID: "rule-id"}).NameOrID(); got != "rule-id" {
		t.Fatalf("NameOrID fallback = %q; want rule-id", got)
	}
}

func TestParseSkillFallbackAndFrontmatterErrors(t *testing.T) {
	skill, err := ParseSkill([]byte("---\nname: simple\ndescription: simple parser\n---\nbody\n"))
	if err != nil {
		t.Fatalf("ParseSkill fallback: %v", err)
	}
	if skill.Name != "simple" || skill.Description != "simple parser" {
		t.Fatalf("fallback fields = %+v", skill)
	}

	cases := []struct {
		raw     string
		wantErr string
	}{
		{"name: missing delimiters\n", "missing opening"},
		{"---\nname: x\n", "missing closing"},
		{"---\nnot-key-value\n---\n", "parse frontmatter"},
	}
	for _, tc := range cases {
		if _, err := ParseSkill([]byte(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("ParseSkill(%q) error = %v; want %q", tc.raw, err, tc.wantErr)
		}
	}
}

func TestParseAgentAndCommandToolErrors(t *testing.T) {
	if _, err := ParseAgent([]byte("---\nname: bad\n: bad\n---\nbody\n")); err == nil || !strings.Contains(err.Error(), "parse frontmatter") {
		t.Fatalf("ParseAgent yaml error = %v; want parse frontmatter", err)
	}
	if _, err := ParseAgent([]byte("---\nname: bad\ndescription: d\ntools: 42\n---\nbody\n")); err == nil || !strings.Contains(err.Error(), "tools") {
		t.Fatalf("ParseAgent tools error = %v; want tools", err)
	}
	if _, err := ParseAgent([]byte("---\nname: bad\ndescription: d\ntools:\n  - Read\n  - 4\n---\nbody\n")); err == nil || !strings.Contains(err.Error(), "tools[1]") {
		t.Fatalf("ParseAgent tools element error = %v; want tools[1]", err)
	}
	if _, err := ParseCommand([]byte("---\nname: bad\nallowed-tools: 4\n---\nbody\n")); err == nil || !strings.Contains(err.Error(), "allowed-tools") {
		t.Fatalf("ParseCommand allowed-tools error = %v; want allowed-tools", err)
	}
	if _, err := ParseCommand([]byte("name = \"bad\"\nallowed-tools = [1]\nprompt = \"x\"\n")); err == nil || !strings.Contains(err.Error(), "allowed-tools") {
		t.Fatalf("ParseCommand TOML allowed-tools error = %v; want allowed-tools", err)
	}
	if _, err := ParseCommand([]byte("not = [valid")); err == nil || !strings.Contains(err.Error(), "toml parse") {
		t.Fatalf("ParseCommand TOML parse error = %v; want toml parse", err)
	}
	if _, err := ParseCommand([]byte("not frontmatter\n")); err == nil || !strings.Contains(err.Error(), "toml parse") {
		t.Fatalf("ParseCommand non-frontmatter parse error = %v; want toml parse", err)
	}
	if _, err := ParseCommand([]byte("---\nname: bad\n: bad\n---\nbody\n")); err == nil || !strings.Contains(err.Error(), "parse frontmatter") {
		t.Fatalf("ParseCommand yaml error = %v; want parse frontmatter", err)
	}

	cmd, err := ParseCommand([]byte("---\nname: modeled\ndescription: d\nmodel: sonnet\n---\nrun\n"))
	if err != nil {
		t.Fatalf("ParseCommand markdown model: %v", err)
	}
	if cmd.Model != "sonnet" || cmd.Description != "d" {
		t.Fatalf("markdown command model/description not populated: %+v", cmd)
	}
	cmd, err = ParseCommand([]byte("name = 'modeled'\nprompt = 'run'\ndescription = 'd'\nmodel = 'sonnet'\nallowed-tools = 'Read, Write'\n"))
	if err != nil {
		t.Fatalf("ParseCommand TOML string tools/model: %v", err)
	}
	if cmd.Model != "sonnet" || cmd.Description != "d" || len(cmd.AllowedTools) != 2 {
		t.Fatalf("toml command fields not populated: %+v", cmd)
	}
}

func TestParseRuleAndSkillAdditionalErrorBranches(t *testing.T) {
	if _, err := ParseRule([]byte("not frontmatter\n")); err == nil || !strings.Contains(err.Error(), "missing opening") {
		t.Fatalf("ParseRule opening error = %v; want missing opening", err)
	}
	if _, err := ParseRule([]byte("---\nname: bad\n: bad\n---\nbody\n")); err == nil || !strings.Contains(err.Error(), "parse frontmatter") {
		t.Fatalf("ParseRule yaml error = %v; want parse frontmatter", err)
	}
	rule, err := ParseRule([]byte("---\nname: named-rule\n---\nbody\n"))
	if err != nil {
		t.Fatalf("ParseRule name: %v", err)
	}
	if rule.NameOrID() != "named-rule" {
		t.Fatalf("rule name = %q", rule.NameOrID())
	}

	if _, err := ParseSkill([]byte("---\n# comment\nname: [only-name\n---\nbody\n")); err == nil || !strings.Contains(err.Error(), "parse frontmatter") {
		t.Fatalf("ParseSkill missing description fallback error = %v; want parse frontmatter", err)
	}
	if _, err := ParseSkill([]byte("---\n: empty-key\nname: x\ndescription: d\n---\nbody\n")); err == nil || !strings.Contains(err.Error(), "parse frontmatter") {
		t.Fatalf("ParseSkill empty key fallback error = %v; want parse frontmatter", err)
	}
}

func TestRuleWithSourcePathDeletedCWDAbsFallback(t *testing.T) {
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	doomed := t.TempDir()
	if err := os.Chdir(doomed); err != nil {
		t.Fatalf("Chdir doomed: %v", err)
	}
	if err := os.RemoveAll(doomed); err != nil {
		_ = os.Chdir(oldwd)
		t.Fatalf("RemoveAll doomed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	rule := (&Rule{Name: "r"}).WithSourcePath(filepath.Join("relative", "rule.md"))
	if !strings.Contains(rule.SourcePath, filepath.Join("relative", "rule.md")) {
		t.Fatalf("SourcePath fallback = %q", rule.SourcePath)
	}
}

func TestParseMCPServerValidAndInvalidShapes(t *testing.T) {
	stdio, err := ParseMCPServer([]byte(`{"mcpServers":{"github":{"command":"npx","args":["-y"],"env":{"TOKEN":"x"},"enabled_tools":["issues"]}}}`))
	if err != nil {
		t.Fatalf("ParseMCPServer stdio: %v", err)
	}
	if stdio.Kind() != KindMCPServer || stdio.ResourceName() != "github" || stdio.Command != "npx" || len(stdio.Args) != 1 || stdio.Env["TOKEN"] != "x" || stdio.Extensions()["enabled_tools"] == nil {
		t.Fatalf("stdio server fields not populated: %+v extensions=%v", stdio, stdio.Extensions())
	}
	stdio.WithExtensions(map[string]any{"bearer_token_env_var": "TOKEN"})
	if stdio.Raw != nil || stdio.Extensions()["bearer_token_env_var"] != "TOKEN" {
		t.Fatalf("MCPServer.WithExtensions should clear Raw and set ext")
	}

	http, err := ParseMCPServer([]byte(`{"mcpServers":{"remote":{"url":"https://example.com/mcp"}}}`))
	if err != nil {
		t.Fatalf("ParseMCPServer http: %v", err)
	}
	if http.URL == "" || http.Name != "remote" {
		t.Fatalf("http server fields not populated: %+v", http)
	}

	cases := []struct {
		raw     string
		wantErr string
	}{
		{`{`, "parse JSON"},
		{`{}`, "empty source"},
		{`{"mcpServers":{},"x":{}}`, "multiple top-level"},
		{`{"mcp_servers":{}}`, "not \"mcpServers\""},
		{`{"mcpServers":[]}`, "parse mcpServers value"},
		{`{"mcpServers":{}}`, "map is empty"},
		{`{"mcpServers":{"a":{},"b":{}}}`, "source has 2 server entries"},
		{`{"mcpServers":{"a":[]}}`, "parse mcpServers[\"a\"] value"},
		{`{"mcpServers":{"a":{"command":4}}}`, "command must be a string"},
		{`{"mcpServers":{"a":{"args":"bad"}}}`, "args must be a string array"},
		{`{"mcpServers":{"a":{"args":[4]}}}`, "args[0]"},
		{`{"mcpServers":{"a":{"url":4}}}`, "url must be a string"},
		{`{"mcpServers":{"a":{"env":[]}}}`, "env must be"},
		{`{"mcpServers":{"a":{"env":{"X":4}}}}`, "env[\"X\"]"},
	}
	for _, tc := range cases {
		if _, err := ParseMCPServer([]byte(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("ParseMCPServer(%s) error = %v; want %q", tc.raw, err, tc.wantErr)
		}
	}
}

func TestParseHookValidAndInvalidShapes(t *testing.T) {
	hook, err := ParseHook([]byte(`{"hooks":{"PostToolUse":[{"matcher":"Bash","name":"audit","hooks":[{"type":"command","command":"echo ok","timeout":5,"statusMessage":"done","env":{"A":"B"},"description":"desc"}]}]}}`))
	if err != nil {
		t.Fatalf("ParseHook: %v", err)
	}
	hook.WithName("hook-one")
	if hook.Kind() != KindHook || hook.ResourceName() != "hook-one" || len(hook.Events) != 1 || hook.Extensions()["name"] != "audit" || hook.Events[0].Bindings[0].Hooks[0].Env["A"] != "B" {
		t.Fatalf("hook fields not populated: %+v extensions=%v", hook, hook.Extensions())
	}
	hook.WithExtensions(map[string]any{"async": true})
	if hook.Raw != nil || hook.Extensions()["async"] != true {
		t.Fatalf("Hook.WithExtensions should clear Raw and set ext")
	}

	cases := []struct {
		raw     string
		wantErr string
	}{
		{`{`, "parse JSON"},
		{`{}`, "empty source"},
		{`{"hooks":{},"x":{}}`, "multiple top-level"},
		{`{"hookz":{}}`, "not \"hooks\""},
		{`{"hooks":[]}`, "parse hooks value"},
		{`{"hooks":{}}`, "empty hooks map"},
		{`{"hooks":{"PreToolUse":{}}}`, "parse as binding array"},
		{`{"hooks":{"PreToolUse":[]}}`, "empty binding array"},
		{`{"hooks":{"PreToolUse":[[]]}}`, "parse as binding object"},
		{`{"hooks":{"PreToolUse":[{"matcher":4,"hooks":[{"type":"command","command":"x"}]}]}}`, "matcher must be a string"},
		{`{"hooks":{"PreToolUse":[{"hooks":"bad"}]}}`, "hooks must be an array"},
		{`{"hooks":{"PreToolUse":[{"hooks":[4]}]}}`, "hooks[0] must be an object"},
		{`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`, "hooks is required"},
		{`{"hooks":{"PreToolUse":[{"hooks":[{"type":4}]}]}}`, "type must be a string"},
		{`{"hooks":{"PreToolUse":[{"hooks":[{"command":4}]}]}}`, "command must be a string"},
		{`{"hooks":{"PreToolUse":[{"hooks":[{"timeout":1.5}]}]}}`, "timeout must be an integer"},
		{`{"hooks":{"PreToolUse":[{"hooks":[{"timeout":"bad"}]}]}}`, "timeout must be an integer"},
		{`{"hooks":{"PreToolUse":[{"hooks":[{"statusMessage":4}]}]}}`, "statusMessage must be a string"},
		{`{"hooks":{"PreToolUse":[{"hooks":[{"env":4}]}]}}`, "env must be"},
		{`{"hooks":{"PreToolUse":[{"hooks":[{"env":{"A":4}}]}]}}`, "env[\"A\"]"},
	}
	for _, tc := range cases {
		if _, err := ParseHook([]byte(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("ParseHook(%s) error = %v; want %q", tc.raw, err, tc.wantErr)
		}
	}
}
