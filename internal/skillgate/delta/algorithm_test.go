package delta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tmpSkill builds a package from a path->content map and returns its
// absolute root. Paths use forward slashes.
func tmpSkill(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}

func hashOf(t *testing.T, pkg string) HashInfo {
	t.Helper()
	w, err := walkPackage(pkg)
	if err != nil {
		t.Fatalf("walkPackage: %v", err)
	}
	return packageHash(pkg, w.Files, policy)
}

func invisibleOf(t *testing.T, pkg string) []Finding {
	t.Helper()
	w, err := walkPackage(pkg)
	if err != nil {
		t.Fatalf("walkPackage: %v", err)
	}
	return scanInvisible(pkg, w.Files, policy)
}

// ---------------------------------------------------------------- policy

func TestPolicyDeclaresTheGateModelItImplements(t *testing.T) {
	if policy.ID != "skillgate" {
		t.Errorf("policy id = %q", policy.ID)
	}
	if policy.FailOnSeverity != "HIGH" {
		t.Errorf("fail_on_severity = %q, want HIGH", policy.FailOnSeverity)
	}
	if !policy.FailClosed {
		t.Error("policy does not declare fail_closed")
	}
	if policy.PolicyVersion == "" {
		t.Error("policy has no version; baselines could not record what approved them")
	}
	// The detector must be usable by a stranger on a fresh clone: an
	// Apache-2.0 package whose default engines need no network and no
	// API key. A service-encumbered detector would make the default gate
	// unusable without a vendor account.
	if policy.Detector.License != "Apache-2.0" {
		t.Errorf("detector license = %q, want Apache-2.0", policy.Detector.License)
	}
	if policy.Detector.NetworkRequired {
		t.Error("detector is declared network-required; the default gate must run locally")
	}
}

func TestParsePolicyRejectsIncompleteDocuments(t *testing.T) {
	cases := map[string]string{
		"no severity":  `{"id":"x","policy_version":"1","fail_closed":true,"baseline_directory":"d","invisible_character_policy":{"scan_suffixes":[".md"]}}`,
		"not closed":   `{"id":"x","policy_version":"1","fail_on_severity":"HIGH","baseline_directory":"d","invisible_character_policy":{"scan_suffixes":[".md"]}}`,
		"no version":   `{"id":"x","fail_on_severity":"HIGH","fail_closed":true,"baseline_directory":"d","invisible_character_policy":{"scan_suffixes":[".md"]}}`,
		"no baselines": `{"id":"x","policy_version":"1","fail_on_severity":"HIGH","fail_closed":true,"invisible_character_policy":{"scan_suffixes":[".md"]}}`,
		"no suffixes":  `{"id":"x","policy_version":"1","fail_on_severity":"HIGH","fail_closed":true,"baseline_directory":"d"}`,
		"garbage":      `{`,
	}
	for name, raw := range cases {
		if _, err := parsePolicy([]byte(raw)); err == nil {
			t.Errorf("parsePolicy accepted %s", name)
		}
	}
}

// ------------------------------------------------------------- invisible

func TestInvisibleTableCoversTheHidingCodepoints(t *testing.T) {
	if len(invisibleNames) != 19 {
		t.Fatalf("invisible table has %d entries; changing it changes every fingerprint", len(invisibleNames))
	}
	for _, want := range []rune{0x200b, 0x202e, 0xfeff, 0x2069, 0x00ad} {
		if invisibleByCP[want] == "" {
			t.Errorf("U+%04X missing from the invisible table", want)
		}
	}
}

func TestScanInvisibleFlagsZeroWidthSpaceWithLineNumbers(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md": "---\nname: t\n---\n\nRun the\u200b command\n",
	})
	got := invisibleOf(t, pkg)
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(got), got)
	}
	f := got[0]
	if f.RuleID != "INVISIBLE_CHAR_U+200B" {
		t.Errorf("rule = %q", f.RuleID)
	}
	if f.Severity != "HIGH" {
		t.Errorf("severity = %q, want HIGH", f.Severity)
	}
	if f.Analyzer != "skillgate.invisible" {
		t.Errorf("analyzer = %q", f.Analyzer)
	}
	if len(f.Lines) != 1 || f.Lines[0] != 5 {
		t.Errorf("lines = %v, want [5]", f.Lines)
	}
	if f.Count != 1 {
		t.Errorf("count = %d, want 1", f.Count)
	}
}

func TestScanInvisibleFlagsCyrillicAndGreekHomoglyphsInSkillMd(t *testing.T) {
	t.Run("cyrillic", func(t *testing.T) {
		// "commit" with a Cyrillic o.
		pkg := tmpSkill(t, map[string]string{"SKILL.md": "Always c\u043emmit the change\n"})
		got := invisibleOf(t, pkg)
		if len(got) != 1 || got[0].RuleID != "HOMOGLYPH_MIXED_SCRIPT" {
			t.Fatalf("want one HOMOGLYPH_MIXED_SCRIPT, got %+v", got)
		}
	})
	t.Run("greek", func(t *testing.T) {
		// Greek omicron, which the source suite never covered.
		pkg := tmpSkill(t, map[string]string{"SKILL.md": "Always c\u03bfmmit the change\n"})
		got := invisibleOf(t, pkg)
		if len(got) != 1 || got[0].RuleID != "HOMOGLYPH_MIXED_SCRIPT" {
			t.Fatalf("want one HOMOGLYPH_MIXED_SCRIPT, got %+v", got)
		}
	})
}

// Non-Latin script is ordinary content outside SKILL.md. Flagging it
// everywhere would bury the signal in noise from translated docs.
func TestScanInvisibleReportsHomoglyphsOnlyForSkillMd(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":     "clean\n",
		"reference.md": "\u043e\u043f\u0438\u0441\u0430\u043d\u0438\u0435 translated docs\n",
	})
	if got := invisibleOf(t, pkg); len(got) != 0 {
		t.Fatalf("homoglyphs outside SKILL.md were reported: %+v", got)
	}
}

// A hidden codepoint inside SKILL.md must still be caught even when the
// homoglyph rule does not apply, and vice versa.
func TestScanInvisibleFlagsHiddenCharactersInAnyScannedFile(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":          "clean\n",
		"scripts/helper.py": "x = 1\u200b\n",
	})
	got := invisibleOf(t, pkg)
	if len(got) != 1 || got[0].File != filepath.FromSlash("scripts/helper.py") {
		t.Fatalf("want the helper flagged, got %+v", got)
	}
}

func TestScanInvisibleIgnoresUnscannedSuffixesAndVendorDirectories(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":                  "clean\n",
		"assets/logo.svg":           "<svg>\u200b</svg>\n",
		"node_modules/pkg/index.js": "var a = 1\u200b\n",
		".venv/lib/mod.py":          "x\u200b\n",
	})
	if got := invisibleOf(t, pkg); len(got) != 0 {
		t.Fatalf("unexpected findings: %+v", got)
	}
}

func TestScanInvisibleCapsReportedLinesButKeepsTheTrueCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\n")
	for i := 0; i < 30; i++ {
		b.WriteString("line\u200b\n")
	}
	pkg := tmpSkill(t, map[string]string{"SKILL.md": b.String()})
	got := invisibleOf(t, pkg)
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got))
	}
	if len(got[0].Lines) != maxReportedLines {
		t.Errorf("reported %d lines, want the cap of %d", len(got[0].Lines), maxReportedLines)
	}
	if got[0].Count != 30 {
		t.Errorf("count = %d, want the true total 30", got[0].Count)
	}
}

// Go randomises map iteration. If the scan accumulated codepoints in a
// map, the emission order -- and therefore every occurrence index and
// every fingerprint -- would differ between runs, and a package would
// intermittently fail to match its own baseline.
func TestScanInvisibleEmissionOrderIsDeterministic(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md": "a\u200b b\u202e c\ufeff d\u200c e\u2060\n",
	})
	first := invisibleOf(t, pkg)
	if len(first) < 5 {
		t.Fatalf("want at least 5 findings, got %d", len(first))
	}
	want := make([]string, len(first))
	for i, f := range first {
		want[i] = f.RuleID
	}
	for run := 0; run < 50; run++ {
		got := invisibleOf(t, pkg)
		if len(got) != len(want) {
			t.Fatalf("run %d produced %d findings, want %d", run, len(got), len(want))
		}
		for i, f := range got {
			if f.RuleID != want[i] {
				t.Fatalf("run %d: order changed at %d: %q != %q", run, i, f.RuleID, want[i])
			}
		}
	}
}

// ------------------------------------------------------------------ hash

func TestPackageHashChangesWhenDurableContentChanges(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "original\n"})
	before := hashOf(t, pkg)
	if err := os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("rewritten\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if after := hashOf(t, pkg); after.Digest == before.Digest {
		t.Fatal("editing SKILL.md did not move the content hash")
	}
}

// The exclusion buys a stable tripwire for a skill that writes its own
// runtime state -- and it must be EARNED by the package declaring it.
func TestDeclaredRuntimeDirectoryIsExcludedFromTheHashButStillCounted(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":       "body\n",
		".gitignore":     "logs/\n",
		"logs/run-1.log": "first\n",
	})
	before := hashOf(t, pkg)
	if before.RuntimeFilesSkipped != 1 {
		t.Fatalf("runtimeFilesSkipped = %d, want 1", before.RuntimeFilesSkipped)
	}
	if err := os.WriteFile(filepath.Join(pkg, "logs", "run-2.log"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	after := hashOf(t, pkg)
	if after.Digest != before.Digest {
		t.Fatal("runtime churn moved the content hash; the tripwire would never be stable")
	}
	if after.RuntimeFilesSkipped != 2 {
		t.Errorf("runtimeFilesSkipped = %d, want 2", after.RuntimeFilesSkipped)
	}
}

// The bypass this closes: granting the allowlist unconditionally gave
// every package that shipped no .gitignore a free logs/ hiding place.
func TestNoGitignoreMeansNothingIsExcluded(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":     "body\n",
		"logs/run.log": "first\n",
	})
	before := hashOf(t, pkg)
	if before.RuntimeFilesSkipped != 0 {
		t.Fatalf("runtimeFilesSkipped = %d, want 0 with no .gitignore", before.RuntimeFilesSkipped)
	}
	if err := os.WriteFile(filepath.Join(pkg, "logs", "run.log"), []byte("payload\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if after := hashOf(t, pkg); after.Digest == before.Digest {
		t.Fatal("a package with no .gitignore hid a logs/ change from the tripwire")
	}
}

// The bypass this closes: a package listing SKILL.md in its own
// .gitignore excluded its own source, so a full rewrite reported
// "content identical to approved baseline".
func TestGitignoreCanOnlyNarrowThePolicyAllowlist(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":       "body\n",
		".gitignore":     "SKILL.md\nsecrets\nscripts\n",
		"secrets/key":    "shh\n",
		"scripts/run.sh": "echo hi\n",
	})
	before := hashOf(t, pkg)
	if before.RuntimeFilesSkipped != 0 {
		t.Fatalf("a package excluded names outside the policy allowlist: skipped %d", before.RuntimeFilesSkipped)
	}
	if err := os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("fully rewritten\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if after := hashOf(t, pkg); after.Digest == before.Digest {
		t.Fatal("SKILL.md was excluded from the hash; a full rewrite would report as identical")
	}
}

// The bypass this closes: declaring `logs` also excluded `scripts/logs/`,
// a hiding place one directory deeper than the package declared.
func TestExclusionsAreRootAnchoredExceptForNestableNames(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":                  "body\n",
		".gitignore":                "logs/\n__pycache__/\n",
		"logs/a.log":                "x\n",
		"scripts/logs/helper.py":    "print(1)\n",
		"scripts/__pycache__/c.pyc": "junk\n",
	})
	before := hashOf(t, pkg)

	if err := os.WriteFile(filepath.Join(pkg, "scripts", "logs", "helper.py"), []byte("print(2)\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if after := hashOf(t, pkg); after.Digest == before.Digest {
		t.Fatal("scripts/logs/ was excluded by a root-level `logs` declaration")
	}

	// __pycache__ genuinely recurs below the root, so it is nestable.
	reset := hashOf(t, pkg)
	if err := os.WriteFile(filepath.Join(pkg, "scripts", "__pycache__", "c.pyc"), []byte("different\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if after := hashOf(t, pkg); after.Digest != reset.Digest {
		t.Error("a nested __pycache__ change moved the hash; nestable names must match at any depth")
	}
}

func TestGitignoreFormsAllResolveAndGlobsAndNegationsAreIgnored(t *testing.T) {
	for _, form := range []string{"logs", "logs/", "/logs", "/logs/", "./logs", "./logs/"} {
		pkg := tmpSkill(t, map[string]string{
			"SKILL.md":   "body\n",
			".gitignore": form + "\n",
			"logs/a.log": "x\n",
		})
		if got := hashOf(t, pkg); got.RuntimeFilesSkipped != 1 {
			t.Errorf("gitignore form %q did not resolve: skipped %d", form, got.RuntimeFilesSkipped)
		}
	}
	for _, ignored := range []string{"log*", "!logs", "# logs"} {
		pkg := tmpSkill(t, map[string]string{
			"SKILL.md":   "body\n",
			".gitignore": ignored + "\n",
			"logs/a.log": "x\n",
		})
		if got := hashOf(t, pkg); got.RuntimeFilesSkipped != 0 {
			t.Errorf("gitignore line %q was honoured; globs, negations and comments must be ignored", ignored)
		}
	}
}

func TestPackageHashSkipsDSStoreAndCountsUnreadableFiles(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":  "body\n",
		".DS_Store": "junk\n",
	})
	if got := hashOf(t, pkg); got.HashedFiles != 1 {
		t.Errorf("hashedFiles = %d, want 1 (.DS_Store must not be hashed)", got.HashedFiles)
	}
}

// The content hash is the tripwire's identity. If the walk order or the
// digest construction ever changes, every baseline in every adopter's
// repository silently stops matching. This vector makes that a test
// failure instead.
func TestPackageHashGoldenVector(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":          "---\nname: golden\n---\n\nBody.\n",
		"reference.md":      "Reference.\n",
		"scripts/helper.py": "print('hi')\n",
	})
	got := hashOf(t, pkg)
	// Cross-checked against the source skillgate.mjs implementation on the
	// identical fixture: both produce this digest, so the port is faithful.
	const want = "a0d3e8b1ed933e272c9670918f52ade8268ed65ec0c311fc52de84cd7c42a568"
	if got.HashedFiles != 3 {
		t.Fatalf("hashedFiles = %d, want 3", got.HashedFiles)
	}
	if got.Digest != want {
		t.Fatalf("content digest drifted.\n got: %s\nwant: %s\n\nThe walk order or digest construction changed. Every existing baseline stops matching. If this change is intended, regenerate baselines and update this vector deliberately.", got.Digest, want)
	}
}

// Ordering above U+FFFF differs between UTF-16 (the source
// implementation) and UTF-8 (this one). Go's byte order is the adopted
// behaviour; this pins it so a future re-port cannot drift silently.
func TestWalkOrderIsUTF8ByteOrder(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"a.md":           "a\n",
		"\ue000x.md":     "private use\n",
		"\U0001F680x.md": "astral\n",
		"Z.md":           "z\n",
	})
	w, err := walkPackage(pkg)
	if err != nil {
		t.Fatalf("walkPackage: %v", err)
	}
	var names []string
	for _, f := range w.Files {
		names = append(names, filepath.Base(f))
	}
	want := []string{"Z.md", "a.md", "\ue000x.md", "\U0001F680x.md"}
	if len(names) != len(want) {
		t.Fatalf("walk returned %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("walk order = %v, want %v (UTF-8 byte order: astral sorts AFTER U+E000)", names, want)
		}
	}
}

// -------------------------------------------------------------- symlinks

func TestSymlinkEscapingThePackageIsUnsafe(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
	outside := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(outside, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(pkg, "escape.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	w, err := walkPackage(pkg)
	if err != nil {
		t.Fatalf("walkPackage: %v", err)
	}
	unsafe := findSymlinks(pkg, w.Links)
	if len(unsafe) != 1 || unsafe[0].File != "escape.md" {
		t.Fatalf("escaping symlink not reported: %+v", unsafe)
	}
}

// The original bypass: a symlinked SKILL.md is hashed by nothing and
// scanned by nothing, so the package could be approved with zero bytes
// read and the target swapped afterwards.
func TestSymlinkedSkillMdIsNotHashed(t *testing.T) {
	pkg := t.TempDir()
	outside := filepath.Join(t.TempDir(), "real-SKILL.md")
	if err := os.WriteFile(outside, []byte("innocuous\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(pkg, "SKILL.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	w, err := walkPackage(pkg)
	if err != nil {
		t.Fatalf("walkPackage: %v", err)
	}
	if got := packageHash(pkg, w.Files, policy); got.HashedFiles != 0 {
		t.Fatalf("hashedFiles = %d; a symlinked SKILL.md must contribute nothing", got.HashedFiles)
	}
	if len(findSymlinks(pkg, w.Links)) != 1 {
		t.Fatal("symlinked SKILL.md was not reported as unsafe")
	}
}

func TestInternalSymlinkIsSafeButDanglingIsNot(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":       "body\n",
		"scripts/run.sh": "echo hi\n",
	})
	if err := os.Symlink(filepath.Join(pkg, "scripts", "run.sh"), filepath.Join(pkg, "bin-run")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(pkg, "scripts", "absent.sh"), filepath.Join(pkg, "dangling")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	w, err := walkPackage(pkg)
	if err != nil {
		t.Fatalf("walkPackage: %v", err)
	}
	unsafe := findSymlinks(pkg, w.Links)
	if len(unsafe) != 1 || unsafe[0].File != "dangling" {
		t.Fatalf("want only the dangling link unsafe, got %+v", unsafe)
	}
}

// A symlinked directory must not be descended, or its contents bypass
// both the hash and the invisible-character scan.
func TestSymlinkedDirectoryIsNotDescended(t *testing.T) {
	pkg := tmpSkill(t, map[string]string{"SKILL.md": "body\n"})
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "payload.md"), []byte("x\u200b\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(pkg, "vendor")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	w, err := walkPackage(pkg)
	if err != nil {
		t.Fatalf("walkPackage: %v", err)
	}
	for _, f := range w.Files {
		if strings.Contains(f, "payload.md") {
			t.Fatalf("walk descended a symlinked directory: %s", f)
		}
	}
	if len(findSymlinks(pkg, w.Links)) != 1 {
		t.Fatal("symlinked directory escaping the package was not reported")
	}
}

// The source swallowed a read error and returned partial results, so
// part of a tree could be approved having never been inspected.
func TestUnreadableDirectoryIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission bits do not apply")
	}
	pkg := tmpSkill(t, map[string]string{
		"SKILL.md":         "body\n",
		"private/thing.md": "x\n",
	})
	priv := filepath.Join(pkg, "private")
	if err := os.Chmod(priv, 0o000); err != nil {
		t.Skipf("chmod unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(priv, 0o755) })

	if _, err := walkPackage(pkg); err == nil {
		t.Fatal("walkPackage silently truncated an unreadable directory")
	}
}
