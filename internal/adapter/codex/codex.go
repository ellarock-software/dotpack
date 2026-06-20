// Package codex is the codex CLI per-host shell. It dispatches per-kind
// to the deep adapter modules:
//
//   - skill → internal/adapter/filedrop (one file written per resource).
//   - mcp-server + hook → internal/adapter/configfrag (one or more
//     (path, value) merges into ~/.codex/config.toml; ADR-0012 §5–§7+§9).
//
// Both modules implement adapter.Adapter; the shell's job is to declare
// the per-host policy data (Policy for filedrop; configfragPolicy() for
// configfrag) and dispatch Plan by resource Kind. Mirror of claudecode's
// pattern; the split keeps each deep module focused on one apply
// contract.
//
// Codex mcp-server and hook BOTH target the same ~/.codex/config.toml
// file — mcp-server installs Set into mcp_servers.<name>; hook installs
// Append into hooks.<Event> arrays-of-tables per schema/hook.yaml's
// codex source_locations entry. The orchestrator runs one install at a
// time, so there's no apply-time collision; the read-modify-write
// preserves whichever sibling tables (mcp_servers + hooks) the file
// already carries.
//
// Codex supports skill on the file-drop side and agent via TOML translation.
// Because codex expects agents as .toml files under AgentsHome/agents/,
// KindAgent is absent from Policy.Layouts (which handles raw file-drop) and
// is instead handled natively by planAgentCodex to emit TOML.
//
// Skill paths: <AgentsHome>/skills/<name>/SKILL.md (user) or
// <ProjectHome>/.agents/skills/<name>/SKILL.md (project). Per
// developers.openai.com/codex/skills, AgentsHome is codex's only
// documented native skill root. Gemini CLI ALSO reads ~/.agents/skills/
// as a convergence path, but the gemini-cli adapter writes to its
// host-specific path so `--agent codex` and `--agent gemini-cli` don't
// collide here today. The `--agent agents-cli` umbrella flag
// (ADR-0012 §1) writes to AgentsHome/skills/ as its write-once
// convergence; collision handling is owned by that umbrella's CLI-flag-
// to-adapter-set special case.
//
// MCP-server and hook paths: <CodexHome>/config.toml (user) or
// <ProjectHome>/.codex/config.toml (project) per schema source_locations.
// Rule paths: <CodexHome>/rules/<name>.md (user) or
// <ProjectHome>/.codex/rules/<name>.md (project).
package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/adapter/configfrag"
	"github.com/ellarock-software/dotpack/internal/adapter/filedrop"
	"github.com/ellarock-software/dotpack/internal/adapter/hookcmd"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
)

// hostID is the dotpack adapter HostID for codex. MUST match the
// `host:` strings in schema/*.yaml aliases — schema.HostKeepsExtension
// compares on string equality.
const hostID = "codex"

// userRoot returns AgentsHome with the host-specific missing-dir error.
// Codex's skill path is AgentsHome (not CodexHome) per the package
// docstring — keep this resolver focused on the skill case so a future
// reader debugging "where does codex write skills?" sees the right env
// var (DOTPACK_AGENTS_HOME).
func userRoot(d dirs.Dirs) (string, error) {
	if d.AgentsHome == "" {
		return "", fmt.Errorf("codex: user scope requires dirs.AgentsHome to be set")
	}
	return d.AgentsHome, nil
}

// userConfigTomlFile returns <CodexHome>/config.toml — codex's
// user-scope target for mcp-server installs per schema/mcp-server.yaml's
// template.source_locations entry for host codex. CodexHome (NOT
// AgentsHome) — see package docstring on why these are distinct env
// vars.
func userConfigTomlFile(d dirs.Dirs) (string, error) {
	if d.CodexHome == "" {
		return "", fmt.Errorf("codex: user scope requires dirs.CodexHome to be set")
	}
	return filepath.Join(d.CodexHome, "config.toml"), nil
}

// projectConfigTomlFile returns <ProjectHome>/.codex/config.toml —
// codex's project-scope target for mcp-server and hook installs.
func projectConfigTomlFile(d dirs.Dirs) (string, error) {
	if d.ProjectHome == "" {
		return "", fmt.Errorf("codex: project scope requires dirs.ProjectHome to be set")
	}
	return filepath.Join(d.ProjectHome, ".codex", "config.toml"), nil
}

// Policy is the codex per-host filedrop policy (skill and rule kinds).
// KindAgent is INTENTIONALLY ABSENT from Layouts — codex CLI uses TOML
// files for agents (handled directly by Adapter.Plan -> planAgentCodex)
// rather than dropping raw frontmatter/markdown like skill and rule do.
// Mcp-server and hook live in configfragPolicy(); they're config-fragment
// kinds, not file-drop, so they ride a separate policy structure.
var Policy = filedrop.Policy{
	HostID: hostID,
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot:      userRoot,
			ProjectSubdir: ".agents",
			KindDir:       "skills",
			Nested:        true,
			NestedFile:    "SKILL.md",
		},
		resource.KindRule: {
			UserRoot:      userConfigRoot,
			ProjectSubdir: ".codex",
			KindDir:       "rules",
			Nested:        false,
		},
		resource.KindCommand: {
			UserRoot:      userConfigRoot,
			ProjectSubdir: ".codex",
			KindDir:       "commands",
			Nested:        false,
		},
		resource.KindMemory: {
			UserRoot:      userConfigRoot,
			ProjectSubdir: "",
			KindDir:       "",
			Nested:        false,
			PreserveName:  true,
		},
	},
}

func userConfigRoot(d dirs.Dirs) (string, error) {
	if d.CodexHome == "" {
		return "", fmt.Errorf("codex: user scope requires dirs.CodexHome to be set")
	}
	return d.CodexHome, nil
}

// emitMCPServerCodex turns a *resource.MCPServer into one (path, value)
// fragment the configfrag adapter merges into ~/.codex/config.toml. The
// path is TOML-dotted (`mcp_servers.<name>`) — codex's config.toml uses
// snake_case `mcp_servers` as the top-level table key per
// schema/mcp-server.yaml's source_locations.
//
// Value layout: universal core (command, args, url, env) PLUS the
// codex-superset extension fields verbatim from m.Extensions(). Per
// schema/mcp-server.yaml's ecosystem_notes + ADR-0010, codex's
// mcp-server schema is a SUPERSET of the cross-host common core; the
// codex adapter preserves extensions on round-trip (`enabled_tools`,
// `http_headers`, `bearer_token_env_var`, `tools.<id>.approval_mode`,
// etc.). Per-instance lossy detection (ADR-0003 addendum) is a
// non-codex adapter concern; codex emit is non-lossy by definition.
//
// Schema's `headers` vs `http_headers` normalization is INTENTIONALLY
// NOT done here. The schema names canonicalization as a TRANSLATOR
// concern (per ecosystem_notes: "Translator canonicalises on import"),
// not an adapter concern. The codex adapter passes whichever spelling
// arrived; the translator (when it lands) rewrites at the source-import
// boundary so this emit never sees the inconsistency.
//
// Output uses map[string]any so go-toml/v2's Marshal sorts keys
// lexicographically — same convention as emitMCPServer/applyJSONMergedKey,
// which keeps cacheKey's hash deterministic per the cacheKey
// docstring's "emit returns map[string]any" guard. orchestrator.writeTOML
// runs normalizeForTOML on the root before marshal to coerce integral
// float64 (e.g., codex-superset `startup_timeout_sec: 30` arriving from
// a JSON source) → int64, so the user sees `30` not `30.0` in their
// config.toml.
func emitMCPServerCodex(r resource.Resource) ([]configfrag.MergedFragment, error) {
	m, ok := r.(*resource.MCPServer)
	if !ok {
		return nil, fmt.Errorf("emit mcp-server: resource type %T is not *resource.MCPServer", r)
	}
	value := map[string]any{}
	if m.Command != "" {
		value["command"] = m.Command
	}
	if m.Args != nil {
		value["args"] = m.Args
	}
	if m.URL != "" {
		value["url"] = m.URL
	}
	if len(m.Env) > 0 {
		value["env"] = m.Env
	}
	for k, v := range m.Extensions() {
		// Universal-core field names take precedence — extensions
		// must not silently overwrite a typed field. The parser
		// already routes core fields to typed slots, so this branch
		// is defensive (a future ParseMCPServer that adds a new core
		// field whose name overlaps with an old extension key would
		// surface as a duplicate write to value[k] otherwise).
		if _, taken := value[k]; taken {
			continue
		}
		value[k] = v
	}
	return []configfrag.MergedFragment{{
		Path:  "mcp_servers." + m.Name,
		Value: value,
	}}, nil
}

// emitHookCodex turns a *resource.Hook into one MergedFragment per
// binding across all the resource's events. Op=Append so the
// orchestrator walker appends each binding to the host's hooks.<Event>
// array-of-tables rather than overwriting it; the manifest's sha256
// Selector for each fragment is computed by the orchestrator at install
// time and re-derived at uninstall to find the right array element
// regardless of sibling installs/reorders.
//
// Per-host divergence vs claudecode.emitHook: TOML-dotted Path
// ("hooks." + ev.Event, no `$.` prefix) — codex's config.toml uses TOML
// syntax, and parseTOMLPath rejects $-prefixed paths as a cross-format
// safety net. Event names and timeout units are identity with claudecode
// per schema/hook.yaml's cross_ecosystem_event_aliases (gemini-only
// rewrites; codex shares Claude's PascalCase + seconds convention).
//
// Event-name validation is NOT this function's responsibility — the
// canonical schema (schema/hook.yaml structure.top_level.canonical_event
// _names) is the authority and the validator gates upstream. Two
// codex-specific corpus notes the emit consumer should know about:
//   - PostToolUseFailure is observed in the codex corpus but is NOT in
//     the published Codex spec event-name table per schema/hook.yaml
//     ecosystem_notes. dotpack writes [[hooks.PostToolUseFailure]]
//     happily; codex's parser behavior on it is undocumented.
//   - The "in-the-wild non-spec TOML shapes" (flat `[hooks]` map and
//     anonymous `[[hooks]] event = '...'` AOT) are rejected on import
//     (preflightMergedKeyCollisions for the flat-map case;
//     appendJSONPath's intermediate-non-map check for the anonymous
//     AOT case). dotpack emits only the canonical nested shape.
//
// encodeHookBindingCodex + encodeHookSpecCodex are duplicated from the
// claudecode package rather than shared. The honest reason: ~30 LOC of
// bit-identical duplication is small enough to defer the
// lift-to-shared-package decision until a third caller appears. The
// duplication will silently rot if claude or codex adds a universal-
// core field the other doesn't — the lift to a shared helper is a
// single small slice when that happens, and waiting reduces the
// premature-abstraction risk.
//
// Selector hash stability: the emit's value (universal-core hook shape:
// string + int + map[string]string + []any-of-map[string]any) round-
// trips through TOML identically per the probe in
// orchestrator/probe_toml_aot_test.go. selectorFor over the un-
// normalized value at install time equals selectorFor over the TOML-
// roundtripped value at uninstall time, so un-merge-by-content-hash
// works without per-format hash adaptation.
func emitHookCodex(r resource.Resource) ([]configfrag.MergedFragment, error) {
	h, ok := r.(*resource.Hook)
	if !ok {
		return nil, fmt.Errorf("emit hook: resource type %T is not *resource.Hook", r)
	}
	if len(h.Events) == 0 {
		return nil, fmt.Errorf("emit hook: %s has no events", h.Name)
	}
	frags := make([]configfrag.MergedFragment, 0)
	for _, ev := range h.Events {
		for _, b := range ev.Bindings {
			frags = append(frags, configfrag.MergedFragment{
				Path:  "hooks." + ev.Event,
				Value: encodeHookBindingCodex(b),
				Op:    adapter.MergedKeyAppend,
			})
		}
	}
	return frags, nil
}

// encodeHookBindingCodex serialises one Binding into the on-disk shape
// codex's config.toml expects: { matcher, hooks: [...specs...] }.
// Identical to claudecode's encodeBinding today — see emitHookCodex
// docstring for why this is duplicated rather than shared.
//
// Output uses map[string]any (not a typed struct) so the value is
// format-agnostic — applyTOMLMergedKey serialises via go-toml/v2's
// Marshal which sorts map keys lexicographically. Same convention as
// emitMCPServerCodex, which keeps cacheKey's hash deterministic per
// the cacheKey docstring's "emit returns map[string]any" guard.
func encodeHookBindingCodex(b resource.Binding) map[string]any {
	out := map[string]any{}
	if b.Matcher != "" {
		out["matcher"] = b.Matcher
	}
	specs := make([]any, 0, len(b.Hooks))
	for _, s := range b.Hooks {
		specs = append(specs, encodeHookSpecCodex(s))
	}
	out["hooks"] = specs
	return out
}

// encodeHookSpecCodex serialises one HookSpec leaf. type + command
// always present; optional fields suppressed when absent so the on-disk
// TOML shape matches what a hand-author would have written (readability
// matters for a file the user diffs by hand).
//
// Field-name identity with claudecode: codex's hook-spec uses the same
// `type`, `command`, `timeout`, `statusMessage`, `env` field names per
// schema/hook.yaml structure.hook_spec.fields. The CamelCase quirk
// (`statusMessage`) is shared cross-host; the survey corpus confirms.
func encodeHookSpecCodex(s resource.HookSpec) map[string]any {
	out := map[string]any{
		"type":    s.Type,
		"command": hookcmd.ForHost(s.Command, hookcmd.ProjectRootExpr),
	}
	if s.HasTimeout {
		out["timeout"] = s.Timeout
	}
	if s.StatusMessage != "" {
		out["statusMessage"] = s.StatusMessage
	}
	if len(s.Env) > 0 {
		// map[string]string round-trips through TOML identically to
		// map[string]any with string values for selectorFor purposes:
		// json.Marshal of either form produces byte-identical bytes
		// when keys are sorted. The probe in orchestrator/
		// probe_toml_aot_test.go pins this. Mirror of claudecode.
		// encodeHookSpec's hostile-review #4 reasoning from slice v16.
		out["env"] = s.Env
	}
	return out
}

// configfragPolicy returns the codex configfrag policy. Function (not
// var) mirroring claudecode's pattern: nothing reads it via the package
// surface today, and promotion to var would suggest cross-package
// introspection that doesn't exist.
func configfragPolicy() configfrag.Policy {
	return configfrag.Policy{
		HostID: hostID,
		Kinds: map[resource.Kind]configfrag.KindConfig{
			resource.KindMCPServer: {
				Format: configfrag.FormatTOML,
				Files: configfrag.ScopeFiles{
					User:    userConfigTomlFile,
					Project: projectConfigTomlFile,
				},
				Emit: emitMCPServerCodex,
			},
			resource.KindHook: {
				Format: configfrag.FormatTOML,
				Files: configfrag.ScopeFiles{
					User:    userConfigTomlFile,
					Project: projectConfigTomlFile,
				},
				Emit: emitHookCodex,
			},
		},
	}
}

func planAgentCodex(r resource.Resource, scope adapter.Scope, d dirs.Dirs) (adapter.InstallPlan, error) {
	a, ok := r.(*resource.Agent)
	if !ok {
		return adapter.InstallPlan{}, fmt.Errorf("plan agent: resource type %T is not *resource.Agent", r)
	}

	var rootDir string
	var err error
	if scope == adapter.ScopeUser {
		rootDir, err = userConfigRoot(d)
		if err != nil {
			return adapter.InstallPlan{}, err
		}
	} else if scope == adapter.ScopeProject {
		if d.ProjectHome == "" {
			return adapter.InstallPlan{}, fmt.Errorf("codex: project scope requires dirs.ProjectHome to be set")
		}
		rootDir = filepath.Join(d.ProjectHome, ".codex")
	} else {
		return adapter.InstallPlan{}, fmt.Errorf("codex: unknown scope %q", scope)
	}

	targetDir := filepath.Join(rootDir, "agents")
	targetFile := filepath.Join(targetDir, a.Name+".toml")

	value := map[string]any{
		"name":                   a.Name,
		"description":            a.Description,
		"model":                  a.Model,
		"developer_instructions": a.Body,
	}

	if len(a.Tools) > 0 {
		value["skills"] = a.Tools
	}

	for k, v := range a.Extensions() {
		if _, taken := value[k]; taken {
			continue
		}
		value[k] = v
	}

	b, err := toml.Marshal(value)
	if err != nil {
		return adapter.InstallPlan{}, fmt.Errorf("codex: marshal agent: %w", err)
	}

	return adapter.InstallPlan{
		Files: []adapter.FileWrite{{
			Path:    targetFile,
			Content: b,
			Mode:    0644,
		}},
	}, nil
}

// Adapter is the codex per-host shell that dispatches Plan to the right
// deep module by resource Kind. Mirror of claudecode.Adapter; the
// per-host registries in internal/cli (adapterFactories) type these as
// adapter.Adapter so the concrete return type is transparent across
// the boundary.
type Adapter struct {
	dirs       dirs.Dirs
	filedrop   *filedrop.Adapter
	configfrag *configfrag.Adapter
}

// New constructs the codex adapter, wiring the package-level Policy
// (filedrop) with the given Dirs and constructing the configfrag
// adapter from configfragPolicy.
func New(d dirs.Dirs) *Adapter {
	return &Adapter{
		dirs:       d,
		filedrop:   filedrop.New(d, Policy),
		configfrag: configfrag.New(d, configfragPolicy()),
	}
}

// HostID returns the schema-side host alias.
func (a *Adapter) HostID() string { return hostID }

// Plan dispatches by Kind. File-drop kinds (skill, rule) go to the filedrop
// adapter; config-fragment kinds (mcp-server, hook) go to the
// configfrag adapter. Kinds neither adapter supports surface
// as a structured "not yet supported" error — the per-adapter Plan
// paths already produce that message, so we delegate.
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch r.Kind() {
	case resource.KindSkill, resource.KindRule, resource.KindCommand, resource.KindMemory:
		return a.filedrop.Plan(r, scope)
	case resource.KindAgent:
		return planAgentCodex(r, scope, a.dirs)
	case resource.KindMCPServer:
		return a.configfrag.Plan(r, scope)
	case resource.KindHook:
		plan, err := a.configfrag.Plan(r, scope)
		if err != nil {
			return adapter.InstallPlan{}, err
		}
		if err := rejectCodexHooksJSONConflict(plan); err != nil {
			return adapter.InstallPlan{}, err
		}
		return plan, nil
	default:
		return adapter.InstallPlan{}, fmt.Errorf("%s: kind %q not yet supported", hostID, r.Kind())
	}
}

func rejectCodexHooksJSONConflict(plan adapter.InstallPlan) error {
	for _, mk := range plan.MergedKeys {
		hooksJSONPath := filepath.Join(filepath.Dir(mk.File), "hooks.json")
		hasHooks, err := codexHooksJSONDefinesHooks(hooksJSONPath)
		if err != nil {
			return err
		}
		if hasHooks {
			return fmt.Errorf("codex: refusing hook install into %s because %s already defines hooks; Codex rejects hooks from both hooks.json and config.toml in the same layer, so remove or migrate hooks.json first", mk.File, hooksJSONPath)
		}
	}
	return nil
}

func codexHooksJSONDefinesHooks(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("codex: inspect %s: %w", path, err)
	}
	if len(raw) == 0 {
		return false, nil
	}

	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return false, fmt.Errorf("codex: %s exists but is not valid JSON; remove or fix it before installing hooks into config.toml: %w", path, err)
	}
	if hooks, ok := root["hooks"].(map[string]any); ok {
		return codexHookEventMapDefinesHooks(hooks), nil
	}

	for _, event := range []string{
		"preToolUse",
		"PreToolUse",
		"postToolUse",
		"PostToolUse",
		"postToolUseFailure",
		"PostToolUseFailure",
		"userPromptSubmit",
		"UserPromptSubmit",
		"stop",
		"Stop",
	} {
		if value, ok := root[event]; ok {
			if items, ok := value.([]any); ok && len(items) == 0 {
				continue
			}
			return true, nil
		}
	}

	return false, nil
}

func codexHookEventMapDefinesHooks(hooks map[string]any) bool {
	for _, value := range hooks {
		if codexHookEventValueDefinesHooks(value) {
			return true
		}
	}
	return false
}

func codexHookEventValueDefinesHooks(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	case string:
		return v != ""
	default:
		return true
	}
}
