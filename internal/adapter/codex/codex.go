// Package codex is the codex CLI Adapter implementation for ADR-0016.
// Skill is Native; agent is declared Unsupported in the capability
// matrix (Plan returns a structured error). Agent will be promoted
// out of Unsupported only if/when the codex CLI documents a native
// agent loading directory analogous to .claude/agents/ or .gemini/agents/.
//
// Skill paths follow developers.openai.com/codex/skills:
//   - User-scope:    <AgentsHome>/skills/<name>/SKILL.md  (default ~/.agents)
//   - Project-scope: <ProjectHome>/.agents/skills/<name>/SKILL.md
//
// Codex CLI does NOT document a host-specific ~/.codex/skills/ path
// (the third-party blog ecosystem disagrees on this — the OpenAI docs
// are authoritative). AgentsHome is the codex-native root for skills.
// Gemini CLI also reads ~/.agents/skills/ as a convergence path, but
// the gemini-cli adapter writes to its host-specific <GeminiHome>/skills/
// instead, so `--agent codex` and `--agent gemini-cli` don't collide
// today. The future `--agent agents-cli` umbrella flag (ADR-0016 §1)
// will write to AgentsHome/skills/ as its write-once convergence; that
// IS a future collision with `--agent codex` at the same path, owned
// by the umbrella's CLI-flag-to-adapter-set special case when it lands.
package codex

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

// hostID is the dotpack adapter HostID for codex. MUST match the
// `host:` strings in schema/*.yaml aliases (schema/schema.go Alias
// docstring is the load-bearing contract) — LossyExtensions compares
// on equality, so a mismatch silently flips codex-native concepts to
// lossy on their own host.
const hostID = "codex"

// Adapter is the codex host adapter. Construct via New(dirs); the
// Dirs value is the only injected state so tests can target a tempdir
// instead of the user's real ~/.agents/.
type Adapter struct {
	dirs dirs.Dirs
}

// New constructs the codex adapter with explicit Dirs.
func New(d dirs.Dirs) *Adapter {
	return &Adapter{dirs: d}
}

// HostID returns "codex".
func (*Adapter) HostID() string { return hostID }

// Capabilities returns the per-kind ratings from ADR-0007's matrix.
// Skill is Native. Agent is declared Unsupported EXPLICITLY (rather
// than left absent and relying on CapabilityLevel's iota zero value):
// codex CLI documents no native agent loading directory, and inventing
// one (e.g. ~/.codex/agents/) would install to a path codex never
// reads. The explicit declaration makes "we deliberately decided not
// to support this" visible at the call site rather than indistinguishable
// from "nobody thought about it yet". Plan enforces the same; this
// matrix is metadata only (ADR-0007).
func (*Adapter) Capabilities() adapter.KindCapabilityMatrix {
	return adapter.KindCapabilityMatrix{
		resource.KindSkill: adapter.Native,
		resource.KindAgent: adapter.Unsupported,
	}
}

// Plan returns the install plan for a Resource. Dispatch by Kind() —
// per-kind logic per ADR-0016 §3 (typed structs, not a generic walker).
// Codex supports skill only today; everything else (including agent)
// returns a structured "kind X not yet supported" error so the CLI
// surfaces an actionable message instead of silently writing to a
// fictional path.
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch v := r.(type) {
	case *resource.Skill:
		return a.planSkill(v, scope)
	default:
		return adapter.InstallPlan{}, fmt.Errorf("codex: kind %q not yet supported", r.Kind())
	}
}

// planSkill produces the install plan for one skill. Per
// developers.openai.com/codex/skills, codex's native skill location is
// AgentsHome/skills/<name>/SKILL.md (user scope) or
// <ProjectHome>/.agents/skills/<name>/SKILL.md (project scope). The
// on-disk layout (per-name dir with SKILL.md + optional scripts/,
// references/, assets/) matches the SKILL.md spec used by claude-code
// and gemini-cli — only the root differs.
func (a *Adapter) planSkill(s *resource.Skill, scope adapter.Scope) (adapter.InstallPlan, error) {
	content, err := encodeSkill(s)
	if err != nil {
		return adapter.InstallPlan{}, fmt.Errorf("codex: encode skill %q: %w", s.Name, err)
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
// User scope: <AgentsHome>/skills/<name>/SKILL.md. Project scope:
// <ProjectHome>/.agents/skills/<name>/SKILL.md. Both absolute. The
// missing-field error names AgentsHome explicitly — codex has no
// CodexHome (~/.codex/skills/ is not documented by OpenAI), so a future
// reader debugging "where does codex write?" sees the actual env var
// they need (DOTPACK_AGENTS_HOME).
func skillTarget(d dirs.Dirs, scope adapter.Scope, name string) (string, error) {
	switch scope {
	case adapter.ScopeUser:
		if d.AgentsHome == "" {
			return "", fmt.Errorf("codex: user scope requires dirs.AgentsHome to be set")
		}
		return filepath.Join(d.AgentsHome, "skills", name, "SKILL.md"), nil
	case adapter.ScopeProject:
		if d.ProjectHome == "" {
			return "", fmt.Errorf("codex: project scope requires dirs.ProjectHome to be set")
		}
		return filepath.Join(d.ProjectHome, ".agents", "skills", name, "SKILL.md"), nil
	default:
		return "", fmt.Errorf("codex: unknown scope %q", scope)
	}
}

// encodeSkill produces the SKILL.md bytes the orchestrator will write.
// Same byte-pass-through-when-safe logic as claudecode + gemini: if Raw
// is set and no extension would be dropped on codex per §8, ship Raw
// verbatim. Otherwise re-encode.
func encodeSkill(s *resource.Skill) ([]byte, error) {
	if len(s.Raw) > 0 {
		pass, err := canPassThrough(s)
		if err != nil {
			return nil, fmt.Errorf("codex: schema unavailable for pass-through check: %w", err)
		}
		if pass {
			return s.Raw, nil
		}
	}
	return reencodeSkill(s)
}

// canPassThrough is a third copy of the same shape now in claudecode +
// gemini — only the per-host `keeps` predicate differs. Acknowledged
// duplication: adapters don't depend on each other, so codex can't
// import gemini's helper, and a shared package would have to take the
// hostID as a parameter. Per the project's no-premature-abstraction
// rule, the extraction shape is left to a future architecture pass
// rather than introduced speculatively in this commit.
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
		if !codexKeeps(sc, k) {
			return false, nil
		}
	}
	return true, nil
}

// codexKeeps reports whether codex's emit should retain the extension
// keyed by fieldName. True if the schema lists codex under the matching
// concept's aliases, or if the concept is pass-through metadata
// (lossy_when_dropped: false). False for unknown fields — those surface
// via the orchestrator's §8 lossy check.
func codexKeeps(sc *schema.Schema, fieldName string) bool {
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
	// shape as claudecode + gemini reencodeSkill — only codexKeeps
	// differs.
	front := []*yaml.Node{}
	var encodeErr error
	addScalar := func(key string, val any) {
		// Encode value FIRST, then append (key, value) as a pair.
		// Appending the key before encoding would leave front[] with
		// a dangling key on encode failure — a malformed mapping if
		// the encodeErr guard ever gets bypassed. Mirror of
		// claudecode + gemini reencodeSkill atomicity; the third copy
		// fixed identically.
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
			return nil, fmt.Errorf("codex: schema unavailable for re-encode: %w", err)
		}
		keys := make([]string, 0, len(s.Extensions()))
		for k := range s.Extensions() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !codexKeeps(sc, k) {
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
