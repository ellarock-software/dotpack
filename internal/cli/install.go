package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	orch := orchestrator.New(d, a, mf)

	absSrc, _ := filepath.Abs(source)
	result, err := orch.Install(res, scope, orchestrator.InstallOptions{
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

func buildAdapter(name string, d dirs.Dirs) (adapter.Adapter, error) {
	switch name {
	case "claude-code":
		return claudecode.New(d), nil
	case "gemini-cli":
		return gemini.New(d), nil
	case "codex":
		return codex.New(d), nil
	case "agents-cli":
		return nil, fmt.Errorf("agent %q not yet implemented", name)
	default:
		return nil, fmt.Errorf("unknown agent %q", name)
	}
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
