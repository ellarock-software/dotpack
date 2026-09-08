package sourceregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ellarock-software/dotpack/internal/adapter"
	_ "github.com/ellarock-software/dotpack/internal/adapter/all"
	"github.com/ellarock-software/dotpack/internal/adapter/registry"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/orchestrator"
	"github.com/ellarock-software/dotpack/internal/resource"
)

// SecurityGateEvaluator checks discovered skills for security compliance.
// It returns a list of blocked skill names and their failure messages.
type SecurityGateEvaluator func(sourceRoot string, skills []DiscoveredSkill, bypassNames []string, d dirs.Dirs) (blocked map[string]string, err error)

// UpdateOptions configures the update or add run.
type UpdateOptions struct {
	AllowLossy          bool
	Force               bool
	BypassSecurityNames []string
	GateEvaluator       SecurityGateEvaluator
}

// SkillAction records the verdict for one skill on one target.
type SkillAction string

const (
	ActionAdded     SkillAction = "added"
	ActionUpdated   SkillAction = "updated"
	ActionRemoved   SkillAction = "removed"
	ActionUnchanged SkillAction = "unchanged"
	ActionConflict  SkillAction = "conflict"
	ActionBlocked   SkillAction = "blocked"
	ActionFailed    SkillAction = "failed"
)

// SkillResult records what happened to a skill during update.
type SkillResult struct {
	Name   string
	Action SkillAction
	Reason string
}

// TargetResult records the outcome of updating one target binding.
type TargetResult struct {
	Target   TargetBinding
	Skills   []SkillResult
	Added    int
	Updated  int
	Removed  int
	Steady   int
	Conflict int
	Blocked  int
	Failed   int
}

// SourceResult records the outcome for one repository.
type SourceResult struct {
	SourceID        string
	Ref             string
	CandidateCommit string
	Targets         []TargetResult
	Err             error
}

// SummaryCounts records aggregated outcome counts across all repositories.
type SummaryCounts struct {
	Added    int
	Updated  int
	Removed  int
	Steady   int
	Conflict int
	Blocked  int
	Failed   int
}

// UpdateResult is the complete result of an update run across all sources.
type UpdateResult struct {
	Sources []SourceResult
	Summary SummaryCounts
}

// HasFailures reports whether any conflict, block, or failure occurred.
func (ur UpdateResult) HasFailures() bool {
	return ur.Summary.Conflict > 0 || ur.Summary.Blocked > 0 || ur.Summary.Failed > 0
}

// ExecuteSourceTargetUpdate executes the update pipeline for a single source and target.
func ExecuteSourceTargetUpdate(
	sourceRec *SourceRecord,
	target *TargetBinding,
	candidateCommit, cacheDir string,
	discovered []DiscoveredSkill,
	allSources []SourceRecord,
	opts UpdateOptions,
	d dirs.Dirs,
	mf *manifest.Store,
) TargetResult {
	tr := TargetResult{Target: *target}

	scope, err := parseScope(target.Scope)
	if err != nil {
		tr.Failed++
		tr.Skills = append(tr.Skills, SkillResult{
			Name:   "*",
			Action: ActionFailed,
			Reason: fmt.Sprintf("invalid scope %q: %v", target.Scope, err),
		})
		return tr
	}

	targetRoot := target.TargetRoot
	if scope == adapter.ScopeProject && targetRoot == "" {
		targetRoot = d.ProjectHome
	}

	// Index discovered skills
	discByName := make(map[string]DiscoveredSkill, len(discovered))
	for _, ds := range discovered {
		discByName[ds.Name] = ds
	}

	// Index currently managed skills for this target
	managedByName := make(map[string]ManagedSkill, len(target.InstalledSkills))
	for _, ms := range target.InstalledSkills {
		managedByName[ms.Name] = ms
	}

	// Load manifest for ownership and claim checking
	m, err := mf.Load()
	if err != nil {
		tr.Failed++
		tr.Skills = append(tr.Skills, SkillResult{
			Name:   "*",
			Action: ActionFailed,
			Reason: fmt.Sprintf("load manifest: %v", err),
		})
		return tr
	}

	// Map existing manifest records for this target
	manifestByID := make(map[string]manifest.Record)
	for _, rec := range m.Installs {
		if rec.Kind == string(resource.KindSkill) && rec.Agent == target.Agent && rec.Scope == target.Scope && rec.TargetRoot == targetRoot {
			manifestByID[rec.ID] = rec
		}
	}

	// Run security gate for discovered candidate skills that will actually be written/installed/updated.
	// Skills that are already installed, intact on disk, and unchanged in content are skipped.
	blockedSkills := make(map[string]string)
	if opts.GateEvaluator != nil && len(discovered) > 0 {
		var toEvaluate []DiscoveredSkill
		for _, ds := range discovered {
			ms, exists := managedByName[ds.Name]
			rec, hasRec := manifestByID[skillRecordID(target.Agent, ds.Name)]
			if !exists || !isSkillIntactAndUnchanged(ms, ds, rec, hasRec) {
				toEvaluate = append(toEvaluate, ds)
			}
		}
		if len(toEvaluate) > 0 {
			b, gErr := opts.GateEvaluator(cacheDir, toEvaluate, opts.BypassSecurityNames, d)
			if gErr != nil {
				tr.Failed++
				tr.Skills = append(tr.Skills, SkillResult{
					Name:   "*",
					Action: ActionFailed,
					Reason: fmt.Sprintf("security gate error: %v", gErr),
				})
				return tr
			}
			for k, v := range b {
				blockedSkills[k] = v
			}
		}
	}

	// Check cross-source collisions
	crossSourceOwners := make(map[string]string)
	for _, otherSrc := range allSources {
		if strings.EqualFold(otherSrc.ID, sourceRec.ID) {
			continue
		}
		for _, ot := range otherSrc.Targets {
			if SameTarget(ot, *target) {
				for _, oms := range ot.InstalledSkills {
					crossSourceOwners[oms.Name] = otherSrc.ID
				}
			}
		}
	}

	newInstalledSkills := make([]ManagedSkill, 0)
	var newRecordsToUpsert []manifest.Record
	var recordsToRemove []manifest.Record

	// 1. Process discovered skills (Added, Updated, Unchanged)
	for _, ds := range discovered {
		// Check cross-source collision
		if otherOwner, exists := crossSourceOwners[ds.Name]; exists {
			tr.Conflict++
			tr.Skills = append(tr.Skills, SkillResult{
				Name:   ds.Name,
				Action: ActionConflict,
				Reason: fmt.Sprintf("collides with skill from repository %q", otherOwner),
			})
			if ms, exists := managedByName[ds.Name]; exists {
				newInstalledSkills = append(newInstalledSkills, ms)
			}
			continue
		}

		ms, isManaged := managedByName[ds.Name]
		existingRec, hasManifestRec := manifestByID[skillRecordID(target.Agent, ds.Name)]

		// Check if blocked by security gate
		if reason, blocked := blockedSkills[ds.Name]; blocked {
			tr.Blocked++
			tr.Skills = append(tr.Skills, SkillResult{
				Name:   ds.Name,
				Action: ActionBlocked,
				Reason: reason,
			})
			if isManaged {
				newInstalledSkills = append(newInstalledSkills, ms)
			}
			continue
		}

		// Check if intact and truly unchanged
		if isManaged && isSkillIntactAndUnchanged(ms, ds, existingRec, hasManifestRec) {
			tr.Steady++
			tr.Skills = append(tr.Skills, SkillResult{
				Name:   ds.Name,
				Action: ActionUnchanged,
				Reason: "content matches applied revision",
			})
			newInstalledSkills = append(newInstalledSkills, ManagedSkill{
				Name:          ds.Name,
				SourceRelPath: ds.SourceRelPath,
				AppliedCommit: ms.AppliedCommit,
				ContentSHA256: ds.ContentSHA256,
			})
			continue
		}

		planFiles, err := planSkillFiles(ds.Skill, target.Agent, scope, d)
		if err != nil {
			tr.Failed++
			tr.Skills = append(tr.Skills, SkillResult{
				Name:   ds.Name,
				Action: ActionFailed,
				Reason: fmt.Sprintf("plan error: %v", err),
			})
			if isManaged {
				newInstalledSkills = append(newInstalledSkills, ms)
			}
			continue
		}

		// Conflict & collision detection:
		if isManaged && hasManifestRec {
			// Check for local modifications of existing claims
			if locallyModified, modFile := checkLocalModifications(existingRec); locallyModified {
				tr.Conflict++
				tr.Skills = append(tr.Skills, SkillResult{
					Name:   ds.Name,
					Action: ActionConflict,
					Reason: fmt.Sprintf("local modifications detected in %s; preserved", modFile),
				})
				newInstalledSkills = append(newInstalledSkills, ms)
				continue
			}

			// Check if new support files in this skill collide with untracked files on disk
			if !opts.Force {
				if collides, collidePath := checkNewFileCollisions(planFiles, existingRec); collides {
					tr.Conflict++
					tr.Skills = append(tr.Skills, SkillResult{
						Name:   ds.Name,
						Action: ActionConflict,
						Reason: fmt.Sprintf("untracked file collision at %s; pass --force or remove file", collidePath),
					})
					newInstalledSkills = append(newInstalledSkills, ms)
					continue
				}
			}
		} else {
			// Not previously managed by this target binding
			if hasManifestRec {
				// Check source provenance
				if !matchesSourceProvenance(existingRec, sourceRec, cacheDir) {
					if !opts.Force {
						tr.Conflict++
						tr.Skills = append(tr.Skills, SkillResult{
							Name:   ds.Name,
							Action: ActionConflict,
							Reason: fmt.Sprintf("collides with existing installation (source %q); explicit adoption or --force required", existingRec.Source),
						})
						continue
					}
				} else {
					// Provenance matches: check if local modifications exist
					if locallyModified, modFile := checkLocalModifications(existingRec); locallyModified {
						tr.Conflict++
						tr.Skills = append(tr.Skills, SkillResult{
							Name:   ds.Name,
							Action: ActionConflict,
							Reason: fmt.Sprintf("local modifications detected in %s; preserved", modFile),
						})
						continue
					}
					// If intact and matches upstream content SHA256, adopt as unchanged
					if existingRec.SourceSHA256 == ds.ContentSHA256 && isSkillIntactAndUnchanged(ManagedSkill{ContentSHA256: ds.ContentSHA256}, ds, existingRec, hasManifestRec) {
						tr.Steady++
						tr.Skills = append(tr.Skills, SkillResult{
							Name:   ds.Name,
							Action: ActionUnchanged,
							Reason: "content matches applied revision",
						})
						newInstalledSkills = append(newInstalledSkills, ManagedSkill{
							Name:          ds.Name,
							SourceRelPath: ds.SourceRelPath,
							AppliedCommit: candidateCommit,
							ContentSHA256: ds.ContentSHA256,
						})
						continue
					}
				}
			} else {
				// Check for untracked collisions on all plan files
				if !opts.Force {
					if collides, collidePath := checkUntrackedPlanCollisions(planFiles); collides {
						tr.Conflict++
						tr.Skills = append(tr.Skills, SkillResult{
							Name:   ds.Name,
							Action: ActionConflict,
							Reason: fmt.Sprintf("untracked file collision at %s; pass --force or remove file", collidePath),
						})
						continue
					}
				}
			}
		}

		// Apply installation / update
		installRes, err := runSkillInstall(ds.Skill, ds.SkillDir, target.Agent, scope, targetRoot, opts.AllowLossy, opts.Force, d, mf)
		if err != nil {
			tr.Failed++
			tr.Skills = append(tr.Skills, SkillResult{
				Name:   ds.Name,
				Action: ActionFailed,
				Reason: err.Error(),
			})
			if isManaged {
				newInstalledSkills = append(newInstalledSkills, ms)
			}
			continue
		}

		action := ActionAdded
		if isManaged {
			action = ActionUpdated
			tr.Updated++
		} else {
			tr.Added++
		}

		tr.Skills = append(tr.Skills, SkillResult{
			Name:   ds.Name,
			Action: action,
			Reason: fmt.Sprintf("applied commit %s", shortSHA(candidateCommit)),
		})

		newInstalledSkills = append(newInstalledSkills, ManagedSkill{
			Name:          ds.Name,
			SourceRelPath: ds.SourceRelPath,
			AppliedCommit: candidateCommit,
			ContentSHA256: ds.ContentSHA256,
		})
		newRecordsToUpsert = append(newRecordsToUpsert, installRes.Record)
	}

	// 2. Process removed skills (upstream deleted)
	for _, ms := range target.InstalledSkills {
		if _, stillDiscovered := discByName[ms.Name]; stillDiscovered {
			continue
		}

		// Skill was deleted upstream!
		recID := skillRecordID(target.Agent, ms.Name)
		existingRec, hasManifestRec := manifestByID[recID]

		// Check for local modifications before removing
		if hasManifestRec {
			if locallyModified, modFile := checkLocalModifications(existingRec); locallyModified {
				tr.Conflict++
				tr.Skills = append(tr.Skills, SkillResult{
					Name:   ms.Name,
					Action: ActionConflict,
					Reason: fmt.Sprintf("upstream removed skill, but local modifications exist in %s; preserved", modFile),
				})
				newInstalledSkills = append(newInstalledSkills, ms)
				continue
			}

			// Remove installed files
			if err := removeClaimedFiles(existingRec); err != nil {
				tr.Failed++
				tr.Skills = append(tr.Skills, SkillResult{
					Name:   ms.Name,
					Action: ActionFailed,
					Reason: fmt.Sprintf("remove files: %v", err),
				})
				newInstalledSkills = append(newInstalledSkills, ms)
				continue
			}

			recordsToRemove = append(recordsToRemove, existingRec)
		}

		tr.Removed++
		tr.Skills = append(tr.Skills, SkillResult{
			Name:   ms.Name,
			Action: ActionRemoved,
			Reason: "removed upstream",
		})
	}

	// Persist manifest records and collect errors
	var persistErrors []error
	for _, rec := range newRecordsToUpsert {
		if err := mf.Upsert(rec); err != nil {
			persistErrors = append(persistErrors, fmt.Errorf("upsert manifest record %s: %w", rec.ID, err))
		}
	}
	for _, rec := range recordsToRemove {
		if err := mf.RemoveRecord(rec); err != nil {
			persistErrors = append(persistErrors, fmt.Errorf("remove manifest record %s: %w", rec.ID, err))
		}
	}
	if len(persistErrors) > 0 {
		tr.Failed++
		tr.Skills = append(tr.Skills, SkillResult{
			Name:   "*",
			Action: ActionFailed,
			Reason: fmt.Sprintf("manifest persistence error: %v", errors.Join(persistErrors...)),
		})
	}

	// Only advance target AppliedCommit when no failure, block, conflict, or manifest persistence error occurred
	if tr.Failed == 0 && tr.Blocked == 0 && tr.Conflict == 0 && len(persistErrors) == 0 {
		tr.Target.AppliedCommit = candidateCommit
	} else {
		tr.Target.AppliedCommit = target.AppliedCommit
	}
	tr.Target.InstalledSkills = newInstalledSkills

	return tr
}

func skillRecordID(agent, name string) string {
	return agent + ":skill:" + name
}

func parseScope(s string) (adapter.Scope, error) {
	switch s {
	case "user":
		return adapter.ScopeUser, nil
	case "project":
		return adapter.ScopeProject, nil
	default:
		return "", fmt.Errorf("unknown scope %q", s)
	}
}

func isSkillIntactAndUnchanged(ms ManagedSkill, ds DiscoveredSkill, rec manifest.Record, hasRec bool) bool {
	if !hasRec || ms.ContentSHA256 != ds.ContentSHA256 {
		return false
	}
	if len(rec.FileClaims) == 0 && len(rec.Files) == 0 {
		return false
	}
	for _, claim := range rec.FileClaims {
		raw, err := os.ReadFile(claim.Path)
		if err != nil {
			return false
		}
		if claim.SHA256 != "" && sha256String(raw) != claim.SHA256 {
			return false
		}
	}
	for _, f := range rec.Files {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	return true
}

func matchesSourceProvenance(rec manifest.Record, sourceRec *SourceRecord, cacheDir string) bool {
	if sourceRec == nil {
		return false
	}
	if rec.Source == sourceRec.URL || rec.Source == "file://"+sourceRec.URL || "file://"+rec.Source == sourceRec.URL {
		return true
	}
	if rec.CanonicalRoot != "" && cacheDir != "" {
		if rec.CanonicalRoot == cacheDir || strings.HasPrefix(rec.CanonicalRoot, cacheDir+string(filepath.Separator)) {
			return true
		}
	}
	if rec.SourcePath != "" && cacheDir != "" {
		if strings.HasPrefix(rec.SourcePath, cacheDir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func checkLocalModifications(rec manifest.Record) (bool, string) {
	for _, claim := range rec.FileClaims {
		raw, err := os.ReadFile(claim.Path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return true, claim.Path
		}
		currentSHA := sha256String(raw)
		if claim.SHA256 != "" && currentSHA != claim.SHA256 {
			return true, claim.Path
		}
	}
	return false, ""
}

func planSkillFiles(skill *resource.Skill, agentName string, scope adapter.Scope, d dirs.Dirs) ([]adapter.FileWrite, error) {
	if registry.IsUmbrella(agentName) {
		subs, writers, ok := registry.BuildUmbrella(agentName, d)
		if !ok {
			return nil, fmt.Errorf("umbrella %q not registered", agentName)
		}
		var files []adapter.FileWrite
		for _, w := range writers[resource.KindSkill] {
			p, err := w.Plan(skill, scope)
			if err != nil {
				return nil, err
			}
			files = append(files, p.Files...)
		}
		_ = subs
		return files, nil
	}

	a, err := registry.Build(agentName, d)
	if err != nil {
		return nil, err
	}
	p, err := a.Plan(skill, scope)
	if err != nil {
		return nil, err
	}
	return p.Files, nil
}

func isPathInClaims(path string, rec manifest.Record) bool {
	clean := filepath.Clean(path)
	for _, c := range rec.FileClaims {
		if filepath.Clean(c.Path) == clean {
			return true
		}
	}
	for _, f := range rec.Files {
		if filepath.Clean(f) == clean {
			return true
		}
	}
	return false
}

func checkNewFileCollisions(files []adapter.FileWrite, rec manifest.Record) (bool, string) {
	for _, fw := range files {
		if !isPathInClaims(fw.Path, rec) {
			if _, err := os.Lstat(fw.Path); err == nil {
				return true, fw.Path
			}
		}
	}
	return false, ""
}

func checkUntrackedPlanCollisions(files []adapter.FileWrite) (bool, string) {
	for _, fw := range files {
		if _, err := os.Lstat(fw.Path); err == nil {
			return true, fw.Path
		}
	}
	return false, ""
}

func runSkillInstall(skill *resource.Skill, skillDir, agentName string, scope adapter.Scope, targetRoot string, allowLossy, force bool, d dirs.Dirs, mf *manifest.Store) (orchestrator.InstallResult, error) {
	opts := orchestrator.InstallOptions{
		Source:        "file://" + filepath.Join(skillDir, "SKILL.md"),
		CanonicalRoot: filepath.Dir(skillDir),
		TargetRoot:    targetRoot,
		AllowLossy:    allowLossy,
		Force:         force,
	}

	if registry.IsUmbrella(agentName) {
		subs, writers, ok := registry.BuildUmbrella(agentName, d)
		if !ok {
			return orchestrator.InstallResult{}, fmt.Errorf("umbrella %q not registered", agentName)
		}
		ui := orchestrator.NewUmbrellaInstaller(d, agentName, subs, writers, mf)
		return ui.Install(skill, scope, opts)
	}

	a, err := registry.Build(agentName, d)
	if err != nil {
		return orchestrator.InstallResult{}, err
	}
	inst := orchestrator.NewInstaller(d, a, mf)
	return inst.Install(skill, scope, opts)
}

func removeClaimedFiles(rec manifest.Record) error {
	var errs []error
	seen := make(map[string]bool)
	for _, claim := range rec.FileClaims {
		if seen[claim.Path] {
			continue
		}
		seen[claim.Path] = true
		if err := os.Remove(claim.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", claim.Path, err))
		} else {
			cleanEmptyDirs(filepath.Dir(claim.Path))
		}
	}
	for _, f := range rec.Files {
		if seen[f] {
			continue
		}
		seen[f] = true
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", f, err))
		} else {
			cleanEmptyDirs(filepath.Dir(f))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func cleanEmptyDirs(dir string) {
	for {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		_ = os.Remove(dir)
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." || parent == "/" {
			break
		}
		dir = parent
	}
}

func sha256String(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
