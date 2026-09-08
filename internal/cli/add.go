package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/sourceregistry"
)

func newAddCmd() *cobra.Command {
	var (
		agentName  string
		scopeName  string
		targetRoot string
		skillsPath string
		allowLossy bool
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "add <repository>",
		Short: "Register an upstream repository and install its skills",
		Long: `Register an upstream GitHub repository and install its discoverable skills.

Registration saves the repository and its target destination in ~/.dotpack/sources.yaml,
so running 'dotpack update' later refreshes all registered repositories across their
saved destinations.

By default, add uses user scope and the agents-cli umbrella target. An optional
--skills-path flag restricts discovery to a specific directory within the repository.`,
		Example: `  dotpack add mattpocock/skills
  dotpack add github:BuilderIO/skills --agent claude-code --scope user
  dotpack add https://github.com/owner/repo@v1.0.0 --agent agents-cli --scope project`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(cmd, args[0], agentName, scopeName, targetRoot, skillsPath, allowLossy, force)
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", "agents-cli", "Target host adapter or umbrella (default: agents-cli)")
	cmd.Flags().StringVar(&scopeName, "scope", "user", "Install scope (user|project, default: user)")
	cmd.Flags().StringVar(&targetRoot, "target", "", "Target project root (used when --scope project)")
	cmd.Flags().StringVar(&skillsPath, "skills-path", "", "Optional directory path within the repository to restrict skill discovery")
	cmd.Flags().BoolVar(&allowLossy, "allow-lossy", false, "Proceed even if the adapter cannot honour all source fields")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite untracked collisions at the install target")
	addSkillSecurityBypassFlag(cmd)
	return cmd
}

func runAdd(cmd *cobra.Command, rawSource, agentName, scopeName, targetRoot, skillsPath string, allowLossy, force bool) error {
	d, err := dirs.FromEnv()
	if err != nil {
		return err
	}

	scope, err := parseScope(scopeName)
	if err != nil {
		return err
	}

	if scope == adapter.ScopeProject {
		d, targetRoot, err = dirsForTarget(targetRoot)
		if err != nil {
			return err
		}
	} else {
		targetRoot = ""
	}

	parsed, err := sourceregistry.ParseSourceSpec(rawSource)
	if err != nil {
		return err
	}

	// Concurrency lock
	lock, err := sourceregistry.AcquireLock(d.DotpackHome)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	store := sourceregistry.NewStore(filepath.Join(d.DotpackHome, "sources.yaml"))
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))

	// Fetch candidate revision
	commitSHA, cacheDir, err := sourceregistry.FetchSource(cmd.Context(), parsed, d)
	if err != nil {
		return fmt.Errorf("add: fetch repository %s: %w", parsed.ID, err)
	}

	// Register source and target binding
	targetBinding := sourceregistry.TargetBinding{
		Agent:      agentName,
		Scope:      scopeName,
		TargetRoot: targetRoot,
	}

	existingSrc, found, _ := store.GetSource(parsed.ID)
	if found && existingSrc != nil {
		for _, t := range existingSrc.Targets {
			if sourceregistry.SameTarget(t, targetBinding) {
				targetBinding = t
				break
			}
		}
	}

	srcRec := sourceregistry.SourceRecord{
		ID:                parsed.ID,
		URL:               parsed.CloneURL,
		Ref:               parsed.Ref,
		LastFetchedCommit: commitSHA,
		DiscoveryPath:     skillsPath,
		Targets:           []sourceregistry.TargetBinding{targetBinding},
	}

	if err := store.UpsertSource(srcRec); err != nil {
		return fmt.Errorf("add: save source registry: %w", err)
	}

	// Discover skills
	discovered, err := sourceregistry.DiscoverSkills(cacheDir, skillsPath)
	if err != nil {
		return fmt.Errorf("add: discover skills in %s: %w", parsed.ID, err)
	}

	if len(discovered) == 0 {
		cmd.Printf("Registered %s (commit: %s); no skills discovered\n", parsed.ID, shortDigest(commitSHA))
		return nil
	}

	// Read current registry to pass all registered sources for collision checks
	reg, _ := store.Load()
	var allSources []sourceregistry.SourceRecord
	if reg != nil {
		allSources = reg.Sources
	}

	bypassNames := requestedSkillSecurityBypasses(cmd)
	evaluator := makeCLISecurityGateEvaluator(cmd)

	opts := sourceregistry.UpdateOptions{
		AllowLossy:          allowLossy,
		Force:               force,
		BypassSecurityNames: bypassNames,
		GateEvaluator:       evaluator,
	}

	tr := sourceregistry.ExecuteSourceTargetUpdate(
		&srcRec,
		&targetBinding,
		commitSHA,
		cacheDir,
		discovered,
		allSources,
		opts,
		d,
		mf,
	)

	// Save the updated target binding (installed skills and applied commit)
	srcRec.Targets = []sourceregistry.TargetBinding{tr.Target}
	if err := store.UpsertSource(srcRec); err != nil {
		return fmt.Errorf("add: save applied skills to registry: %w", err)
	}

	// Output report
	cmd.Printf("Registered repository %s (commit: %s)\n", parsed.ID, shortDigest(commitSHA))
	cmd.Printf("Target: %s (scope: %s", agentName, scopeName)
	if targetRoot != "" {
		cmd.Printf(", root: %s", targetRoot)
	}
	cmd.Printf(")\n")

	for _, s := range tr.Skills {
		switch s.Action {
		case sourceregistry.ActionAdded:
			cmd.Printf("  + added: %s\n", s.Name)
		case sourceregistry.ActionUpdated:
			cmd.Printf("  ~ updated: %s\n", s.Name)
		case sourceregistry.ActionUnchanged:
			cmd.Printf("  = unchanged: %s\n", s.Name)
		case sourceregistry.ActionConflict:
			cmd.Printf("  ! conflict: %s (%s)\n", s.Name, s.Reason)
		case sourceregistry.ActionBlocked:
			cmd.Printf("  X blocked: %s (%s)\n", s.Name, s.Reason)
		case sourceregistry.ActionFailed:
			cmd.Printf("  ! failed: %s (%s)\n", s.Name, s.Reason)
		}
	}

	if tr.Conflict > 0 || tr.Blocked > 0 || tr.Failed > 0 {
		var msgs []string
		if tr.Blocked > 0 {
			msgs = append(msgs, fmt.Sprintf("%d skill(s) blocked by security gate; run 'dotpack approve-skill' to approve", tr.Blocked))
		}
		if tr.Conflict > 0 {
			msgs = append(msgs, fmt.Sprintf("%d skill(s) had conflicts", tr.Conflict))
		}
		if tr.Failed > 0 {
			msgs = append(msgs, fmt.Sprintf("%d skill(s) failed", tr.Failed))
		}
		return fmt.Errorf("add completed with issues: %s", strings.Join(msgs, "; "))
	}

	return nil
}

func makeCLISecurityGateEvaluator(cmd *cobra.Command) sourceregistry.SecurityGateEvaluator {
	return func(sourceRoot string, skills []sourceregistry.DiscoveredSkill, bypassNames []string, d dirs.Dirs) (map[string]string, error) {
		skillFiles := make([]string, 0, len(skills))
		for _, s := range skills {
			skillFiles = append(skillFiles, filepath.Join(s.SkillDir, "SKILL.md"))
		}

		err := ensureMandatorySkillScanForSkillFiles(cmd, "update", skillFiles, sourceRoot, bypassNames, d)
		if err == nil {
			return nil, nil
		}

		blocked := make(map[string]string)
		for _, s := range skills {
			blocked[s.Name] = err.Error()
		}
		return blocked, nil
	}
}
