package sourceregistry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	_ "github.com/ellarock-software/dotpack/internal/adapter/all"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
)

func TestHasFailures(t *testing.T) {
	urClean := UpdateResult{Summary: SummaryCounts{Added: 1, Steady: 2}}
	if urClean.HasFailures() {
		t.Errorf("expected clean result to have no failures")
	}

	urConflict := UpdateResult{Summary: SummaryCounts{Conflict: 1}}
	if !urConflict.HasFailures() {
		t.Errorf("expected conflict to be a failure")
	}

	urBlocked := UpdateResult{Summary: SummaryCounts{Blocked: 1}}
	if !urBlocked.HasFailures() {
		t.Errorf("expected blocked to be a failure")
	}

	urFailed := UpdateResult{Summary: SummaryCounts{Failed: 1}}
	if !urFailed.HasFailures() {
		t.Errorf("expected failed to be a failure")
	}
}

func TestShortSHA(t *testing.T) {
	if got := shortSHA("12345"); got != "12345" {
		t.Errorf("shortSHA(12345) = %q, want 12345", got)
	}
	if got := shortSHA("1234567890abcdef"); got != "12345678" {
		t.Errorf("shortSHA(long) = %q, want 12345678", got)
	}
}

func TestParseScope_All(t *testing.T) {
	if s, err := parseScope("user"); err != nil || s != "user" {
		t.Errorf("parseScope user: %v, %v", s, err)
	}
	if s, err := parseScope("project"); err != nil || s != "project" {
		t.Errorf("parseScope project: %v, %v", s, err)
	}
	if _, err := parseScope(""); err == nil {
		t.Errorf("expected error for empty scope")
	}
	if _, err := parseScope("invalid-scope"); err == nil {
		t.Errorf("expected error for invalid scope")
	}
}

func TestStore_SaveDirErrors(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "file_blocking_dir")
	mustWriteFile(t, filePath, "blocker", 0o644)

	// Save where parent is a file (MkdirAll fails)
	badStore := NewStore(filepath.Join(filePath, "child", "sources.yaml"))
	if err := badStore.Save(&Registry{}); err == nil {
		t.Errorf("expected save error when parent path is blocked by file")
	}
}

func TestLock_Errors(t *testing.T) {
	if _, err := AcquireLock(""); err == nil {
		t.Errorf("AcquireLock with empty home should fail")
	}

	// nil release
	var l *Lock
	if err := l.Release(); err != nil {
		t.Errorf("nil lock Release should not error: %v", err)
	}

	// Concurrent lock held
	tmpHome := t.TempDir()
	l1, err := AcquireLock(tmpHome)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer l1.Release()

	// Second acquire should fail with concurrency error
	l2, err := AcquireLock(tmpHome)
	if err == nil {
		l2.Release()
		t.Fatalf("expected second AcquireLock to fail")
	}
	if !strings.Contains(err.Error(), "another dotpack process is currently modifying state") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestStore_CorruptedAndErrorCases(t *testing.T) {
	tmp := t.TempDir()
	corruptPath := filepath.Join(tmp, "corrupt.yaml")
	mustWriteFile(t, corruptPath, "sources: [ invalid: yaml: {", 0o644)

	store := NewStore(corruptPath)
	if _, err := store.Load(); err == nil {
		t.Errorf("expected error loading corrupted YAML")
	}
	if _, _, err := store.GetSource("foo/bar"); err == nil {
		t.Errorf("expected error in GetSource on corrupted YAML")
	}
	if err := store.UpsertSource(SourceRecord{ID: "foo/bar"}); err == nil {
		t.Errorf("expected error in UpsertSource on corrupted YAML")
	}
	if err := store.RemoveSource("foo/bar"); err == nil {
		t.Errorf("expected error in RemoveSource on corrupted YAML")
	}

	// Read unreadable path
	unreadableStore := NewStore(filepath.Join(tmp, "dir-unreadable", "file.yaml"))
	mustWriteFile(t, filepath.Join(tmp, "dir-unreadable"), "not-a-dir", 0o644)
	if _, err := unreadableStore.Load(); err == nil {
		t.Errorf("expected error reading through non-directory")
	}
	if err := unreadableStore.Save(&Registry{}); err == nil {
		t.Errorf("expected error saving to invalid directory")
	}
}

func TestDiscoverSkills_ErrorCases(t *testing.T) {
	// 0. Empty repo root
	if _, err := DiscoverSkills("", ""); err == nil {
		t.Errorf("expected error for empty repo root")
	}

	// 1. Non-existent path
	if _, err := DiscoverSkills(filepath.Join(t.TempDir(), "nonexistent"), ""); err == nil {
		t.Errorf("expected error for non-existent path")
	}

	// 2. DiscoveryPath is a file, not a directory
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "afile.txt")
	mustWriteFile(t, filePath, "content", 0o644)
	if _, err := DiscoverSkills(tmp, "afile.txt"); err == nil {
		t.Errorf("expected error when discovery path is a file")
	}

	// 3. SKILL.md is not regular file (directory named SKILL.md)
	dirSKILL := filepath.Join(tmp, "skills", "fake-skill", "SKILL.md")
	if err := os.MkdirAll(dirSKILL, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := DiscoverSkills(tmp, ""); err == nil {
		t.Errorf("expected error when SKILL.md is a directory")
	}
	_ = os.RemoveAll(filepath.Join(tmp, "skills", "fake-skill"))

	// 4. Invalid SKILL.md YAML
	badSkillDir := filepath.Join(tmp, "skills", "bad-skill")
	mustWriteFile(t, filepath.Join(badSkillDir, "SKILL.md"), "---\nname: [ bad: yaml\n---\n", 0o644)
	if _, err := DiscoverSkills(tmp, ""); err == nil {
		t.Errorf("expected error when SKILL.md YAML is malformed")
	}
	_ = os.RemoveAll(badSkillDir)

	// 5. Invalid skill name schema validation error (e.g. uppercase name)
	invalidNameDir := filepath.Join(tmp, "skills", "invalid-name")
	mustWriteFile(t, filepath.Join(invalidNameDir, "SKILL.md"), "---\nname: UPPER_CASE_NAME\ndescription: desc\n---\nBody", 0o644)
	if _, err := DiscoverSkills(tmp, ""); err == nil {
		t.Errorf("expected validation error for invalid skill name")
	}
}

func TestDiscoverSkills_SkippedDirectoriesAndRelPaths(t *testing.T) {
	tmp := t.TempDir()
	// Skill inside skipped node_modules directory
	nodeModulesSkill := filepath.Join(tmp, "node_modules", "ignored-skill")
	writeTestSkill(t, nodeModulesSkill, "ignored-nm", "NM body")

	// Skill inside .dotpack
	dotpackSkill := filepath.Join(tmp, ".dotpack", "ignored-dotpack")
	writeTestSkill(t, dotpackSkill, "ignored-dp", "DP body")

	// Skill inside .github
	githubSkill := filepath.Join(tmp, ".github", "ignored-github")
	writeTestSkill(t, githubSkill, "ignored-gh", "GH body")

	// Valid skill with support files
	validSkill := filepath.Join(tmp, "skills", "valid-skill")
	writeTestSkill(t, validSkill, "valid-skill", "Valid body")
	mustWriteFile(t, filepath.Join(validSkill, "scripts", "run.sh"), "#!/bin/sh\necho hi\n", 0o755)
	mustWriteFile(t, filepath.Join(validSkill, "reference", "notes.txt"), "some notes\n", 0o644)

	discovered, err := DiscoverSkills(tmp, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	if len(discovered) != 1 || discovered[0].Name != "valid-skill" {
		t.Fatalf("expected only 1 valid skill, got %d", len(discovered))
	}
	if len(discovered[0].Skill.SupportFiles) != 2 {
		t.Fatalf("expected 2 support files, got %d", len(discovered[0].Skill.SupportFiles))
	}

	// Relative discovery path
	discRel, err := DiscoverSkills(tmp, "skills")
	if err != nil || len(discRel) != 1 {
		t.Fatalf("expected discovery with relative path to succeed, got %d, err=%v", len(discRel), err)
	}
}

func TestGit_ResolveCommitSHA_FallbacksAndErrors(t *testing.T) {
	// Remote repo with master branch instead of main
	remoteDir, workDir := createTestGitRepo(t, "master")
	writeTestSkill(t, filepath.Join(workDir, "skills", "s1"), "s1", "body")
	shaMaster := commitAndPush(t, workDir, "master", "master commit")

	dotpackHome := t.TempDir()
	d := dirs.Dirs{DotpackHome: dotpackHome}

	ps := ParsedSource{
		ID:       "owner/masterrepo",
		CloneURL: remoteDir,
		Ref:      "",
		Owner:    "owner",
		Repo:     "masterrepo",
	}

	sha, _, err := FetchSource(context.Background(), ps, d)
	if err != nil {
		t.Fatalf("FetchSource master default: %v", err)
	}
	if sha != shaMaster {
		t.Errorf("sha = %q, want %q", sha, shaMaster)
	}

	// Empty DotpackHome
	if _, _, err := FetchSource(context.Background(), ps, dirs.Dirs{}); err == nil {
		t.Errorf("expected error for empty DotpackHome")
	}

	// Test GitRunner default execution
	out, err := DefaultGitRunner(context.Background(), workDir, "status")
	if err != nil || !strings.Contains(string(out), "master") {
		t.Fatalf("DefaultGitRunner: out=%s, err=%v", string(out), err)
	}
	// Bad command
	if _, err := DefaultGitRunner(context.Background(), workDir, "invalid-git-command-xyz"); err == nil {
		t.Errorf("expected error on invalid git command")
	}
}

func TestExecuteSourceTargetUpdate_IndividualAdapterAndErrors(t *testing.T) {
	dotpackHome := t.TempDir()
	userHome := t.TempDir()
	claudeHome := filepath.Join(userHome, ".claude")
	d := dirs.Dirs{
		DotpackHome: dotpackHome,
		HomeDir:     userHome,
		ClaudeHome:  claudeHome,
	}

	mf := manifest.NewStore(filepath.Join(dotpackHome, "installs.yaml"))

	sourceRec := SourceRecord{
		ID:  "upstream/single-host",
		URL: "https://github.com/upstream/single-host.git",
	}

	// Test single per-host adapter (claude-code)
	targetClaude := TargetBinding{
		Agent: "claude-code",
		Scope: "user",
	}

	cacheDir := t.TempDir()
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "single-skill"), "single-skill", "Single host body")

	disc, err := DiscoverSkills(cacheDir, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}

	// 1. Install to claude-code adapter
	tr := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetClaude,
		"1111111111111111111111111111111111111111",
		cacheDir,
		disc,
		nil,
		UpdateOptions{},
		d,
		mf,
	)

	if tr.Added != 1 || tr.Failed != 0 {
		t.Fatalf("claude-code add: added=%d, failed=%d", tr.Added, tr.Failed)
	}

	// Verify installed at ~/.claude/skills/single-skill/SKILL.md
	if _, err := os.Stat(filepath.Join(claudeHome, "skills", "single-skill", "SKILL.md")); err != nil {
		t.Fatalf("claude-code skill not installed: %v", err)
	}

	// 2. Test untracked collision check for claude-code
	targetClaude2 := TargetBinding{
		Agent: "claude-code",
		Scope: "user",
	}
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "collision-skill"), "collision-skill", "Collision body")
	// Put unowned file at destination
	mustWriteFile(t, filepath.Join(claudeHome, "skills", "collision-skill", "SKILL.md"), "untracked", 0o644)

	discCollision, _ := DiscoverSkills(cacheDir, "")
	trCollision := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetClaude2,
		"1111111111111111111111111111111111111111",
		cacheDir,
		discCollision,
		nil,
		UpdateOptions{Force: false},
		d,
		mf,
	)

	if trCollision.Conflict != 1 {
		t.Fatalf("expected 1 conflict for untracked collision, got %d", trCollision.Conflict)
	}

	// 3. Test invalid scope error in ExecuteSourceTargetUpdate
	targetBadScope := TargetBinding{
		Agent: "claude-code",
		Scope: "invalid-scope",
	}
	trBadScope := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetBadScope,
		"1111111111111111111111111111111111111111",
		cacheDir,
		disc,
		nil,
		UpdateOptions{},
		d,
		mf,
	)
	if trBadScope.Failed != 1 {
		t.Fatalf("expected 1 failure for bad scope, got %d", trBadScope.Failed)
	}

	// 4. Test security evaluator returning error
	errEvaluator := func(sourceRoot string, skills []DiscoveredSkill, bypassNames []string, d dirs.Dirs) (map[string]string, error) {
		return nil, errors.New("simulated gate evaluation network timeout")
	}
	trGateErr := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetClaude,
		"2222222222222222222222222222222222222222",
		cacheDir,
		disc,
		nil,
		UpdateOptions{GateEvaluator: errEvaluator},
		d,
		mf,
	)
	if trGateErr.Failed != 1 {
		t.Fatalf("expected 1 failure on gate error, got %d", trGateErr.Failed)
	}

	// 5. Test invalid adapter install error
	targetInvalidAdapter := TargetBinding{
		Agent: "non-existent-adapter-xyz",
		Scope: "user",
	}
	trInvalidAdapter := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetInvalidAdapter,
		"2222222222222222222222222222222222222222",
		cacheDir,
		disc,
		nil,
		UpdateOptions{},
		d,
		mf,
	)
	if trInvalidAdapter.Failed != 1 {
		t.Fatalf("expected failure for invalid adapter")
	}

	// 6. Test cleanEmptyDirs
	cleanDirRoot := t.TempDir()
	nestedEmptyDir := filepath.Join(cleanDirRoot, "a", "b", "c", "d")
	_ = os.MkdirAll(nestedEmptyDir, 0o755)
	cleanEmptyDirs(nestedEmptyDir)
	if _, err := os.Stat(filepath.Join(cleanDirRoot, "a")); !os.IsNotExist(err) {
		t.Errorf("cleanEmptyDirs should have removed empty nested tree")
	}
	cleanEmptyDirs(".")
	cleanEmptyDirs("/")
}

func TestResolveCommitSHA_FallbacksDirect(t *testing.T) {
	tmpRepo := t.TempDir()
	runCmd(t, tmpRepo, "git", "init", "-b", "custom-branch")
	runCmd(t, tmpRepo, "git", "config", "user.name", "Tester")
	runCmd(t, tmpRepo, "git", "config", "user.email", "tester@example.com")
	mustWriteFile(t, filepath.Join(tmpRepo, "file.txt"), "hello", 0o644)
	runCmd(t, tmpRepo, "git", "add", ".")
	runCmd(t, tmpRepo, "git", "commit", "-m", "init commit")

	ctx := context.Background()

	// 1. Resolve HEAD
	sha, err := resolveCommitSHA(ctx, tmpRepo, "")
	if err != nil || len(sha) != 40 {
		t.Fatalf("resolve HEAD commit: %v, sha=%s", err, sha)
	}

	// 2. Explicit branch
	shaBranch, err := resolveCommitSHA(ctx, tmpRepo, "custom-branch")
	if err != nil || shaBranch != sha {
		t.Fatalf("resolve custom-branch commit: %v, sha=%s", err, shaBranch)
	}

	// 3. Non-existent ref
	if _, err := resolveCommitSHA(ctx, tmpRepo, "non-existent-ref"); err == nil {
		t.Fatalf("expected error for non-existent ref")
	}

	// 4. Broken repo dir
	if _, err := resolveCommitSHA(ctx, filepath.Join(t.TempDir(), "empty"), ""); err == nil {
		t.Fatalf("expected error for empty non-repo directory")
	}
}

func TestStore_UpsertAndRemoveEdgeCases(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "sources.yaml")
	store := NewStore(storePath)

	// Empty ID errors
	if err := store.UpsertSource(SourceRecord{}); err == nil {
		t.Errorf("UpsertSource with empty ID should fail")
	}
	if err := store.RemoveSource(""); err == nil {
		t.Errorf("RemoveSource with empty ID should fail")
	}
	if src, ok, err := store.GetSource(""); err != nil || ok || src != nil {
		t.Errorf("GetSource with empty ID should return false, nil")
	}

	// Get non-existent
	if src, ok, err := store.GetSource("non/existent"); err != nil || ok || src != nil {
		t.Errorf("GetSource on non-existent: src=%v, ok=%v, err=%v", src, ok, err)
	}

	// Remove non-existent
	if err := store.RemoveSource("non/existent"); err == nil {
		t.Errorf("RemoveSource on non-existent should fail")
	}

	// Upsert initial
	src1 := SourceRecord{
		ID:                "owner/repo",
		URL:               "https://github.com/owner/repo",
		Ref:               "main",
		DiscoveryPath:     "skills",
		LastFetchedCommit: "1111111111111111111111111111111111111111",
		Targets: []TargetBinding{
			{
				Agent:         "claude-code",
				Scope:         "user",
				AppliedCommit: "1111111111111111111111111111111111111111",
				InstalledSkills: []ManagedSkill{
					{Name: "skill-a", ContentSHA256: "aaa"},
				},
			},
		},
	}
	if err := store.UpsertSource(src1); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}

	// Upsert update existing source & target
	srcUpdate := SourceRecord{
		ID:                "owner/repo",
		URL:               "https://github.com/owner/repo-updated",
		Ref:               "v2",
		DiscoveryPath:     "custom-skills",
		LastFetchedCommit: "2222222222222222222222222222222222222222",
		Targets: []TargetBinding{
			{
				Agent:         "claude-code",
				Scope:         "user",
				AppliedCommit: "2222222222222222222222222222222222222222",
				InstalledSkills: []ManagedSkill{
					{Name: "skill-a", ContentSHA256: "bbb"},
					{Name: "skill-b", ContentSHA256: "ccc"},
				},
			},
			{
				Agent:         "gemini-cli",
				Scope:         "user",
				AppliedCommit: "2222222222222222222222222222222222222222",
			},
		},
	}
	if err := store.UpsertSource(srcUpdate); err != nil {
		t.Fatalf("UpsertSource update: %v", err)
	}

	fetched, ok, err := store.GetSource("owner/repo")
	if err != nil || !ok || fetched == nil {
		t.Fatalf("GetSource: %v", err)
	}
	if fetched.URL != "https://github.com/owner/repo-updated" || fetched.Ref != "v2" || len(fetched.Targets) != 2 {
		t.Errorf("Unexpected updated source: %+v", fetched)
	}

	// Remove source
	if err := store.RemoveSource("owner/repo"); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}
	fetchedAfter, okAfter, _ := store.GetSource("owner/repo")
	if okAfter || fetchedAfter != nil {
		t.Errorf("expected source to be removed")
	}
}

func TestParseSourceSpec_AllVariants(t *testing.T) {
	cases := []struct {
		input       string
		expectErr   bool
		expectedID  string
		expectedRef string
	}{
		{input: "", expectErr: true},
		{input: "owner", expectErr: true},
		{input: "owner/repo/extra/parts", expectErr: true},
		{input: "inv$lid/repo", expectErr: true},
		{input: "owner/inv$lid", expectErr: true},
		{input: "github:owner/repo", expectedID: "owner/repo", expectedRef: ""},
		{input: "github:owner/repo@v1.2.3", expectedID: "owner/repo", expectedRef: "v1.2.3"},
		{input: "github:owner/repo#v2.0.0", expectedID: "owner/repo", expectedRef: "v2.0.0"},
		{input: "https://github.com/%zz/repo", expectErr: true},
		{input: "https://github.com/org/repo#v1.0.0", expectedID: "org/repo", expectedRef: "v1.0.0"},
		{input: "https://github.com/org/repo.git#v1.0.0", expectedID: "org/repo", expectedRef: "v1.0.0"},
		{input: "owner/repo@main", expectedID: "owner/repo", expectedRef: "main"},
		{input: "owner/repo#main", expectedID: "owner/repo", expectedRef: "main"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			ps, err := ParseSourceSpec(tc.input)
			if tc.expectErr {
				if err == nil {
					t.Errorf("expected error for %q", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if tc.expectedID != "" && ps.ID != tc.expectedID {
				t.Errorf("ID = %q, want %q", ps.ID, tc.expectedID)
			}
			if tc.expectedRef != "" && ps.Ref != tc.expectedRef {
				t.Errorf("Ref = %q, want %q", ps.Ref, tc.expectedRef)
			}
		})
	}
}

func TestCheckLocalModifications_Branches(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "skill.md")
	content := "original content"
	mustWriteFile(t, filePath, content, 0o644)
	expectedSHA := sha256String([]byte(content))

	// Clean match
	recClean := manifest.Record{
		FileClaims: []manifest.FileClaim{
			{Path: filePath, SHA256: expectedSHA},
		},
	}
	modified, _ := checkLocalModifications(recClean)
	if modified {
		t.Errorf("expected clean, got modified=true")
	}

	// Modified content
	mustWriteFile(t, filePath, "modified content", 0o644)
	modified, modFile := checkLocalModifications(recClean)
	if !modified || modFile != filePath {
		t.Errorf("expected modified file %s, got modified=%v, file=%s", filePath, modified, modFile)
	}

	// Missing file on disk
	_ = os.Remove(filePath)
	modifiedMissing, _ := checkLocalModifications(recClean)
	if modifiedMissing {
		t.Errorf("expected not modified for missing file")
	}
}

func TestExecuteSourceTargetUpdate_FullBranches(t *testing.T) {
	dotpackHome := t.TempDir()
	userHome := t.TempDir()
	claudeHome := filepath.Join(userHome, ".claude")
	d := dirs.Dirs{
		DotpackHome: dotpackHome,
		HomeDir:     userHome,
		ClaudeHome:  claudeHome,
	}

	mf := manifest.NewStore(filepath.Join(dotpackHome, "installs.yaml"))

	sourceRec := SourceRecord{
		ID:  "org/repo",
		URL: "https://github.com/org/repo.git",
	}

	targetClaude := TargetBinding{
		Agent: "claude-code",
		Scope: "user",
	}

	// 1. Initial install of skill-a and skill-b
	cacheDir := t.TempDir()
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "skill-a"), "skill-a", "Body A")
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "skill-b"), "skill-b", "Body B")
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "skill-c"), "skill-c", "Body C")

	disc1, err := DiscoverSkills(cacheDir, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}

	tr1 := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetClaude,
		"1111111111111111111111111111111111111111",
		cacheDir,
		disc1,
		nil,
		UpdateOptions{},
		d,
		mf,
	)

	if tr1.Added != 3 {
		t.Fatalf("expected 3 added, got %d", tr1.Added)
	}

	// 2. Steady state (no changes)
	trSteady := ExecuteSourceTargetUpdate(
		&sourceRec,
		&tr1.Target,
		"1111111111111111111111111111111111111111",
		cacheDir,
		disc1,
		nil,
		UpdateOptions{},
		d,
		mf,
	)
	if trSteady.Steady != 3 {
		t.Fatalf("expected 3 steady, got %d", trSteady.Steady)
	}

	// 3. Upstream removal and update:
	// - Remove skill-b upstream (clean removal)
	// - Locally modify skill-c (conflict upon upstream removal)
	// - Update skill-a upstream (updated)
	cacheDir2 := t.TempDir()
	writeTestSkill(t, filepath.Join(cacheDir2, "skills", "skill-a"), "skill-a", "Body A Updated!")
	disc2, _ := DiscoverSkills(cacheDir2, "")

	// Modify skill-c locally
	skillCPath := filepath.Join(claudeHome, "skills", "skill-c", "SKILL.md")
	mustWriteFile(t, skillCPath, "locally modified body", 0o644)

	tr2 := ExecuteSourceTargetUpdate(
		&sourceRec,
		&tr1.Target,
		"2222222222222222222222222222222222222222",
		cacheDir2,
		disc2,
		nil,
		UpdateOptions{},
		d,
		mf,
	)

	if tr2.Updated != 1 || tr2.Removed != 1 || tr2.Conflict != 1 {
		t.Fatalf("tr2: updated=%d (want 1), removed=%d (want 1), conflict=%d (want 1)", tr2.Updated, tr2.Removed, tr2.Conflict)
	}

	// 4. Cross-source conflict
	otherSource := SourceRecord{
		ID: "other/repo",
		Targets: []TargetBinding{
			{
				Agent: "claude-code",
				Scope: "user",
				InstalledSkills: []ManagedSkill{
					{Name: "skill-a"},
				},
			},
		},
	}

	trCross := ExecuteSourceTargetUpdate(
		&sourceRec,
		&tr1.Target,
		"2222222222222222222222222222222222222222",
		cacheDir2,
		disc2,
		[]SourceRecord{sourceRec, otherSource},
		UpdateOptions{},
		d,
		mf,
	)
	if trCross.Conflict != 2 { // skill-a (cross collision) and skill-c (upstream removal with local modification)
		t.Fatalf("trCross: conflict=%d, want 2", trCross.Conflict)
	}

	// 5. Blocked by security gate on existing managed skill
	gateBlock := func(sourceRoot string, skills []DiscoveredSkill, bypassNames []string, d dirs.Dirs) (map[string]string, error) {
		return map[string]string{"skill-a": "security policy blocked skill-a"}, nil
	}
	trBlocked := ExecuteSourceTargetUpdate(
		&sourceRec,
		&tr1.Target,
		"3333333333333333333333333333333333333333",
		cacheDir2,
		disc2,
		nil,
		UpdateOptions{GateEvaluator: gateBlock},
		d,
		mf,
	)
	if trBlocked.Blocked != 1 {
		t.Fatalf("trBlocked: blocked=%d, want 1", trBlocked.Blocked)
	}
}

func TestDiscoverSkills_Symlinks(t *testing.T) {
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "real_skill.md")
	mustWriteFile(t, targetFile, "---\nname: sym-skill\n---\nbody", 0o644)

	// Symlinked SKILL.md
	symSkillDir := filepath.Join(tmpDir, "skills", "sym-skill")
	_ = os.MkdirAll(symSkillDir, 0o755)
	_ = os.Symlink(targetFile, filepath.Join(symSkillDir, "SKILL.md"))

	if _, err := DiscoverSkills(tmpDir, ""); err == nil {
		t.Errorf("expected error discovering symlinked SKILL.md")
	}

	// Remove symlinked SKILL.md and make it real
	_ = os.Remove(filepath.Join(symSkillDir, "SKILL.md"))
	mustWriteFile(t, filepath.Join(symSkillDir, "SKILL.md"), "---\nname: sym-skill\n---\nbody", 0o644)

	// Symlinked support file
	_ = os.Symlink(targetFile, filepath.Join(symSkillDir, "support.txt"))
	if _, err := DiscoverSkills(tmpDir, ""); err == nil {
		t.Errorf("expected error discovering symlinked support file")
	}
}

func TestFetchSource_ExistingCacheAndRefTypes(t *testing.T) {
	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkill(t, filepath.Join(workDir, "skills", "skill-fetch"), "skill-fetch", "v1 body")
	sha1 := commitAndPush(t, workDir, "main", "commit 1")

	dotpackHome := t.TempDir()
	d := dirs.Dirs{DotpackHome: dotpackHome}

	ps := ParsedSource{
		ID:       "fetch/repo",
		CloneURL: remoteDir,
		Ref:      "main",
		Owner:    "fetch",
		Repo:     "repo",
	}

	// 1. Initial fetch (fresh clone)
	shaGot1, cacheDir1, err := FetchSource(context.Background(), ps, d)
	if err != nil {
		t.Fatalf("FetchSource 1: %v", err)
	}
	if shaGot1 != sha1 {
		t.Errorf("shaGot1 = %s, want %s", shaGot1, sha1)
	}

	// 2. Commit update to remote
	writeTestSkill(t, filepath.Join(workDir, "skills", "skill-fetch"), "skill-fetch", "v2 body")
	sha2 := commitAndPush(t, workDir, "main", "commit 2")

	// 3. Second fetch (existing clone)
	shaGot2, cacheDir2, err := FetchSource(context.Background(), ps, d)
	if err != nil {
		t.Fatalf("FetchSource 2: %v", err)
	}
	if cacheDir1 != cacheDir2 {
		t.Errorf("cacheDir changed: %s vs %s", cacheDir1, cacheDir2)
	}
	if shaGot2 != sha2 {
		t.Errorf("shaGot2 = %s, want %s", shaGot2, sha2)
	}

	// 4. Second fetch with empty ref (default branch)
	psDefault := ps
	psDefault.Ref = ""
	shaGot3, _, err := FetchSource(context.Background(), psDefault, d)
	if err != nil {
		t.Fatalf("FetchSource 3 (default ref): %v", err)
	}
	if shaGot3 != sha2 {
		t.Errorf("shaGot3 = %s, want %s", shaGot3, sha2)
	}
}

func TestParseSourceSpec_AbsPathAndRefErrors(t *testing.T) {
	absDir := "/tmp/test/owner/myrepo"
	ps, err := ParseSourceSpec(absDir + "#v1.0")
	if err != nil {
		t.Fatalf("ParseSourceSpec abs path: %v", err)
	}
	if ps.ID != "owner/myrepo" || ps.Ref != "v1.0" {
		t.Errorf("ParseSourceSpec abs: got ID=%q, Ref=%q", ps.ID, ps.Ref)
	}

	// Bad ref characters
	if _, err := ParseSourceSpec("owner/repo#-bad"); err == nil {
		t.Errorf("expected error for ref starting with '-'")
	}
	if _, err := ParseSourceSpec("owner/repo#foo..bar"); err == nil {
		t.Errorf("expected error for ref with '..'")
	}
}

func TestExecuteSourceTargetUpdate_Umbrella(t *testing.T) {
	dotpackHome := t.TempDir()
	userHome := t.TempDir()
	d := dirs.Dirs{
		DotpackHome: dotpackHome,
		HomeDir:     userHome,
		AgentsHome:  filepath.Join(userHome, ".agents"),
		ClaudeHome:  filepath.Join(userHome, ".claude"),
		GeminiHome:  filepath.Join(userHome, ".gemini"),
	}

	mf := manifest.NewStore(filepath.Join(dotpackHome, "installs.yaml"))

	sourceRec := SourceRecord{
		ID:  "upstream/umbrella-repo",
		URL: "https://github.com/upstream/umbrella-repo.git",
	}

	targetUmbrella := TargetBinding{
		Agent: "agents-cli",
		Scope: "user",
	}

	cacheDir := t.TempDir()
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "umbrella-skill"), "umbrella-skill", "Umbrella skill body")

	disc, err := DiscoverSkills(cacheDir, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}

	tr := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetUmbrella,
		"1111111111111111111111111111111111111111",
		cacheDir,
		disc,
		nil,
		UpdateOptions{},
		d,
		mf,
	)

	if tr.Added != 1 || tr.Failed != 0 {
		t.Fatalf("umbrella install: added=%d, failed=%d, skills=%+v", tr.Added, tr.Failed, tr.Skills)
	}
}

func TestUpdater_EdgeCoverageHelpers(t *testing.T) {
	// 1. matchesSourceProvenance
	if matchesSourceProvenance(manifest.Record{}, nil, "") {
		t.Errorf("expected false for nil sourceRec")
	}

	cacheDir := t.TempDir()
	src := SourceRecord{
		ID:  "owner/repo",
		URL: "https://github.com/owner/repo.git",
	}

	rec1 := manifest.Record{
		Source: "https://github.com/owner/repo.git",
	}
	if !matchesSourceProvenance(rec1, &src, cacheDir) {
		t.Errorf("expected true for exact URL match")
	}

	rec2 := manifest.Record{
		CanonicalRoot: filepath.Join(cacheDir, "nested"),
	}
	if !matchesSourceProvenance(rec2, &src, cacheDir) {
		t.Errorf("expected true for CanonicalRoot under cacheDir")
	}

	rec3 := manifest.Record{
		SourcePath: filepath.Join(cacheDir, "nested", "SKILL.md"),
	}
	if !matchesSourceProvenance(rec3, &src, cacheDir) {
		t.Errorf("expected true for SourcePath under cacheDir")
	}

	rec4 := manifest.Record{
		Source:        "file:///other/path",
		CanonicalRoot: "/unrelated/root",
		SourcePath:    "/unrelated/path",
	}
	if matchesSourceProvenance(rec4, &src, cacheDir) {
		t.Errorf("expected false for unrelated provenance")
	}

	// 2. isSkillIntactAndUnchanged with Files
	tmpFile := filepath.Join(t.TempDir(), "SKILL.md")
	mustWriteFile(t, tmpFile, "test", 0o644)

	recFiles := manifest.Record{
		Files: []string{tmpFile},
	}
	if !isSkillIntactAndUnchanged(ManagedSkill{ContentSHA256: "abc"}, DiscoveredSkill{ContentSHA256: "abc"}, recFiles, true) {
		t.Errorf("expected true for intact Files record")
	}

	recFilesMissing := manifest.Record{
		Files: []string{filepath.Join(t.TempDir(), "nonexistent.md")},
	}
	if isSkillIntactAndUnchanged(ManagedSkill{ContentSHA256: "abc"}, DiscoveredSkill{ContentSHA256: "abc"}, recFilesMissing, true) {
		t.Errorf("expected false for missing Files")
	}

	// 3. isPathInClaims
	recClaims := manifest.Record{
		FileClaims: []manifest.FileClaim{{Path: tmpFile}},
		Files:      []string{tmpFile + ".other"},
	}
	if !isPathInClaims(tmpFile, recClaims) {
		t.Errorf("expected isPathInClaims true for claimed path")
	}
	if !isPathInClaims(tmpFile+".other", recClaims) {
		t.Errorf("expected isPathInClaims true for Files path")
	}
	if isPathInClaims("/unrelated/path", recClaims) {
		t.Errorf("expected isPathInClaims false for unrelated path")
	}

	// 4. removeClaimedFiles deduplication and non-empty dir handling
	dir := t.TempDir()
	file1 := filepath.Join(dir, "sub", "file1.txt")
	file2 := filepath.Join(dir, "sub", "keep.txt")
	mustWriteFile(t, file1, "1", 0o644)
	mustWriteFile(t, file2, "keep", 0o644)

	recToRemove := manifest.Record{
		FileClaims: []manifest.FileClaim{{Path: file1}, {Path: file1}}, // duplicate claim
		Files:      []string{file1},                                    // duplicate in files
	}
	if err := removeClaimedFiles(recToRemove); err != nil {
		t.Fatalf("removeClaimedFiles: %v", err)
	}
	if _, err := os.Stat(file1); !os.IsNotExist(err) {
		t.Errorf("expected file1 removed")
	}
	if _, err := os.Stat(file2); err != nil {
		t.Errorf("expected file2 preserved")
	}

	// 5. Store Save error and success
	unwritableStore := NewStore(filepath.Join("/dev/null", "unwritable", "sources.yaml"))
	if err := unwritableStore.Save(&Registry{}); err == nil {
		t.Errorf("expected Save to fail on invalid path")
	}

	validStore := NewStore(filepath.Join(dir, "sources.yaml"))
	if err := validStore.Save(&Registry{Version: 1}); err != nil {
		t.Fatalf("valid Save failed: %v", err)
	}

	// 6. checkNewFileCollisions when untracked file exists
	collidingRec := manifest.Record{}
	planFiles := []adapter.FileWrite{{Path: file2}} // file2 exists on disk and is not in collidingRec
	if collides, _ := checkNewFileCollisions(planFiles, collidingRec); !collides {
		t.Errorf("expected checkNewFileCollisions true when file exists and not in claims")
	}

	// 7. removeClaimedFiles error branch
	readOnlyDir := filepath.Join(dir, "readonly")
	roFile := filepath.Join(readOnlyDir, "ro.txt")
	mustWriteFile(t, roFile, "content", 0o644)
	_ = os.Chmod(readOnlyDir, 0o500)
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0o755) })

	roRec := manifest.Record{
		FileClaims: []manifest.FileClaim{{Path: roFile}},
	}
	if err := removeClaimedFiles(roRec); err == nil {
		t.Errorf("expected removeClaimedFiles error when permission denied")
	}

	// 8. HasFailures branch
	if !(UpdateResult{Summary: SummaryCounts{Conflict: 1}}).HasFailures() {
		t.Errorf("expected HasFailures true on conflict")
	}
	if !(UpdateResult{Summary: SummaryCounts{Blocked: 1}}).HasFailures() {
		t.Errorf("expected HasFailures true on blocked")
	}
	if !(UpdateResult{Summary: SummaryCounts{Failed: 1}}).HasFailures() {
		t.Errorf("expected HasFailures true on failed")
	}
	if (UpdateResult{Summary: SummaryCounts{Added: 1}}).HasFailures() {
		t.Errorf("expected HasFailures false on success only")
	}
}
