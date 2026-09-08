package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Story 21: Clear conflict when multiple repositories provide the same skill name for the same target.
// Asserts that the second add reports a repository-attributed conflict identifying the owning repository,
// produces a nonzero exit, and preserves the installed bytes from repository one unchanged.
func TestStory21_CrossSourceCollision_RepositoryAttribution(t *testing.T) {
	_, userHome, _ := setupTestEnv(t)

	// Repository 1 fixture
	remote1, work1 := createTestGitRepo(t, "main")
	repo1ID := fmt.Sprintf("%s/%s", filepath.Base(filepath.Dir(remote1)), filepath.Base(remote1))
	writeTestSkillFile(t, filepath.Join(work1, "skills", "same-name"), "same-name", "Repo 1 body")
	commitAndPush(t, work1, "main", "initial skill from repo 1")

	// Repository 2 fixture
	remote2, work2 := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(work2, "skills", "same-name"), "same-name", "Repo 2 body")
	commitAndPush(t, work2, "main", "conflicting skill from repo 2")

	// Register repository 1: must succeed and install repo 1's skill
	stdout1, stderr1, err1 := executeCLI(t, "add", remote1)
	if err1 != nil {
		t.Fatalf("add repository 1 failed: %v\nstdout: %s\nstderr: %s", err1, stdout1, stderr1)
	}
	if !strings.Contains(stdout1, "+ added: same-name") {
		t.Fatalf("expected repo 1 to add same-name, stdout: %s", stdout1)
	}

	skillPath := filepath.Join(userHome, ".agents", "skills", "same-name", "SKILL.md")
	contentBefore, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read installed skill after repo 1 add: %v", err)
	}
	if !strings.Contains(string(contentBefore), "Repo 1 body") {
		t.Fatalf("expected installed skill to contain 'Repo 1 body', got: %s", string(contentBefore))
	}

	// Register repository 2 providing the same skill name for the same target:
	// must fail with specific repository-attributed conflict output.
	stdout2, stderr2, err2 := executeCLI(t, "add", remote2)
	if err2 == nil {
		t.Fatalf("expected add repository 2 to fail with conflict, but succeeded\nstdout: %s\nstderr: %s", stdout2, stderr2)
	}

	// Assert the specific repository-attributed conflict line:
	// "  ! conflict: same-name (collides with skill from repository "<repo1ID>")"
	expectedConflictLine := fmt.Sprintf("! conflict: same-name (collides with skill from repository %q)", repo1ID)
	if !strings.Contains(stdout2, expectedConflictLine) {
		t.Fatalf("expected repository-attributed conflict output %q in stdout, got:\n%s", expectedConflictLine, stdout2)
	}

	// Ensure the failure is not masked by generic conflict or legacy-adoption errors
	if strings.Contains(stdout2, "collides with existing installation") {
		t.Fatalf("found legacy-adoption error instead of repository-attributed conflict: %s", stdout2)
	}
	if !strings.Contains(err2.Error(), "1 skill(s) had conflicts") {
		t.Fatalf("expected conflict summary in error message, got: %v", err2)
	}

	// Assert unchanged bytes from repository 1
	contentAfter, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read installed skill after colliding repo 2 add: %v", err)
	}
	if string(contentAfter) != string(contentBefore) {
		t.Fatalf("installed skill bytes changed after colliding add!\nbefore: %s\nafter: %s", string(contentBefore), string(contentAfter))
	}
	if !strings.Contains(string(contentAfter), "Repo 1 body") || strings.Contains(string(contentAfter), "Repo 2 body") {
		t.Fatalf("installed skill does not contain repo 1 body or was overwritten by repo 2: %s", string(contentAfter))
	}
}
