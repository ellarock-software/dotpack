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
	"github.com/ellarock/dotpack/internal/adapter/antigravity"
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
		Long: `Install a single portable resource into the named agent host.

This is dotpack's .agents -> host-native translation command. The source
may live under .agents or anywhere else; the resource must match the selected
kind's schema and template before dotpack writes host files.

Supported today:
  --agent claude-code | gemini-cli | antigravity-cli | codex | agents-cli
  --kind  skill | agent | mcp-server | hook | rule (skill is inferred when
          the source is named SKILL.md; rule is inferred for direct
          .agents/rules/*.md files; agent/mcp-server/hook otherwise require
          --kind explicitly.)
  --scope user | project

Host translation map:
  skill:
    claude-code     -> .claude/skills/<name>/SKILL.md
    gemini-cli      -> .gemini/skills/<name>/SKILL.md
    antigravity-cli -> .antigravity/skills/<name>/SKILL.md
    codex           -> .agents/skills/<name>/SKILL.md
    agents-cli      -> .agents/skills/<name>/SKILL.md once for sub-adapters
  agent:
    claude-code     -> .claude/agents/<name>.md
    gemini-cli      -> .gemini/agents/<name>.md
    antigravity-cli -> .antigravity/agents/<name>.md
    codex           -> .codex/agents/<name>.toml
    agents-cli      -> fans out to sub-adapters (markdown for most, TOML for codex)
  mcp-server:
    claude-code     -> .mcp.json (project) or ~/.claude.json (user)
    gemini-cli      -> .gemini/settings.json
    antigravity-cli -> .antigravity/settings.json
    codex           -> .codex/config.toml
    agents-cli      -> fans out to sub-adapter config files
  hook:
    claude-code     -> .claude/settings.json
    gemini-cli      -> .gemini/settings.json
    antigravity-cli -> .antigravity/settings.json
    codex           -> .codex/config.toml
    agents-cli      -> fans out to sub-adapter config files

User scope writes under $DOTPACK_CLAUDE_HOME / ~/.claude,
$DOTPACK_GEMINI_HOME / ~/.gemini, $DOTPACK_ANTIGRAVITY_HOME / ~/.antigravity,
$DOTPACK_AGENTS_HOME / ~/.agents, $DOTPACK_CODEX_HOME / ~/.codex,
or ~/.claude.json depending on host and kind.
Project scope writes under $DOTPACK_PROJECT_HOME or the current directory.

When --agent is omitted and the manifest already has a matching
(kind, name) on a different host, install refuses with a
"did you mean --agent X?" hint. Pass --agent explicitly (including
--agent claude-code) to opt in. Symmetric to the cross-host hint
uninstall surfaces when a short-name lookup misses on the defaulted
host.

Use --agent agents-cli for the umbrella flag. Skill installs use write-once
convergence to ~/.agents/skills/ across gemini-cli + codex; mcp-server and
hook installs fan out to each host's config file. The manifest record
carries Agent="agents-cli" so the user-typed umbrella identity is preserved
through ` + "`dotpack list`" + ` and uninstall. Lossy aggregation across the
sub-adapter set is the strict union per ADR-0012 §8: a field whose
canonical_concept is unsupported by ANY sub-adapter requires --allow-lossy.
Sub-adapter set and per-kind writer lists are declared in umbrellaFactories
(this file).

After materialization, install runs matching post-install lifecycle tasks from
lifecycle_tasks.yaml. Those tasks are declarative command hooks with their own
agent filters, binary-install steps, verification commands, and failure policy;
the install command only owns the lifecycle extension point and failure
reporting.`,
		Example: `  dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent gemini-cli --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent codex --scope project
  dotpack install .agents/mcp-servers/github.mcp.json --kind mcp-server --agent codex --scope user
  dotpack install .agents/hooks/bash-guard.hook.json --kind hook --agent agents-cli --scope project`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args[0], agentName, kindName, scopeName, allowLossy, force)
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", "claude-code", "Target host adapter (claude-code | gemini-cli | antigravity-cli | codex | agents-cli)")
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
	//
	// The hint check runs BEFORE the agentName branch on umbrella vs
	// per-host, so a user defaulting to --agent claude-code with an
	// existing agents-cli record gets "did you mean --agent agents-cli?"
	// — checkDefaultAgentMisroute treats umbrella names as buildable
	// suggestions via the isBuildableAgent helper (see its docstring).
	if !cmd.Flags().Changed("agent") {
		if err := checkDefaultAgentMisroute(res, agentName, mf); err != nil {
			return err
		}
	}

	// Umbrella branch — agents-cli (and any future umbrella name in
	// umbrellaFactories) routes to orchestrator.UmbrellaInstaller per
	// ADR-0012 §1. The per-host buildAdapter path is unchanged; the
	// umbrella branch is the orchestrator-side fan-out the ADR calls
	// for, kept narrow so per-host installs aren't paying for the
	// umbrella machinery they don't use.
	if _, ok := umbrellaFactories[agentName]; ok {
		return runUmbrellaInstall(cmd, source, agentName, kind, res, scope, allowLossy, force, d, mf)
	}

	a, err := buildAdapter(agentName, d)
	if err != nil {
		return err
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

	if err := runPostInstallLifecycle(agentName); err != nil {
		return fmt.Errorf("installed %s, but post-install lifecycle failed: %w", result.Record.ID, err)
	}

	cmd.Printf("Installed %s onto %s\n", result.Record.ID, agentName)
	for _, f := range result.Plan.Files {
		cmd.Printf("  wrote %s\n", f.Path)
	}
	for _, rm := range result.Plan.RemoveFiles {
		cmd.Printf("  removed stale %s\n", rm.Path)
	}
	for _, mk := range result.Plan.MergedKeys {
		cmd.Printf("  merged %s into %s\n", mk.Path, mk.File)
	}
	return nil
}

// runUmbrellaInstall is the per-umbrella install dispatch. Resolves the
// umbrella's sub-adapter set + per-kind writer list from
// umbrellaFactories, constructs an UmbrellaInstaller, and runs the
// install. The error-handling shape mirrors runInstall's per-host branch
// (LossyError / CollisionError pass through unwrapped) so users see the
// same actionable failure messages regardless of which --agent flag
// they typed.
//
// The split keeps runInstall's signature stable (a future umbrella
// doesn't reshape the per-host path) and isolates umbrella concerns
// behind one branch — easy to grep for "where do umbrellas execute?".
func runUmbrellaInstall(cmd *cobra.Command, source, agentName string, kind resource.Kind, res resource.Resource, scope adapter.Scope, allowLossy, force bool, d dirs.Dirs, mf *manifest.Store) error {
	subs, writers, err := buildUmbrella(agentName, d)
	if err != nil {
		return err
	}

	ui := orchestrator.NewUmbrellaInstaller(d, agentName, subs, writers, mf)

	absSrc, _ := filepath.Abs(source)
	result, err := ui.Install(res, scope, orchestrator.InstallOptions{
		Source:     "file://" + absSrc,
		AllowLossy: allowLossy,
		Force:      force,
	})
	if err != nil {
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

	if err := runPostInstallLifecycle(agentName); err != nil {
		return fmt.Errorf("installed %s, but post-install lifecycle failed: %w", result.Record.ID, err)
	}

	cmd.Printf("Installed %s onto %s\n", result.Record.ID, agentName)
	for _, f := range result.Plan.Files {
		cmd.Printf("  wrote %s\n", f.Path)
	}
	for _, rm := range result.Plan.RemoveFiles {
		cmd.Printf("  removed stale %s\n", rm.Path)
	}
	for _, mk := range result.Plan.MergedKeys {
		cmd.Printf("  merged %s into %s\n", mk.Path, mk.File)
	}
	return nil
}

// resolveKind picks the resource Kind from --kind (when set) or infers
// from the source filename. Inference only fires for unambiguous
// canonical paths: SKILL.md for skills and direct .agents/rules/*.md
// files for rules. Agent has no canonical filename (<agent-name>.md
// collides with anything else), so explicit --kind agent is required —
// inferring "any .md → agent" would treat a mis-named SKILL.md as an
// agent.
func resolveKind(explicit, sourcePath string) (resource.Kind, error) {
	if explicit != "" {
		switch resource.Kind(explicit) {
		case resource.KindSkill:
			return resource.KindSkill, nil
		case resource.KindAgent:
			return resource.KindAgent, nil
		case resource.KindMCPServer:
			return resource.KindMCPServer, nil
		case resource.KindHook:
			return resource.KindHook, nil
		case resource.KindRule:
			return resource.KindRule, nil
		case resource.KindCommand, resource.KindMemory:
			return "", fmt.Errorf("kind %q not yet supported", explicit)
		default:
			return "", fmt.Errorf("unknown kind %q", explicit)
		}
	}
	if filepath.Base(sourcePath) == "SKILL.md" {
		return resource.KindSkill, nil
	}
	if isDirectAgentsRulePath(sourcePath) {
		return resource.KindRule, nil
	}
	// No inference for mcp-server: the .mcp.json filename collides with
	// non-resource fragments a user may have lying around (the JSON
	// shape doesn't carry a kind discriminator), so we require
	// --kind mcp-server explicitly. Matches agent's explicit-only
	// inference policy.
	return "", fmt.Errorf("cannot infer --kind from %q; pass --kind explicitly", sourcePath)
}

func isDirectAgentsRulePath(sourcePath string) bool {
	if filepath.Ext(sourcePath) != ".md" {
		return false
	}
	dir := filepath.ToSlash(filepath.Clean(filepath.Dir(sourcePath)))
	return strings.HasSuffix(dir, "/.agents/rules") || dir == ".agents/rules"
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
	case resource.KindMCPServer:
		mcp, err := resource.ParseMCPServer(raw)
		if err != nil {
			return nil, err
		}
		if errs := validator.ValidateMCPServer(mcp); len(errs) > 0 {
			return nil, validationError(errs)
		}
		return mcp, nil
	case resource.KindHook:
		hook, err := resource.ParseHook(raw)
		if err != nil {
			return nil, err
		}
		// Hook source has no in-source name field (no frontmatter, no
		// map-key wrapper). The filesystem encodes identity: strip the
		// .hook.json / .json / .hook suffix from the basename and use
		// the stem as the install name. Mirrors how skill/agent get
		// names from frontmatter (which hooks lack); the validator
		// then gates on the kebab shape.
		hook.WithName(hookNameFromPath(source))
		if errs := validator.ValidateHook(hook); len(errs) > 0 {
			return nil, validationError(errs)
		}
		return hook, nil
	case resource.KindRule:
		rule, err := resource.ParseRule(raw)
		if err != nil {
			return nil, err
		}
		rule.WithSourcePath(source)
		if errs := validator.ValidateRule(rule); len(errs) > 0 {
			return nil, validationError(errs)
		}
		return rule, nil
	default:
		return nil, fmt.Errorf("kind %q not supported", kind)
	}
}

// hookNameFromPath derives the install name for a hook resource from
// its source filename. Strips a layered suffix: .hook.json → "" (a
// double-extension), then .json or .hook from whatever remains, then
// returns the basename's stem. Matches the most common naming pattern
// in the corpus (e.g., `bash-guard.hook.json` → "bash-guard"); a
// non-conforming filename (`hook.json`) yields the bare stem and gets
// rejected by hookNameRE downstream.
func hookNameFromPath(source string) string {
	base := filepath.Base(source)
	for _, suffix := range []string{".hook.json", ".hook.yaml", ".hook.yml", ".json", ".yaml", ".yml", ".hook"} {
		if strings.HasSuffix(base, suffix) {
			return base[:len(base)-len(suffix)]
		}
	}
	return base
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

// adapterFactories is the per-host registry of buildable --agent values.
// Driving both buildAdapter dispatch AND checkDefaultAgentMisroute's
// "is this host buildable?" filter (via isBuildableAgent) from the same
// map removes the keep-in-sync hazard the prior pair of switch + map
// literal carried. Closures wrap the per-host New(d) constructors
// because each returns the concrete *filedrop.Adapter, not
// adapter.Adapter — Go's lack of return-type variance means the
// wrappers are mandatory for a uniform map value type.
//
// adapterFactories is per-HOST: one entry per per-host Adapter that the
// orchestrator's Installer (host, manifest) pair can run. Umbrella names
// (agents-cli) live in the sibling umbrellaFactories map and dispatch
// through runUmbrellaInstall / orchestrator.UmbrellaInstaller — NOT
// through this map. The split keeps the per-host install path narrow
// and isolates umbrella machinery behind one branch in runInstall. The
// misroute hint treats both registries as buildable via
// isBuildableAgent so users see umbrella suggestions when an umbrella
// install matches their (kind, name) tuple.
var adapterFactories = map[string]func(dirs.Dirs) adapter.Adapter{
	"claude-code":     func(d dirs.Dirs) adapter.Adapter { return claudecode.New(d) },
	"gemini-cli":      func(d dirs.Dirs) adapter.Adapter { return gemini.New(d) },
	"antigravity-cli": func(d dirs.Dirs) adapter.Adapter { return antigravity.New(d) },
	"codex":           func(d dirs.Dirs) adapter.Adapter { return codex.New(d) },
}

// umbrellaFactories is the per-umbrella registry of CLI-flag-to-adapter-
// set aliases per ADR-0012 §1 + §10. Each umbrella declares:
//
//   - subs: the sub-adapter HostID set the umbrella fans out to for
//     lossy aggregation (resolved against adapterFactories at install
//     time; a HostID not in adapterFactories is a programmer error and
//     panics at process start via validateUmbrellaFactories below).
//   - writers: per-kind canonical writer HostID. The umbrella's
//     write-once contract for file-drop kinds picks ONE sub-adapter to
//     supply the InstallPlan; config-fragment kinds list every sub-
//     adapter that must write its own host config file. Kinds absent
//     from writers are explicitly unsupported under the umbrella —
//     UmbrellaInstaller.Install returns a structured "kind not supported
//     under umbrella" error rather than silently picking a default.
//
// To add a new umbrella (e.g., "all", "cursor-ish" per ADR-0012 §10's
// future-note): append an entry here, add per-kind writers as
// convergence paths get documented, and the rest is mechanical (misroute
// hint, install dispatch, and uninstall round-tripping all work without
// per-umbrella code). The agents-cli pattern is the template.
//
// To add a new kind to an existing umbrella: extend writers with either
// one canonical writer for documented file-drop convergence or an
// ordered writer list for config fragments where each sub-adapter owns a
// distinct host config file.
var umbrellaFactories = map[string]umbrellaConfig{
	"agents-cli": {
		subs: []string{"gemini-cli", "antigravity-cli", "codex"},
		writers: map[resource.Kind][]string{
			// Skill: codex writes to AgentsHome/skills/<name>/SKILL.md
			// per developers.openai.com/codex/skills. Gemini CLI ALSO
			// reads ~/.agents/skills/ per schema/skill.yaml's
			// ecosystem_notes, so codex's single write is consumed by
			// both runtimes — the file-drop write-once convergence
			// per ADR-0012 §1.
			resource.KindSkill: {"codex"},

			// Agent kind fans out: dotpack translates the abstraction
			// into TOML for Codex and drops Markdown for Gemini/Claude.
			// Because the formats differ, there is no shared write-once
			// path; each adapter writes its own host config file.
			resource.KindAgent: {"gemini-cli", "antigravity-cli", "codex"},
			//
			// Config-fragment kinds fan out: each sub-adapter writes
			// to its own config file and the single umbrella manifest
			// record aggregates both merged-key tuples.
			resource.KindMCPServer: {"gemini-cli", "antigravity-cli", "codex"},
			resource.KindHook:      {"gemini-cli", "antigravity-cli", "codex"},
			resource.KindRule:      {"gemini-cli", "antigravity-cli", "codex"},
		},
	},
}

// umbrellaConfig is the per-umbrella declaration — see umbrellaFactories
// for full semantics. Sub-adapter HostIDs reference adapterFactories
// entries (validated at process start by validateUmbrellaFactories).
type umbrellaConfig struct {
	subs    []string
	writers map[resource.Kind][]string
}

// init validates umbrellaFactories at process start. A typo in a sub
// HostID or a writer HostID would otherwise surface only when a user
// runs an umbrella install — failing fast at binary-startup time
// catches the mistake during CI/dev before users see it.
func init() {
	for name, cfg := range umbrellaFactories {
		for _, sub := range cfg.subs {
			if _, ok := adapterFactories[sub]; !ok {
				panic(fmt.Sprintf("cli: umbrellaFactories[%q] references unknown sub-adapter %q (not in adapterFactories)", name, sub))
			}
		}
		for kind, writers := range cfg.writers {
			if len(writers) == 0 {
				panic(fmt.Sprintf("cli: umbrellaFactories[%q].writers[%q] has no writers", name, kind))
			}
			for _, writer := range writers {
				if _, ok := adapterFactories[writer]; !ok {
					panic(fmt.Sprintf("cli: umbrellaFactories[%q].writers[%q] includes %q which is unknown (not in adapterFactories)", name, kind, writer))
				}
				// Writer must also be a sub-adapter so lossy aggregation
				// covers its emits. Otherwise the orchestrator's
				// NewUmbrellaInstaller validation fires at runtime — moving
				// it to init keeps user-facing failures away from this
				// programmer error.
				inSubs := false
				for _, sub := range cfg.subs {
					if sub == writer {
						inSubs = true
						break
					}
				}
				if !inSubs {
					panic(fmt.Sprintf("cli: umbrellaFactories[%q].writers[%q] includes %q which is not in subs %v — lossy aggregation would not cover the writer's emits",
						name, kind, writer, cfg.subs))
				}
			}
		}
	}
}

// buildAdapter constructs a single per-host adapter. Umbrellas are NOT
// resolved here — runInstall recognizes umbrella names from
// umbrellaFactories before calling buildAdapter. An umbrella name that
// reaches buildAdapter would surface as "unknown agent X" — fine, since
// the only path that reaches here for an umbrella name is a programmer
// bug in runInstall's branching, and "unknown agent" is a clear-enough
// failure mode that any user-facing error in that hypothetical reads
// as "we lost the umbrella before you ran the install".
func buildAdapter(name string, d dirs.Dirs) (adapter.Adapter, error) {
	if f, ok := adapterFactories[name]; ok {
		return f(d), nil
	}
	return nil, fmt.Errorf("unknown agent %q", name)
}

// buildUmbrella resolves an umbrella name to (subAdapters, writers)
// via adapterFactories. Caller is runUmbrellaInstall; the lookup is
// guaranteed by runInstall's umbrellaFactories check, so a missing
// entry here is an internal-error path that should never fire in
// production (umbrella names are hardcoded).
func buildUmbrella(name string, d dirs.Dirs) ([]adapter.Adapter, map[resource.Kind][]adapter.Adapter, error) {
	cfg, ok := umbrellaFactories[name]
	if !ok {
		return nil, nil, fmt.Errorf("internal: umbrella %q not found in umbrellaFactories", name)
	}
	subs := make([]adapter.Adapter, 0, len(cfg.subs))
	for _, hostID := range cfg.subs {
		factory := adapterFactories[hostID] // existence guaranteed by init
		subs = append(subs, factory(d))
	}
	writers := make(map[resource.Kind][]adapter.Adapter, len(cfg.writers))
	for kind, hostIDs := range cfg.writers {
		for _, hostID := range hostIDs {
			factory := adapterFactories[hostID] // existence guaranteed by init
			writers[kind] = append(writers[kind], factory(d))
		}
	}
	return subs, writers, nil
}

// isBuildableAgent reports whether name is a constructible --agent
// value — either a per-host adapter (adapterFactories) or an umbrella
// (umbrellaFactories). checkDefaultAgentMisroute uses this to filter
// the manifest's record set so the "did you mean --agent X?" suggestion
// only names hosts/umbrellas this binary can actually run.
func isBuildableAgent(name string) bool {
	if _, ok := adapterFactories[name]; ok {
		return true
	}
	if _, ok := umbrellaFactories[name]; ok {
		return true
	}
	return false
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
		// progress. isBuildableAgent treats both per-host adapters AND
		// umbrellas (agents-cli) as buildable, so a user defaulting to
		// claude-code with an existing agents-cli record gets the
		// umbrella suggestion. Pinned by
		// TestInstall_DefaultAgent_StaleManifestHost_OnlyMatch_NoHint
		// and TestInstall_DefaultAgent_MixedStaleAndBuildableHosts_HintsOnlyBuildable
		// (per-host filtering) plus
		// TestInstall_DefaultAgent_AgentsCliExistingMatch_HintsUmbrella
		// (umbrella inclusion).
		if !isBuildableAgent(rec.Agent) {
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
