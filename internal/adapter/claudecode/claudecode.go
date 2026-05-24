// Package claudecode is the claude-code Adapter implementation for
// ADR-0016. Skill + agent are Native; command / memory / hook /
// mcp-server are added as their per-kind work lands. Skills nest
// under <root>/skills/<name>/SKILL.md (dotpack owns the per-name dir);
// agents are flat <root>/agents/<name>.md (dotpack does not own the
// shared agents/ dir).
package claudecode

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

// hostID is the dotpack adapter HostID for claude-code. Schema lookups
// (ADR-0016 §8) key by this — kept as a package-level const so the
// schema package and the adapter agree on spelling without a string
// literal floating in both places.
const hostID = "claude-code"

// Adapter is the claude-code host adapter. Construct via New(dirs); the
// Dirs value is the only injected state so tests can target a tempdir
// instead of the user's real ~/.claude/.
type Adapter struct {
	dirs dirs.Dirs
}

// New constructs the claude-code adapter with explicit Dirs. Per advisor
// guidance, no os.UserHomeDir() at call sites — dirs is populated once
// from env in main() and threaded through.
func New(d dirs.Dirs) *Adapter {
	return &Adapter{dirs: d}
}

// HostID returns "claude-code".
func (*Adapter) HostID() string { return hostID }

// Capabilities returns the per-kind ratings from ADR-0007's matrix.
// Skill + agent are Native; other kinds default to Unsupported
// (zero-value of CapabilityLevel) and will be promoted as their
// per-kind work lands.
func (*Adapter) Capabilities() adapter.KindCapabilityMatrix {
	return adapter.KindCapabilityMatrix{
		resource.KindSkill: adapter.Native,
		resource.KindAgent: adapter.Native,
	}
}

// Plan returns the install plan for a Resource. Dispatch is by Kind();
// per-kind logic per ADR-0016 §3 (typed structs, not a generic walker).
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch v := r.(type) {
	case *resource.Skill:
		return a.planSkill(v, scope)
	case *resource.Agent:
		return a.planAgent(v, scope)
	default:
		return adapter.InstallPlan{}, fmt.Errorf("claude-code: kind %q not yet supported", r.Kind())
	}
}

// planSkill produces the install plan for one skill. Per ADR-0009 the
// target is <root>/skills/<name>/SKILL.md, where <root> is dirs.ClaudeHome
// (user scope) or dirs.ProjectHome/.claude (project scope; both are
// absolute paths post slice 2 task #2).
func (a *Adapter) planSkill(s *resource.Skill, scope adapter.Scope) (adapter.InstallPlan, error) {
	content, err := encodeSkill(s)
	if err != nil {
		return adapter.InstallPlan{}, fmt.Errorf("claude-code: encode skill %q: %w", s.Name, err)
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
		// when it becomes empty.
		TargetDir: filepath.Dir(target),
	}, nil
}

// skillTarget computes the on-disk path for a skill at the given scope.
// Both scopes return absolute paths — project scope is rooted at
// dirs.ProjectHome (resolved at FromEnv time from DOTPACK_PROJECT_HOME
// or CWD) so the manifest record's paths survive uninstall/list from
// a different CWD. Absent ProjectHome under ScopeProject is a hard
// error rather than a silent fallback to CWD — the adapter is a pure
// function of its inputs.
func skillTarget(d dirs.Dirs, scope adapter.Scope, name string) (string, error) {
	switch scope {
	case adapter.ScopeUser:
		if d.ClaudeHome == "" {
			return "", fmt.Errorf("claude-code: user scope requires dirs.ClaudeHome to be set")
		}
		return filepath.Join(d.ClaudeHome, "skills", name, "SKILL.md"), nil
	case adapter.ScopeProject:
		if d.ProjectHome == "" {
			return "", fmt.Errorf("claude-code: project scope requires dirs.ProjectHome to be set")
		}
		return filepath.Join(d.ProjectHome, ".claude", "skills", name, "SKILL.md"), nil
	default:
		return "", fmt.Errorf("claude-code: unknown scope %q", scope)
	}
}

// encodeSkill produces the SKILL.md bytes the orchestrator will write.
//
// Per ADR-0008, drop-file resources install byte-identical to their
// cache copy. The byte-pass-through path fires when (a) the Skill was
// parsed from source bytes (Raw is set) and (b) no Extension on this
// resource would need to be dropped on claude-code per ADR-0016 §8.
// "Drop" means the schema flags the field as lossy on a non-supporting
// host; for claude-code the runtime-overrides concept (allowed-tools,
// model, etc.) lists claude-code in its aliases, so a SKILL.md
// carrying those fields passes through unchanged.
//
// The re-encode fallback fires for synthesised Skills (no Raw — e.g.
// translator output or cache misses) and for parsed Skills whose
// extensions include fields the schema marks as droppable on
// claude-code. The latter only happens after the orchestrator has
// accepted --allow-lossy, so producing a different file is the
// expected behaviour. Extension keys we DO keep (claude-supported or
// pass-through metadata) are sorted and merged with the universal
// core; yaml.Marshal on a map is non-deterministic, so a sorted []Node
// approach is used to keep output stable for diffing and cache_key
// reproducibility.
func encodeSkill(s *resource.Skill) ([]byte, error) {
	if len(s.Raw) > 0 {
		pass, err := canPassThrough(s)
		if err != nil {
			return nil, fmt.Errorf("claude-code: schema unavailable for pass-through check: %w", err)
		}
		if pass {
			return s.Raw, nil
		}
	}
	return reencodeSkill(s)
}

// planAgent produces the install plan for one agent. Per ADR-0010 the
// target is <root>/agents/<name>.md — a FLAT file in the agents/ dir,
// NOT a per-name subdirectory (skills nest, agents don't). TargetDir
// is intentionally empty: agents/ is a shared directory holding
// sibling agents (other dotpack installs + user-authored agents)
// which orchestrator.Uninstall must NOT reclaim.
//
// Always re-encodes, never byte-pass-through. The schema accepts tools
// in two shapes (comma-separated string per Claude convention, YAML
// array per Gemini convention) and ParseAgent normalises to []string.
// Pass-through would ship the source's shape unchanged, which on a
// Gemini-shaped source landing on claude-code is unverified to load
// — install would succeed silently with no tools at runtime. Cost: one
// kind loses ADR-0008's byte-pass-through guarantee. Trade: tools is
// always in the host's preferred form.
func (a *Adapter) planAgent(ag *resource.Agent, scope adapter.Scope) (adapter.InstallPlan, error) {
	target, err := agentTarget(a.dirs, scope, ag.Name)
	if err != nil {
		return adapter.InstallPlan{}, err
	}
	content, err := reencodeAgent(ag)
	if err != nil {
		return adapter.InstallPlan{}, fmt.Errorf("claude-code: encode agent %q: %w", ag.Name, err)
	}
	return adapter.InstallPlan{
		Files: []adapter.FileWrite{{
			Path:    target,
			Content: content,
			Mode:    fs.FileMode(0o644),
		}},
		// TargetDir intentionally empty — agents/ is shared. See type
		// docstring on adapter.InstallPlan.TargetDir.
	}, nil
}

// agentTarget computes the on-disk path for an agent at the given scope.
// Symmetric with skillTarget: user scope → ClaudeHome/agents/<name>.md;
// project scope → ProjectHome/.claude/agents/<name>.md; both absolute.
func agentTarget(d dirs.Dirs, scope adapter.Scope, name string) (string, error) {
	switch scope {
	case adapter.ScopeUser:
		if d.ClaudeHome == "" {
			return "", fmt.Errorf("claude-code: user scope requires dirs.ClaudeHome to be set")
		}
		return filepath.Join(d.ClaudeHome, "agents", name+".md"), nil
	case adapter.ScopeProject:
		if d.ProjectHome == "" {
			return "", fmt.Errorf("claude-code: project scope requires dirs.ProjectHome to be set")
		}
		return filepath.Join(d.ProjectHome, ".claude", "agents", name+".md"), nil
	default:
		return "", fmt.Errorf("claude-code: unknown scope %q", scope)
	}
}

// reencodeAgent emits the agent's frontmatter (name, description,
// optional model, optional tools) and body. Tools is always written as
// a comma-separated string (5/5 corpus presence; Claude's convention).
// Retained extensions are sorted for deterministic output, same as
// reencodeSkill — diffability and cache-key stability are universal
// requirements of the re-encode path.
func reencodeAgent(ag *resource.Agent) ([]byte, error) {
	front := []*yaml.Node{}
	var encodeErr error
	addScalar := func(key string, val any) {
		// Encode value FIRST. Appending the key before encoding leaves
		// the front[] slice with a dangling key on encode failure, which
		// the marshalled YAML would render as a malformed mapping if the
		// encodeErr guard at the end ever gets bypassed. Pair atomicity
		// here means the partial state can't escape regardless of what
		// future code does at the return path.
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
		addScalar("tools", strings.Join(ag.Tools, ", "))
	}

	if len(ag.Extensions()) > 0 {
		sc, err := schema.Load(resource.KindAgent)
		if err != nil {
			return nil, fmt.Errorf("claude-code: schema unavailable for re-encode: %w", err)
		}
		keys := make([]string, 0, len(ag.Extensions()))
		for k := range ag.Extensions() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !claudeKeeps(sc, k) {
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

// canPassThrough reports whether emitting Raw bytes verbatim would
// drop nothing of semantic value on claude-code. True iff every
// extension key resolves (via the schema) to either a claude-code-
// supported concept or pass-through metadata.
//
// Generic over Resource: uses r.Kind() to select the schema and
// r.Extensions() to enumerate keys. Kept here (rather than promoting
// to the orchestrator) because the "should this be kept?" decision is
// adapter-side — the orchestrator's §8 algorithm only answers "is this
// lossy?" which is a complementary question with a different default.
//
// Schema-load failure here is propagated to the caller, not swallowed.
// The embedded YAML cannot fail in production, but if it ever does,
// the install must fail loudly rather than silently re-encoding with
// an unknown-to-us extension set.
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
		if !claudeKeeps(sc, k) {
			return false, nil
		}
	}
	return true, nil
}

// claudeKeeps reports whether claude-code's emit should retain the
// extension keyed by fieldName. True if the schema lists claude-code
// under the matching concept's aliases, or if the concept is
// pass-through metadata (lossy_when_dropped: false). False otherwise
// — including for unknown fields, which the orchestrator's §8 check
// surfaces as lossy.
func claudeKeeps(sc *schema.Schema, fieldName string) bool {
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
	// Then sorted retained extensions for deterministic output —
	// yaml.Marshal on a map iterates in random order, so we build the
	// frontmatter as an explicit MappingNode with controlled ordering.
	front := []*yaml.Node{}
	var encodeErr error
	addScalar := func(key string, val any) {
		// Encode value FIRST, then append key+value as a pair. Appending
		// the key before encoding leaves front[] with a dangling key on
		// failure — a malformed mapping if the encodeErr guard ever gets
		// bypassed. Fail loudly: a Sprintf fallback would silently emit
		// non-YAML (e.g. Go's map[k:v] syntax) and break the file for
		// any parser including Claude Code's loader.
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
		// Re-encode must consult the schema — without it we cannot
		// answer "should this key be kept or dropped on claude-code?"
		// and emitting unknown keys silently is the failure mode §8
		// exists to prevent. If schema.Load fails, error out rather
		// than guessing.
		sc, err := schema.Load(resource.KindSkill)
		if err != nil {
			return nil, fmt.Errorf("claude-code: schema unavailable for re-encode: %w", err)
		}
		keys := make([]string, 0, len(s.Extensions()))
		for k := range s.Extensions() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !claudeKeeps(sc, k) {
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
