// Package claudecode is the claude-code Adapter implementation for
// ADR-0016. Slice 1 supports skill only; agent / command / memory /
// hook / mcp-server are added as their per-kind tests come online.
package claudecode

import (
	"bytes"
	"fmt"
	"io/fs"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
)

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
func (*Adapter) HostID() string { return "claude-code" }

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

	plan := adapter.InstallPlan{
		Files: []adapter.FileWrite{{
			Path:    target,
			Content: content,
			Mode:    fs.FileMode(0o644),
		}},
	}

	// Slice 1: any Extensions present → mark Lossy. Schema-driven
	// per-instance lossy detection (ADR-0016 §8) is orchestrator-side
	// work; the adapter just signals "the extension fields would not
	// survive my emit" so the orchestrator can require --allow-lossy.
	for key := range s.Extensions {
		plan.Lossy = true
		plan.LossyReasons = append(plan.LossyReasons, adapter.LossyReason{
			FieldPath:      key,
			SupportedHosts: nil, // schema lookup comes in slice 2
		})
	}

	return plan, nil
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
// cache copy. When ParseSkill captured the source bytes (Skill.Raw) and
// no Extension routing decisions need re-encoding, return them verbatim.
// This preserves authorial YAML formatting (folded scalars, key order,
// comments) that yaml.Marshal would normalise away.
//
// The fallback re-encodes universal-core fields. Used when the Skill
// was synthesised (e.g. translator output, or cache hits where the
// original bytes were lost) or when slice 1's overly-strict lossy
// classification dropped Extensions and re-encoding is unavoidable.
// Slice 2's schema-driven Extension routing will narrow the fallback
// to genuine non-conformance cases.
func encodeSkill(s *resource.Skill) ([]byte, error) {
	if len(s.Raw) > 0 && len(s.Extensions) == 0 {
		return s.Raw, nil
	}

	front := map[string]any{
		"name":        s.Name,
		"description": s.Description,
	}
	if s.License != "" {
		front["license"] = s.License
	}
	frontBytes, err := yaml.Marshal(front)
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
