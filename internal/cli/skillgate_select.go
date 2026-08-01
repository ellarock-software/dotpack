package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillgate/registry"
)

const (
	skillGateFlag = "skill-gate"
	skillGateEnv  = "DOTPACK_SKILL_GATE"
)

// selectedSkillGate is the operator's choice, captured by the root
// command before any subcommand runs.
//
// It is package state rather than a plumbed parameter because the gate
// funnel is reached from five call sites across three files, and
// threading a value through all of them would create five opportunities
// to accidentally source it from somewhere else. There is exactly one
// writer, resolveSelectedSkillGate, and it reads only the flag and the
// environment.
var selectedSkillGate string

// addSkillGateFlag registers the operator's gate selection on the root
// command.
//
// SECURITY INVARIANT: gate selection is operator-controlled only. It is
// never read from the package being installed, from its policy root, or
// from any file in a source tree. A source that could choose its own
// gate could choose the weakest one, which would make every other
// control here decorative. That is why this is a flag and an environment
// variable and nothing else.
func addSkillGateFlag(root *cobra.Command) {
	root.PersistentFlags().String(skillGateFlag, "", fmt.Sprintf(
		"Skill security gate to enforce: %s (default %q, or $%s). Operator-controlled; dotpack never reads gate selection from the package being installed",
		strings.Join(registry.Names(), ", "), registry.DefaultName(), skillGateEnv))
}

// resolveSelectedSkillGate captures the gate choice. Precedence: the
// explicit flag, then the environment, then the registry default.
//
// An unknown name is an error rather than a fall back to the default: a
// typo'd --skill-gate must not silently run a different gate than the
// operator asked for, because they would believe a gate ran that did not.
func resolveSelectedSkillGate(cmd *cobra.Command) error {
	name := ""
	if f := cmd.Flags().Lookup(skillGateFlag); f != nil && f.Changed {
		name = strings.TrimSpace(f.Value.String())
	}
	if name == "" {
		name = strings.TrimSpace(os.Getenv(skillGateEnv))
	}
	if name == "" {
		name = registry.DefaultName()
	}
	if !registry.Has(name) {
		return fmt.Errorf("unknown skill gate %q; registered gates: %s", name, strings.Join(registry.Names(), ", "))
	}
	selectedSkillGate = name
	return nil
}

// currentSkillGate returns the resolved gate name, falling back to the
// registry default when the root command's hook has not run -- which
// happens in tests that construct a subcommand directly.
func currentSkillGate() string {
	if selectedSkillGate != "" {
		return selectedSkillGate
	}
	return registry.DefaultName()
}

// resolvePolicyRoot determines which repository owns .dotpack/ policy
// for a run, and whether that repository can be trusted to supply it.
//
// The trust question is not academic. dotpack clones a `github:` source
// into DotpackHome and then hands that clone's path back as the source
// root, so a remote repository that ships .dotpack/skillgate/baselines/
// would be supplying its own approvals -- approving itself. The same
// applies to the incumbent gate's baseline directory, which has honoured
// remote-shipped suppressions since it was written.
//
// A policy root under DotpackHome is therefore always untrusted: nothing
// dotpack fetched can vouch for itself.
func resolvePolicyRoot(sourceRoot string, d dirs.Dirs) (root string, trusted bool, err error) {
	if strings.TrimSpace(sourceRoot) == "" {
		return "", false, nil
	}
	policyRoot, err := filepath.Abs(filepath.Clean(sourceRoot))
	if err != nil {
		return "", false, fmt.Errorf("resolve policy root %s: %w", sourceRoot, err)
	}

	// A host root such as .agents or .claude is a layout directory inside
	// the repository, not the repository itself.
	switch filepath.Base(policyRoot) {
	case ".agents", ".claude", ".gemini", ".antigravity", ".opencode", ".hermes":
		policyRoot = filepath.Dir(policyRoot)
	}

	return policyRoot, isTrustedPolicyRoot(policyRoot, d), nil
}

// isTrustedPolicyRoot reports whether approvals found under policyRoot
// may be honoured. Anything inside DotpackHome was put there by dotpack
// itself -- the github source cache lives there -- so it cannot be a
// trusted source of policy.
func isTrustedPolicyRoot(policyRoot string, d dirs.Dirs) bool {
	home := strings.TrimSpace(d.DotpackHome)
	if home == "" {
		return true
	}
	absHome, err := filepath.Abs(filepath.Clean(home))
	if err != nil {
		// Cannot prove the root is outside dotpack-managed state, so do
		// not extend trust to it.
		return false
	}
	if policyRoot == absHome {
		return false
	}
	return !strings.HasPrefix(policyRoot, absHome+string(filepath.Separator))
}
