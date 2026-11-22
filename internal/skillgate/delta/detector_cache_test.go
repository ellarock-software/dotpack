package delta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ellarock-software/dotpack/internal/skillgate"
	"github.com/ellarock-software/dotpack/internal/skillscanner"
)

func cacheRuntime(t *testing.T) skillscanner.Runtime {
	t.Helper()
	rt := fakeRuntime()
	rt.ScannerBin = filepath.Join(t.TempDir(), "scanner")
	cacheWrite(t, rt.ScannerBin, "scanner version one")
	return rt
}

func cacheWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func detectorReportTo(args []string, report string) error {
	for i, arg := range args {
		if arg == "--output-json" && i+1 < len(args) {
			return os.WriteFile(args[i+1], []byte(report), 0o644)
		}
	}
	return errors.New("missing report argument")
}

func countedDetector(t *testing.T, report func(string) string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	prev := runDetector
	runDetector = func(_ context.Context, _ string, args ...string) error {
		calls.Add(1)
		return detectorReportTo(args, report(args[1]))
	}
	t.Cleanup(func() { runDetector = prev })
	return &calls
}

func seedCacheBaseline(t *testing.T, root, name, pkg string, findings []Finding) Baseline {
	t.Helper()
	walked, err := walkPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	e := evaluate(name, pkg, nil, withOccurrences(findings, pkg), packageHash(pkg, walked.Files, policy), policy.FailOnSeverity)
	b := newBaseline(name, e, fakeRuntime().Metadata.DetectorVersion(), "test", "reviewed", time.Now())
	if err := writeBaseline(BaselinePath(root, name), b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDetectorCacheSharesFindingsAcrossCheckoutPaths(t *testing.T) {
	g, _ := gateFor(t)
	rt := cacheRuntime(t)
	root := t.TempDir()
	pkgA := tmpSkill(t, map[string]string{"SKILL.md": "body\n", "scripts/a.py": "print('demo')\n"})
	pkgB := tmpSkill(t, map[string]string{"SKILL.md": "body\n", "scripts/a.py": "print('demo')\n"})
	line := 1
	f := Finding{RuleID: "KNOWN", Severity: "HIGH", Analyzer: "static", File: filepath.Join(pkgA, "scripts/a.py"),
		Line: &line, Title: "Known finding", Why: "open(\"" + pkgA + "\") scripts at " + pkgA + "/scripts/a.py",
		Snippet: "read(\"" + pkgA + "/scripts/a.py\")"}
	// Duplicate evidence must retain two distinct occurrence fingerprints.
	b := seedCacheBaseline(t, root, "demo", pkgA, []Finding{f, f})
	calls := countedDetector(t, func(pkg string) string {
		x := f
		x.File = strings.ReplaceAll(x.File, pkgA, pkg)
		x.Why = strings.ReplaceAll(x.Why, pkgA, pkg)
		x.Snippet = strings.ReplaceAll(x.Snippet, pkgA, pkg)
		finding := map[string]any{"rule_id": x.RuleID, "severity": x.Severity, "analyzer": x.Analyzer,
			"file_path": x.File, "line_number": x.Line, "title": x.Title, "description": x.Why, "snippet": x.Snippet}
		raw, err := json.Marshal(map[string]any{"findings": []any{finding, finding}})
		if err != nil {
			t.Error(err)
		}
		return string(raw)
	})

	var first PackageResult
	for i, pkg := range []string{pkgA, pkgB, pkgA, pkgB} {
		// A new Gate instance ensures this is a persistent cache hit.
		got := New(g.dirs).Inspect(context.Background(), skillgate.Target{Name: "demo", SkillDir: pkg}, root, rt)
		if got.Decision != DecisionApproved || got.Evaluation.ContentSHA256 != b.ContentSHA256 {
			t.Fatalf("checkout %d: %+v", i, got)
		}
		if len(got.Evaluation.fingerprints) != 2 {
			t.Fatalf("duplicate findings collapsed: %+v", got.Evaluation)
		}
		for _, fp := range got.Evaluation.fingerprints {
			if _, ok := b.FindingDetail[fp]; !ok {
				t.Fatalf("fingerprint %s did not match pre-created baseline", fp)
			}
		}
		if i == 0 {
			first = got
		} else if !reflect.DeepEqual(first.Evaluation.fingerprints, got.Evaluation.fingerprints) {
			t.Fatalf("fingerprints differ across checkouts: %v != %v", first.Evaluation.fingerprints, got.Evaluation.fingerprints)
		}
		for _, f := range got.Evaluation.current {
			if f.File != "scripts/a.py" || strings.Contains(f.Why, pkgA) || strings.Contains(f.Snippet, pkgA) {
				t.Fatalf("checkout-specific evidence survived: %+v", f)
			}
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("detector calls = %d, want 1", calls.Load())
	}
}

func TestDetectorCacheInvalidation(t *testing.T) {
	for _, change := range []string{"skill byte", ".gitignore only", "excluded logs file", "detector version", "scanner content", "policy version"} {
		t.Run(change, func(t *testing.T) {
			g, _ := gateFor(t)
			rt := cacheRuntime(t)
			pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n", ".gitignore": "logs/\n", "logs/old.log": "old\n"})
			root := t.TempDir()
			target := skillgate.Target{Name: "demo", SkillDir: pkg}
			seedCacheBaseline(t, root, target.Name, pkg, nil)
			calls := countedDetector(t, func(string) string { return cleanReport })
			first := g.Inspect(context.Background(), target, root, rt)
			if first.Decision != DecisionApproved {
				t.Fatalf("first: %+v", first)
			}
			switch change {
			case "skill byte":
				cacheWrite(t, filepath.Join(pkg, "SKILL.md"), "Body\n")
			case ".gitignore only":
				cacheWrite(t, filepath.Join(pkg, ".gitignore"), "# runtime files\nlogs/\n")
			case "excluded logs file":
				cacheWrite(t, filepath.Join(pkg, "logs/new.log"), "payload\n")
			case "detector version":
				rt.Metadata.Version += ".changed"
			case "scanner content":
				cacheWrite(t, rt.ScannerBin, "scanner version two")
			case "policy version":
				previous := policy.PolicyVersion
				policy.PolicyVersion += ".changed"
				t.Cleanup(func() { policy.PolicyVersion = previous })
			}
			second := g.Inspect(context.Background(), target, root, rt)
			if second.Decision == DecisionBlocked {
				t.Fatalf("second: %+v", second)
			}
			if calls.Load() != 2 {
				t.Fatalf("detector calls = %d, want 2", calls.Load())
			}
			if change == "excluded logs file" && first.Evaluation.ContentSHA256 != second.Evaluation.ContentSHA256 {
				t.Fatal("fixture did not exercise the runtime hash exclusion")
			}
			if change == ".gitignore only" && (first.Evaluation.Hash.RuntimeFilesSkipped != 1 || second.Evaluation.Hash.RuntimeFilesSkipped != 1) {
				t.Fatal("fixture did not preserve the runtime hash exclusion")
			}
		})
	}
}

func TestDetectorCacheNeverStoresInputsChangedDuringScan(t *testing.T) {
	for _, change := range []string{"file content", "added file", "removed file", "added symlink", "scanner content", "removed scanner"} {
		t.Run(change, func(t *testing.T) {
			g, _ := gateFor(t)
			rt := cacheRuntime(t)
			const malicious = "malicious payload\n"
			pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n", "payload.txt": malicious})
			payload := filepath.Join(pkg, "payload.txt")
			extra := filepath.Join(pkg, "extra")
			root := t.TempDir()
			target := skillgate.Target{Name: "demo", SkillDir: pkg}
			seedCacheBaseline(t, root, target.Name, pkg, nil)
			firstScan := true
			calls := countedDetector(t, func(string) string {
				if !firstScan {
					return `{"findings":[{"rule_id":"PAYLOAD","severity":"CRITICAL","file_path":"payload.txt","title":"Malicious payload"}]}`
				}
				firstScan = false
				switch change {
				case "file content":
					cacheWrite(t, payload, "benign content\n")
				case "added file":
					cacheWrite(t, extra, "new content\n")
				case "removed file":
					if err := os.Remove(payload); err != nil {
						t.Fatal(err)
					}
				case "added symlink":
					if err := os.Symlink("SKILL.md", extra); err != nil {
						t.Fatal(err)
					}
				case "scanner content":
					cacheWrite(t, rt.ScannerBin, "scanner version two")
				case "removed scanner":
					if err := os.Remove(rt.ScannerBin); err != nil {
						t.Fatal(err)
					}
				}
				return cleanReport
			})
			if got := g.Inspect(context.Background(), target, root, rt); got.Decision != DecisionApproved {
				t.Fatalf("live scan result changed: %+v", got)
			}
			entries, err := os.ReadDir(filepath.Join(g.dirs.DotpackHome, "skillgate", "detector-cache"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatal("inputs changed during scan created a cache entry")
			}
			// Restore the original inputs: their cache key must not have
			// acquired the clean report from the scan of changed inputs.
			cacheWrite(t, payload, malicious)
			cacheWrite(t, rt.ScannerBin, "scanner version one")
			if err := os.Remove(extra); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			got := New(g.dirs).Inspect(context.Background(), target, root, rt)
			if calls.Load() != 2 || got.Decision != DecisionBlocked || len(got.Evaluation.Blocking) != 1 || got.Evaluation.Blocking[0].Severity != "CRITICAL" {
				t.Fatalf("restored inputs reused a poisoned report: calls = %d, result = %+v", calls.Load(), got)
			}
		})
	}
}

func TestDetectorCacheNeverStoresFailures(t *testing.T) {
	g, _ := gateFor(t)
	rt := cacheRuntime(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
	target := skillgate.Target{Name: "demo", SkillDir: pkg}
	calls := countedDetector(t, func(string) string { return "{broken report" })
	for range 2 {
		if got := g.Inspect(context.Background(), target, t.TempDir(), rt); got.Decision != DecisionBlocked || !strings.Contains(got.Reason, "unparseable") {
			t.Fatalf("detector failure did not block: %+v", got)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("detector calls = %d, want 2", calls.Load())
	}
	entries, err := os.ReadDir(filepath.Join(g.dirs.DotpackHome, "skillgate", "detector-cache"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("failed scan created a cache entry")
	}
}

func TestDetectorCacheCorruptionFallsBackToLiveScan(t *testing.T) {
	for _, bad := range []string{"{broken", `{"schema_version":2,"findings":[]}`, `{"schema_version":1}`, `null`, `{"schema_version":1,"findings":"wrong type"}`} {
		t.Run(bad, func(t *testing.T) {
			g, _ := gateFor(t)
			rt := cacheRuntime(t)
			pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
			root := t.TempDir()
			target := skillgate.Target{Name: "demo", SkillDir: pkg}
			seedCacheBaseline(t, root, target.Name, pkg, nil)
			calls := countedDetector(t, func(string) string { return cleanReport })
			if got := g.Inspect(context.Background(), target, root, rt); got.Decision != DecisionApproved {
				t.Fatalf("first: %+v", got)
			}
			entries, err := filepath.Glob(filepath.Join(g.dirs.DotpackHome, "skillgate", "detector-cache", "*.json"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("cache entries = %v: %v", entries, err)
			}
			cacheWrite(t, entries[0], bad)
			if got := g.Inspect(context.Background(), target, root, rt); got.Decision != DecisionApproved {
				t.Fatalf("corrupt cache blocked valid live scan: %+v", got)
			}
			if calls.Load() != 2 {
				t.Fatalf("detector calls = %d, want 2", calls.Load())
			}
		})
	}
}

func TestDetectorCacheUnavailableIsOnlyAnOptimizationFailure(t *testing.T) {
	for _, unavailable := range []string{"missing scanner", "nonregular scanner", "unwritable cache"} {
		t.Run(unavailable, func(t *testing.T) {
			g, _ := gateFor(t)
			rt := cacheRuntime(t)
			switch unavailable {
			case "missing scanner":
				rt.ScannerBin = filepath.Join(t.TempDir(), "missing")
			case "nonregular scanner":
				rt.ScannerBin = t.TempDir()
			case "unwritable cache":
				cacheWrite(t, filepath.Join(g.dirs.DotpackHome, "skillgate", "detector-cache"), "regular file blocks mkdir")
			}
			pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
			root := t.TempDir()
			target := skillgate.Target{Name: "demo", SkillDir: pkg}
			seedCacheBaseline(t, root, target.Name, pkg, nil)
			calls := countedDetector(t, func(string) string { return cleanReport })
			for range 2 {
				if got := g.Inspect(context.Background(), target, root, rt); got.Decision != DecisionApproved {
					t.Fatalf("unavailable cache blocked valid scan: %+v", got)
				}
			}
			if calls.Load() != 2 {
				t.Fatalf("detector calls = %d, want 2", calls.Load())
			}
		})
	}
}

func TestFullPackageDigestFailsClosedAndMatchesFraming(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n", "nested/a": "data\n"})
	walked, err := walkPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := fullPackageDigest(pkg, walked.Files)
	if !ok || got != packageHash(pkg, walked.Files, policy).Digest {
		t.Fatal("full digest framing differs from packageHash without exclusions")
	}
	for _, files := range [][]string{nil, {filepath.Join(pkg, "missing")}, {walked.Files[0], filepath.Join(pkg, "nested")}} {
		if digest, ok := fullPackageDigest(pkg, files); ok || digest != "" {
			t.Fatalf("partial/nonregular tree earned a digest: %q %v", digest, ok)
		}
	}
	link := filepath.Join(pkg, "link")
	if err := os.Symlink(walked.Files[0], link); err != nil {
		t.Fatal(err)
	}
	if digest, ok := fullPackageDigest(pkg, []string{link}); ok || digest != "" {
		t.Fatal("replaced regular file/symlink earned a digest")
	}
	if digest, ok := regularFileDigest(link); !ok || digest == "" {
		t.Fatal("scanner symlink to a regular file did not resolve")
	}
}

func TestDetectorCacheKeepsSecurityChecksLive(t *testing.T) {
	g, _ := gateFor(t)
	rt := cacheRuntime(t)
	root := t.TempDir()
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "bo\u200bdy\n"})
	target := skillgate.Target{Name: "demo", SkillDir: pkg}
	walked, err := walkPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	b := seedCacheBaseline(t, root, target.Name, pkg, scanInvisible(pkg, walked.Files, policy))
	calls := countedDetector(t, func(string) string { return cleanReport })
	if got := g.Inspect(context.Background(), target, root, rt); got.Decision != DecisionApproved {
		t.Fatalf("first: %+v", got)
	}
	// The detector cache contains no invisible-scan findings or verdict.
	b.ApprovedFindings = nil
	if err := writeBaseline(BaselinePath(root, target.Name), b); err != nil {
		t.Fatal(err)
	}
	got := g.Inspect(context.Background(), target, root, rt)
	if got.Decision != DecisionBlocked || len(got.Evaluation.Blocking) != 1 || got.Evaluation.Blocking[0].RuleID != "INVISIBLE_CHAR_U+200B" {
		t.Fatalf("cache hid a live invisible-character finding/baseline change: %+v", got)
	}
	cacheWrite(t, BaselinePath(root, target.Name), "{bad baseline")
	if got := g.Inspect(context.Background(), target, root, rt); got.Decision != DecisionBlocked || !strings.Contains(got.Reason, "parse baseline") {
		t.Fatalf("cache hid baseline corruption: %+v", got)
	}
	// Restore approval, then prove a warm detector cache grants no trust to
	// an operator request whose policy root is untrusted.
	seedCacheBaseline(t, root, target.Name, pkg, scanInvisible(pkg, walked.Files, policy))
	prev := ensureRuntime
	ensureRuntime = func(context.Context, string) (skillscanner.Runtime, error) { return rt, nil }
	t.Cleanup(func() { ensureRuntime = prev })
	req := requestFor(t, pkg, root, false)
	v, err := g.Run(context.Background(), req)
	if err != nil || v.Pass || len(v.Blocked) != 1 || !strings.Contains(v.Blocked[0].Reason, "no approved baseline") {
		t.Fatalf("cache granted trust to a fetched source: %+v, %v", v, err)
	}
	link := filepath.Join(pkg, "escape")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), link); err != nil {
		t.Fatal(err)
	}
	if got := g.Inspect(context.Background(), target, root, rt); got.Decision != DecisionBlocked || len(got.UnsafeLinks) != 1 {
		t.Fatalf("warm cache bypassed symlink checks: %+v", got)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(pkg, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if got := g.Inspect(context.Background(), target, root, rt); got.Decision != DecisionBlocked || !strings.Contains(got.Reason, "no SKILL.md") {
		t.Fatalf("warm cache bypassed SKILL.md check: %+v", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected a warm cache throughout security-check test, calls = %d", calls.Load())
	}
}

func TestDetectorCacheScansPermittedSymlinksLive(t *testing.T) {
	g, _ := gateFor(t)
	rt := cacheRuntime(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n", "a.txt": "a\n", "b.txt": "b\n"})
	link := filepath.Join(pkg, "internal.txt")
	if err := os.Symlink("a.txt", link); err != nil {
		t.Fatal(err)
	}
	calls := countedDetector(t, func(string) string { return cleanReport })
	target := skillgate.Target{Name: "demo", SkillDir: pkg}
	root := t.TempDir()
	seedCacheBaseline(t, root, target.Name, pkg, nil)
	for _, dest := range []string{"a.txt", "b.txt"} {
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dest, link); err != nil {
			t.Fatal(err)
		}
		if got := g.Inspect(context.Background(), target, root, rt); got.Decision != DecisionApproved {
			t.Fatalf("permitted link blocked: %+v", got)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("changing an internal link reused detector output")
	}
}

func TestDetectorCacheConcurrentWritersPublishCompleteEntries(t *testing.T) {
	g, _ := gateFor(t)
	rt := cacheRuntime(t)
	prev := ensureRuntime
	ensureRuntime = func(context.Context, string) (skillscanner.Runtime, error) { return rt, nil }
	t.Cleanup(func() { ensureRuntime = prev })
	calls := countedDetector(t, func(string) string { return cleanReport })
	root := t.TempDir()
	req := skillgate.Request{Command: "install", PolicyRoot: root, PolicyRootTrusted: true}
	for i := range 24 {
		pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
		name := fmt.Sprintf("skill-%02d", i)
		seedCacheBaseline(t, root, name, pkg, nil)
		req.Selection.Targets = append(req.Selection.Targets, skillgate.Target{Name: name, SkillDir: pkg})
	}
	for i := range 2 {
		before := calls.Load()
		v, err := g.Run(context.Background(), req)
		if err != nil || !v.Pass {
			t.Fatalf("run %d: %+v, %v", i, v, err)
		}
		if i == 1 && calls.Load() != before {
			t.Fatal("steady-state run did not reuse the shared tree entry")
		}
	}
	dir := filepath.Join(g.dirs.DotpackHome, "skillgate", "detector-cache")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries or temporary files left: %v, %v", entries, err)
	}
	info, err := entries[0].Info()
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("cache entry permissions: %v, %v", info, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var entry detectorCacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil || entry.SchemaVersion != detectorCacheVersion || entry.Findings == nil {
		t.Fatalf("incomplete cache entry: %s, %v", raw, err)
	}
}

func TestGateRunWorkersAreBoundedAndOutputsStayInSelectionOrder(t *testing.T) {
	for _, procs := range []int{1, 2, 16} {
		t.Run(fmt.Sprintf("GOMAXPROCS=%d", procs), func(t *testing.T) {
			previousProcs := runtime.GOMAXPROCS(procs)
			t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })
			const count = 17
			workers := min(count, max(1, min(8, procs)))
			g, _ := gateFor(t)
			root := t.TempDir()
			req := skillgate.Request{Command: "install", PolicyRoot: root, PolicyRootTrusted: true}
			for i := range count {
				pkg := filepath.Join(t.TempDir(), strconv.Itoa(i))
				cacheWrite(t, filepath.Join(pkg, "SKILL.md"), "body\n")
				name := fmt.Sprintf("skill-%02d", i)
				req.Selection.Targets = append(req.Selection.Targets, skillgate.Target{Name: name, SkillDir: pkg})
				if i%2 == 0 {
					b := seedCacheBaseline(t, root, name, pkg, nil)
					b.ContentSHA256 = "previous content"
					if err := writeBaseline(BaselinePath(root, name), b); err != nil {
						t.Fatal(err)
					}
				}
			}
			started := make(chan struct{}, count)
			release := make(chan struct{})
			othersDone := make(chan struct{})
			var active, peak, others, last atomic.Int32
			prev := runDetector
			runDetector = func(ctx context.Context, _ string, args ...string) error {
				n := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				idx, err := strconv.Atoi(filepath.Base(args[1]))
				if err != nil {
					return err
				}
				// Make the first selected package finish last when parallel.
				if idx == 0 && workers > 1 {
					select {
					case <-othersDone:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				err = detectorReportTo(args, cleanReport)
				last.Store(int32(idx))
				if idx != 0 && others.Add(1) == count-1 {
					close(othersDone)
				}
				return err
			}
			t.Cleanup(func() { runDetector = prev })
			type outcome struct {
				v   skillgate.Verdict
				err error
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			done := make(chan outcome, 1)
			go func() {
				v, err := g.Run(ctx, req)
				done <- outcome{v, err}
			}()
			for range workers {
				select {
				case <-started:
				case <-ctx.Done():
					cancel()
					<-done
					t.Fatal("worker pool did not fill")
				}
			}
			close(release)
			out := <-done
			if out.err != nil {
				t.Fatal(out.err)
			}
			if peak.Load() != int32(workers) || (workers > 1 && last.Load() != 0) {
				t.Fatalf("peak workers = %d, want %d; last completed = %d", peak.Load(), workers, last.Load())
			}
			raw, err := os.ReadFile(out.v.ArtifactPath)
			if err != nil {
				t.Fatal(err)
			}
			var artifact runArtifact
			if err := json.Unmarshal(raw, &artifact); err != nil {
				t.Fatal(err)
			}
			if len(artifact.Results) != count || out.v.Pass || artifact.Pass {
				t.Fatalf("unexpected aggregate: %+v", artifact)
			}
			var notices []string
			var blocked []string
			for i, result := range artifact.Results {
				if result.Skill != req.Selection.Targets[i].Name {
					t.Fatalf("result %d out of selection order: %s", i, result.Skill)
				}
				if i%2 == 0 {
					if result.Decision != DecisionApprovedWithDrift {
						t.Fatalf("approved target: %+v", result)
					}
					notices = append(notices, result.Notices()...)
					seen, err := loadSeen(g.dirs.DotpackHome, root, result.Skill)
					if err != nil || seen == nil || seen.ContentSHA256 != result.Evaluation.ContentSHA256 {
						t.Fatalf("missing seen record for %s: %v", result.Skill, err)
					}
				} else {
					blocked = append(blocked, result.Skill)
				}
			}
			var gotBlocked []string
			for _, b := range out.v.Blocked {
				gotBlocked = append(gotBlocked, b.Skill)
			}
			if !reflect.DeepEqual(notices, out.v.Notices) || !reflect.DeepEqual(blocked, gotBlocked) {
				t.Fatalf("output order changed: notices %v / %v; blocked %v / %v", notices, out.v.Notices, blocked, gotBlocked)
			}
		})
	}
}
