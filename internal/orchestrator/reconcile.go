package orchestrator

import (
	"fmt"
	"os"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/manifest"
)

// ReconcileStatus is one manifest record compared against the current
// filesystem. It is intentionally provenance-driven: dotpack can safely
// reason about files and merged keys it previously recorded, but it
// cannot infer ownership for arbitrary host config entries that lack a
// manifest claim.
type ReconcileStatus struct {
	Record            manifest.Record
	PresentFiles      []string
	MissingFiles      []string
	DriftedFiles      []FileDrift
	PresentMergedKeys []manifest.MergedKey
	MissingMergedKeys []manifest.MergedKey
	Errors            []string
}

// FileDrift is one manifest-owned file whose current bytes differ from the
// hash recorded at install time.
type FileDrift struct {
	Path           string
	ExpectedSHA256 string
	ActualSHA256   string
}

// HasClaims reports whether any recorded install claim still exists on disk.
func (s ReconcileStatus) HasClaims() bool {
	return len(s.PresentFiles) > 0 || len(s.PresentMergedKeys) > 0
}

// HasDrift reports whether any recorded install claim is missing or
// unreadable relative to the manifest.
func (s ReconcileStatus) HasDrift() bool {
	return len(s.MissingFiles) > 0 || len(s.DriftedFiles) > 0 || len(s.MissingMergedKeys) > 0 || len(s.Errors) > 0
}

// PruneResult reports what stale records were removed and which drifting
// records were deliberately kept because at least one claim is still live.
type PruneResult struct {
	Pruned  []manifest.Record
	Partial []ReconcileStatus
}

// Reconcile compares every manifest record against the filesystem. It
// does not mutate host files or the manifest; the CLI renders the result
// as an audit report.
func (r *Reader) Reconcile() ([]ReconcileStatus, error) {
	m, err := r.manifest.Load()
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	out := make([]ReconcileStatus, 0, len(m.Installs))
	for _, rec := range m.Installs {
		if rec.Scope == string(adapter.ScopeProject) && !recordMatchesTarget(rec, r.dirs.ProjectHome) {
			continue
		}
		st := ReconcileStatus{Record: rec}
		fileClaims := map[string]string{}
		for _, fc := range rec.FileClaims {
			if fc.Path != "" && fc.SHA256 != "" {
				fileClaims[fc.Path] = fc.SHA256
			}
		}
		for _, p := range rec.Files {
			if _, err := os.Lstat(p); err == nil {
				st.PresentFiles = append(st.PresentFiles, p)
				if expected := fileClaims[p]; expected != "" {
					actual, err := fileSHA256(p)
					if err != nil {
						st.Errors = append(st.Errors, fmt.Sprintf("hash file %s: %v", p, err))
					} else if actual != expected {
						st.DriftedFiles = append(st.DriftedFiles, FileDrift{
							Path:           p,
							ExpectedSHA256: expected,
							ActualSHA256:   actual,
						})
					}
				}
				continue
			} else if os.IsNotExist(err) {
				st.MissingFiles = append(st.MissingFiles, p)
				continue
			} else {
				st.Errors = append(st.Errors, fmt.Sprintf("stat file %s: %v", p, err))
			}
		}
		for _, mk := range rec.MergedKeys {
			present, err := mergedKeyExists(mk)
			if err != nil {
				st.Errors = append(st.Errors, err.Error())
				continue
			}
			if present {
				st.PresentMergedKeys = append(st.PresentMergedKeys, mk)
			} else {
				st.MissingMergedKeys = append(st.MissingMergedKeys, mk)
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// PruneStaleRecords removes manifest rows whose recorded claims are all
// absent. Partially-present rows are kept: pruning them would silently
// abandon live host state that dotpack still has enough provenance to
// uninstall.
func (r *Reader) PruneStaleRecords() (PruneResult, error) {
	statuses, err := r.Reconcile()
	if err != nil {
		return PruneResult{}, err
	}
	var result PruneResult
	for _, st := range statuses {
		switch {
		case len(st.Errors) > 0:
			if st.HasDrift() {
				result.Partial = append(result.Partial, st)
			}
		case !st.HasClaims():
			if st.Record.TargetDir != "" {
				_ = removeEmptyDirsUnder(st.Record.TargetDir)
			}
			if err := r.manifest.RemoveRecord(st.Record); err != nil {
				return PruneResult{}, fmt.Errorf("prune %s: %w", st.Record.ID, err)
			}
			result.Pruned = append(result.Pruned, st.Record)
		case st.HasDrift():
			result.Partial = append(result.Partial, st)
		}
	}
	return result, nil
}

func mergedKeyExists(mk manifest.MergedKey) (bool, error) {
	b, err := backendFor(mk.File)
	if err != nil {
		return false, fmt.Errorf("check merged key %s#%s: %w", mk.File, mk.Path, err)
	}
	root, exists, err := b.readRootForPreflight(mk.File)
	if err != nil {
		return false, fmt.Errorf("check merged key %s#%s: %w", mk.File, mk.Path, err)
	}
	if !exists {
		return false, nil
	}
	path, err := b.parsePath(mk.Path)
	if err != nil {
		return false, fmt.Errorf("check merged key %s#%s: parse path: %w", mk.File, mk.Path, err)
	}
	switch adapter.MergedKeyOp(mk.Op) {
	case adapter.MergedKeySet:
		_, exists := getJSONPath(root, path)
		return exists, nil
	case adapter.MergedKeyAppend:
		if mk.Selector == "" {
			return false, fmt.Errorf("check merged key %s#%s: append op requires selector", mk.File, mk.Path)
		}
		leaf, exists := getJSONPath(root, path)
		if !exists {
			return false, nil
		}
		arr, ok := leaf.([]any)
		if !ok {
			return false, nil
		}
		for i, el := range arr {
			hash, err := selectorFor(el)
			if err != nil {
				return false, fmt.Errorf("check merged key %s#%s: hash element %d: %w", mk.File, mk.Path, i, err)
			}
			if hash == mk.Selector {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("check merged key %s#%s: unknown op %q", mk.File, mk.Path, mk.Op)
	}
}

func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(raw), nil
}
