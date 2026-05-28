// Package filedrop is the deep Adapter implementation for hosts whose
// kinds install as file drops (one file written per resource, no config
// merging). claudecode, gemini, and codex are all file-drop hosts; each
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

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
	"github.com/ellarock/dotpack/schema"
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
//   - Nested (skill): <root>/<KindDir>/<name>/<NestedFile> — host
//     owns the per-name subdir; TargetDir is set so uninstall can
//     reclaim it.
//   - Flat (agent/rule): <root>/<KindDir>/<name>.md — sibling resources
//     share the dir; TargetDir is empty so uninstall does NOT reclaim.
//
// The flat-layout file extension is hardcoded ".md" — every corpus
// kind today uses .md. A future kind requiring a different extension
// adds a FlatExt field; until then, hardcoding keeps the Layout API
// minimal.
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
	//   false → <root>/<KindDir>/<name>.md            (shared dir)
	Nested bool
	// NestedFile is the filename inside the per-name subdir when
	// Nested=true (e.g. "SKILL.md"). UNUSED when Nested=false — the
	// flat path hardcodes <name>.md.
	NestedFile string
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
	target, err := a.targetPath(layout, scope, named.ResourceName())
	if err != nil {
		return adapter.InstallPlan{}, err
	}
	content, err := a.encode(r)
	if err != nil {
		return adapter.InstallPlan{}, err
	}
	plan := adapter.InstallPlan{
		Files: []adapter.FileWrite{{
			Path:    target,
			Content: content,
			Mode:    fs.FileMode(0o644),
		}},
	}
	if rule, ok := r.(*resource.Rule); ok {
		plan.RemoveFiles = a.staleRuleFiles(rule, target, scope)
	}
	if layout.Nested {
		plan.TargetDir = filepath.Dir(target)
	}
	return plan, nil
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
	return filepath.Join(root, layout.KindDir, name+".md"), nil
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
