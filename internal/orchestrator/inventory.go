package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/manifest"
)

const (
	InventoryTracked            = "tracked"
	InventoryDrifted            = "drifted"
	InventoryMissing            = "missing"
	InventoryCanonicalUntracked = "canonical-untracked"
	InventoryForeignUntracked   = "foreign-untracked"
)

// FileObservation is one materialized host file found by a CLI scanner.
type FileObservation struct {
	Path      string
	RelPath   string
	TargetDir string
	Agent     string
	Kind      string
	Name      string
}

// ExpectedFile is one file output that would be emitted from canonical input.
type ExpectedFile struct {
	Path   string
	Source string
	SHA256 string
}

// InventoryItem is one classified materialized output or missing manifest claim.
type InventoryItem struct {
	Status         string
	Path           string
	RelPath        string
	TargetDir      string
	Agent          string
	Kind           string
	Name           string
	RecordID       string
	ExpectedSHA256 string
	ActualSHA256   string
	Source         string
}

// ResetOptions controls target-scoped removal of materialized outputs.
type ResetOptions struct {
	TargetRoot       string
	CanonicalRoot    string
	IncludeUntracked bool
	ObservedFiles    []FileObservation
}

// ResetResult reports manifest-owned uninstalls and any untracked scanned
// files removed by a hard reset.
type ResetResult struct {
	Uninstalled      []UninstallResult
	RemovedUntracked []string
	MissingUntracked []string
}

// InventoryFiles compares observed materialized file outputs against manifest
// file claims and optional canonical expectations.
func (r *Reader) InventoryFiles(observed []FileObservation, expected []ExpectedFile, targetRoot string) ([]InventoryItem, error) {
	m, err := r.manifest.Load()
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}

	expectedByPath := map[string]ExpectedFile{}
	for _, e := range expected {
		if e.Path == "" {
			continue
		}
		expectedByPath[filepath.Clean(e.Path)] = e
	}

	observedByPath := map[string]FileObservation{}
	for _, obs := range observed {
		if obs.Path == "" {
			continue
		}
		obs.Path = filepath.Clean(obs.Path)
		observedByPath[obs.Path] = obs
	}

	claimed := map[string]struct{}{}
	var out []InventoryItem
	for _, rec := range m.Installs {
		if !recordMatchesTarget(rec, targetRoot) {
			continue
		}
		claims := recordFileClaims(rec)
		for _, p := range rec.Files {
			p = filepath.Clean(p)
			claimed[p] = struct{}{}
			item := InventoryItem{
				Status:         InventoryMissing,
				Path:           p,
				Agent:          rec.Agent,
				Kind:           rec.Kind,
				Name:           shortName(rec.ID),
				RecordID:       rec.ID,
				ExpectedSHA256: claims[p],
			}
			if obs, ok := observedByPath[p]; ok {
				item.RelPath = obs.RelPath
				item.TargetDir = obs.TargetDir
				item.ActualSHA256, err = fileSHA256(p)
				if err != nil {
					return nil, fmt.Errorf("hash observed file %s: %w", p, err)
				}
				if item.ExpectedSHA256 != "" && item.ActualSHA256 != item.ExpectedSHA256 {
					item.Status = InventoryDrifted
				} else {
					item.Status = InventoryTracked
				}
			}
			out = append(out, item)
		}
	}

	for _, obs := range observedByPath {
		if _, ok := claimed[obs.Path]; ok {
			continue
		}
		actual, err := fileSHA256(obs.Path)
		if err != nil {
			return nil, fmt.Errorf("hash untracked file %s: %w", obs.Path, err)
		}
		item := InventoryItem{
			Status:       InventoryForeignUntracked,
			Path:         obs.Path,
			RelPath:      obs.RelPath,
			TargetDir:    obs.TargetDir,
			Agent:        obs.Agent,
			Kind:         obs.Kind,
			Name:         obs.Name,
			ActualSHA256: actual,
		}
		if exp, ok := expectedByPath[obs.Path]; ok {
			item.ExpectedSHA256 = exp.SHA256
			item.Source = exp.Source
			if exp.SHA256 != "" && exp.SHA256 == actual {
				item.Status = InventoryCanonicalUntracked
			}
		}
		out = append(out, item)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// ResetMaterialized removes manifest-owned claims that match the target and
// optional canonical root. If IncludeUntracked is true, it also removes the
// observed file outputs not claimed by matching manifest records.
func (r *Reader) ResetMaterialized(opts ResetOptions) (ResetResult, error) {
	m, err := r.manifest.Load()
	if err != nil {
		return ResetResult{}, fmt.Errorf("load manifest: %w", err)
	}

	var result ResetResult
	claimed := map[string]struct{}{}
	for _, rec := range m.Installs {
		if !recordMatchesTarget(rec, opts.TargetRoot) || !recordMatchesCanonical(rec, opts.CanonicalRoot) {
			continue
		}
		for _, p := range rec.Files {
			claimed[filepath.Clean(p)] = struct{}{}
		}
		uninstalled, err := r.uninstallRecord(rec)
		if err != nil {
			return ResetResult{}, err
		}
		if err := r.manifest.RemoveRecord(rec); err != nil {
			return ResetResult{}, fmt.Errorf("remove manifest record %s: %w", rec.ID, err)
		}
		result.Uninstalled = append(result.Uninstalled, uninstalled)
	}

	if opts.IncludeUntracked {
		for _, obs := range opts.ObservedFiles {
			p := filepath.Clean(obs.Path)
			if _, ok := claimed[p]; ok {
				continue
			}
			switch err := os.Remove(p); {
			case err == nil:
				result.RemovedUntracked = append(result.RemovedUntracked, p)
				if obs.TargetDir != "" {
					_ = removeEmptyDirsUnder(obs.TargetDir)
				}
			case os.IsNotExist(err):
				result.MissingUntracked = append(result.MissingUntracked, p)
			default:
				return ResetResult{}, fmt.Errorf("remove untracked %s: %w", p, err)
			}
		}
	}

	sort.Strings(result.RemovedUntracked)
	sort.Strings(result.MissingUntracked)
	return result, nil
}

func (r *Reader) uninstallRecord(rec manifest.Record) (UninstallResult, error) {
	var removed, missing []string
	for _, p := range rec.Files {
		err := os.Remove(p)
		switch {
		case err == nil:
			removed = append(removed, p)
		case os.IsNotExist(err):
			missing = append(missing, p)
		default:
			return UninstallResult{}, fmt.Errorf("remove %s: %w", p, err)
		}
	}

	for _, mk := range rec.MergedKeys {
		if err := unmergeKey(MergedKeySelector{
			File:     mk.File,
			Path:     mk.Path,
			Op:       adapter.MergedKeyOp(mk.Op),
			Selector: mk.Selector,
		}); err != nil {
			return UninstallResult{}, fmt.Errorf("un-merge key %s in %s: %w", mk.Path, mk.File, err)
		}
	}

	dirRemoved := false
	if rec.TargetDir != "" {
		if err := removeEmptyDirsUnder(rec.TargetDir); err == nil {
			dirRemoved = true
		}
	}

	return UninstallResult{
		Record:           rec,
		RemovedPaths:     removed,
		MissingPaths:     missing,
		TargetDirRemoved: dirRemoved,
	}, nil
}

func removeEmptyDirsUnder(root string) error {
	if _, err := os.Stat(root); err != nil {
		return err
	}
	var dirs []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Remove(dirs[i]); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if i == 0 {
				return err
			}
		}
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	return os.ErrExist
}

func recordFileClaims(rec manifest.Record) map[string]string {
	out := map[string]string{}
	for _, fc := range rec.FileClaims {
		if fc.Path != "" && fc.SHA256 != "" {
			out[filepath.Clean(fc.Path)] = fc.SHA256
		}
	}
	return out
}

func recordMatchesTarget(rec manifest.Record, targetRoot string) bool {
	targetRoot = cleanAbs(targetRoot)
	if targetRoot == "" {
		return true
	}
	if rec.TargetRoot != "" && cleanAbs(rec.TargetRoot) == targetRoot {
		return true
	}
	for _, p := range rec.Files {
		if pathWithin(targetRoot, p) {
			return true
		}
	}
	for _, mk := range rec.MergedKeys {
		if pathWithin(targetRoot, mk.File) {
			return true
		}
	}
	return false
}

func recordMatchesCanonical(rec manifest.Record, canonicalRoot string) bool {
	canonicalRoot = cleanAbs(canonicalRoot)
	if canonicalRoot == "" {
		return true
	}
	if rec.CanonicalRoot != "" && cleanAbs(rec.CanonicalRoot) == canonicalRoot {
		return true
	}
	sourcePath := rec.SourcePath
	if sourcePath == "" {
		sourcePath = fileURIPath(rec.Source)
	}
	if sourcePath != "" && pathWithin(canonicalRoot, sourcePath) {
		return true
	}
	return false
}

func pathWithin(root, path string) bool {
	root = cleanAbs(root)
	path = cleanAbs(path)
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func shortName(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		return id[i+1:]
	}
	return id
}
