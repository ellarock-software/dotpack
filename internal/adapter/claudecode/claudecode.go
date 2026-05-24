// Package claudecode is the claude-code Adapter implementation for
// ADR-0016. Slice 1 supports skill only; agent / command / memory /
// hook / mcp-server are added as their per-kind tests come online.
package claudecode

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
// Slice 1 declares only skill; other kinds default to Unsupported
// (zero-value of CapabilityLevel) and will be promoted as their
// per-kind work lands.
func (*Adapter) Capabilities() adapter.KindCapabilityMatrix {
	return adapter.KindCapabilityMatrix{
		resource.KindSkill: adapter.Native,
	}
}

// Plan returns the install plan for a Resource. Dispatch is by Kind();
// per-kind logic per ADR-0016 §3 (typed structs, not a generic walker).
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch v := r.(type) {
	case *resource.Skill:
		return a.planSkill(v, scope)
	default:
		return adapter.InstallPlan{}, fmt.Errorf("claude-code: kind %q not yet supported", r.Kind())
	}
}

// planSkill produces the install plan for one skill. Per ADR-0009 the
// target is <root>/skills/<name>/SKILL.md, where <root> is dirs.ClaudeHome
// (user scope) or ./.claude (project scope, relative to CWD).
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
	}, nil
}

// skillTarget computes the on-disk path for a skill at the given scope.
func skillTarget(d dirs.Dirs, scope adapter.Scope, name string) (string, error) {
	switch scope {
	case adapter.ScopeUser:
		if d.ClaudeHome == "" {
			return "", fmt.Errorf("claude-code: user scope requires dirs.ClaudeHome to be set")
		}
		return filepath.Join(d.ClaudeHome, "skills", name, "SKILL.md"), nil
	case adapter.ScopeProject:
		return filepath.Join(".claude", "skills", name, "SKILL.md"), nil
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
	if len(s.Raw) > 0 && canPassThrough(s) {
		return s.Raw, nil
	}
	return reencodeSkill(s)
}

// canPassThrough reports whether emitting Raw bytes verbatim would
// drop nothing of semantic value on claude-code. True iff every
// extension key resolves (via the schema) to either a claude-code-
// supported concept or pass-through metadata.
func canPassThrough(s *resource.Skill) bool {
	if len(s.Extensions) == 0 {
		return true
	}
	sc, err := schema.Load(resource.KindSkill)
	if err != nil {
		// Schema load fail is an unrecoverable bug at this point
		// (embedded files), but fail safe to re-encode rather than
		// pretending we know the answer. Re-encode without extensions
		// is conservative and gives the orchestrator a chance to fail
		// loudly via §8.
		return false
	}
	for k := range s.Extensions {
		if !claudeKeeps(sc, k) {
			return false
		}
	}
	return true
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
	addScalar := func(key string, val any) {
		front = append(front, &yaml.Node{Kind: yaml.ScalarNode, Value: key})
		valNode := &yaml.Node{}
		if err := valNode.Encode(val); err != nil {
			valNode = &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", val)}
		}
		front = append(front, valNode)
	}
	addScalar("name", s.Name)
	addScalar("description", s.Description)
	if s.License != "" {
		addScalar("license", s.License)
	}

	if len(s.Extensions) > 0 {
		var sc *schema.Schema
		if loaded, err := schema.Load(resource.KindSkill); err == nil {
			sc = loaded
		}
		keys := make([]string, 0, len(s.Extensions))
		for k := range s.Extensions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if sc != nil && !claudeKeeps(sc, k) {
				continue
			}
			addScalar(k, s.Extensions[k])
		}
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
