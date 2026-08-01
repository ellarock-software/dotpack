package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillgate/registry"
)

func resolveGateVia(t *testing.T, args ...string) (string, error) {
	t.Helper()
	prev := selectedSkillGate
	t.Cleanup(func() { selectedSkillGate = prev })
	selectedSkillGate = ""

	// "version" rather than "--help": cobra short-circuits help without
	// running PersistentPreRunE, so a help probe would never exercise the
	// resolution this test is about.
	root := NewRootCmd()
	root.SetOut(io_DiscardWriter())
	root.SetErr(io_DiscardWriter())
	root.SetArgs(append(args, "version"))
	err := root.Execute()
	return selectedSkillGate, err
}

func TestDefaultGateIsTheDeltaGate(t *testing.T) {
	t.Setenv(skillGateEnv, "")
	got, err := resolveGateVia(t)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got != "skillgate" {
		t.Fatalf("default gate = %q, want skillgate", got)
	}
}

func TestSkillGateFlagAndEnvironmentSelectAGate(t *testing.T) {
	t.Run("flag", func(t *testing.T) {
		t.Setenv(skillGateEnv, "")
		got, err := resolveGateVia(t, "--skill-gate", spectorGateName)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got != spectorGateName {
			t.Fatalf("gate = %q, want %q", got, spectorGateName)
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv(skillGateEnv, spectorGateName)
		got, err := resolveGateVia(t)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got != spectorGateName {
			t.Fatalf("gate = %q, want %q", got, spectorGateName)
		}
	})

	t.Run("flag beats environment", func(t *testing.T) {
		t.Setenv(skillGateEnv, spectorGateName)
		got, err := resolveGateVia(t, "--skill-gate", "skillgate")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got != "skillgate" {
			t.Fatalf("gate = %q, want the flag to win", got)
		}
	})
}

// A typo must not silently run a different gate than the operator asked
// for; they would believe a gate ran that did not.
func TestUnknownGateNameIsRejectedAndListsTheRegisteredGates(t *testing.T) {
	t.Setenv(skillGateEnv, "")
	_, err := resolveGateVia(t, "--skill-gate", "skilgate")
	if err == nil {
		t.Fatal("an unknown gate name was accepted")
	}
	for _, want := range append([]string{"skilgate"}, registry.Names()...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// THE security invariant. A source that could choose its own gate could
// choose the weakest one, which would make every other control
// decorative. Gate selection comes from the operator and nowhere else.
func TestGateSelectionIsNeverReadFromTheSourceTree(t *testing.T) {
	t.Setenv(skillGateEnv, "")
	source := t.TempDir()

	// Every plausible place a package might try to declare a gate.
	for _, rel := range []string{
		".dotpack/skillgate.json",
		".dotpack/config.json",
		".dotpack/skill-gate",
		"dotpack.yaml",
		".dotpackrc",
	} {
		path := filepath.Join(source, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(`{"skill_gate":"skillspector","skill-gate":"skillspector"}`), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	t.Setenv("DOTPACK_PROJECT_HOME", source)
	got, err := resolveGateVia(t)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got != registry.DefaultName() {
		t.Fatalf("gate = %q; a file in the source tree changed the gate selection", got)
	}
}

// The other half of the same hole: dotpack clones a github: source into
// DotpackHome and hands that path back as the source root, so approvals
// found there would be a repository vouching for itself.
func TestPolicyRootInsideDotpackHomeIsUntrusted(t *testing.T) {
	home := t.TempDir()
	d := dirs.Dirs{DotpackHome: home}

	fetched := filepath.Join(home, "cache", "github", "attacker", "skills", "main-abc123")
	if err := os.MkdirAll(fetched, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	root, trusted, err := resolvePolicyRoot(fetched, d)
	if err != nil {
		t.Fatalf("resolvePolicyRoot: %v", err)
	}
	if root == "" {
		t.Fatal("no policy root resolved")
	}
	if trusted {
		t.Fatal("a policy root inside DotpackHome was trusted; a fetched repository could ship its own approvals and approve itself")
	}
}

func TestPolicyRootOutsideDotpackHomeIsTrusted(t *testing.T) {
	d := dirs.Dirs{DotpackHome: t.TempDir()}
	project := t.TempDir()

	root, trusted, err := resolvePolicyRoot(project, d)
	if err != nil {
		t.Fatalf("resolvePolicyRoot: %v", err)
	}
	if !trusted {
		t.Fatal("an ordinary project root was not trusted")
	}
	if root != filepath.Clean(project) {
		t.Fatalf("policy root = %q, want %q", root, project)
	}
}

// A host layout directory is inside the repository, not the repository.
// Approvals live at the repository root.
func TestPolicyRootStripsHostLayoutDirectories(t *testing.T) {
	d := dirs.Dirs{DotpackHome: t.TempDir()}
	project := t.TempDir()

	for _, host := range []string{".agents", ".claude", ".gemini", ".antigravity", ".opencode", ".hermes"} {
		got, _, err := resolvePolicyRoot(filepath.Join(project, host), d)
		if err != nil {
			t.Fatalf("resolvePolicyRoot(%s): %v", host, err)
		}
		if got != filepath.Clean(project) {
			t.Errorf("policy root for %s = %q, want %q", host, got, project)
		}
	}
}

// A sibling directory whose name merely starts with DotpackHome's path
// must not be swept up as dotpack-managed state.
func TestPolicyRootTrustCheckIsPathAnchored(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "dotpack")
	sibling := filepath.Join(base, "dotpack-workspace")
	for _, dir := range []string{home, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	_, trusted, err := resolvePolicyRoot(sibling, dirs.Dirs{DotpackHome: home})
	if err != nil {
		t.Fatalf("resolvePolicyRoot: %v", err)
	}
	if !trusted {
		t.Fatalf("%q was treated as being inside %q by prefix match alone", sibling, home)
	}
}

func TestSkillGateFlagIsAvailableOnSkillBearingSubcommands(t *testing.T) {
	root := NewRootCmd()
	if root.PersistentFlags().Lookup(skillGateFlag) == nil {
		t.Fatal("--skill-gate is not registered on the root command")
	}
	for _, name := range []string{"install", "install-all", "import", "inventory", "sync-back", "approve-skill"} {
		sub, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		if sub.InheritedFlags().Lookup(skillGateFlag) == nil {
			t.Errorf("%s does not inherit --skill-gate", name)
		}
	}
}

// Approving is the opposite of bypassing; offering both on one command
// invites reaching for the wrong one.
func TestApproveSkillDoesNotOfferASecurityBypass(t *testing.T) {
	root := NewRootCmd()
	sub, _, err := root.Find([]string{"approve-skill"})
	if err != nil {
		t.Fatalf("find approve-skill: %v", err)
	}
	if sub.Flags().Lookup(skillSecurityBypassFlag) != nil {
		t.Error("approve-skill offers --skill-bypass-security")
	}
}

func TestApproveSkillRequiresAnExplicitSelection(t *testing.T) {
	source := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", source)

	root := NewRootCmd()
	root.SetOut(io_DiscardWriter())
	root.SetErr(io_DiscardWriter())
	root.SetArgs([]string{"approve-skill", source})

	err := root.Execute()
	if err == nil {
		t.Fatal("approve-skill ran with neither --skill nor --all")
	}
	if !strings.Contains(err.Error(), "--all") {
		t.Errorf("error does not explain the required selection: %v", err)
	}
}
