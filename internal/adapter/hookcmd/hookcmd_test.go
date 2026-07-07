package hookcmd

import "testing"

func TestForHostRewritesClaudeProjectDirPlaceholders(t *testing.T) {
	got := ForHost("cd $CLAUDE_PROJECT_DIR && test -f ${CLAUDE_PROJECT_DIR}/README.md", ProjectRootExpr)
	want := "cd " + ProjectRootExpr + " && test -f " + ProjectRootExpr + "/README.md"
	if got != want {
		t.Fatalf("ForHost() = %q; want %q", got, want)
	}
}
