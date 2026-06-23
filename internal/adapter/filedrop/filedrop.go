// Package filedrop is the deep Adapter implementation for hosts whose
// kinds install as file drops (standalone files, no config merging).
// Skill resources can emit SKILL.md plus packaged support files.
// claudecode, gemini, and codex are all file-drop hosts; each
// becomes a thin shell exporting a host-specific Policy + a New(d)
// constructor that returns a filedrop.Adapter wired with that policy.
// The per-host triplication of canPassThrough / reencode / planSkill /
// addScalar that this module replaces was the architecture review's
// card #1.
//
// The hook + mcp-server kinds (per ADR-0012 §5–§7) are NOT file-drop —
// they merge config fragments into existing JSON / TOML files. Those
// will live in a sibling adapter module when they land, not here.
package filedrop

import (
	"bytes"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pelletier/go-toml/v2"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/schema"
)

// ToolsShape selects how the agent's `tools` field is emitted in
// re-encoded frontmatter. Claude convention is comma-separated string;
// Gemini convention is YAML array. Codex doesn't support agent kind, so
// its policy's AgentToolsShape is zero-value (unused).
type ToolsShape int

const (
	// ToolsShapeUnused is the zero value — emit nothing for tools.
	// Policies that don't include KindAgent in Layouts leave this unset.
	ToolsShapeUnused ToolsShape = iota
	// ToolsCommaString emits `tools: a, b, c` (Claude convention).
	ToolsCommaString
	// ToolsYAMLArray emits `tools:\n    - a\n    - b\n    - c` (Gemini
	// convention).
	ToolsYAMLArray
)

// Layout is per-(host, kind) on-disk shape. The two layout patterns the
// corpus exhibits today:
//
//   - Nested (skill): <root>/<KindDir>/<name>/<NestedFile> plus optional
//     support files under the same per-name subdir. The host owns that
//     subdir; TargetDir is set so uninstall can reclaim it when empty.
//   - Flat (agent/rule/command/memory): <root>/<KindDir>/<name><ext>
//     — sibling resources share the dir; TargetDir is empty so uninstall
//     does NOT reclaim.
//
// Flat layouts default to ".md"; FlatExt overrides that for hosts like
// Gemini commands, and PreserveName writes a complete filename exactly
// for memory files.
type Layout struct {
	// UserRoot resolves the per-host user-scope root (e.g. ClaudeHome)
	// from the Dirs value. A function rather than a Dirs field name so
	// each policy declares the dirs accessor inline and the
	// missing-root error message carries the host-specific phrasing.
	// REQUIRED — filedrop.New panics if any Layout has UserRoot == nil.
	UserRoot func(dirs.Dirs) (string, error)
	// ProjectSubdir is the per-host subdirectory under ProjectHome for
	// project-scope installs (e.g. ".claude", ".gemini", ".agents").
	ProjectSubdir string
	// KindDir is the per-kind directory name under the scope root
	// (e.g. "skills", "agents"). Concatenated as <root>/<KindDir>/...
	KindDir string
	// Nested switches between the two on-disk patterns:
	//   true  → <root>/<KindDir>/<name>/<NestedFile>  (host owns subdir)
	//   false → <root>/<KindDir>/<name><ext>          (shared dir)
	Nested bool
	// NestedFile is the filename inside the per-name subdir when
	// Nested=true (e.g. "SKILL.md"). UNUSED when Nested=false — the
	// flat path hardcodes <name>.md unless FlatExt is set.
	NestedFile string
	// FlatExt overrides the default ".md" extension for flat layouts.
	FlatExt string
	// PreserveName writes the resource name exactly for flat layouts.
	// Memory resources use this because their host-native name is the
	// complete filename (CLAUDE.md, GEMINI.md, AGENTS.md, ...).
	PreserveName bool
}

// Policy is the per-host data the deep filedrop module dispatches on.
// HostID matches the schema-side host alias. Layouts is the kind →
// layout map; absent kinds are unsupported (the codex-no-agent case,
// generalised — map membership replaces a separate "supports?" flag).
// AgentToolsShape parametrizes the one genuine per-host emit
// divergence the corpus shows today.
type Policy struct {
	HostID          string
	Layouts         map[resource.Kind]Layout
	AgentToolsShape ToolsShape
}

// Adapter is the file-drop host adapter. Construct via New(d, policy);
// the policy is the only per-host data and is injected at construction
// so the same deep module can satisfy claudecode, gemini, and codex.
type Adapter struct {
	dirs   dirs.Dirs
	policy Policy
}

// New constructs the file-drop adapter for the given dirs + policy.
// Panics on policy misconfiguration (empty HostID, nil UserRoot, agent
// Layout without AgentToolsShape) — these are programmer bugs that
// would otherwise surface as inscrutable runtime errors on the user's
// first install. Validating here means a future host that ships with a
// broken policy fails at process start, when buildAdapter runs, not on
// the user's first tools-bearing agent install.
func New(d dirs.Dirs, p Policy) *Adapter {
	if p.HostID == "" {
		panic("filedrop: Policy.HostID is required")
	}
	for k, l := range p.Layouts {
		if l.UserRoot == nil {
			panic(fmt.Sprintf("filedrop: %s: Layouts[%q].UserRoot is nil — every Layout must define a user-scope root resolver", p.HostID, k))
		}
	}
	if _, hasAgent := p.Layouts[resource.KindAgent]; hasAgent && p.AgentToolsShape == ToolsShapeUnused {
		panic(fmt.Sprintf("filedrop: %s: Layouts contains KindAgent but AgentToolsShape is ToolsShapeUnused — set AgentToolsShape to ToolsCommaString or ToolsYAMLArray to declare agent emit shape", p.HostID))
	}
	return &Adapter{dirs: d, policy: p}
}

// HostID returns the policy's host ID — the schema-side alias adapters
// are keyed by.
func (a *Adapter) HostID() string { return a.policy.HostID }

// Plan returns the install plan for a resource. Dispatch is data-driven
// — Layouts membership decides which kinds this host supports; absent
// kinds return a structured error. The path is computed from the
// per-kind Layout entry; encoding is per-kind (skill = pass-through or
// re-encode; agent = always re-encode).
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	layout, ok := a.policy.Layouts[r.Kind()]
	if !ok {
		return adapter.InstallPlan{}, fmt.Errorf("%s: kind %q not yet supported", a.policy.HostID, r.Kind())
	}
	named, ok := r.(resource.Named)
	if !ok {
		return adapter.InstallPlan{}, fmt.Errorf("%s: kind %q has no name-derivation path", a.policy.HostID, r.Kind())
	}
	name := named.ResourceName()
	if memory, ok := r.(*resource.Memory); ok {
		name = a.memoryFilename(memory)
	}
	target, err := a.targetPath(layout, scope, name)
	if err != nil {
		return adapter.InstallPlan{}, err
	}
	content, err := a.encode(r)
	if err != nil {
		return adapter.InstallPlan{}, err
	}
	files := []adapter.FileWrite{{
		Path:    target,
		Content: content,
		Mode:    fs.FileMode(0o644),
	}}
	if skill, ok := r.(*resource.Skill); ok && layout.Nested {
		supportFiles, err := a.planSkillSupportFiles(skill, filepath.Dir(target))
		if err != nil {
			return adapter.InstallPlan{}, err
		}
		files = append(files, supportFiles...)
	}
	plan := adapter.InstallPlan{
		Files: files,
	}
	if rule, ok := r.(*resource.Rule); ok {
		plan.RemoveFiles = a.staleRuleFiles(rule, target, scope)
	}
	if layout.Nested {
		plan.TargetDir = filepath.Dir(target)
	}
	return plan, nil
}

func (a *Adapter) planSkillSupportFiles(s *resource.Skill, targetDir string) ([]adapter.FileWrite, error) {
	if len(s.SupportFiles) == 0 {
		return nil, nil
	}
	supportFiles := append([]resource.SupportFile(nil), s.SupportFiles...)
	sort.Slice(supportFiles, func(i, j int) bool { return supportFiles[i].RelPath < supportFiles[j].RelPath })

	seen := map[string]struct{}{"SKILL.md": {}}
	out := make([]adapter.FileWrite, 0, len(supportFiles))
	for _, sf := range supportFiles {
		rel, err := cleanSupportRelPath(sf.RelPath)
		if err != nil {
			return nil, fmt.Errorf("%s: skill support file %q: %w", a.policy.HostID, sf.RelPath, err)
		}
		relSlash := filepath.ToSlash(rel)
		if _, ok := seen[relSlash]; ok {
			return nil, fmt.Errorf("%s: duplicate skill support file target %q", a.policy.HostID, relSlash)
		}
		seen[relSlash] = struct{}{}
		mode := sf.Mode
		if mode == 0 {
			mode = 0o644
		}
		out = append(out, adapter.FileWrite{
			Path:    filepath.Join(targetDir, rel),
			Content: sf.Content,
			Mode:    mode,
		})
	}
	return out, nil
}

func cleanSupportRelPath(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("relative path is empty")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("relative path must not be absolute")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("relative path escapes the skill directory")
	}
	return clean, nil
}

// targetPath computes the on-disk target for a resource. Scope picks
// the root (UserRoot for user; <ProjectHome>/<ProjectSubdir> for
// project); Layout picks the per-kind shape (nested vs flat).
func (a *Adapter) targetPath(layout Layout, scope adapter.Scope, name string) (string, error) {
	var root string
	switch scope {
	case adapter.ScopeUser:
		r, err := layout.UserRoot(a.dirs)
		if err != nil {
			return "", err
		}
		root = r
	case adapter.ScopeProject:
		if a.dirs.ProjectHome == "" {
			return "", fmt.Errorf("%s: project scope requires dirs.ProjectHome to be set", a.policy.HostID)
		}
		root = filepath.Join(a.dirs.ProjectHome, layout.ProjectSubdir)
	default:
		return "", fmt.Errorf("%s: unknown scope %q", a.policy.HostID, scope)
	}
	if layout.Nested {
		return filepath.Join(root, layout.KindDir, name, layout.NestedFile), nil
	}
	if layout.PreserveName {
		if layout.KindDir == "" {
			return filepath.Join(root, name), nil
		}
		return filepath.Join(root, layout.KindDir, name), nil
	}
	ext := ".md"
	if layout.FlatExt != "" {
		ext = layout.FlatExt
	}
	if layout.KindDir == "" {
		return filepath.Join(root, name+ext), nil
	}
	return filepath.Join(root, layout.KindDir, name+ext), nil
}

// encode dispatches per-kind:
//   - Skill: byte-pass-through (Raw verbatim) when every extension key
//     passes schema.HostKeepsExtension; otherwise re-encode the
//     universal core + retained extensions.
//   - Agent: always re-encode (the tools-shape divergence means a
//     pass-through across hosts is unsafe — see claudecode.planAgent's
//     pre-consolidation comment for the rationale).
//
// All schema lookups go through schema.HostKeepsExtension (consolidated
// in card #2); the per-host emit divergences are encoded as Policy
// fields, not as branching code.
func (a *Adapter) encode(r resource.Resource) ([]byte, error) {
	switch v := r.(type) {
	case *resource.Skill:
		return a.encodeSkill(v)
	case *resource.Agent:
		return a.encodeAgent(v)
	case *resource.Rule:
		return a.encodeRule(v)
	case *resource.Command:
		return a.encodeCommand(v)
	case *resource.Memory:
		return a.encodeMemory(v)
	default:
		return nil, fmt.Errorf("%s: encode: kind %q has no encoder", a.policy.HostID, r.Kind())
	}
}

// encodeSkill emits SKILL.md bytes. ADR-0004 byte-identity: when Raw is
// set AND every extension would be kept by this host, ship Raw
// verbatim. Otherwise re-encode the universal core + retained
// extensions in deterministic order.
func (a *Adapter) encodeSkill(s *resource.Skill) ([]byte, error) {
	if len(s.Raw) > 0 {
		pass, err := a.canPassThrough(s)
		if err != nil {
			return nil, fmt.Errorf("%s: schema unavailable for pass-through check: %w", a.policy.HostID, err)
		}
		if pass {
			return s.Raw, nil
		}
	}
	return a.reencodeSkill(s)
}

// reencodeSkill emits SKILL.md frontmatter (name, description, optional
// license) + retained extensions in sorted order + body.
func (a *Adapter) reencodeSkill(s *resource.Skill) ([]byte, error) {
	front := []*yaml.Node{}
	var encodeErr error
	addScalar := mkAddScalar(&front, &encodeErr)

	addScalar("name", s.Name)
	addScalar("description", s.Description)
	if s.License != "" {
		addScalar("license", s.License)
	}

	if len(s.Extensions()) > 0 {
		sc, err := schema.Load(resource.KindSkill)
		if err != nil {
			return nil, fmt.Errorf("%s: schema unavailable for re-encode: %w", a.policy.HostID, err)
		}
		keys := sortedKeys(s.Extensions())
		for _, k := range keys {
			if !sc.HostKeepsExtension(a.policy.HostID, k) {
				continue
			}
			addScalar(k, s.Extensions()[k])
		}
	}

	if encodeErr != nil {
		return nil, encodeErr
	}
	return marshalFrontmatterAndBody(front, s.Body)
}

// encodeAgent emits agent frontmatter (name, description, optional
// model, tools per Policy.AgentToolsShape) + retained extensions +
// body. Always re-encodes; the tools-shape divergence makes
// pass-through unsafe across hosts.
func (a *Adapter) encodeAgent(ag *resource.Agent) ([]byte, error) {
	front := []*yaml.Node{}
	var encodeErr error
	addScalar := mkAddScalar(&front, &encodeErr)

	addScalar("name", ag.Name)
	addScalar("description", ag.Description)
	if ag.Model != "" {
		addScalar("model", ag.Model)
	}
	if len(ag.Tools) > 0 {
		switch a.policy.AgentToolsShape {
		case ToolsCommaString:
			addScalar("tools", strings.Join(ag.Tools, ", "))
		case ToolsYAMLArray:
			addScalar("tools", ag.Tools)
		default:
			return nil, fmt.Errorf("%s: agent layout present but AgentToolsShape unset on policy", a.policy.HostID)
		}
	}

	if len(ag.Extensions()) > 0 {
		sc, err := schema.Load(resource.KindAgent)
		if err != nil {
			return nil, fmt.Errorf("%s: schema unavailable for re-encode: %w", a.policy.HostID, err)
		}
		keys := sortedKeys(ag.Extensions())
		for _, k := range keys {
			if !sc.HostKeepsExtension(a.policy.HostID, k) {
				continue
			}
			addScalar(k, ag.Extensions()[k])
		}
	}

	if encodeErr != nil {
		return nil, encodeErr
	}
	return marshalFrontmatterAndBody(front, ag.Body)
}

// encodeRule emits Markdown rule bytes. Rule metadata is preserved
// byte-for-byte when Raw is available. If a caller constructs a Rule in
// memory, re-encode id/name plus schema-known pass-through metadata.
func (a *Adapter) encodeRule(r *resource.Rule) ([]byte, error) {
	if len(r.Raw) > 0 {
		pass, err := a.canPassThrough(r)
		if err != nil {
			return nil, fmt.Errorf("%s: schema unavailable for pass-through check: %w", a.policy.HostID, err)
		}
		if pass {
			return r.Raw, nil
		}
	}
	return a.reencodeRule(r)
}

func (a *Adapter) reencodeRule(r *resource.Rule) ([]byte, error) {
	front := []*yaml.Node{}
	var encodeErr error
	addScalar := mkAddScalar(&front, &encodeErr)

	if r.ID != "" {
		addScalar("id", r.ID)
	}
	if r.Name != "" {
		addScalar("name", r.Name)
	}

	if len(r.Extensions()) > 0 {
		sc, err := schema.Load(resource.KindRule)
		if err != nil {
			return nil, fmt.Errorf("%s: schema unavailable for re-encode: %w", a.policy.HostID, err)
		}
		keys := sortedKeys(r.Extensions())
		for _, k := range keys {
			if !sc.HostKeepsExtension(a.policy.HostID, k) {
				continue
			}
			addScalar(k, r.Extensions()[k])
		}
	}

	if encodeErr != nil {
		return nil, encodeErr
	}
	return marshalFrontmatterAndBody(front, r.Body)
}

func (a *Adapter) encodeCommand(c *resource.Command) ([]byte, error) {
	if len(c.Raw) > 0 && a.commandRawMatchesTarget(c) {
		pass, err := a.canPassThrough(c)
		if err != nil {
			return nil, fmt.Errorf("%s: schema unavailable for pass-through check: %w", a.policy.HostID, err)
		}
		if pass {
			return c.Raw, nil
		}
	}
	return a.reencodeCommand(c)
}

func (a *Adapter) commandRawMatchesTarget(c *resource.Command) bool {
	sourceIsMarkdown := bytes.HasPrefix(c.Raw, []byte("---"))
	targetIsTOML := a.policy.Layouts[resource.KindCommand].FlatExt == ".toml"
	return sourceIsMarkdown != targetIsTOML
}

func (a *Adapter) reencodeCommand(c *resource.Command) ([]byte, error) {
	front := []*yaml.Node{}
	var encodeErr error
	addScalar := mkAddScalar(&front, &encodeErr)

	if c.Description != "" {
		addScalar("description", c.Description)
	}
	if c.Model != "" {
		addScalar("model", c.Model)
	}
	if len(c.AllowedTools) > 0 {
		switch a.policy.AgentToolsShape {
		case ToolsCommaString:
			addScalar("allowed-tools", strings.Join(c.AllowedTools, ", "))
		case ToolsYAMLArray:
			addScalar("allowed-tools", c.AllowedTools)
		default:
			// Fallback: command formatting isn't explicitly defined, use comma string if ToolsShapeUnused
			if a.policy.HostID == "gemini-cli" {
				addScalar("allowed-tools", c.AllowedTools)
			} else {
				addScalar("allowed-tools", strings.Join(c.AllowedTools, ", "))
			}
		}
	}

	if len(c.Extensions()) > 0 {
		sc, err := schema.Load(resource.KindCommand)
		if err != nil {
			return nil, fmt.Errorf("%s: schema unavailable for re-encode: %w", a.policy.HostID, err)
		}
		keys := sortedKeys(c.Extensions())
		for _, k := range keys {
			if !sc.HostKeepsExtension(a.policy.HostID, k) {
				continue
			}
			addScalar(k, c.Extensions()[k])
		}
	}

	if encodeErr != nil {
		return nil, encodeErr
	}

	if a.policy.HostID == "gemini-cli" {
		tomlMap := map[string]any{
			"prompt": c.Prompt,
		}
		if c.Description != "" {
			tomlMap["description"] = c.Description
		}
		if c.Model != "" {
			tomlMap["model"] = c.Model
		}
		if len(c.AllowedTools) > 0 {
			tomlMap["allowed-tools"] = c.AllowedTools
		}
		for k, v := range c.Extensions() {
			tomlMap[k] = v
		}
		return toml.Marshal(tomlMap)
	}

	return marshalFrontmatterAndBody(front, c.Prompt)
}

func (a *Adapter) encodeMemory(m *resource.Memory) ([]byte, error) {
	if len(m.Raw) > 0 {
		return m.Raw, nil
	}
	return []byte(m.Body), nil
}

func (a *Adapter) memoryFilename(m *resource.Memory) string {
	switch a.policy.HostID {
	case "claude-code":
		return "CLAUDE.md"
	case "gemini-cli":
		return "GEMINI.md"
	case "antigravity-cli":
		return "ANTIGRAVITY.md"
	case "codex":
		return "AGENTS.md"
	default:
		if m.Name != "" {
			return m.Name
		}
		return "AGENTS.md"
	}
}

func (a *Adapter) staleRuleFiles(r *resource.Rule, target string, scope adapter.Scope) []adapter.FileRemove {
	if scope != adapter.ScopeProject || a.dirs.ProjectHome == "" || r.SourcePath == "" {
		return nil
	}
	source, err := filepath.Abs(r.SourcePath)
	if err != nil {
		source = r.SourcePath
	}
	canonicalDir := filepath.Join(a.dirs.ProjectHome, ".agents", "rules")
	sourceDir := filepath.Dir(source)
	if sourceDir != canonicalDir {
		return nil
	}

	staleHosts := []string{"claude", "claude-code", "codex", "gemini", "gemini-cli", "agents-cli"}
	out := make([]adapter.FileRemove, 0, len(staleHosts))
	for _, host := range staleHosts {
		p := filepath.Join(canonicalDir, host, r.NameOrID()+".md")
		if p == source || p == target {
			continue
		}
		out = append(out, adapter.FileRemove{Path: p})
	}
	return out
}

// canPassThrough reports whether emitting Raw bytes verbatim would
// drop nothing of semantic value on this host. True iff every
// extension key passes schema.HostKeepsExtension.
func (a *Adapter) canPassThrough(r resource.Resource) (bool, error) {
	ext := r.Extensions()
	if len(ext) == 0 {
		return true, nil
	}
	sc, err := schema.Load(r.Kind())
	if err != nil {
		return false, err
	}
	for k := range ext {
		if !sc.HostKeepsExtension(a.policy.HostID, k) {
			return false, nil
		}
	}
	return true, nil
}

// mkAddScalar returns the addScalar closure that the three reencode
// loops (Skill, Agent, future kinds) share. Value is encoded FIRST so
// front[] never holds a dangling key on encode failure — atomicity
// preserved across the pair regardless of what surrounding code does
// at the return path. Single source for the brittle-pair fix that the
// three per-host packages previously each carried.
func mkAddScalar(front *[]*yaml.Node, encodeErr *error) func(string, any) {
	return func(key string, val any) {
		valNode := &yaml.Node{}
		if err := valNode.Encode(val); err != nil {
			*encodeErr = fmt.Errorf("encode key %q: %w", key, err)
			return
		}
		*front = append(*front, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, valNode)
	}
}

// sortedKeys returns the map's keys in lexical order — yaml.Marshal on
// a map iterates randomly, so the re-encode path builds an ordered
// MappingNode to keep output stable for diffing and cache_key
// reproducibility.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// marshalFrontmatterAndBody wraps the ordered frontmatter nodes with
// the `---` delimiters and appends the body. Single source for the
// "marshal MappingNode + delimit + append body" sequence that the
// three per-host packages previously each carried.
func marshalFrontmatterAndBody(front []*yaml.Node, body string) ([]byte, error) {
	mapNode := yaml.Node{Kind: yaml.MappingNode, Content: front}
	frontBytes, err := yaml.Marshal(&mapNode)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(frontBytes)
	buf.WriteString("---\n")
	buf.WriteString(body)
	return buf.Bytes(), nil
}
