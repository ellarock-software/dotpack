package sourceregistry

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
)

func TestParseSourceSpec(t *testing.T) {
	cases := []struct {
		input   string
		wantID  string
		wantURL string
		wantRef string
		wantErr bool
	}{
		{"mattpocock/skills", "mattpocock/skills", "https://github.com/mattpocock/skills.git", "", false},
		{"github:mattpocock/skills", "mattpocock/skills", "https://github.com/mattpocock/skills.git", "", false},
		{"github:mattpocock/skills@v1.2.0", "mattpocock/skills", "https://github.com/mattpocock/skills.git", "v1.2.0", false},
		{"https://github.com/mattpocock/skills", "mattpocock/skills", "https://github.com/mattpocock/skills.git", "", false},
		{"https://github.com/mattpocock/skills.git", "mattpocock/skills", "https://github.com/mattpocock/skills.git", "", false},
		{"https://github.com/mattpocock/skills#main", "mattpocock/skills", "https://github.com/mattpocock/skills.git", "main", false},
		{"https://github.com/mattpocock/skills@develop", "mattpocock/skills", "https://github.com/mattpocock/skills.git", "develop", false},
		{"http://github.com/owner/repo", "owner/repo", "https://github.com/owner/repo.git", "", false},
		{"", "", "", "", true},
		{"singleword", "", "", "", true},
		{"owner/repo/extra", "", "", "", true},
		{"../traversal/repo", "", "", "", true},
		{"owner/..", "", "", "", true},
		{"owner/repo@-badref", "", "", "", true},
		{"owner/repo@bad..ref", "", "", "", true},
		{"owner/repo@bad\nref", "", "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			ps, err := ParseSourceSpec(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseSourceSpec(%q) expected error, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSourceSpec(%q) unexpected error: %v", tc.input, err)
			}
			if ps.ID != tc.wantID {
				t.Errorf("ID = %q, want %q", ps.ID, tc.wantID)
			}
			if ps.CloneURL != tc.wantURL {
				t.Errorf("CloneURL = %q, want %q", ps.CloneURL, tc.wantURL)
			}
			if ps.Ref != tc.wantRef {
				t.Errorf("Ref = %q, want %q", ps.Ref, tc.wantRef)
			}
		})
	}
}

func TestStore_Operations(t *testing.T) {
	tmp := t.TempDir()
	storePath := filepath.Join(tmp, "sources.yaml")
	store := NewStore(storePath)

	if store.Path() != storePath {
		t.Fatalf("Path = %q, want %q", store.Path(), storePath)
	}

	// Initial load should be empty
	reg, err := store.Load()
	if err != nil {
		t.Fatalf("initial Load: %v", err)
	}
	if len(reg.Sources) != 0 {
		t.Fatalf("expected 0 sources, got %d", len(reg.Sources))
	}

	// Upsert empty ID error
	if err := store.UpsertSource(SourceRecord{}); err == nil {
		t.Fatalf("UpsertSource with empty ID should fail")
	}

	// Upsert source 1
	src1 := SourceRecord{
		ID:                "owner1/repo1",
		URL:               "https://github.com/owner1/repo1.git",
		Ref:               "main",
		LastFetchedCommit: "1111111111111111111111111111111111111111",
		DiscoveryPath:     "skills",
		Targets: []TargetBinding{
			{
				Agent:         "agents-cli",
				Scope:         "user",
				AppliedCommit: "1111111111111111111111111111111111111111",
				InstalledSkills: []ManagedSkill{
					{Name: "skill-a", SourceRelPath: "skills/skill-a", AppliedCommit: "1111111111111111111111111111111111111111", ContentSHA256: "sha256:aaa"},
				},
			},
		},
	}
	if err := store.UpsertSource(src1); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}

	// Get source 1
	got, found, err := store.GetSource("owner1/repo1")
	if err != nil || !found {
		t.Fatalf("GetSource: found=%v, err=%v", found, err)
	}
	if got.ID != "owner1/repo1" || got.Ref != "main" || len(got.Targets) != 1 {
		t.Fatalf("GetSource returned unexpected record: %+v", got)
	}

	// Re-upsert source 1 with a new target
	src1NewTarget := SourceRecord{
		ID:  "owner1/repo1",
		Ref: "v2.0",
		Targets: []TargetBinding{
			{
				Agent:      "claude-code",
				Scope:      "project",
				TargetRoot: "/tmp/project1",
			},
		},
	}
	if err := store.UpsertSource(src1NewTarget); err != nil {
		t.Fatalf("UpsertSource merge: %v", err)
	}

	gotMerged, found, err := store.GetSource("owner1/repo1")
	if err != nil || !found {
		t.Fatalf("GetSource merged: %v", err)
	}
	if gotMerged.Ref != "v2.0" {
		t.Errorf("Ref = %q, want v2.0", gotMerged.Ref)
	}
	if len(gotMerged.Targets) != 2 {
		t.Fatalf("expected 2 targets after merge, got %d", len(gotMerged.Targets))
	}

	// Upsert source 2
	src2 := SourceRecord{
		ID:  "owner2/repo2",
		URL: "https://github.com/owner2/repo2.git",
	}
	if err := store.UpsertSource(src2); err != nil {
		t.Fatalf("UpsertSource 2: %v", err)
	}

	reg, err = store.Load()
	if err != nil || len(reg.Sources) != 2 {
		t.Fatalf("Load sources: count=%d, err=%v", len(reg.Sources), err)
	}

	// RemoveSource non-existent
	if err := store.RemoveSource("nonexistent/repo"); err == nil {
		t.Fatalf("RemoveSource nonexistent should fail")
	}
	if err := store.RemoveSource(""); err == nil {
		t.Fatalf("RemoveSource empty ID should fail")
	}

	// RemoveSource owner1/repo1
	if err := store.RemoveSource("owner1/repo1"); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}
	reg, _ = store.Load()
	if len(reg.Sources) != 1 || reg.Sources[0].ID != "owner2/repo2" {
		t.Fatalf("expected 1 remaining source (owner2/repo2), got %+v", reg.Sources)
	}
}

func TestLock_AcquireReleaseAndConflict(t *testing.T) {
	dotpackHome := t.TempDir()

	lock1, err := AcquireLock(dotpackHome)
	if err != nil {
		t.Fatalf("AcquireLock 1: %v", err)
	}

	// Second acquire must fail with conflict
	lock2, err := AcquireLock(dotpackHome)
	if err == nil {
		_ = lock2.Release()
		t.Fatalf("AcquireLock 2 should have failed due to active lock")
	}
	if !strings.Contains(err.Error(), "another dotpack process is currently modifying state") {
		t.Fatalf("unexpected conflict error: %v", err)
	}

	// Release lock 1
	if err := lock1.Release(); err != nil {
		t.Fatalf("Release lock 1: %v", err)
	}

	// Third acquire should succeed
	lock3, err := AcquireLock(dotpackHome)
	if err != nil {
		t.Fatalf("AcquireLock 3 after release: %v", err)
	}
	_ = lock3.Release()

	// Release on nil/already released is safe
	if err := lock1.Release(); err != nil {
		t.Fatalf("Release nil lock error: %v", err)
	}
}

func TestDiscoverSkills_HierarchyAndSafety(t *testing.T) {
	repoRoot := t.TempDir()

	// 1. Canonical layout
	writeTestSkill(t, filepath.Join(repoRoot, ".agents", "skills", "canonical-skill"), "canonical-skill", "Canonical skill body")

	// 2. Root-level layout
	writeTestSkill(t, filepath.Join(repoRoot, "skills", "root-skill"), "root-skill", "Root skill body")

	// 3. Category-nested layout
	nestedDir := filepath.Join(repoRoot, "skills", "categories", "frontend", "react-skill")
	writeTestSkill(t, nestedDir, "react-skill", "React skill body")
	// Add support file to react-skill
	mustWriteFile(t, filepath.Join(nestedDir, "scripts", "build.sh"), "#!/bin/sh\necho building\n", 0o755)
	mustWriteFile(t, filepath.Join(nestedDir, "references", "guide.md"), "# Guide\nReact tips\n", 0o644)

	// 4. Subdirectory with SKILL.md inside an existing skill package (MUST NOT be discovered as separate package!)
	mustWriteFile(t, filepath.Join(nestedDir, "examples", "sub-example", "SKILL.md"), "---\nname: ignored-sub\n---\nIgnored", 0o644)

	skills, err := DiscoverSkills(repoRoot, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}

	wantNames := []string{"canonical-skill", "react-skill", "root-skill"}
	if len(skills) != len(wantNames) {
		var gotNames []string
		for _, s := range skills {
			gotNames = append(gotNames, s.Name)
		}
		t.Fatalf("Discovered %d skills (%v), want %d (%v)", len(skills), gotNames, len(wantNames), wantNames)
	}

	for i, name := range wantNames {
		if skills[i].Name != name {
			t.Errorf("skill[%d] = %q, want %q", i, skills[i].Name, name)
		}
	}

	// Verify react-skill has its support files and computed content hash
	var reactSkill *DiscoveredSkill
	for i := range skills {
		if skills[i].Name == "react-skill" {
			reactSkill = &skills[i]
			break
		}
	}
	if reactSkill == nil {
		t.Fatalf("react-skill not found")
	}
	if len(reactSkill.Skill.SupportFiles) < 2 {
		t.Fatalf("react-skill expected at least 2 support files, got %d", len(reactSkill.Skill.SupportFiles))
	}
	if reactSkill.ContentSHA256 == "" || !strings.HasPrefix(reactSkill.ContentSHA256, "sha256:") {
		t.Errorf("invalid ContentSHA256: %q", reactSkill.ContentSHA256)
	}
}

func TestDiscoverSkills_RootLevelPackage(t *testing.T) {
	repoRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(repoRoot, "SKILL.md"), "---\nname: root-level-skill\ndescription: root\n---\nBody", 0o644)
	mustWriteFile(t, filepath.Join(repoRoot, "scripts", "run.sh"), "#!/bin/sh\necho hi\n", 0o755)

	skills, err := DiscoverSkills(repoRoot, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "root-level-skill" {
		t.Errorf("skill name = %q, want root-level-skill", skills[0].Name)
	}
	if len(skills[0].Skill.SupportFiles) != 1 {
		t.Errorf("expected 1 support file, got %d", len(skills[0].Skill.SupportFiles))
	}
}

func TestDiscoverSkills_DuplicateNameRejection(t *testing.T) {
	repoRoot := t.TempDir()
	writeTestSkill(t, filepath.Join(repoRoot, "skills", "dir1"), "duplicate-name", "body 1")
	writeTestSkill(t, filepath.Join(repoRoot, "skills", "dir2"), "duplicate-name", "body 2")

	_, err := DiscoverSkills(repoRoot, "")
	if err == nil {
		t.Fatalf("expected error on duplicate skill names within one repository")
	}
	if !strings.Contains(err.Error(), "duplicate skill name \"duplicate-name\"") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestDiscoverSkills_SymlinkRejection(t *testing.T) {
	repoRoot := t.TempDir()
	skillDir := filepath.Join(repoRoot, "skills", "symlink-skill")
	writeTestSkill(t, skillDir, "symlink-skill", "body")
	targetFile := filepath.Join(repoRoot, "external.txt")
	mustWriteFile(t, targetFile, "external", 0o644)
	if err := os.Symlink(targetFile, filepath.Join(skillDir, "link.txt")); err != nil {
		t.Skipf("symlink not supported on this filesystem: %v", err)
	}

	_, err := DiscoverSkills(repoRoot, "")
	if err == nil {
		t.Fatalf("expected error on symlink support file")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestGit_FetchAndRefResolution_RealGit(t *testing.T) {
	remoteDir := t.TempDir()
	runCmd(t, remoteDir, "git", "init", "--bare", "-b", "main")

	workDir := t.TempDir()
	runCmd(t, workDir, "git", "init", "-b", "main")
	runCmd(t, workDir, "git", "config", "user.name", "Tester")
	runCmd(t, workDir, "git", "config", "user.email", "test@example.com")
	runCmd(t, workDir, "git", "remote", "add", "origin", remoteDir)

	writeTestSkill(t, filepath.Join(workDir, "skills", "test-skill"), "test-skill", "v1 body")
	runCmd(t, workDir, "git", "add", ".")
	runCmd(t, workDir, "git", "commit", "-m", "v1 commit")
	runCmd(t, workDir, "git", "tag", "v1.0.0")
	runCmd(t, workDir, "git", "push", "origin", "main", "--tags")

	dotpackHome := t.TempDir()
	d := dirs.Dirs{DotpackHome: dotpackHome}

	// 1. Fetch default branch
	psDefault := ParsedSource{
		ID:       "testowner/testrepo",
		CloneURL: remoteDir,
		Ref:      "",
		Owner:    "testowner",
		Repo:     "testrepo",
	}

	sha1, cacheDir1, err := FetchSource(context.Background(), psDefault, d)
	if err != nil {
		t.Fatalf("FetchSource default branch: %v", err)
	}
	if len(sha1) != 40 {
		t.Fatalf("expected 40-hex SHA, got %q", sha1)
	}

	// 2. Fetch explicit tag
	psTag := ParsedSource{
		ID:       "testowner/testrepo",
		CloneURL: remoteDir,
		Ref:      "v1.0.0",
		Owner:    "testowner",
		Repo:     "testrepo",
	}
	shaTag, _, err := FetchSource(context.Background(), psTag, d)
	if err != nil {
		t.Fatalf("FetchSource tag: %v", err)
	}
	if shaTag != sha1 {
		t.Errorf("tag sha %q != commit sha %q", shaTag, sha1)
	}

	// 3. Advance main on remote
	writeTestSkill(t, filepath.Join(workDir, "skills", "test-skill"), "test-skill", "v2 body")
	runCmd(t, workDir, "git", "add", ".")
	runCmd(t, workDir, "git", "commit", "-m", "v2 commit")
	runCmd(t, workDir, "git", "push", "origin", "main")

	sha2, cacheDir2, err := FetchSource(context.Background(), psDefault, d)
	if err != nil {
		t.Fatalf("FetchSource advance main: %v", err)
	}
	if sha2 == sha1 {
		t.Fatalf("expected new commit after main advanced, got same %s", sha2)
	}
	if cacheDir1 != cacheDir2 {
		t.Errorf("cache dir changed between fetches: %s vs %s", cacheDir1, cacheDir2)
	}

	// 4. Pin to old commit
	psPinned := ParsedSource{
		ID:       "testowner/testrepo",
		CloneURL: remoteDir,
		Ref:      sha1,
		Owner:    "testowner",
		Repo:     "testrepo",
	}
	shaPinned, _, err := FetchSource(context.Background(), psPinned, d)
	if err != nil {
		t.Fatalf("FetchSource pinned commit: %v", err)
	}
	if shaPinned != sha1 {
		t.Errorf("pinned sha %q != %q", shaPinned, sha1)
	}

	// 5. Invalid ref fails
	psBad := ParsedSource{
		ID:       "testowner/testrepo",
		CloneURL: remoteDir,
		Ref:      "nonexistent-branch-xyz",
		Owner:    "testowner",
		Repo:     "testrepo",
	}
	if _, _, err := FetchSource(context.Background(), psBad, d); err == nil {
		t.Fatalf("FetchSource bad ref should have failed")
	}
}

func TestUpdater_LifecycleFull(t *testing.T) {
	dotpackHome := t.TempDir()
	userHome := t.TempDir()
	claudeHome := filepath.Join(userHome, ".claude")
	agentsHome := filepath.Join(userHome, ".agents")
	d := dirs.Dirs{
		DotpackHome: dotpackHome,
		HomeDir:     userHome,
		ClaudeHome:  claudeHome,
		AgentsHome:  agentsHome,
	}

	mf := manifest.NewStore(filepath.Join(dotpackHome, "installs.yaml"))

	sourceRec := SourceRecord{
		ID:  "upstream/catalog",
		URL: "https://github.com/upstream/catalog.git",
		Ref: "",
	}
	targetBinding := TargetBinding{
		Agent: "agents-cli",
		Scope: "user",
	}

	cacheDir := t.TempDir()
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "skill-alpha"), "skill-alpha", "Alpha v1")
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "skill-beta"), "skill-beta", "Beta v1")

	disc1, err := DiscoverSkills(cacheDir, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}

	opts := UpdateOptions{}

	// 1. Initial Add
	tr1 := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetBinding,
		"1111111111111111111111111111111111111111",
		cacheDir,
		disc1,
		nil,
		opts,
		d,
		mf,
	)

	if tr1.Added != 2 || tr1.Updated != 0 || tr1.Removed != 0 || tr1.Failed != 0 {
		t.Fatalf("Initial add: added=%d, updated=%d, removed=%d, failed=%d", tr1.Added, tr1.Updated, tr1.Removed, tr1.Failed)
	}
	targetBinding = tr1.Target

	// Verify files written on disk
	installedAlpha := filepath.Join(agentsHome, "skills", "skill-alpha", "SKILL.md")
	if _, err := os.Stat(installedAlpha); err != nil {
		t.Fatalf("installed alpha skill missing: %v", err)
	}

	// 2. Rerun unchanged (idempotent no-op)
	tr2 := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetBinding,
		"1111111111111111111111111111111111111111",
		cacheDir,
		disc1,
		nil,
		opts,
		d,
		mf,
	)
	if tr2.Steady != 2 || tr2.Added != 0 || tr2.Updated != 0 || tr2.Removed != 0 {
		t.Fatalf("Idempotent update: steady=%d, added=%d, updated=%d", tr2.Steady, tr2.Added, tr2.Updated)
	}

	// 3. Upstream changes: update alpha, remove beta, add gamma
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "skill-alpha"), "skill-alpha", "Alpha v2 with changes")
	_ = os.RemoveAll(filepath.Join(cacheDir, "skills", "skill-beta"))
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "skill-gamma"), "skill-gamma", "Gamma v1")

	disc2, err := DiscoverSkills(cacheDir, "")
	if err != nil {
		t.Fatalf("DiscoverSkills 2: %v", err)
	}

	tr3 := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetBinding,
		"2222222222222222222222222222222222222222",
		cacheDir,
		disc2,
		nil,
		opts,
		d,
		mf,
	)

	if tr3.Updated != 1 || tr3.Added != 1 || tr3.Removed != 1 || tr3.Failed != 0 {
		t.Fatalf("Update outcome: updated=%d, added=%d, removed=%d, failed=%d", tr3.Updated, tr3.Added, tr3.Removed, tr3.Failed)
	}
	targetBinding = tr3.Target

	// Verify beta was deleted from disk
	installedBeta := filepath.Join(agentsHome, "skills", "skill-beta", "SKILL.md")
	if _, err := os.Stat(installedBeta); !os.IsNotExist(err) {
		t.Fatalf("removed beta skill still exists on disk")
	}

	// Verify alpha was updated
	alphaContent, _ := os.ReadFile(installedAlpha)
	if !strings.Contains(string(alphaContent), "Alpha v2 with changes") {
		t.Fatalf("alpha content not updated: %s", string(alphaContent))
	}

	// 4. Local modifications conflict preservation
	// Modify gamma locally
	installedGamma := filepath.Join(agentsHome, "skills", "skill-gamma", "SKILL.md")
	mustWriteFile(t, installedGamma, "---\nname: skill-gamma\n---\nLocal edit by user", 0o644)

	// Upstream updates gamma
	writeTestSkill(t, filepath.Join(cacheDir, "skills", "skill-gamma"), "skill-gamma", "Gamma v2 from upstream")
	disc3, _ := DiscoverSkills(cacheDir, "")

	tr4 := ExecuteSourceTargetUpdate(
		&sourceRec,
		&targetBinding,
		"3333333333333333333333333333333333333333",
		cacheDir,
		disc3,
		nil,
		opts,
		d,
		mf,
	)

	if tr4.Conflict != 1 {
		t.Fatalf("expected 1 conflict for locally modified skill, got %d", tr4.Conflict)
	}

	// Verify user's local edit was preserved!
	gammaContent, _ := os.ReadFile(installedGamma)
	if !strings.Contains(string(gammaContent), "Local edit by user") {
		t.Fatalf("user's local edits were overwritten: %s", string(gammaContent))
	}
}

func writeTestSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	mustWriteFile(t, filepath.Join(dir, "SKILL.md"), fmt.Sprintf("---\nname: %s\ndescription: %s description\n---\n%s\n", name, name, body), 0o644)
}

func mustWriteFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func createTestGitRepo(t *testing.T, branch string) (string, string) {
	t.Helper()
	remoteDir := t.TempDir()
	runCmd(t, remoteDir, "git", "init", "--bare", "-b", branch)

	workDir := t.TempDir()
	runCmd(t, workDir, "git", "init", "-b", branch)
	runCmd(t, workDir, "git", "config", "user.name", "Tester")
	runCmd(t, workDir, "git", "config", "user.email", "test@example.com")
	runCmd(t, workDir, "git", "remote", "add", "origin", remoteDir)

	return remoteDir, workDir
}

func commitAndPush(t *testing.T, workDir, branch, msg string) string {
	t.Helper()
	runCmd(t, workDir, "git", "add", ".")
	runCmd(t, workDir, "git", "commit", "-m", msg)
	runCmd(t, workDir, "git", "push", "origin", branch)
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %s in %s: %v\noutput: %s", name, strings.Join(args, " "), dir, err, string(out))
	}
}
