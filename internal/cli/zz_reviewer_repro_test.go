package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/dirs"
)

// R1 (Story 31): partially-applied/interrupted install must be reconciled on retry.
func TestRepro_Story31_DeletedOutputCountedUnchanged(t *testing.T) {
	_, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "stable-skill"), "stable-skill", "Stable body")
	commitAndPush(t, workDir, "main", "v1")
	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("add: %v", err)
	}
	installed := filepath.Join(userHome, ".agents", "skills", "stable-skill", "SKILL.md")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	// Simulate an interrupted write: managed output file is gone.
	if err := os.Remove(installed); err != nil {
		t.Fatalf("remove: %v", err)
	}
	stdout, _, err := executeCLI(t, "update")
	t.Logf("update stdout:\n%s\nerr=%v", stdout, err)
	if _, statErr := os.Stat(installed); statErr != nil {
		t.Errorf("REPRO Story31: managed file NOT restored by retry: %v; stdout=%s", statErr, stdout)
	}
}

// R2 (Story 22/25): reinstall after uninstall must still pass the security gate.
func TestRepro_Story22_GateSkippedOnReinstall(t *testing.T) {
	_, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "gated-skill"), "gated-skill", "body")
	commitAndPush(t, workDir, "main", "v1")

	orig := mandatorySkillScan
	t.Cleanup(func() { mandatorySkillScan = orig })
	blocking := false
	gateCalls := 0
	mandatorySkillScan = func(command string, sel skillScanSelection, d dirs.Dirs) error {
		gateCalls++
		if blocking {
			return fmt.Errorf("skillgate gate blocked 1 skill package(s): no approved baseline")
		}
		return nil
	}

	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Operator uninstalls the skill (subscription remains).
	if _, _, err := executeCLI(t, "uninstall", "gated-skill", "--agent", "agents-cli"); err != nil {
		t.Logf("uninstall err (non-fatal): %v", err)
	}
	installed := filepath.Join(userHome, ".agents", "skills", "gated-skill", "SKILL.md")
	_ = os.RemoveAll(filepath.Dir(installed))

	// Operator revokes approval; the next update must NOT silently reinstall.
	blocking = true
	before := gateCalls
	stdout, _, err := executeCLI(t, "update")
	t.Logf("update stdout:\n%s\nerr=%v gateCallsDuringUpdate=%d", stdout, err, gateCalls-before)
	if gateCalls == before {
		t.Errorf("REPRO Story22: security gate NEVER consulted while reinstalling gated-skill")
	}
	if _, statErr := os.Stat(installed); statErr == nil {
		t.Errorf("REPRO Story22: unapproved skill was (re)installed at %s despite blocking gate", installed)
	}
}

// R3 (Story 34): a pre-existing unrelated local install must not be silently adopted.
func TestRepro_Story34_LegacyAdoptionWithoutProvenance(t *testing.T) {
	_, userHome, targetRoot := setupTestEnv(t)

	local := filepath.Join(targetRoot, "canonical", "shared-skill")
	writeTestSkillFile(t, local, "shared-skill", "LOCAL HAND WRITTEN BODY")
	if _, _, err := executeCLI(t, "install", filepath.Join(local, "SKILL.md"), "--agent", "agents-cli", "--scope", "user"); err != nil {
		t.Fatalf("local install: %v", err)
	}
	installed := filepath.Join(userHome, ".agents", "skills", "shared-skill", "SKILL.md")
	pre, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("precondition read: %v", err)
	}
	if !strings.Contains(string(pre), "LOCAL HAND WRITTEN BODY") {
		t.Fatalf("precondition body: %s", pre)
	}

	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "shared-skill"), "shared-skill", "UPSTREAM BODY")
	commitAndPush(t, workDir, "main", "v1")

	stdout, _, addErr := executeCLI(t, "add", remoteDir)
	post, _ := os.ReadFile(installed)
	t.Logf("add stdout:\n%s\nerr=%v\npost body: %s", stdout, addErr, string(post))
	if strings.Contains(string(post), "UPSTREAM BODY") {
		t.Errorf("REPRO Story34: unrelated local install silently adopted+overwritten with no provenance match (add err=%v)", addErr)
	}
}

// R4 (Story 9/28): a removal that cannot be performed must not be reported as removed.
func TestRepro_Story9_RemovalErrorReportedAsSuccess(t *testing.T) {
	_, userHome, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "doomed"), "doomed", "body")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "keeper"), "keeper", "body")
	commitAndPush(t, workDir, "main", "v1")
	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("add: %v", err)
	}
	doomed := filepath.Join(userHome, ".agents", "skills", "doomed")
	if _, err := os.Stat(filepath.Join(doomed, "SKILL.md")); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	// Upstream deletes the skill.
	if err := os.RemoveAll(filepath.Join(workDir, "skills", "doomed")); err != nil {
		t.Fatalf("rm upstream: %v", err)
	}
	commitAndPush(t, workDir, "main", "drop doomed")

	// Make the deletion impossible: parent directory is not writable.
	if err := os.Chmod(doomed, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(doomed, 0o755) })

	stdout, _, err := executeCLI(t, "update")
	t.Logf("update stdout:\n%s\nerr=%v", stdout, err)
	_, statErr := os.Stat(filepath.Join(doomed, "SKILL.md"))
	fileStillThere := statErr == nil
	if fileStillThere && strings.Contains(stdout, "- removed: doomed") {
		t.Errorf("REPRO Story9: reported '- removed: doomed' but file still exists at %s (os.Remove error swallowed)", doomed)
	}
	if fileStillThere && err == nil {
		t.Errorf("REPRO Story9/29: exit status zero despite failed removal")
	}
}

// R5 (Story 13/27): an unresolvable explicit ref must fail, not fall back to a stale FETCH_HEAD.
func TestRepro_Story13_MissingRefFallsBackSilently(t *testing.T) {
	_, _, _ = setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "ref-skill"), "ref-skill", "v1 body")
	commitAndPush(t, workDir, "main", "v1")

	if _, _, err := executeCLI(t, "add", remoteDir); err != nil {
		t.Fatalf("add default: %v", err)
	}
	// Same repository, now requested at a ref that does not exist.
	stdout, _, err := executeCLI(t, "add", remoteDir+"@does-not-exist-ref")
	t.Logf("add bogus ref stdout:\n%s\nerr=%v", stdout, err)
	if err == nil {
		t.Errorf("REPRO Story13: add with unresolvable ref succeeded (silent fallback to cached FETCH_HEAD/default branch): %s", stdout)
	}
}

// R6 (Story 27): a run in which nothing was applied must not record the candidate as applied.
func TestRepro_Story27_AppliedCommitAdvancesOnBlocked(t *testing.T) {
	dotpackHome, _, _ := setupTestEnv(t)
	remoteDir, workDir := createTestGitRepo(t, "main")
	writeTestSkillFile(t, filepath.Join(workDir, "skills", "blocked-skill"), "blocked-skill", "body")
	commitAndPush(t, workDir, "main", "v1")

	orig := mandatorySkillScan
	t.Cleanup(func() { mandatorySkillScan = orig })
	mandatorySkillScan = func(string, skillScanSelection, dirs.Dirs) error {
		return fmt.Errorf("skillgate gate blocked 1 skill package(s): no approved baseline")
	}

	stdout, _, err := executeCLI(t, "add", remoteDir)
	if err == nil {
		t.Fatalf("precondition: add should have been blocked: %s", stdout)
	}
	raw, readErr := os.ReadFile(filepath.Join(dotpackHome, "sources.yaml"))
	if readErr != nil {
		t.Fatalf("read sources.yaml: %v", readErr)
	}
	t.Logf("sources.yaml after fully blocked add:\n%s", string(raw))
	if strings.Contains(string(raw), "applied_commit:") {
		t.Errorf("REPRO Story27: target applied_commit recorded although zero skills were applied (all blocked)")
	}
}
