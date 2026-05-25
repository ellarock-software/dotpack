// Package gemini is the gemini-cli Adapter implementation for ADR-0016.
// Skill + agent are Native. Skills nest under <root>/skills/<name>/SKILL.md
// (gemini-cli owns the per-name dir); agents are flat
// <root>/agents/<name>.md (shared with sibling agents and user-authored
// content, gemini-cli does not own the dir).
//
// Convergence note: per schema/skill.yaml ecosystem_notes, gemini-cli
// ALSO reads the shared `~/.agents/skills/` path for the skill kind.
// Codex (slice 3 task #8) WRITES to that shared path — it's codex's
// only documented native skill root per developers.openai.com/codex/skills.
// gemini-cli deliberately writes to its host-specific GeminiHome/skills/
// instead so that `--agent gemini-cli` and `--agent codex` produce
// distinct manifest-tracked targets today; the gemini-cli runtime still
// picks up the codex-installed skill via its read-side convergence.
// The future `--agent agents-cli` umbrella flag (ADR-0016 §1) will
// special-case writing to ~/.agents/skills/ ONCE for both hosts; that
// IS a future collision with `--agent codex` at the same path, owned
// by the umbrella's CLI-flag-to-adapter-set logic when it lands. For
// the agent kind, per schema/agent.yaml ecosystem_notes, `.agents/agents/`
// is NOT yet honoured by either Claude or Gemini — agents-cli must fan
// out to each host's native agents/ dir until that changes. (Codex's
// agent capability is documented authoritatively in package codex.)
package gemini

import (
	"bytes"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
	"github.com/ellarock/dotpack/schema"
)

// hostID is the dotpack adapter HostID for gemini-cli. MUST match the
// `host:` strings in schema/*.yaml aliases — LossyExtensions compares on
// equality, so a mismatch silently flips gemini-native concepts to lossy
// on their own host.
const hostID = "gemini-cli"

// Adapter is the gemini-cli host adapter. Construct via New(dirs); the
// Dirs value is the only injected state so tests can target a tempdir
// instead of the user's real ~/.gemini/.
type Adapter struct {
	dirs dirs.Dirs
}

// New constructs the gemini-cli adapter with explicit Dirs.
func New(d dirs.Dirs) *Adapter {
	return &Adapter{dirs: d}
}

// HostID returns "gemini-cli".
func (*Adapter) HostID() string { return hostID }

// Capabilities returns the per-kind ratings from ADR-0007's matrix.
// Skill + agent are Native; other kinds default to Unsupported and
// will be promoted as their per-kind work lands.
func (*Adapter) Capabilities() adapter.KindCapabilityMatrix {
	return adapter.KindCapabilityMatrix{
		resource.KindSkill: adapter.Native,
		resource.KindAgent: adapter.Native,
	}
}

// Plan returns the install plan for a Resource. Dispatch by Kind() —
// per-kind logic per ADR-0016 §3 (typed structs, not a generic walker).
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch v := r.(type) {
	case *resource.Skill:
		return a.planSkill(v, scope)
	case *resource.Agent:
		return a.planAgent(v, scope)
	default:
		return adapter.InstallPlan{}, fmt.Errorf("gemini-cli: kind %q not yet supported", r.Kind())
	}
}

// planSkill produces the install plan for one skill. Per
// schema/skill.yaml ecosystem_notes, gemini-cli's native skill location
// is <root>/skills/<name>/SKILL.md — same on-disk layout as claude-code,
// different root (.gemini vs .claude). The ~/.agents/ shared path is
// agents-cli's concern; this adapter writes the native path.
func (a *Adapter) planSkill(s *resource.Skill, scope adapter.Scope) (adapter.InstallPlan, error) {
	content, err := encodeSkill(s)
	if err != nil {
		return adapter.InstallPlan{}, fmt.Errorf("gemini-cli: encode skill %q: %w", s.Name, err)
	}

	target, err := skillTarget(a.dirs, scope, s.Name)
	if err != nil {
		return adapter.InstallPlan{}, err
	}

	return adapter.InstallPlan{
		Files: []adapter.FileWrite{{
			Path:    target,
			Content: content,
			Mode:    fs.FileMode(0o644),
		}},
		// Skills own their per-name subdirectory: <root>/skills/<name>/
		// holds SKILL.md plus optional scripts/, references/, assets/.
		// orchestrator.Uninstall reclaims the dir with one os.Remove
		// when it becomes empty (mirror claudecode.planSkill).
		TargetDir: filepath.Dir(target),
	}, nil
}

// skillTarget computes the on-disk path for a skill at the given scope.
// User scope: <GeminiHome>/skills/<name>/SKILL.md. Project scope:
// <ProjectHome>/.gemini/skills/<name>/SKILL.md. Both absolute.
func skillTarget(d dirs.Dirs, scope adapter.Scope, name string) (string, error) {
	switch scope {
	case adapter.ScopeUser:
		if d.GeminiHome == "" {
			return "", fmt.Errorf("gemini-cli: user scope requires dirs.GeminiHome to be set")
		}
		return filepath.Join(d.GeminiHome, "skills", name, "SKILL.md"), nil
	case adapter.ScopeProject:
		if d.ProjectHome == "" {
			return "", fmt.Errorf("gemini-cli: project scope requires dirs.ProjectHome to be set")
		}
		return filepath.Join(d.ProjectHome, ".gemini", "skills", name, "SKILL.md"), nil
	default:
		return "", fmt.Errorf("gemini-cli: unknown scope %q", scope)
	}
}

// encodeSkill produces the SKILL.md bytes the orchestrator will write.
// Same byte-pass-through-when-safe logic as claudecode: if the source
// bytes are available (Raw set) and no extension would be dropped on
// gemini-cli per §8, ship Raw verbatim. Otherwise re-encode.
func encodeSkill(s *resource.Skill) ([]byte, error) {
	if len(s.Raw) > 0 {
		pass, err := canPassThrough(s)
		if err != nil {
			return nil, fmt.Errorf("gemini-cli: schema unavailable for pass-through check: %w", err)
		}
		if pass {
			return s.Raw, nil
		}
	}
	return reencodeSkill(s)
}

// planAgent produces the install plan for one agent. Per ADR-0010 the
// target is <root>/agents/<name>.md — flat file, not a per-name subdir.
// TargetDir intentionally empty: agents/ is shared with sibling agents
// and user-authored content; orchestrator.Uninstall must NOT reclaim it.
//
// Always re-encodes, never byte-pass-through — same trade as claudecode,
// inverse direction. The schema accepts tools in two shapes; ParseAgent
// normalises to []string; re-encode always emits gemini-cli's preferred
// YAML-array form. Cost: agent loses ADR-0008 byte-identity. Trade: a
// claude-shaped source landing on gemini-cli emits tools in the shape
// gemini-cli actually parses.
func (a *Adapter) planAgent(ag *resource.Agent, scope adapter.Scope) (adapter.InstallPlan, error) {
	target, err := agentTarget(a.dirs, scope, ag.Name)
	if err != nil {
		return adapter.InstallPlan{}, err
	}
	content, err := reencodeAgent(ag)
	if err != nil {
		return adapter.InstallPlan{}, fmt.Errorf("gemini-cli: encode agent %q: %w", ag.Name, err)
	}
	return adapter.InstallPlan{
		Files: []adapter.FileWrite{{
			Path:    target,
			Content: content,
			Mode:    fs.FileMode(0o644),
		}},
		// TargetDir intentionally empty — agents/ is shared.
	}, nil
}

// agentTarget computes the on-disk path for an agent at the given scope.
// Symmetric with skillTarget — both scopes return absolute paths.
func agentTarget(d dirs.Dirs, scope adapter.Scope, name string) (string, error) {
	switch scope {
	case adapter.ScopeUser:
		if d.GeminiHome == "" {
			return "", fmt.Errorf("gemini-cli: user scope requires dirs.GeminiHome to be set")
		}
		return filepath.Join(d.GeminiHome, "agents", name+".md"), nil
	case adapter.ScopeProject:
		if d.ProjectHome == "" {
			return "", fmt.Errorf("gemini-cli: project scope requires dirs.ProjectHome to be set")
		}
		return filepath.Join(d.ProjectHome, ".gemini", "agents", name+".md"), nil
	default:
		return "", fmt.Errorf("gemini-cli: unknown scope %q", scope)
	}
}

// reencodeAgent emits gemini-cli-shaped agent frontmatter. Tools is
// emitted as a YAML array (Gemini's documented form per
// schema/agent.yaml notes), NOT a comma-separated string. This is the
// inverse of claudecode.reencodeAgent's coercion direction.
func reencodeAgent(ag *resource.Agent) ([]byte, error) {
	front := []*yaml.Node{}
	var encodeErr error
	addScalar := func(key string, val any) {
		// Encode value FIRST, then append (key, value) atomically. Same
		// brittle-pair fix as claudecode.reencodeAgent — a dangling key
		// in front[] would render as a malformed mapping if the
		// encodeErr guard ever got bypassed.
		valNode := &yaml.Node{}
		if err := valNode.Encode(val); err != nil {
			encodeErr = fmt.Errorf("encode key %q: %w", key, err)
			return
		}
		front = append(front, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, valNode)
	}

	addScalar("name", ag.Name)
	addScalar("description", ag.Description)
	if ag.Model != "" {
		addScalar("model", ag.Model)
	}
	if len(ag.Tools) > 0 {
		// Gemini-cli's preferred tools shape is a YAML array
		// (schema/agent.yaml `tools` field notes: Gemini uses
		// `["read_file", "grep_search"]`). Passing the []string directly
		// to yaml.Node.Encode produces a sequence node — the inverse of
		// claudecode's strings.Join("...").
		addScalar("tools", ag.Tools)
	}

	if len(ag.Extensions()) > 0 {
		sc, err := schema.Load(resource.KindAgent)
		if err != nil {
			return nil, fmt.Errorf("gemini-cli: schema unavailable for re-encode: %w", err)
		}
		keys := make([]string, 0, len(ag.Extensions()))
		for k := range ag.Extensions() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !geminiKeeps(sc, k) {
				continue
			}
			addScalar(k, ag.Extensions()[k])
		}
	}

	if encodeErr != nil {
		return nil, encodeErr
	}

	mapNode := yaml.Node{Kind: yaml.MappingNode, Content: front}
	frontBytes, err := yaml.Marshal(&mapNode)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(frontBytes)
	buf.WriteString("---\n")
	buf.WriteString(ag.Body)
	return buf.Bytes(), nil
}

// canPassThrough mirrors claudecode.canPassThrough's shape for the
// gemini-cli host. Kept duplicated rather than promoted to a shared
// package — only one parameter (the host string) differs, but the
// extraction shape is better decided after the third adapter (codex)
// shows whether the seam wants a HostID-parameter helper or richer
// per-host emit policies. Per advisor: "Two hosts isn't data; it's two
// cases. Wait for codex."
func canPassThrough(r resource.Resource) (bool, error) {
	ext := r.Extensions()
	if len(ext) == 0 {
		return true, nil
	}
	sc, err := schema.Load(r.Kind())
	if err != nil {
		return false, err
	}
	for k := range ext {
		if !geminiKeeps(sc, k) {
			return false, nil
		}
	}
	return true, nil
}

// geminiKeeps reports whether gemini-cli's emit should retain the
// extension keyed by fieldName. True if the schema lists gemini-cli
// under the matching concept's aliases, or if the concept is
// pass-through metadata (lossy_when_dropped: false). False for unknown
// fields — those surface via the orchestrator's §8 lossy check.
func geminiKeeps(sc *schema.Schema, fieldName string) bool {
	c := sc.LookupExtension(fieldName)
	if c == nil {
		return false
	}
	if !c.IsLossyWhenDropped() {
		return true
	}
	return c.SupportsHost(hostID)
}

func reencodeSkill(s *resource.Skill) ([]byte, error) {
	// Universal core first (fixed order: name, description, license).
	// Then sorted retained extensions for deterministic output. Same
	// shape as claudecode.reencodeSkill — only geminiKeeps differs.
	front := []*yaml.Node{}
	var encodeErr error
	addScalar := func(key string, val any) {
		// Encode value FIRST, then append (key, value) as a pair.
		// Appending the key before encoding would leave front[] with
		// a dangling key on encode failure — a malformed mapping if
		// the encodeErr guard ever gets bypassed. Mirror of
		// claudecode.reencodeSkill's atomicity; both copies fixed
		// identically in slice 3 task #6 hostile-review fold.
		valNode := &yaml.Node{}
		if err := valNode.Encode(val); err != nil {
			encodeErr = fmt.Errorf("encode key %q: %w", key, err)
			return
		}
		front = append(front, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, valNode)
	}
	addScalar("name", s.Name)
	addScalar("description", s.Description)
	if s.License != "" {
		addScalar("license", s.License)
	}

	if len(s.Extensions()) > 0 {
		sc, err := schema.Load(resource.KindSkill)
		if err != nil {
			return nil, fmt.Errorf("gemini-cli: schema unavailable for re-encode: %w", err)
		}
		keys := make([]string, 0, len(s.Extensions()))
		for k := range s.Extensions() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !geminiKeeps(sc, k) {
				continue
			}
			addScalar(k, s.Extensions()[k])
		}
	}

	if encodeErr != nil {
		return nil, encodeErr
	}

	mapNode := yaml.Node{Kind: yaml.MappingNode, Content: front}
	frontBytes, err := yaml.Marshal(&mapNode)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(frontBytes)
	buf.WriteString("---\n")
	buf.WriteString(s.Body)
	return buf.Bytes(), nil
}
