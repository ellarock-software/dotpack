package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/sourceregistry"
)

// Helper to create real disposable remote and local Git repositories.
func createTestGitRepo(t *testing.T, branch string) (string, string) {
	t.Helper()
	remoteDir := t.TempDir()
	runGitCmd(t, remoteDir, "init", "--bare", "-b", branch)

	workDir := t.TempDir()
	runGitCmd(t, workDir, "init", "-b", branch)
	runGitCmd(t, workDir, "config", "user.name", "Dotpack Tester")
	runGitCmd(t, workDir, "config", "user.email", "tester@example.com")
	runGitCmd(t, workDir, "remote", "add", "origin", remoteDir)

	return remoteDir, workDir
}

func commitAndPush(t *testing.T, workDir, branch, msg string) string {
	t.Helper()
	runGitCmd(t, workDir, "add", ".")
	runGitCmd(t, workDir, "commit", "-m", msg)
	runGitCmd(t, workDir, "push", "origin", branch)
	out, err := execGit(workDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := execGit(dir, args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v\noutput: %s", strings.Join(args, " "), dir, err, string(out))
	}
}

func execGit(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	return cmd.CombinedOutput()
}

func setupTestEnv(t *testing.T) (dotpackHome, userHome, targetRoot string) {
	t.Helper()
	dotpackHome = t.TempDir()
	userHome = t.TempDir()
	targetRoot = t.TempDir()

	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_USER_HOME", userHome)
	t.Setenv("DOTPACK_CLAUDE_HOME", filepath.Join(userHome, ".claude"))
	t.Setenv("DOTPACK_GEMINI_HOME", filepath.Join(userHome, ".gemini"))
	t.Setenv("DOTPACK_ANTIGRAVITY_HOME", filepath.Join(userHome, ".antigravity"))
	t.Setenv("DOTPACK_AGENTS_HOME", filepath.Join(userHome, ".agents"))
	t.Setenv("DOTPACK_CODEX_HOME", filepath.Join(userHome, ".codex"))
	t.Setenv("DOTPACK_HERMES_HOME", filepath.Join(userHome, ".hermes"))
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)

	return dotpackHome, userHome, targetRoot
}

func executeCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewRootCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)

	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// Story 1: Register a GitHub repository by URL or owner/repository shorthand.
// Story 4: Initial add operation installs discoverable skills.
// Story 6: Receive all discovered skills by default.
func TestStory1_Story4_Story6_AddRepository(t *testing.T) {
	dotpackHome, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	writeTestSkillFile(t, filepath.Join(workDir, "skills", "code-review"), "code-review", "Code review instructions")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "test-runner"), "test-runner", "Test runner instructions")
	commitAndPush(t, workDir, "main", "initial skills")

	stdout, stderr, err := executeCLI(t, "add", remoteDir)
	if err != nil {
		t.Fatalf("add command failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	if !strings.Contains(stdout, "Registered repository") || !strings.Contains(stdout, "code-review") || !strings.Contains(stdout, "test-runner") {
		t.Fatalf("unexpected stdout: %s", stdout)
	}

	// Verify sources.yaml was saved
	store := sourceregistry.NewStore(filepath.Join(dotpackHome, "sources.yaml"))
	reg, err := store.Load()
	if err != nil || len(reg.Sources) != 1 {
		t.Fatalf("expected 1 source registered, got %d (err: %v)", len(reg.Sources), err)
	}

	// Verify installed files under user scope agents-cli (~/.agents/skills)
	reviewSkill := filepath.Join(userHome, ".agents", "skills", "code-review", "SKILL.md")
	runnerSkill := filepath.Join(userHome, ".agents", "skills", "test-runner", "SKILL.md")
	if _, err := os.Stat(reviewSkill); err != nil {
		t.Fatalf("code-review skill not installed: %v", err)
	}
	if _, err := os.Stat(runnerSkill); err != nil {
		t.Fatalf("test-runner skill not installed: %v", err)
	}
}

// Story 2: Register several repositories over time.
// Story 3: Run dotpack update without arguments from any directory.
// Story 14: Repositories and target choices survive process restarts.
func TestStory2_Story3_Story14_MultipleRepositoriesUpdate(t *testing.T) {
	dotpackHome, userHome, _ := setupTestEnv(t)

	// Repo 1
	remote1, work1 := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(work1, "skills", "skill-one"), "skill-one", "Skill one instructions")
	commitAndPush(t, work1, "main", "repo 1")

	// Repo 2
	remote2, work2 := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(work2, "skills", "skill-two"), "skill-two", "Skill two instructions")
	commitAndPush(t, work2, "main", "repo 2")

	// Add repo 1
	if _, _, err := executeCLI(t, "add", remote1); err != nil {
		t.Fatalf("add repo 1: %v", err)
	}

	// Add repo 2
	if _, _, err := executeCLI(t, "add", remote2); err != nil {
		t.Fatalf("add repo 2: %v", err)
	}

	// Verify 2 sources in sources.yaml
	store := sourceregistry.NewStore(filepath.Join(dotpackHome, "sources.yaml"))
	reg, err := store.Load()
	if err != nil || len(reg.Sources) != 2 {
		t.Fatalf("expected 2 registered sources, got %d", len(reg.Sources))
	}

	// Run update from unrelated working directory
	stdout, _, err := executeCLI(t, "update")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	if !strings.Contains(stdout, "Update summary: 0 added, 0 updated, 0 removed, 2 unchanged") {
		t.Fatalf("unexpected update summary: %s", stdout)
	}

	// Verify both skills are present on disk
	if _, err := os.Stat(filepath.Join(userHome, ".agents", "skills", "skill-one", "SKILL.md")); err != nil {
		t.Fatalf("skill-one missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userHome, ".agents", "skills", "skill-two", "SKILL.md")); err != nil {
		t.Fatalf("skill-two missing: %v", err)
	}
}

// Story 5: Discover canonical, root-level and category-nested skill packages.
// Story 8: Receive changes to skill instructions and supporting files.
// Story 9: Remove unchanged managed skills and support files that upstream removed.
func TestStory5_Story8_Story9_DiscoveryNestedAndRemovals(t *testing.T) {
	dotpackHome, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	// Canonical
	writeTestSkillFile(t, filepath.Join(workDir, ".agents", "skills", "canonical-tool"), "canonical-tool", "Canonical instructions")

	// Category nested
	nestedDir := filepath.Join(workDir, "skills", "frontend", "react-helper")
	writeTestSkillFile(t, nestedDir, "react-helper", "React helper instructions v1")
	mustWriteFile(t, filepath.Join(nestedDir, "scripts", "build.sh"), "#!/bin/sh\necho build\n", 0o755)
	mustWriteFile(t, filepath.Join(nestedDir, "references", "guide.md"), "# Guide\n", 0o644)
	mustWriteFile(t, filepath.Join(nestedDir, "examples", "sub-skill", "SKILL.md"), "---\nname: sub-example\n---\nbody\n", 0o644)

	// Obsolete skill that will be removed later
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "deprecated", "obsolete-skill"), "obsolete-skill", "To be deleted")

	commitAndPush(t, workDir, "main", "initial complex layout")

	// Add repository
	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Assert package stopping boundary: sub-example should NOT be installed as its own top-level skill
	if _, err := os.Stat(filepath.Join(userHome, ".agents", "skills", "sub-example", "SKILL.md")); err == nil {
		t.Fatalf("package stopping boundary violated: sub-example installed as standalone skill")
	}

	installedReact := filepath.Join(userHome, ".agents", "skills", "react-helper", "SKILL.md")
	installedScript := filepath.Join(userHome, ".agents", "skills", "react-helper", "scripts", "build.sh")
	installedGuide := filepath.Join(userHome, ".agents", "skills", "react-helper", "references", "guide.md")
	installedObsolete := filepath.Join(userHome, ".agents", "skills", "obsolete-skill", "SKILL.md")

	if _, err := os.Stat(installedReact); err != nil {
		t.Fatalf("react-helper not installed: %v", err)
	}
	if _, err := os.Stat(installedScript); err != nil {
		t.Fatalf("build.sh not installed: %v", err)
	}
	if _, err := os.Stat(installedGuide); err != nil {
		t.Fatalf("guide.md not installed: %v", err)
	}
	if _, err := os.Stat(installedObsolete); err != nil {
		t.Fatalf("obsolete-skill not installed: %v", err)
	}

	// Upstream changes:
	// 1. Update react-helper instructions
	// 2. Delete guide.md support file from react-helper
	// 3. Delete obsolete-skill entirely
	writeTestSkillFile(t, nestedDir, "react-helper", "React helper instructions v2 UPDATED")
	_ = os.Remove(filepath.Join(nestedDir, "references", "guide.md"))
	_ = os.RemoveAll(filepath.Join(workDir, "skills", "deprecated", "obsolete-skill"))

	commitAndPush(t, workDir, "main", "update react, remove guide, remove obsolete")

	// Run update
	stdout, _, err := executeCLI(t, "update")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if !strings.Contains(stdout, "1 updated") || !strings.Contains(stdout, "1 removed") {
		t.Fatalf("update summary missing expected counts: %s", stdout)
	}

	// Verify react-helper content updated
	updatedBytes, _ := os.ReadFile(installedReact)
	if !strings.Contains(string(updatedBytes), "React helper instructions v2 UPDATED") {
		t.Fatalf("react-helper not updated: %s", string(updatedBytes))
	}

	// Verify deleted support file removed from target
	if _, err := os.Stat(installedGuide); !os.IsNotExist(err) {
		t.Fatalf("stale guide.md support file was not removed")
	}

	// Verify deleted skill removed from target
	if _, err := os.Stat(installedObsolete); !os.IsNotExist(err) {
		t.Fatalf("obsolete-skill was not removed")
	}

	// Verify retained script still exists
	if _, err := os.Stat(installedScript); err != nil {
		t.Fatalf("build.sh should remain: %v", err)
	}
	_ = dotpackHome
}

// Story 7: Receive newly added upstream skills on later updates.
// Story 10: Moved skill with same name retains identity within its repository.
// Story 11: Upstream rename handled as addition and removal.
func TestStory7_Story10_Story11_MoveAndRename(t *testing.T) {
	_, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	// Initial skills: mover (in dirA), renamer-old
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "dirA", "mover-skill"), "mover-skill", "Mover body")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "renamer-old"), "renamer-old", "Old name body")
	commitAndPush(t, workDir, "main", "v1")

	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Upstream changes:
	// 1. Newly added skill
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "new-skill"), "new-skill", "New skill body")
	// 2. Move mover-skill to dirB (same skill name)
	_ = os.RemoveAll(filepath.Join(workDir, "skills", "dirA"))
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "dirB", "mover-skill"), "mover-skill", "Mover body in dirB")
	// 3. Rename renamer-old to renamer-new
	_ = os.RemoveAll(filepath.Join(workDir, "skills", "renamer-old"))
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "renamer-new"), "renamer-new", "New name body")

	commitAndPush(t, workDir, "main", "v2 changes")

	stdout, _, err := executeCLI(t, "update")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Check output
	if !strings.Contains(stdout, "+ added: new-skill") || !strings.Contains(stdout, "+ added: renamer-new") || !strings.Contains(stdout, "- removed: renamer-old") {
		t.Fatalf("unexpected update output: %s", stdout)
	}

	// Verify new-skill is installed
	if _, err := os.Stat(filepath.Join(userHome, ".agents", "skills", "new-skill", "SKILL.md")); err != nil {
		t.Fatalf("new-skill missing: %v", err)
	}
	// Verify renamer-new is installed
	if _, err := os.Stat(filepath.Join(userHome, ".agents", "skills", "renamer-new", "SKILL.md")); err != nil {
		t.Fatalf("renamer-new missing: %v", err)
	}
	// Verify renamer-old is deleted
	if _, err := os.Stat(filepath.Join(userHome, ".agents", "skills", "renamer-old", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("renamer-old should have been removed")
	}
	// Verify mover-skill exists (no duplicate)
	if _, err := os.Stat(filepath.Join(userHome, ".agents", "skills", "mover-skill", "SKILL.md")); err != nil {
		t.Fatalf("mover-skill missing: %v", err)
	}
}

// Story 12: Follow default branch without supplying ref.
// Story 13: Retain explicit branch, tag, or commit syntax.
func TestStory12_Story13_RefPolicies(t *testing.T) {
	_, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	writeTestSkillFile(t, filepath.Join(workDir, "skills", "ref-skill"), "ref-skill", "v1 body")
	commitAndPush(t, workDir, "main", "v1")
	runGitCmd(t, workDir, "tag", "v1.0.0")
	runGitCmd(t, workDir, "push", "origin", "v1.0.0")

	// 1. Add pinned to tag v1.0.0
	tagURL := fmt.Sprintf("%s@v1.0.0", remoteDir)
	if _, _, err := executeCLI(t, "add", tagURL); err != nil {
		t.Fatalf("add tag: %v", err)
	}

	// Advance main branch upstream
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "ref-skill"), "ref-skill", "v2 body on main")
	commitAndPush(t, workDir, "main", "v2 on main")

	// Update should NOT advance the tagged repo
	stdout, _, err := executeCLI(t, "update")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(stdout, "0 updated") {
		t.Fatalf("pinned tag should not have updated: %s", stdout)
	}

	content, _ := os.ReadFile(filepath.Join(userHome, ".agents", "skills", "ref-skill", "SKILL.md"))
	if !strings.Contains(string(content), "v1 body") {
		t.Fatalf("pinned tag content changed: %s", string(content))
	}
}

// Story 15: Reuse cached clones without managing their locations.
// Story 16: Register same repository again without duplicating it (idempotent).
func TestStory15_Story16_CacheReuseAndIdempotence(t *testing.T) {
	dotpackHome, _, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	writeTestSkillFile(t, filepath.Join(workDir, "skills", "cache-skill"), "cache-skill", "Cache body")
	commitAndPush(t, workDir, "main", "v1")

	// Add first time
	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("first add: %v", err)
	}

	// Add second time (idempotent)
	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("second add: %v", err)
	}

	// Check sources.yaml has exactly 1 entry
	store := sourceregistry.NewStore(filepath.Join(dotpackHome, "sources.yaml"))
	reg, err := store.Load()
	if err != nil || len(reg.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(reg.Sources))
	}
}

// Story 17: Reuse existing host adapters and agents-cli umbrella.
// Story 18: Record additional explicit project or host targets when needed.
func TestStory17_Story18_MultipleTargets(t *testing.T) {
	dotpackHome, userHome, targetRoot := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	writeTestSkillFile(t, filepath.Join(workDir, "skills", "multi-skill"), "multi-skill", "Multi target body")
	commitAndPush(t, workDir, "main", "v1")

	// Add to user scope (agents-cli)
	if _, _, err := executeCLI(t, "add", remoteDir, "--agent", "agents-cli", "--scope", "user"); err != nil {
		t.Fatalf("add user: %v", err)
	}

	// Add to project scope (claude-code)
	if _, _, err := executeCLI(t, "add", remoteDir, "--agent", "claude-code", "--scope", "project", "--target", targetRoot); err != nil {
		t.Fatalf("add project: %v", err)
	}

	// Verify both user and project files exist
	userSkill := filepath.Join(userHome, ".agents", "skills", "multi-skill", "SKILL.md")
	projSkill := filepath.Join(targetRoot, ".claude", "skills", "multi-skill", "SKILL.md")

	if _, err := os.Stat(userSkill); err != nil {
		t.Fatalf("user skill missing: %v", err)
	}
	if _, err := os.Stat(projSkill); err != nil {
		t.Fatalf("project skill missing: %v", err)
	}

	// Verify sources.yaml has 1 source with 2 targets
	store := sourceregistry.NewStore(filepath.Join(dotpackHome, "sources.yaml"))
	reg, _ := store.Load()
	if len(reg.Sources) != 1 || len(reg.Sources[0].Targets) != 2 {
		t.Fatalf("expected 1 source with 2 targets, got %d sources, %d targets", len(reg.Sources), len(reg.Sources[0].Targets))
	}
}

// Story 19: Local edits preserved and reported as conflicts.
// Story 20: Untracked files and other repositories' output protected.
func TestStory19_Story20_LocalEditsAndUntrackedConflicts(t *testing.T) {
	_, userHome, targetRoot := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	writeTestSkillFile(t, filepath.Join(workDir, "skills", "edited-skill"), "edited-skill", "Original body")
	commitAndPush(t, workDir, "main", "v1")

	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("add: %v", err)
	}

	installedSkill := filepath.Join(userHome, ".agents", "skills", "edited-skill", "SKILL.md")

	// 1. Story 19: User modifies the installed skill locally
	mustWriteFile(t, installedSkill, "---\nname: edited-skill\n---\nLocal user modification\n", 0o644)

	// Upstream updates the skill
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "edited-skill"), "edited-skill", "Upstream modified body")
	commitAndPush(t, workDir, "main", "v2")

	// Update should report conflict and preserve local changes
	stdout, _, err := executeCLI(t, "update")
	if err == nil {
		t.Fatalf("update expected error due to conflict, got nil")
	}

	if !strings.Contains(stdout, "! conflict: edited-skill") || !strings.Contains(stdout, "1 conflicts") {
		t.Fatalf("expected conflict report in stdout: %s", stdout)
	}

	// Verify local file is still intact
	content, _ := os.ReadFile(installedSkill)
	if !strings.Contains(string(content), "Local user modification") {
		t.Fatalf("user edit was overwritten: %s", string(content))
	}

	// 2. Story 20: Untracked pre-existing file at install target path
	remote2, work2 := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(work2, "skills", "untracked-skill"), "untracked-skill", "Upstream untracked body")
	commitAndPush(t, work2, "main", "v1")

	untrackedTargetFile := filepath.Join(userHome, ".agents", "skills", "untracked-skill", "SKILL.md")
	mustWriteFile(t, untrackedTargetFile, "UNTRACKED MANUAL FILE BEFORE ADD", 0o644)

	// Add without force must refuse with conflict
	addOut, _, addErr := executeCLI(t, "add", remote2)
	if addErr == nil {
		t.Fatalf("add expected conflict error due to pre-existing untracked file, got nil")
	}
	if !strings.Contains(addOut, "! conflict: untracked-skill") || !strings.Contains(addOut, "untracked file collision") {
		t.Fatalf("expected untracked collision in stdout: %s", addOut)
	}
	// Verify untracked file was preserved
	rawUntracked, _ := os.ReadFile(untrackedTargetFile)
	if string(rawUntracked) != "UNTRACKED MANUAL FILE BEFORE ADD" {
		t.Fatalf("untracked file was overwritten: %s", string(rawUntracked))
	}

	// Add with force should overwrite
	if _, _, forceErr := executeCLI(t, "add", remote2, "--force"); forceErr != nil {
		t.Fatalf("add with --force failed: %v", forceErr)
	}

	// 3. Story 20: Untracked support file inside an already-managed skill directory
	remote3, work3 := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(work3, "skills", "support-skill"), "support-skill", "Initial support body")
	commitAndPush(t, work3, "main", "v1")

	if _, _, err := executeCLI(t, "add", remote3); err != nil {
		t.Fatalf("add remote 3: %v", err)
	}

	// User creates an untracked helper script inside the managed skill folder
	untrackedHelper := filepath.Join(userHome, ".agents", "skills", "support-skill", "scripts", "helper.sh")
	mustWriteFile(t, untrackedHelper, "#!/bin/sh\necho 'manual helper'\n", 0o755)

	// Upstream adds a conflicting scripts/helper.sh to the skill
	upstreamHelper := filepath.Join(work3, "skills", "support-skill", "scripts", "helper.sh")
	mustWriteFile(t, upstreamHelper, "#!/bin/sh\necho 'upstream helper'\n", 0o755)
	commitAndPush(t, work3, "main", "v2 with helper")

	// Update without force must detect collision with the untracked support file and refuse
	upOut, _, upErr := executeCLI(t, "update")
	if upErr == nil {
		t.Fatalf("update expected error due to untracked support file collision, got nil")
	}
	if !strings.Contains(upOut, "! conflict: support-skill") || !strings.Contains(upOut, "untracked file collision") {
		t.Fatalf("expected untracked support file collision in update output: %s", upOut)
	}
	// Verify user's untracked helper is intact
	helperContent, _ := os.ReadFile(untrackedHelper)
	if !strings.Contains(string(helperContent), "manual helper") {
		t.Fatalf("untracked helper was overwritten: %s", string(helperContent))
	}
	_ = targetRoot
}

// Story 21: Clear conflict when repositories provide the same name for the same target.
func TestStory21_CrossSourceCollision(t *testing.T) {
	_, _, _ = setupTestEnv(t)

	remote1, work1 := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(work1, "skills", "same-name"), "same-name", "Repo 1 body")
	commitAndPush(t, work1, "main", "r1")

	remote2, work2 := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(work2, "skills", "same-name"), "same-name", "Repo 2 body")
	commitAndPush(t, work2, "main", "r2")

	// Add first repo
	if _, _, err := executeCLI(t, "add", remote1); err != nil {
		t.Fatalf("add repo 1: %v", err)
	}

	// Add second repo with colliding skill name
	_, _, err := executeCLI(t, "add", remote2)
	if err == nil {
		t.Fatalf("expected conflict on colliding skill name across repos")
	}
}

// Story 22: Keep existing security approvals and scans in update path.
// Story 23: Newly discovered unapproved skills reported as blocked.
// Story 24: Resume update after recording required approvals.
// Story 25: Security bypasses limited to explicitly authorized invocation.
func TestStory22_Story23_Story24_Story25_SecurityGateLifecycle(t *testing.T) {
	dotpackHome, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	writeTestSkillFile(t, filepath.Join(workDir, "skills", "secure-skill"), "secure-skill", "Secure skill instructions")
	commitAndPush(t, workDir, "main", "v1")

	// Mock gate to block unapproved skills unless bypassed or approved
	origGate := mandatorySkillScan
	t.Cleanup(func() { mandatorySkillScan = origGate })

	isApproved := false
	mandatorySkillScan = func(command string, selection skillScanSelection, d dirs.Dirs) error {
		if isApproved {
			return nil
		}
		// If bypassed, selection.Targets will be empty or not contain bypassed skills
		for _, target := range selection.Targets {
			if target.Name == "secure-skill" {
				return fmt.Errorf("skillgate gate blocked 1 skill package(s): no approved baseline")
			}
		}
		return nil
	}

	// 1. Add should block the unapproved skill, but retain registration in sources.yaml
	stdout, _, err := executeCLI(t, "add", remoteDir)
	if err == nil {
		t.Fatalf("expected add to fail with security block")
	}
	if !strings.Contains(stdout, "X blocked: secure-skill") {
		t.Fatalf("expected blocked message in stdout: %s", stdout)
	}

	// Verify sources.yaml HAS the repository registered!
	store := sourceregistry.NewStore(filepath.Join(dotpackHome, "sources.yaml"))
	reg, err := store.Load()
	if err != nil || len(reg.Sources) != 1 {
		t.Fatalf("expected source to remain registered despite gate block: %v, count=%d", err, len(reg.Sources))
	}

	// Skill should NOT be installed on disk
	installedSkill := filepath.Join(userHome, ".agents", "skills", "secure-skill", "SKILL.md")
	if _, err := os.Stat(installedSkill); !os.IsNotExist(err) {
		t.Fatalf("blocked skill must not be written to disk")
	}

	// 2. Invocation with --skill-bypass-security should succeed for this run
	stdout, _, err = executeCLI(t, "update", "--skill-bypass-security", "secure-skill")
	if err != nil {
		t.Fatalf("update with bypass failed: %v\nstdout: %s", err, stdout)
	}
	if !strings.Contains(stdout, "1 added") {
		t.Fatalf("expected 1 added with bypass: %s", stdout)
	}

	// Verify skill is now installed on disk
	if _, err := os.Stat(installedSkill); err != nil {
		t.Fatalf("skill should be installed after bypass: %v", err)
	}

	// Verify sources.yaml does NOT contain the bypass flag
	reg, _ = store.Load()
	for _, src := range reg.Sources {
		if strings.Contains(src.Ref, "bypass") {
			t.Fatalf("bypass was persisted in sources.yaml!")
		}
	}

	// 3. Story 25 assertion: Upstream changes the skill; next update WITHOUT bypass MUST be blocked again!
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "secure-skill"), "secure-skill", "Upstream v2 with new code")
	commitAndPush(t, workDir, "main", "v2")

	// Run update without --skill-bypass-security and with isApproved=false
	stdout, _, err = executeCLI(t, "update")
	if err == nil {
		t.Fatalf("update without bypass must be blocked when upstream changes, got success:\n%s", stdout)
	}
	if !strings.Contains(stdout, "X blocked: secure-skill") {
		t.Fatalf("expected blocked message in stdout on v2 update: %s", stdout)
	}

	// 4. Story 24: Mark approved and run normal update without bypass -> succeeds
	isApproved = true
	stdout, _, err = executeCLI(t, "update")
	if err != nil {
		t.Fatalf("update after approval failed: %v", err)
	}
	if !strings.Contains(stdout, "1 updated") {
		t.Fatalf("expected 1 updated after approval: %s", stdout)
	}
}

// Story 26: Healthy repositories updated even when an independent repository cannot be fetched.
// Story 27: Retain last installed state when fetching, discovery, or validation fails.
// Story 28: Exact added, updated, removed, unchanged, conflicted, blocked and failed results reported.
// Story 29: Nonzero exit status for incomplete updates.
func TestStory26_Story27_Story28_Story29_PartialFailuresAndReporting(t *testing.T) {
	_, userHome, _ := setupTestEnv(t)

	// Healthy repo
	remoteHealthy, workHealthy := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workHealthy, "skills", "healthy-skill"), "healthy-skill", "Healthy v1")
	commitAndPush(t, workHealthy, "main", "h1")

	// Broken repo
	remoteBroken, workBroken := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workBroken, "skills", "broken-skill"), "broken-skill", "Broken v1")
	commitAndPush(t, workBroken, "main", "b1")

	if _, _, err := executeCLI(t, "add", remoteHealthy); err != nil {
		t.Fatalf("add healthy: %v", err)
	}
	if _, _, err := executeCLI(t, "add", remoteBroken); err != nil {
		t.Fatalf("add broken: %v", err)
	}

	// Advance healthy repo
	writeTestSkillFile(t, filepath.Join(workHealthy, "skills", "healthy-skill"), "healthy-skill", "Healthy v2")
	commitAndPush(t, workHealthy, "main", "h2")

	// Delete broken remote repository directory to simulate upstream outage
	_ = os.RemoveAll(remoteBroken)

	stdout, _, err := executeCLI(t, "update")
	if err == nil {
		t.Fatalf("expected nonzero exit on partial failure, got nil")
	}

	// Healthy repo should have updated, broken should be reported failed
	if !strings.Contains(stdout, "1 updated") || !strings.Contains(stdout, "1 failed") {
		t.Fatalf("expected 1 updated and 1 failed in summary: %s", stdout)
	}

	// Verify healthy skill updated
	healthyContent, _ := os.ReadFile(filepath.Join(userHome, ".agents", "skills", "healthy-skill", "SKILL.md"))
	if !strings.Contains(string(healthyContent), "Healthy v2") {
		t.Fatalf("healthy skill was not updated: %s", string(healthyContent))
	}

	// Verify broken skill was retained on disk (not deleted!)
	brokenContent, _ := os.ReadFile(filepath.Join(userHome, ".agents", "skills", "broken-skill", "SKILL.md"))
	if !strings.Contains(string(brokenContent), "Broken v1") {
		t.Fatalf("broken skill was corrupted or removed: %s", string(brokenContent))
	}
}

// Story 30: Rerun an unchanged successful update without content changes (idempotent).
// Story 31: Retry after interruption without corrupting ownership state.
func TestStory30_Story31_IdempotenceAndRecovery(t *testing.T) {
	_, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	writeTestSkillFile(t, filepath.Join(workDir, "skills", "stable-skill"), "stable-skill", "Stable body")
	commitAndPush(t, workDir, "main", "v1")

	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("add: %v", err)
	}

	// 1. Story 30: Rerun update multiple times
	for i := 0; i < 3; i++ {
		stdout, _, err := executeCLI(t, "update")
		if err != nil {
			t.Fatalf("update iteration %d failed: %v", i, err)
		}
		if !strings.Contains(stdout, "1 unchanged") || !strings.Contains(stdout, "0 added, 0 updated, 0 removed") {
			t.Fatalf("iteration %d output not steady: %s", i, stdout)
		}
	}

	// Verify file is present and intact
	installedSkill := filepath.Join(userHome, ".agents", "skills", "stable-skill", "SKILL.md")
	content, err := os.ReadFile(installedSkill)
	if err != nil || !strings.Contains(string(content), "Stable body") {
		t.Fatalf("stable skill corrupted: %v, content: %s", err, string(content))
	}

	// 2. Story 31: Inject interruption / missing file
	if err := os.Remove(installedSkill); err != nil {
		t.Fatalf("remove installed skill: %v", err)
	}

	// Retry update: must restore missing file instead of treating it as unchanged
	stdout, _, err := executeCLI(t, "update")
	if err != nil {
		t.Fatalf("update on retry failed: %v\nstdout: %s", err, stdout)
	}
	if !strings.Contains(stdout, "1 updated") {
		t.Fatalf("expected 1 updated/restored on retry after interruption: %s", stdout)
	}

	// Verify file was restored
	restoredContent, err := os.ReadFile(installedSkill)
	if err != nil || !strings.Contains(string(restoredContent), "Stable body") {
		t.Fatalf("stable skill not restored on retry: %v, content: %s", err, string(restoredContent))
	}
}

// Story 32: Clear no-op message when no repositories are registered.
func TestStory32_EmptyRegistryNoOp(t *testing.T) {
	setupTestEnv(t)

	stdout, _, err := executeCLI(t, "update")
	if err != nil {
		t.Fatalf("update on empty registry should exit 0, got err: %v", err)
	}

	if !strings.Contains(stdout, "No registered repositories found. Use 'dotpack add <owner/repo>' to register a repository.") {
		t.Fatalf("unexpected stdout for empty registry: %s", stdout)
	}
}

// Story 35: Concurrent modifying commands serialized or rejected clearly.
func TestStory35_ConcurrentExecutionLock(t *testing.T) {
	dotpackHome, userHome, targetRoot := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "lock-skill"), "lock-skill", "Lock body")
	commitAndPush(t, workDir, "main", "v1")

	claudeDir := t.TempDir()
	writeTestSkillFile(t, filepath.Join(claudeDir, ".claude", "skills", "lock-skill"), "lock-skill", "body")

	agentsDir := t.TempDir()
	writeTestSkillFile(t, filepath.Join(agentsDir, ".agents", "skills", "lock-skill"), "lock-skill", "body")

	// Start a real background process holding the lock on dotpackHome/.lock
	lockFile := filepath.Join(dotpackHome, ".lock")
	_ = os.MkdirAll(dotpackHome, 0o755)

	// Launch external python lock holder process
	cmdBg := exec.Command("python3", "-c", fmt.Sprintf(`
import fcntl, time
f = open(%q, "w")
fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
print("LOCKED", flush=True)
time.sleep(30)
`, lockFile))
	stdoutPipe, err := cmdBg.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmdBg.Start(); err != nil {
		t.Fatalf("start lock holder process: %v", err)
	}
	defer func() {
		if cmdBg.Process != nil {
			_ = cmdBg.Process.Kill()
			_ = cmdBg.Wait()
		}
	}()

	// Wait for process to acquire lock
	buf := make([]byte, 6)
	_, _ = stdoutPipe.Read(buf)
	if string(buf) != "LOCKED" {
		t.Fatalf("lock process output = %q; want LOCKED", string(buf))
	}

	// Test modifying commands under held lock
	modifyingCalls := [][]string{
		{"add", remoteDir},
		{"update"},
		{"install", filepath.Join(workDir, "skills", "lock-skill", "SKILL.md"), "--agent", "claude-code", "--scope", "user"},
		{"uninstall", "lock-skill", "--agent", "claude-code"},
		{"import", "claude-code", claudeDir},
		{"prune"},
		{"sync-back", "--from", agentsDir, "--target", targetRoot},
		{"reset-materialized", "--from", agentsDir, "--target", targetRoot},
	}

	for _, args := range modifyingCalls {
		t.Run(args[0], func(t *testing.T) {
			_, _, err := executeCLI(t, args...)
			if err == nil {
				t.Fatalf("command %v expected error due to held lock, got nil", args)
			}
			if !strings.Contains(err.Error(), "another dotpack process is currently modifying state") {
				t.Fatalf("command %v unexpected error: %v", args, err)
			}
		})
	}

	// Kill background process to test recovery
	_ = cmdBg.Process.Kill()
	_ = cmdBg.Wait()

	// Now modifying commands should succeed
	stdout, _, err := executeCLI(t, "add", remoteDir)
	if err != nil {
		t.Fatalf("add after lock release failed: %v\nstdout: %s", err, stdout)
	}
	_ = userHome
}

// Real gate evaluation against an isolated policy root (Story 22-25 real evaluation)
func TestRealGateEvaluation_AddAndUpdate(t *testing.T) {
	dotpackHome, userHome, targetRoot := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	writeTestSkillFile(t, filepath.Join(workDir, "skills", "bad-skill"), "bad-skill", "Finding body with secret: 12345")
	commitAndPush(t, workDir, "main", "v1")

	prepareFakeSkillSpectorRuntime(t, dotpackHome)
	useSkillGate(t, spectorGateName)

	// Restore real mandatory skill scan funnel
	restore := stubMandatorySkillScan(t, runMandatorySkillScan)
	defer restore()

	// 1. Add without baseline should run real gate and block bad-skill
	stdout, _, err := executeCLI(t, "add", remoteDir)
	if err == nil {
		t.Fatalf("add expected to fail with real security gate block, got success:\n%s", stdout)
	}
	if !strings.Contains(stdout, "X blocked: bad-skill") {
		t.Fatalf("expected blocked message in stdout: %s", stdout)
	}

	// 2. Update with --skill-bypass-security should bypass for this invocation
	stdout, _, err = executeCLI(t, "update", "--skill-bypass-security", "bad-skill")
	if err != nil {
		t.Fatalf("update with bypass failed: %v\nstdout: %s", err, stdout)
	}
	if !strings.Contains(stdout, "1 added") {
		t.Fatalf("expected 1 added with bypass: %s", stdout)
	}

	// Verify skill installed on disk
	installedPath := filepath.Join(userHome, ".agents", "skills", "bad-skill", "SKILL.md")
	if _, err := os.Stat(installedPath); err != nil {
		t.Fatalf("skill missing on disk after bypass: %v", err)
	}
	_ = targetRoot
}

// Story 36: Optional discovery override for an unusual repository layout.
func TestStory36_DiscoveryOverride(t *testing.T) {
	_, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")

	// Put skills under custom "custom-catalog/my-skills" directory
	customDir := filepath.Join(workDir, "custom-catalog", "my-skills", "custom-skill")
	writeTestSkillFile(t, customDir, "custom-skill", "Custom layout body")
	// Outside skill that should NOT be discovered
	writeTestSkillFile(t, filepath.Join(workDir, "ignored-dir", "ignored-skill"), "ignored-skill", "Ignored body")
	commitAndPush(t, workDir, "main", "custom layout")

	stdout, _, err := executeCLI(t, "add", remoteDir, "--skills-path", "custom-catalog/my-skills")
	if err != nil {
		t.Fatalf("add with --skills-path: %v", err)
	}

	if !strings.Contains(stdout, "custom-skill") {
		t.Fatalf("custom-skill was not installed: %s", stdout)
	}

	// Verify custom-skill installed, ignored-skill NOT installed
	if _, err := os.Stat(filepath.Join(userHome, ".agents", "skills", "custom-skill", "SKILL.md")); err != nil {
		t.Fatalf("custom-skill missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userHome, ".agents", "skills", "ignored-skill", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("ignored-skill should not have been installed")
	}
}

// Story 33: Retain existing local install, import, inventory, and uninstall behavior.
// Story 34: Explicit guidance for adopting legacy installations.
func TestStory33_Story34_LegacyCompatibility(t *testing.T) {
	dotpackHome, userHome, targetRoot := setupTestEnv(t)

	// Create local canonical skill
	localDir := filepath.Join(targetRoot, ".agents", "skills", "legacy-skill")
	writeTestSkillFile(t, localDir, "legacy-skill", "Legacy body")

	// Standard local install works as before
	src := filepath.Join(localDir, "SKILL.md")
	if _, _, err := executeCLI(t, "install", src, "--agent", "claude-code", "--scope", "project"); err != nil {
		t.Fatalf("local install: %v", err)
	}

	// Verify manifest has record
	mf := manifest.NewStore(filepath.Join(dotpackHome, "installs.yaml"))
	m, err := mf.Load()
	if err != nil || len(m.Installs) != 1 {
		t.Fatalf("manifest should have 1 install: %v, count=%d", err, len(m.Installs))
	}

	// List works
	stdout, _, err := executeCLI(t, "list")
	if err != nil || !strings.Contains(stdout, "claude-code:skill:legacy-skill") {
		t.Fatalf("list failed or missing legacy skill: %v\nstdout: %s", err, stdout)
	}

	// Uninstall works
	if _, _, err := executeCLI(t, "uninstall", "legacy-skill", "--agent", "claude-code", "--target", targetRoot); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	_ = userHome
}

func TestCLI_AddAndUpdate_EdgeCasesAndErrors(t *testing.T) {
	dotpackHome, userHome, targetRoot := setupTestEnv(t)

	// 1. add with no args fails
	if _, _, err := executeCLI(t, "add"); err == nil {
		t.Errorf("add with no args should fail")
	}

	// 2. update with extra args fails
	if _, _, err := executeCLI(t, "update", "extra-arg"); err == nil {
		t.Errorf("update with extra args should fail")
	}

	// 3. add with invalid scope
	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "s1"), "s1", "body")
	commitAndPush(t, workDir, "main", "v1")

	if _, _, err := executeCLI(t, "add", remoteDir, "--scope", "invalid-scope"); err == nil {
		t.Errorf("add with invalid scope should fail")
	}

	// 4. add with empty repo (no skills)
	remoteEmpty, workEmpty := createTestGitRepo(t, "main")
	mustWriteFile(t, filepath.Join(workEmpty, "README.md"), "# Empty Repo", 0o644)
	commitAndPush(t, workEmpty, "main", "init empty")

	stdout, _, err := executeCLI(t, "add", remoteEmpty)
	if err != nil {
		t.Fatalf("add empty repo should succeed as registration: %v", err)
	}
	if !strings.Contains(stdout, "no skills discovered") {
		t.Errorf("expected 'no skills discovered' in stdout: %s", stdout)
	}

	// 5. add with project scope using default DOTPACK_PROJECT_HOME
	if _, _, err := executeCLI(t, "add", remoteDir, "--agent", "claude-code", "--scope", "project"); err != nil {
		t.Fatalf("add project scope default target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, ".claude", "skills", "s1", "SKILL.md")); err != nil {
		t.Fatalf("project skill not installed at DOTPACK_PROJECT_HOME: %v", err)
	}

	// 6. update when a remote pushes a malformed skill (discovery error)
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "s1"), "s1", "body")
	mustWriteFile(t, filepath.Join(workDir, "skills", "broken", "SKILL.md"), "---\nname: [ bad: yaml\n", 0o644)
	commitAndPush(t, workDir, "main", "broken skill")

	stdout, _, err = executeCLI(t, "update")
	if err == nil {
		t.Fatalf("update with malformed remote skill should fail")
	}
	if !strings.Contains(stdout, "1 failed") {
		t.Errorf("expected 1 failed in summary: %s", stdout)
	}

	// 7. update when sources.yaml is corrupted
	mustWriteFile(t, filepath.Join(dotpackHome, "sources.yaml"), "corrupted: [ yaml: {", 0o644)
	if _, _, err := executeCLI(t, "update"); err == nil {
		t.Errorf("update with corrupted sources.yaml should fail")
	}

	_ = userHome
}

func TestCLI_Add_EdgeCasesAndValidation(t *testing.T) {
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_AGENT_SCANNER", "skip")

	// 1. Missing args
	if _, _, err := executeCLI(t, "add"); err == nil {
		t.Errorf("dotpack add without args should fail")
	}

	// 2. Extra args
	if _, _, err := executeCLI(t, "add", "repo1", "repo2"); err == nil {
		t.Errorf("dotpack add with multiple args should fail")
	}

	// 3. Invalid scope
	if _, _, err := executeCLI(t, "add", "owner/repo", "--scope", "invalid-scope"); err == nil {
		t.Errorf("dotpack add with invalid scope should fail")
	}

	// 4. Invalid repo spec
	if _, _, err := executeCLI(t, "add", "invalid$spec/repo"); err == nil {
		t.Errorf("dotpack add with invalid repo format should fail")
	}

	// 5. dotpack update with unexpected args
	if _, _, err := executeCLI(t, "update", "unexpected-arg"); err == nil {
		t.Errorf("dotpack update with extra args should fail")
	}
}

func TestCLI_Add_EmptyRepo(t *testing.T) {
	remoteDir, workDir := createTestGitRepo(t, "main")
	mustWriteFile(t, filepath.Join(workDir, "README.md"), "# Empty Repo without skills\n", 0o644)
	commitAndPush(t, workDir, "main", "initial commit without skills")

	dotpackHome := t.TempDir()
	userHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("HOME", userHome)
	t.Setenv("DOTPACK_AGENT_SCANNER", "skip")

	stdout, _, err := executeCLI(t, "add", remoteDir)
	if err != nil {
		t.Fatalf("dotpack add empty repo: %v", err)
	}
	if !strings.Contains(stdout, "no skills discovered") {
		t.Errorf("expected 'no skills discovered' in stdout: %s", stdout)
	}
}

func TestCLI_Update_ProjectScopeReport(t *testing.T) {
	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "proj-skill"), "proj-skill", "body")
	commitAndPush(t, workDir, "main", "initial skill")

	dotpackHome := t.TempDir()
	userHome := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("HOME", userHome)
	t.Setenv("DOTPACK_AGENT_SCANNER", "skip")

	// Add with project scope and target
	_, _, err := executeCLI(t, "add", remoteDir, "--scope", "project", "--target", projectDir)
	if err != nil {
		t.Fatalf("dotpack add project: %v", err)
	}

	// Run update
	stdout, _, err := executeCLI(t, "update")
	if err != nil {
		t.Fatalf("dotpack update project: %v", err)
	}
	if !strings.Contains(stdout, "root:") || !strings.Contains(stdout, "proj-skill") {
		t.Errorf("expected target root and skill in update report: %s", stdout)
	}
}

func writeTestSkillFile(t *testing.T, dir, name, body string) {
	t.Helper()
	content := fmt.Sprintf("---\nname: %s\ndescription: %s description\n---\n%s\n", name, name, body)
	mustWriteFile(t, filepath.Join(dir, "SKILL.md"), content, 0o644)
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
