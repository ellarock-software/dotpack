package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/adapter"
	_ "github.com/ellarock-software/dotpack/internal/adapter/all"
	"github.com/ellarock-software/dotpack/internal/adapter/registry"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/orchestrator"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/internal/validator"
)

func newInstallCmd() *cobra.Command {
	var (
		agentName    string
		kindName     string
		scopeName    string
		allowLossy   bool
		force        bool
		runLifecycle bool
	)

	cmd := &cobra.Command{
		Use:   "install <source-path>",
		Short: "Install a resource into an agent host",
		Long: `Install a single portable resource into the named agent host.

This is dotpack's .agents -> host-native translation command. The source
may live under .agents or anywhere else; the resource must match the selected
kind's schema and template before dotpack writes host files.

Currently shipped adapters (the product intent is universal LLM-tool
coverage; this is the set implemented today — see README):
  --agent claude-code | gemini-cli | antigravity-cli | codex | opencode | hermes | agents-cli
  --kind  skill | agent | command | memory | mcp-server | hook | rule (skill is inferred when
          the source is named SKILL.md; rule is inferred for direct
          .agents/rules/*.md files; command is inferred for direct
          .agents/commands/*.md/.toml files; memory is inferred for
          CLAUDE.md/GEMINI.md/AGENTS.md/ANTIGRAVITY.md/HERMES.md/.hermes.md/SOUL.md;
          agent/mcp-server/hook otherwise require --kind explicitly.)
  --scope user | project

Host translation map:
  skill:
    claude-code     -> .claude/skills/<name>/SKILL.md
    gemini-cli      -> .gemini/skills/<name>/SKILL.md
    antigravity-cli -> .antigravity/skills/<name>/SKILL.md
    codex           -> .agents/skills/<name>/SKILL.md
    opencode        -> .opencode/skills/<name>/SKILL.md
    hermes          -> ~/.hermes/skills/<name>/SKILL.md (user scope only)
    agents-cli      -> .agents/skills/<name>/SKILL.md once for sub-adapters
  agent:
    claude-code     -> .claude/agents/<name>.md
    gemini-cli      -> .gemini/agents/<name>.md
    antigravity-cli -> .antigravity/agents/<name>.md
    codex           -> .codex/agents/<name>.toml
    opencode        -> .opencode/agents/<name>.md
    agents-cli      -> fans out to sub-adapters (markdown for most, TOML for codex)
  command:
    claude-code     -> .claude/commands/<name>.md
    gemini-cli      -> .gemini/commands/<name>.toml
    antigravity-cli -> .antigravity/commands/<name>.md
    codex           -> .codex/commands/<name>.md
    opencode        -> .opencode/commands/<name>.md
    agents-cli      -> fans out to each sub-adapter's command file
  memory:
    claude-code     -> CLAUDE.md
    gemini-cli      -> GEMINI.md
    antigravity-cli -> ANTIGRAVITY.md
    codex           -> AGENTS.md
    opencode        -> AGENTS.md
    hermes          -> SOUL.md (user) or AGENTS.md/.hermes.md/HERMES.md/CLAUDE.md (project)
    agents-cli      -> fans out to each sub-adapter's memory file
  mcp-server:
    claude-code     -> .mcp.json (project) or ~/.claude.json (user)
    gemini-cli      -> .gemini/settings.json
    antigravity-cli -> .antigravity/settings.json
    codex           -> .codex/config.toml
    opencode        -> opencode.json (under $.mcp)
    hermes          -> ~/.hermes/config.yaml (user scope only)
    agents-cli      -> fans out to sub-adapter config files
  hook:
    claude-code     -> .claude/settings.json
    gemini-cli      -> .gemini/settings.json
    antigravity-cli -> .antigravity/settings.json
    codex           -> .codex/config.toml
    opencode        -> unsupported (OpenCode uses JS plugins, not declarative hooks)
    hermes          -> ~/.hermes/config.yaml (user scope only)
    agents-cli      -> fans out to sub-adapter config files
  rule:
    opencode        -> unsupported (OpenCode rules are AGENTS.md, no per-rule file)
    hermes          -> unsupported (Hermes project context is memory files, not per-rule files)

Skill installs also copy regular sibling files under the source SKILL.md
directory, such as references/*.md, scripts/*, and assets/*, to the same
relative paths under the host skill directory. Symlinks are rejected.

User scope writes under $DOTPACK_CLAUDE_HOME / ~/.claude,
$DOTPACK_GEMINI_HOME / ~/.gemini, $DOTPACK_ANTIGRAVITY_HOME / ~/.antigravity,
$DOTPACK_AGENTS_HOME / ~/.agents, $DOTPACK_CODEX_HOME / ~/.codex,
$DOTPACK_HERMES_HOME / ~/.hermes,
or ~/.claude.json depending on host and kind.
Project scope writes under $DOTPACK_PROJECT_HOME or the current directory.

When --agent is omitted and the manifest already has a matching
(kind, name) on a different host, install refuses with a
"did you mean --agent X?" hint. Pass --agent explicitly (including
--agent claude-code) to opt in. Symmetric to the cross-host hint
uninstall surfaces when a short-name lookup misses on the defaulted
host.

Use --agent agents-cli for the umbrella flag. Skill installs use write-once
convergence to ~/.agents/skills/ across the sub-adapters; agent, command,
memory, rule, mcp-server, and hook installs fan out to each host's own file.
The manifest record carries Agent="agents-cli" so the user-typed umbrella
identity is preserved through ` + "`dotpack list`" + ` and uninstall. Lossy
aggregation across the sub-adapter set is the strict union per ADR-0012 §8:
a field whose canonical_concept is unsupported by ANY sub-adapter requires
--allow-lossy. The sub-adapter set and per-kind writer lists self-register in
internal/adapter/all (see registry.Umbrella, ADR-0014).

By default, install only materializes host files. Pass --run-lifecycle to run
matching post-install lifecycle tasks from lifecycle_tasks.yaml after
materialization. Those tasks are declarative command hooks with their own agent
filters, verification commands, and failure policy; the install command only
owns the lifecycle extension point and failure reporting.

Skill installs run a mandatory static SkillSpector gate before dotpack
materializes host files. Use dotpack scan-skills when you want to inspect or
export the same findings directly.`,
		Example: `  dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent gemini-cli --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent codex --scope project
  dotpack install .agents/skills/code-review/SKILL.md --agent hermes --scope user
  dotpack install .agents/mcp-servers/github.mcp.json --kind mcp-server --agent codex --scope user
  dotpack install .agents/hooks/bash-guard.hook.json --kind hook --agent agents-cli --scope project`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args[0], agentName, kindName, scopeName, allowLossy, force, runLifecycle)
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", "claude-code", "Target host adapter (claude-code | gemini-cli | antigravity-cli | codex | opencode | hermes | agents-cli)")
	cmd.Flags().StringVar(&kindName, "kind", "", "Resource kind; inferred from filename when omitted (SKILL.md → skill)")
	cmd.Flags().StringVar(&scopeName, "scope", "user", "Install scope (user|project)")
	cmd.Flags().BoolVar(&allowLossy, "allow-lossy", false, "Proceed even if the adapter cannot honour all source fields")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing untracked files at the install target (collisions otherwise refuse)")
	cmd.Flags().BoolVar(&runLifecycle, "run-lifecycle", false, "Run optional post-install lifecycle tasks after materialization")
	return cmd
}

func runInstall(cmd *cobra.Command, source, agentName, kindName, scopeName string, allowLossy, force, runLifecycle bool) error {
	d, err := dirs.FromEnv()
	if err != nil {
		return err
	}

	kind, err := resolveKind(kindName, source)
	if err != nil {
		return err
	}
	if kind == resource.KindSkill {
		if err := ensureMandatorySkillScanForSource("install", source, d); err != nil {
			return err
		}
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

	// Umbrella branch — agents-cli (and any future registered umbrella)
	// routes to orchestrator.UmbrellaInstaller per ADR-0012 §1. The
	// per-host buildAdapter path is unchanged; the umbrella branch is the
	// orchestrator-side fan-out the ADR calls for, kept narrow so per-host
	// installs aren't paying for the umbrella machinery they don't use.
	if registry.IsUmbrella(agentName) {
		return runUmbrellaInstall(cmd, source, agentName, kind, res, scope, allowLossy, force, runLifecycle, d, mf)
	}

	a, err := buildAdapter(agentName, d)
	if err != nil {
		return err
	}

	inst := orchestrator.NewInstaller(d, a, mf)

	absSrc, _ := filepath.Abs(source)
	result, err := inst.Install(res, scope, orchestrator.InstallOptions{
		Source:        "file://" + absSrc,
		CanonicalRoot: inferCanonicalRoot(absSrc),
		TargetRoot:    targetRootForScope(scope, d),
		AllowLossy:    allowLossy,
		Force:         force,
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

	if runLifecycle {
		if err := runPostInstallLifecycle(agentName); err != nil {
			return fmt.Errorf("installed %s, but post-install lifecycle failed: %w", result.Record.ID, err)
		}
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
// umbrella's sub-adapter set + per-kind writer list via the registry
// (buildUmbrella → registry.BuildUmbrella), constructs an
// UmbrellaInstaller, and runs the install. The error-handling shape
// mirrors runInstall's per-host branch
// (LossyError / CollisionError pass through unwrapped) so users see the
// same actionable failure messages regardless of which --agent flag
// they typed.
//
// The split keeps runInstall's signature stable (a future umbrella
// doesn't reshape the per-host path) and isolates umbrella concerns
// behind one branch — easy to grep for "where do umbrellas execute?".
func runUmbrellaInstall(cmd *cobra.Command, source, agentName string, kind resource.Kind, res resource.Resource, scope adapter.Scope, allowLossy, force, runLifecycle bool, d dirs.Dirs, mf *manifest.Store) error {
	subs, writers, err := buildUmbrella(agentName, d)
	if err != nil {
		return err
	}

	ui := orchestrator.NewUmbrellaInstaller(d, agentName, subs, writers, mf)

	absSrc, _ := filepath.Abs(source)
	result, err := ui.Install(res, scope, orchestrator.InstallOptions{
		Source:        "file://" + absSrc,
		CanonicalRoot: inferCanonicalRoot(absSrc),
		TargetRoot:    targetRootForScope(scope, d),
		AllowLossy:    allowLossy,
		Force:         force,
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

	if runLifecycle {
		if err := runPostInstallLifecycle(agentName); err != nil {
			return fmt.Errorf("installed %s, but post-install lifecycle failed: %w", result.Record.ID, err)
		}
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
		case resource.KindCommand:
			return resource.KindCommand, nil
		case resource.KindMemory:
			return resource.KindMemory, nil
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
	if isDirectAgentsCommandPath(sourcePath) {
		return resource.KindCommand, nil
	}
	if isMemoryPath(sourcePath) {
		return resource.KindMemory, nil
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

func isDirectAgentsCommandPath(sourcePath string) bool {
	ext := filepath.Ext(sourcePath)
	if ext != ".md" && ext != ".toml" {
		return false
	}
	dir := filepath.ToSlash(filepath.Clean(filepath.Dir(sourcePath)))
	return strings.HasSuffix(dir, "/.agents/commands") || dir == ".agents/commands"
}

func isMemoryPath(sourcePath string) bool {
	base := filepath.Base(sourcePath)
	return base == "CLAUDE.md" || base == "GEMINI.md" || base == "AGENTS.md" || base == "ANTIGRAVITY.md" || base == "HERMES.md" || base == ".hermes.md" || base == "SOUL.md"
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
		supportFiles, err := loadSkillSupportFiles(source)
		if err != nil {
			return nil, err
		}
		skill.SupportFiles = supportFiles
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
	case resource.KindCommand:
		command, err := resource.ParseCommand(raw)
		if err != nil {
			return nil, err
		}
		command.WithName(commandNameFromPath(source))
		if errs := validator.ValidateCommand(command); len(errs) > 0 {
			return nil, validationError(errs)
		}
		return command, nil
	case resource.KindMemory:
		memory, err := resource.ParseMemory(raw)
		if err != nil {
			return nil, err
		}
		memory.WithName(filepath.Base(source))
		if errs := validator.ValidateMemory(memory); len(errs) > 0 {
			return nil, validationError(errs)
		}
		return memory, nil
	default:
		return nil, fmt.Errorf("kind %q not supported", kind)
	}
}

func loadSkillSupportFiles(source string) ([]resource.SupportFile, error) {
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return nil, fmt.Errorf("resolve skill source %s: %w", source, err)
	}
	rootAbs, err := filepath.Abs(filepath.Dir(source))
	if err != nil {
		return nil, fmt.Errorf("resolve skill directory for %s: %w", source, err)
	}

	var out []resource.SupportFile
	if err := filepath.WalkDir(rootAbs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == rootAbs {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if path == sourceAbs || relSlash == "SKILL.md" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill support file %s is a symlink; symlinks are not supported", path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat skill support file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill support file %s is not a regular file", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read skill support file %s: %w", path, err)
		}
		out = append(out, resource.SupportFile{
			RelPath: relSlash,
			Content: raw,
			Mode:    info.Mode().Perm(),
		})
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
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

// commandNameFromPath derives the install name for a command resource from
// its source filename (stripping .md or .toml extension).
func commandNameFromPath(source string) string {
	base := filepath.Base(source)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
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

// init validates the adapter/umbrella registry at process start
// (ADR-0014). registry.Validate runs AFTER every adapter and umbrella
// init() because Go runs imported packages' init()s before the
// importer's, and this file blank-imports internal/adapter/all (which
// triggers all registrations). A typo in a sub/writer HostID panics here
// at binary startup, before any user runs an umbrella install — the same
// fail-fast guarantee the prior umbrellaFactories init() carried.
func init() {
	if err := registry.Validate(); err != nil {
		panic(fmt.Sprintf("cli: adapter registry invalid: %v", err))
	}
}

// buildAdapter constructs a single per-host adapter via the registry.
// Umbrellas are NOT resolved here — runInstall recognizes umbrella names
// (registry.IsUmbrella) before calling buildAdapter. An umbrella name
// that reaches here surfaces as "unknown agent X", which only happens on
// a programmer bug in runInstall's branching.
func buildAdapter(name string, d dirs.Dirs) (adapter.Adapter, error) {
	return registry.Build(name, d)
}

// buildUmbrella resolves an umbrella name to (subAdapters, writers) via
// the registry. Caller is runUmbrellaInstall; the lookup is guaranteed
// by runInstall's registry.IsUmbrella check, so a not-found here is an
// internal-error path that should never fire in production.
func buildUmbrella(name string, d dirs.Dirs) ([]adapter.Adapter, map[resource.Kind][]adapter.Adapter, error) {
	subs, writers, ok := registry.BuildUmbrella(name, d)
	if !ok {
		return nil, nil, fmt.Errorf("internal: umbrella %q not registered", name)
	}
	return subs, writers, nil
}

// isBuildableAgent reports whether name is a constructible --agent
// value — either a per-host adapter or an umbrella. checkDefaultAgentMisroute
// uses this to filter the manifest's record set so the "did you mean
// --agent X?" suggestion only names hosts/umbrellas this binary can run.
func isBuildableAgent(name string) bool {
	return registry.IsAdapter(name) || registry.IsUmbrella(name)
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
