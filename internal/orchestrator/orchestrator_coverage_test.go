package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/resource"
)

type coverageAdapter struct {
	host string
	plan adapter.InstallPlan
	err  error
}

func (a coverageAdapter) HostID() string { return a.host }
func (a coverageAdapter) Plan(resource.Resource, adapter.Scope) (adapter.InstallPlan, error) {
	return a.plan, a.err
}

func TestInstallerErrorBranchesAndBuildRecordMetadata(t *testing.T) {
	tmp := t.TempDir()
	planErr := errors.New("plan boom")
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	inst := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", err: planErr}, store)
	if _, err := inst.Install(&resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{}); !errors.Is(err, planErr) {
		t.Fatalf("plan error = %v; want %v", err, planErr)
	}

	source := filepath.Join(tmp, "src", "SKILL.md")
	writeCoverageFile(t, source, "source")
	target := filepath.Join(tmp, "out", "SKILL.md")
	inst = NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: adapter.InstallPlan{Files: []adapter.FileWrite{{Path: target, Content: []byte("out")}}}}, store)
	inst.now = func() time.Time { return time.Date(2026, 6, 23, 13, 0, 0, 0, time.UTC) }
	result, err := inst.Install(&resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeProject, InstallOptions{
		Source:        "file://" + source,
		CanonicalRoot: filepath.Dir(filepath.Dir(source)),
		TargetRoot:    tmp,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Record.SourceRelPath != "src/SKILL.md" || result.Record.SourceSHA256 == "" || result.Record.TargetRoot == "" {
		t.Fatalf("record metadata incomplete: %+v", result.Record)
	}
	if !strings.HasPrefix(result.Record.CacheKey, "sha256:") {
		t.Fatalf("cache key = %q", result.Record.CacheKey)
	}

	badParent := filepath.Join(tmp, "file-parent")
	if err := os.WriteFile(badParent, []byte("parent is file"), 0o644); err != nil {
		t.Fatalf("write bad parent: %v", err)
	}
	badInst := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: adapter.InstallPlan{Files: []adapter.FileWrite{{Path: filepath.Join(badParent, "child"), Content: []byte("x")}}}}, manifest.NewStore(filepath.Join(tmp, "bad.yaml")))
	if _, err := badInst.Install(&resource.Skill{Name: "bad", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "apply file") {
		t.Fatalf("writeAtomic error = %v; want apply file", err)
	}
}

func TestBuildRecordMergedKeyHashErrorsAndCacheBranches(t *testing.T) {
	ch := make(chan int)
	_, err := buildRecord("host", &resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeUser, adapter.InstallPlan{
		MergedKeys: []adapter.MergedKeyWrite{{File: "/tmp/config.json", Path: "$.x", Value: ch, Op: adapter.MergedKeyAppend}},
	}, InstallOptions{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "compute selector") {
		t.Fatalf("buildRecord selector error = %v; want compute selector", err)
	}
	if _, err := hashMergedValue(ch); err == nil {
		t.Fatal("hashMergedValue should reject unmarshalable value")
	}
	key := cacheKey(adapter.InstallPlan{MergedKeys: []adapter.MergedKeyWrite{{File: "f", Path: "p", Value: map[string]any{"x": "y"}}}})
	if !strings.HasPrefix(key, "sha256:") {
		t.Fatalf("cacheKey = %q", key)
	}
	if got := fileURIPath("http://example.com/x"); got != "" {
		t.Fatalf("fileURIPath non-file = %q; want empty", got)
	}
	if got := cleanAbs(""); got != "" {
		t.Fatalf("cleanAbs empty = %q; want empty", got)
	}

	rec, err := buildRecord("host", &resource.Skill{Name: "sorted", Description: "d", Body: "b"}, adapter.ScopeUser, adapter.InstallPlan{
		MergedKeys: []adapter.MergedKeyWrite{
			{File: "/tmp/b.json", Path: "$.z", Value: "set"},
			{File: "/tmp/a.json", Path: "$.hooks.PreToolUse", Value: map[string]any{"name": "b"}, Op: adapter.MergedKeyAppend},
			{File: "/tmp/a.json", Path: "$.hooks.PreToolUse", Value: map[string]any{"name": "a"}, Op: adapter.MergedKeyAppend},
			{File: "/tmp/a.json", Path: "$.alpha", Value: "set"},
		},
	}, InstallOptions{}, time.Now())
	if err != nil {
		t.Fatalf("buildRecord sorted merged keys: %v", err)
	}
	if len(rec.MergedKeys) != 4 {
		t.Fatalf("merged keys = %+v; want 4", rec.MergedKeys)
	}
	if rec.MergedKeys[0].File != "/tmp/a.json" || rec.MergedKeys[0].Path != "$.alpha" {
		t.Fatalf("merged keys not sorted by file/path: %+v", rec.MergedKeys)
	}
	if rec.MergedKeys[1].Selector == "" || rec.MergedKeys[2].Selector == "" || rec.MergedKeys[1].Selector > rec.MergedKeys[2].Selector {
		t.Fatalf("append merged keys not sorted by selector: %+v", rec.MergedKeys)
	}
}

func TestInstallerMergedKeyCollisionAndLossyNoHostMessage(t *testing.T) {
	lossy := (&LossyError{
		Host: "host",
		Reasons: []adapter.LossyReason{{
			FieldPath:        "x",
			CanonicalConcept: "concept_without_hosts",
		}},
	}).Error()
	if !strings.Contains(lossy, "no host natively supports it") {
		t.Fatalf("LossyError no-host rendering = %q", lossy)
	}

	tmp := t.TempDir()
	config := filepath.Join(tmp, "settings.json")
	if err := os.Symlink(filepath.Join(tmp, "target.json"), config); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	inst := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: adapter.InstallPlan{
		MergedKeys: []adapter.MergedKeyWrite{{File: config, Path: "$.x", Value: "y"}},
	}}, manifest.NewStore(filepath.Join(tmp, "installs.yaml")))
	_, err := inst.Install(&resource.Skill{Name: "mk-collision", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{})
	var ce *CollisionError
	if !errors.As(err, &ce) || len(ce.Paths) == 0 || !strings.Contains(ce.Paths[0], "symlink") {
		t.Fatalf("merged-key collision err=%v; want symlink CollisionError", err)
	}
}

func TestWriteAtomicAndRemoveStaleFileErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "out.txt")
	if err := writeAtomic(adapter.FileWrite{Path: target, Content: []byte("x")}); err != nil {
		t.Fatalf("writeAtomic default mode: %v", err)
	}
	if st, err := os.Stat(target); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("writeAtomic mode stat=%v err=%v", st, err)
	}
	if err := removeStaleFile(adapter.FileRemove{}); err != nil {
		t.Fatalf("removeStaleFile empty: %v", err)
	}
	if err := removeStaleFile(adapter.FileRemove{Path: filepath.Join(tmp, "missing")}); err != nil {
		t.Fatalf("removeStaleFile missing: %v", err)
	}
	dir := filepath.Join(tmp, "dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if err := removeStaleFile(adapter.FileRemove{Path: dir}); err == nil {
		t.Fatal("removeStaleFile non-empty directory should error")
	}
}
