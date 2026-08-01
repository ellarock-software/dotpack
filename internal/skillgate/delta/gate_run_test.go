package delta

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillgate"
	"github.com/ellarock-software/dotpack/internal/skillscanner"
)

// fakeDetector installs a detector runner that writes report as the
// scan output and returns runErr. It also records the argv it was given.
//
// The source .mjs suite never exercised the detector at all; every
// failure mode below was therefore untested until now.
func fakeDetector(t *testing.T, report string, runErr error) *[]string {
	t.Helper()
	var argv []string
	prev := runDetector
	runDetector = func(_ context.Context, bin string, args ...string) error {
		argv = append([]string{bin}, args...)
		if report != "" {
			for i, a := range args {
				if a == "--output-json" && i+1 < len(args) {
					if err := os.WriteFile(args[i+1], []byte(report), 0o644); err != nil {
						t.Fatalf("fake detector could not write its report: %v", err)
					}
				}
			}
		}
		return runErr
	}
	t.Cleanup(func() { runDetector = prev })
	return &argv
}

const cleanReport = `{"findings":[]}`

func TestScanDetectorMapsReportFields(t *testing.T) {
	fakeDetector(t, `{"findings":[{"rule_id":"R1","severity":"high","analyzer":"static",
	  "file_path":"/pkg/a.py","line_number":12,"title":"T","description":"D"}]}`, nil)

	got := scanDetector(context.Background(), "scanner", "/pkg", time.Second)
	if got.Err != "" {
		t.Fatalf("unexpected error: %s", got.Err)
	}
	if len(got.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got.Findings))
	}
	f := got.Findings[0]
	if f.RuleID != "R1" || f.Analyzer != "static" || f.File != "/pkg/a.py" || f.Title != "T" || f.Why != "D" {
		t.Errorf("field mapping wrong: %+v", f)
	}
	if f.Severity != "HIGH" {
		t.Errorf("severity = %q, want it upper-cased", f.Severity)
	}
	if f.Line == nil || *f.Line != 12 {
		t.Errorf("line = %v, want 12", f.Line)
	}
}

func TestScanDetectorDefaultsMissingSeverityToInfo(t *testing.T) {
	fakeDetector(t, `{"findings":[{"rule_id":"R1","file_path":"a.py"}]}`, nil)
	got := scanDetector(context.Background(), "scanner", "/pkg", time.Second)
	if len(got.Findings) != 1 || got.Findings[0].Severity != "INFO" {
		t.Fatalf("want a single INFO finding, got %+v", got.Findings)
	}
}

// The scanner exits non-zero when it finds something. A finding is not
// an error here -- the delta comparison decides what matters -- so the
// exit code must be ignored entirely.
func TestScanDetectorIgnoresANonZeroExitWhenTheReportIsValid(t *testing.T) {
	fakeDetector(t, `{"findings":[{"rule_id":"R1","severity":"CRITICAL","file_path":"a.py"}]}`,
		errors.New("exit status 1"))

	got := scanDetector(context.Background(), "scanner", "/pkg", time.Second)
	if got.Err != "" {
		t.Fatalf("a non-zero exit with a valid report was treated as failure: %s", got.Err)
	}
	if len(got.Findings) != 1 {
		t.Fatalf("findings were discarded: %+v", got)
	}
}

func TestScanDetectorArgvIsExactAndDoesNotEnableTheLlm(t *testing.T) {
	argv := fakeDetector(t, cleanReport, nil)
	scanDetector(context.Background(), "scanner", "/pkg", time.Second)

	joined := strings.Join(*argv, " ")
	for _, want := range []string{"scanner", "scan", "/pkg", "--use-behavioral", "--format json", "--output-json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q is missing %q", joined, want)
		}
	}
	// The LLM engine is opt-in, needs a key, and is deliberately not
	// load-bearing. The default gate must never reach the network.
	if strings.Contains(joined, "--use-llm") {
		t.Errorf("the default scan enabled the LLM engine: %q", joined)
	}
}

func TestScanDetectorFailsClosedOnEveryDetectorFailure(t *testing.T) {
	cases := []struct {
		name, report string
		err          error
		wantContains string
	}{
		{"binary missing", "", exec.ErrNotFound, "detector not found"},
		{"no report", "", errors.New("boom"), "produced no report"},
		{"crashed silently", "", nil, "produced no report"},
		{"garbage report", "{not json", nil, "unparseable detector report"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeDetector(t, tc.report, tc.err)
			got := scanDetector(context.Background(), "scanner", "/pkg", time.Second)
			if got.Err == "" {
				t.Fatalf("%s did not fail closed", tc.name)
			}
			if !strings.Contains(got.Err, tc.wantContains) {
				t.Errorf("error %q does not mention %q", got.Err, tc.wantContains)
			}
			if len(got.Findings) != 0 {
				t.Errorf("findings returned alongside a failure: %+v", got.Findings)
			}
		})
	}
}

func TestScanDetectorTimesOutRatherThanHanging(t *testing.T) {
	prev := runDetector
	runDetector = func(ctx context.Context, _ string, _ ...string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { runDetector = prev })

	done := make(chan detectorResult, 1)
	go func() { done <- scanDetector(context.Background(), "scanner", "/pkg", 50*time.Millisecond) }()

	select {
	case got := <-done:
		if !strings.Contains(got.Err, "timed out") {
			t.Fatalf("error %q does not identify a timeout", got.Err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("scanDetector hung past its timeout")
	}
}

func TestScanDetectorRemovesItsTemporaryReport(t *testing.T) {
	var reportPath string
	prev := runDetector
	runDetector = func(_ context.Context, _ string, args ...string) error {
		for i, a := range args {
			if a == "--output-json" && i+1 < len(args) {
				reportPath = args[i+1]
				return os.WriteFile(reportPath, []byte(cleanReport), 0o644)
			}
		}
		return nil
	}
	t.Cleanup(func() { runDetector = prev })

	scanDetector(context.Background(), "scanner", "/pkg", time.Second)
	if reportPath == "" {
		t.Fatal("no report path was used")
	}
	if _, err := os.Stat(reportPath); !os.IsNotExist(err) {
		t.Errorf("temporary report %s was left behind", reportPath)
	}
}

// ------------------------------------------------------------ gate.Run

func gateFor(t *testing.T) (*Gate, dirs.Dirs) {
	t.Helper()
	home := t.TempDir()
	d := dirs.Dirs{DotpackHome: home}
	g := New(d)
	g.ToolVersion = "test"
	return g, d
}

func requestFor(t *testing.T, pkg, policyRoot string, trusted bool) skillgate.Request {
	t.Helper()
	return skillgate.Request{
		Command: "install",
		Selection: skillgate.Selection{
			SourceRoot: policyRoot,
			Targets: []skillgate.Target{{
				Name:      "demo",
				SkillDir:  pkg,
				SkillFile: filepath.Join(pkg, "SKILL.md"),
			}},
		},
		PolicyRoot:        policyRoot,
		PolicyRootTrusted: trusted,
	}
}

func fakeRuntime() skillscanner.Runtime {
	return skillscanner.Runtime{
		ScannerBin: "scanner",
		Metadata:   skillscanner.RuntimeMetadata{Package: skillscanner.PackageName, Version: skillscanner.Version},
	}
}

func TestGateBlocksFirstSightingAndNamesTheRemedy(t *testing.T) {
	fakeDetector(t, cleanReport, nil)
	g, _ := gateFor(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
	policyRoot := t.TempDir()

	res := g.Inspect(context.Background(), requestFor(t, pkg, policyRoot, true).Selection.Targets[0], policyRoot, fakeRuntime())
	if res.Decision != DecisionBlocked {
		t.Fatalf("decision = %s (%s)", res.Decision, res.Reason)
	}
	detail := strings.Join(blockedDetail(res), "\n")
	if !strings.Contains(detail, "dotpack approve-skill") {
		t.Fatalf("a first-sighting block did not name the remedy:\n%s\n\nWithout it the first reaction is a blanket bypass.", detail)
	}
}

func TestGateApproveThenVerifyIsClean(t *testing.T) {
	fakeDetector(t, `{"findings":[{"rule_id":"NOISE","severity":"CRITICAL","file_path":"SKILL.md","description":"known"}]}`, nil)
	g, _ := gateFor(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
	policyRoot := t.TempDir()
	target := requestFor(t, pkg, policyRoot, true).Selection.Targets[0]

	first := g.Inspect(context.Background(), target, policyRoot, fakeRuntime())
	if err := g.Approve(first, "cisco-ai-skill-scanner 2.0.12", "reviewed"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// A baselined CRITICAL must not block. That is the entire point.
	second := g.Inspect(context.Background(), target, policyRoot, fakeRuntime())
	if second.Decision != DecisionApproved {
		t.Fatalf("decision = %s (%s); a baselined finding blocked", second.Decision, second.Reason)
	}
}

func TestGateBlocksAnInjectedInvisibleCharacterAfterApproval(t *testing.T) {
	fakeDetector(t, cleanReport, nil)
	g, _ := gateFor(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
	policyRoot := t.TempDir()
	target := requestFor(t, pkg, policyRoot, true).Selection.Targets[0]

	if err := g.Approve(g.Inspect(context.Background(), target, policyRoot, fakeRuntime()), "d", ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// A zero-width space is invisible to a human reviewer and to an LLM
	// analyser alike; only the deterministic check catches it.
	if err := os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("bo\u200bdy\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := g.Inspect(context.Background(), target, policyRoot, fakeRuntime())
	if got.Decision != DecisionBlocked {
		t.Fatalf("an injected zero-width space did not block: %s (%s)", got.Decision, got.Reason)
	}
	if len(got.Evaluation.Blocking) != 1 || got.Evaluation.Blocking[0].RuleID != "INVISIBLE_CHAR_U+200B" {
		t.Errorf("blocking = %+v", got.Evaluation.Blocking)
	}
}

// The security invariant behind Request.PolicyRootTrusted: dotpack
// clones a github: source into DotpackHome and then treats the clone as
// the source root, so a remote repository that ships its own baselines
// must not be able to approve itself.
func TestGateIgnoresApprovalsFromAnUntrustedPolicyRoot(t *testing.T) {
	fakeDetector(t, cleanReport, nil)
	g, d := gateFor(t)
	fetched := t.TempDir()
	pkg := filepath.Join(fetched, "skills", "demo")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The fetched repository ships an approval for itself.
	trusted := New(d)
	trusted.ToolVersion = "test"
	target := skillgate.Target{Name: "demo", SkillDir: pkg, SkillFile: filepath.Join(pkg, "SKILL.md")}
	if err := trusted.Approve(trusted.Inspect(context.Background(), target, fetched, fakeRuntime()), "d", "self-approved"); err != nil {
		t.Fatalf("seed approval: %v", err)
	}
	if _, err := os.Stat(BaselinePath(fetched, "demo")); err != nil {
		t.Fatalf("the seeded approval was not written: %v", err)
	}

	req := requestFor(t, pkg, fetched, false)
	verdict, err := g.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if verdict.Pass {
		t.Fatal("a fetched repository approved itself: baselines shipped by an untrusted source were honoured")
	}
}

func TestGateRunWritesAReviewableArtifact(t *testing.T) {
	fakeDetector(t, cleanReport, nil)
	g, _ := gateFor(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
	policyRoot := t.TempDir()

	verdict, err := g.Run(context.Background(), requestFor(t, pkg, policyRoot, true))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if verdict.Pass {
		t.Fatal("first sighting passed")
	}
	if verdict.ArtifactPath == "" {
		t.Fatal("no artifact path; a refusal must always be reviewable")
	}
	raw, err := os.ReadFile(verdict.ArtifactPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var a runArtifact
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("artifact is not valid JSON: %v", err)
	}
	if a.Gate != Name || a.Pass || len(a.Results) != 1 {
		t.Errorf("artifact does not describe the run: %+v", a)
	}
	if a.PolicyVersion == "" || a.FailOn == "" {
		t.Errorf("artifact omits the policy it enforced: %+v", a)
	}
}

// A policy refusal is a Verdict, not an error. Returning an error would
// tell the operator their machine is broken when their package is not
// approved.
func TestGateReturnsAVerdictNotAnErrorForAPolicyRefusal(t *testing.T) {
	fakeDetector(t, cleanReport, nil)
	g, _ := gateFor(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})

	verdict, err := g.Run(context.Background(), requestFor(t, pkg, t.TempDir(), true))
	if err != nil {
		t.Fatalf("a policy refusal was reported as an error: %v", err)
	}
	if verdict.Pass || len(verdict.Blocked) != 1 {
		t.Fatalf("verdict = %+v", verdict)
	}
}

func TestGateBlocksAPackageWithNoSkillMd(t *testing.T) {
	fakeDetector(t, cleanReport, nil)
	g, _ := gateFor(t)
	pkg := tmpSkill(t, map[string]string{"README.md": "not a skill\n"})

	got := g.Inspect(context.Background(), skillgate.Target{Name: "demo", SkillDir: pkg}, t.TempDir(), fakeRuntime())
	if got.Decision != DecisionBlocked || !strings.Contains(got.Reason, "no SKILL.md") {
		t.Fatalf("decision = %s (%s)", got.Decision, got.Reason)
	}
}

func TestGateBlocksAnUnsafeSymlinkBeforeScanning(t *testing.T) {
	fakeDetector(t, cleanReport, nil)
	g, _ := gateFor(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
	outside := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(outside, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(pkg, "escape.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := g.Inspect(context.Background(), skillgate.Target{Name: "demo", SkillDir: pkg}, t.TempDir(), fakeRuntime())
	if got.Decision != DecisionBlocked || len(got.UnsafeLinks) != 1 {
		t.Fatalf("decision = %s (%s) links=%+v", got.Decision, got.Reason, got.UnsafeLinks)
	}
}

func TestApproveRefusesWithoutAPolicyRoot(t *testing.T) {
	fakeDetector(t, cleanReport, nil)
	g, _ := gateFor(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})

	res := g.Inspect(context.Background(), skillgate.Target{Name: "demo", SkillDir: pkg}, "", fakeRuntime())
	if err := g.Approve(res, "d", ""); err == nil {
		t.Fatal("Approve wrote an approval with nowhere legitimate to put it")
	}
}

// The machine-local tripwire: the committed approval is not the one this
// machine last installed against. Advisory, never blocking.
func TestGateNotesWhenABaselineChangedSinceTheLastInstall(t *testing.T) {
	fakeDetector(t, cleanReport, nil)
	g, _ := gateFor(t)
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
	policyRoot := t.TempDir()
	target := skillgate.Target{Name: "demo", SkillDir: pkg}

	if err := g.Approve(g.Inspect(context.Background(), target, policyRoot, fakeRuntime()), "d", ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := g.Inspect(context.Background(), target, policyRoot, fakeRuntime()); got.BaselineChangedSinceLastInstall {
		t.Fatal("an unchanged baseline was reported as changed")
	}

	// Someone edits the committed approval.
	path := BaselinePath(policyRoot, "demo")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var b Baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("parse: %v", err)
	}
	b.ApprovedFindings = append(b.ApprovedFindings, "deadbeefdeadbeef")
	if err := writeBaseline(path, b); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := g.Inspect(context.Background(), target, policyRoot, fakeRuntime())
	if !got.BaselineChangedSinceLastInstall {
		t.Fatal("a hand-edited baseline was not flagged as changed since the last install")
	}
	if got.Decision == DecisionBlocked {
		t.Error("the advisory tripwire blocked the install; it must only warn")
	}
}
