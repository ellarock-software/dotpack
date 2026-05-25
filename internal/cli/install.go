package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/adapter/claudecode"
	"github.com/ellarock/dotpack/internal/adapter/codex"
	"github.com/ellarock/dotpack/internal/adapter/gemini"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/manifest"
	"github.com/ellarock/dotpack/internal/orchestrator"
	"github.com/ellarock/dotpack/internal/resource"
	"github.com/ellarock/dotpack/internal/validator"
)

func newInstallCmd() *cobra.Command {
	var (
		agentName  string
		kindName   string
		scopeName  string
		allowLossy bool
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "install <source-path>",
		Short: "Install a resource into an agent host",
		Long: `Install a single resource (skill or agent) into the named agent host.

Supported today:
  --agent claude-code | gemini-cli | codex
  --kind  skill | agent (skill is inferred when the source is named SKILL.md;
          agent requires --kind agent explicitly. Codex supports skill only —
          --kind agent --agent codex returns an error per ADR-0007's
          default-deny posture since codex CLI documents no native agent
          loading directory.)
  --scope user | project

User scope writes to $DOTPACK_CLAUDE_HOME / ~/.claude,
$DOTPACK_GEMINI_HOME / ~/.gemini, or $DOTPACK_AGENTS_HOME / ~/.agents
(codex's only documented native skill root per
developers.openai.com/codex/skills). Project scope writes under
$DOTPACK_PROJECT_HOME / CWD.

When --agent is omitted and the manifest already has a matching
(kind, name) on a different host, install refuses with a
"did you mean --agent X?" hint. Pass --agent explicitly (including
--agent claude-code) to opt in. Symmetric to the cross-host hint
uninstall surfaces when a short-name lookup misses on the defaulted
host.

Future slices add the agents-cli umbrella flag (write-once convergence
to ~/.agents/skills/ across gemini-cli + codex per ADR-0016 §1).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args[0], agentName, kindName, scopeName, allowLossy, force)
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", "claude-code", "Target host adapter (claude-code | gemini-cli | codex)")
	cmd.Flags().StringVar(&kindName, "kind", "", "Resource kind; inferred from filename when omitted (SKILL.md → skill)")
	cmd.Flags().StringVar(&scopeName, "scope", "user", "Install scope (user|project)")
	cmd.Flags().BoolVar(&allowLossy, "allow-lossy", false, "Proceed even if the adapter cannot honour all source fields")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing untracked files at the install target (collisions otherwise refuse)")
	return cmd
}

func runInstall(cmd *cobra.Command, source, agentName, kindName, scopeName string, allowLossy, force bool) error {
	d, err := dirs.FromEnv()
	if err != nil {
		return err
	}

	kind, err := resolveKind(kindName, source)
	if err != nil {
		return err
	}

	res, err := loadResource(kind, source)
	if err != nil {
		return err
	}

	a, err := buildAdapter(agentName, d)
	if err != nil {
		return err
	}

	scope, err := parseScope(scopeName)
	if err != nil {
		return err
	}

	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))

	// Default-agent misroute hint (slice-3 #7-followon). Symmetric in
	// spirit to orchestrator.Uninstall's miss-hint, but lives in the CLI
	// because the trigger condition — "did the user accept the default
	// --agent?" — is a cobra concept the orchestrator (correctly) does
	// not know about. Match is on the (kind, name) TUPLE (sharper than
	// uninstall's bare short-name) because install always knows the
	// resource's Kind() from resolveKind/loadResource.
	if !cmd.Flags().Changed("agent") {
		if err := checkDefaultAgentMisroute(res, agentName, mf); err != nil {
			return err
		}
	}

	inst := orchestrator.NewInstaller(d, a, mf)

	absSrc, _ := filepath.Abs(source)
	result, err := inst.Install(res, scope, orchestrator.InstallOptions{
		Source:     "file://" + absSrc,
		AllowLossy: allowLossy,
		Force:      force,
	})
	if err != nil {
		// LossyError + CollisionError both render their own
		// actionable message (per-field reasons / colliding paths +
		// the relevant bypass flag). Return as-is so cobra prints
		// the structured text rather than wrapping it.
		var le *orchestrator.LossyError
		if errors.As(err, &le) {
			return le
		}
		var ce *orchestrator.CollisionError
		if errors.As(err, &ce) {
			return ce
		}
		return err
	}

	cmd.Printf("Installed %s onto %s\n", result.Record.ID, agentName)
	for _, f := range result.Plan.Files {
		cmd.Printf("  wrote %s\n", f.Path)
	}
	return nil
}

// resolveKind picks the resource Kind from --kind (when set) or infers
// from the source filename. skill + agent supported; the other four
// kinds (command, memory, hook, mcp-server) land as their per-kind work
// comes online. Inference only fires for skill (SKILL.md is the
// canonical filename across all hosts); agent has no canonical filename
// (<agent-name>.md collides with anything else), so explicit --kind
// agent is required — inferring "any .md → agent" would treat a
// mis-named SKILL.md as an agent.
func resolveKind(explicit, sourcePath string) (resource.Kind, error) {
	if explicit != "" {
		switch resource.Kind(explicit) {
		case resource.KindSkill:
			return resource.KindSkill, nil
		case resource.KindAgent:
			return resource.KindAgent, nil
		case resource.KindCommand, resource.KindMemory, resource.KindHook, resource.KindMCPServer:
			return "", fmt.Errorf("kind %q not yet supported", explicit)
		default:
			return "", fmt.Errorf("unknown kind %q", explicit)
		}
	}
	if filepath.Base(sourcePath) == "SKILL.md" {
		return resource.KindSkill, nil
	}
	return "", fmt.Errorf("cannot infer --kind from %q; pass --kind explicitly", sourcePath)
}

func loadResource(kind resource.Kind, source string) (resource.Resource, error) {
	raw, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", source, err)
	}
	switch kind {
	case resource.KindSkill:
		skill, err := resource.ParseSkill(raw)
		if err != nil {
			return nil, err
		}
		if errs := validator.ValidateSkill(skill); len(errs) > 0 {
			return nil, validationError(errs)
		}
		return skill, nil
	case resource.KindAgent:
		agent, err := resource.ParseAgent(raw)
		if err != nil {
			return nil, err
		}
		if errs := validator.ValidateAgent(agent); len(errs) > 0 {
			return nil, validationError(errs)
		}
		return agent, nil
	default:
		return nil, fmt.Errorf("kind %q not supported", kind)
	}
}

// validationError formats a slice of validator errors as a single
// "validation: <msg1>; <msg2>" error so the CLI surfaces all field
// problems in one shot.
func validationError(errs []validator.ValidationError) error {
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return fmt.Errorf("validation: %s", strings.Join(msgs, "; "))
}

// adapterFactories is the single registry of buildable --agent values.
// Driving both buildAdapter dispatch AND checkDefaultAgentMisroute's
// "is this host buildable?" filter from the same map removes the
// keep-in-sync hazard the prior pair of switch + map literal carried.
// Closures wrap the per-host New(d) constructors because each returns
// the concrete *filedrop.Adapter, not adapter.Adapter — Go's lack of
// return-type variance means the wrappers are mandatory for a uniform
// map value type.
//
// agents-cli is intentionally NOT in this map: it's a recognised name
// the CLI rejects with a distinct "not yet implemented" message rather
// than the generic "unknown agent" error. The buildAdapter sentinel
// branch (kept separate from this map) carries that affordance. When
// the agents-cli umbrella flag lands per ADR-0016 §1, add a factory
// here and drop the sentinel branch.
var adapterFactories = map[string]func(dirs.Dirs) adapter.Adapter{
	"claude-code": func(d dirs.Dirs) adapter.Adapter { return claudecode.New(d) },
	"gemini-cli":  func(d dirs.Dirs) adapter.Adapter { return gemini.New(d) },
	"codex":       func(d dirs.Dirs) adapter.Adapter { return codex.New(d) },
}

func buildAdapter(name string, d dirs.Dirs) (adapter.Adapter, error) {
	if f, ok := adapterFactories[name]; ok {
		return f(d), nil
	}
	if name == "agents-cli" {
		return nil, fmt.Errorf("agent %q not yet implemented", name)
	}
	return nil, fmt.Errorf("unknown agent %q", name)
}

// checkDefaultAgentMisroute returns a "did you mean --agent X?" error
// when the user accepted the default --agent value (target is
// claude-code by default) AND the manifest already has a record with
// the same (kind, name) tuple on a host OTHER than the target AND has
// no matching record on the target itself. In all other cases it
// returns nil and the install proceeds normally.
//
// The check stays in the CLI layer (rather than in orchestrator) per
// advisor: the trigger "was --agent defaulted?" is a cobra-flag
// concept; pushing it into the orchestrator would make the orchestrator
// learn about CLI default-resolution semantics it has no business
// knowing.
//
// The match is on the (kind, name) tuple, not just the trailing
// short-name. Install always knows the resource's Kind() from
// resolveKind/loadResource, so a same-name-different-kind record on
// another host is a distinct concept and must NOT trigger the hint —
// sharper than uninstall, which compares bare short-names because its
// default --kind=skill is itself a misroute candidate. The kind filter
// is pinned by
// TestInstall_DefaultAgent_OtherHostDifferentKind_NoHint.
//
// The escape hatch is explicit --agent claude-code: cobra's
// Flags().Changed("agent") flips to true and this function is not
// called. Pinned by TestInstall_ExplicitAgentClaudeCode_SuppressesHint.
//
// The no-write guarantee (anti-theatre) lives in the call site: this
// function is invoked BEFORE orchestrator.NewInstaller / inst.Install, so a
// non-nil return short-circuits the write. Pinned by the anti-theatre
// assertion in TestInstall_DefaultAgent_AlternateHostHasSameKindName_HintsAndRefuses.
func checkDefaultAgentMisroute(res resource.Resource, target string, mf *manifest.Store) error {
	namer, ok := res.(resource.Named)
	if !ok {
		// No name-bearing resource → can't compute the (kind, name) tuple.
		// buildRecord will surface a clearer error downstream for kinds
		// that haven't wired a Named branch (memory, mcp-server when they
		// land). Skipping the hint is the right behaviour here — we'd
		// rather not invent a fake match key.
		return nil
	}
	name := namer.ResourceName()
	kind := string(res.Kind())

	m, err := mf.Load()
	if err != nil {
		return fmt.Errorf("manifest load (hint check): %w", err)
	}

	alternates := map[string]struct{}{}
	onTarget := false
	for _, rec := range m.Installs {
		if rec.Kind != kind {
			continue
		}
		// Compare on the trailing short-name component of the ID rather
		// than maintaining a separate (host, kind, name) tuple key.
		// Mirrors orchestrator.Uninstall's short-name extraction so the
		// install-side and uninstall-side hint code use the same
		// id-decomposition convention — drift between the two would
		// produce subtly inconsistent hint behaviour across the two
		// commands.
		short := rec.ID
		if i := strings.LastIndex(short, ":"); i >= 0 {
			short = short[i+1:]
		}
		if short != name {
			continue
		}
		if rec.Agent == target {
			onTarget = true
			continue
		}
		// Drop hosts the current binary cannot build. Surfacing a stale
		// or removed adapter name as a --agent suggestion would move
		// the user from this error to "unknown agent X" with no
		// progress. adapterFactories is the single buildable-host
		// registry — see its docstring for the agents-cli exclusion
		// rationale. Pinned by
		// TestInstall_DefaultAgent_StaleManifestHost_OnlyMatch_NoHint
		// and TestInstall_DefaultAgent_MixedStaleAndBuildableHosts_HintsOnlyBuildable.
		if _, ok := adapterFactories[rec.Agent]; !ok {
			continue
		}
		alternates[rec.Agent] = struct{}{}
	}
	if onTarget || len(alternates) == 0 {
		return nil
	}

	hosts := make([]string, 0, len(alternates))
	for h := range alternates {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)

	// Message shape mirrors orchestrator.Uninstall's "did you mean" hint
	// in spirit: surface the evidence (kind + name + the hosts that
	// already own a matching record), name the single-shot fix (the
	// alphabetically-first alternate host so the suggestion is stable),
	// and document the explicit-opt-in escape hatch in case the user
	// really did want the default. Listing all alternate hosts (not
	// just the first) avoids cherry-picking when the manifest is
	// ambiguous — TestInstall_DefaultAgent_MultipleAlternateHosts_HintsAll
	// pins this.
	primary := hosts[0]
	return fmt.Errorf("install would default to --agent %s, but %s %q is already installed on: %s; "+
		"did you mean --agent %s? (pass --agent %s explicitly to override)",
		target, kind, name, strings.Join(hosts, ", "), primary, target)
}

func parseScope(name string) (adapter.Scope, error) {
	switch name {
	case "user":
		return adapter.ScopeUser, nil
	case "project":
		return adapter.ScopeProject, nil
	default:
		return "", fmt.Errorf("scope %q invalid (must be user|project)", name)
	}
}
