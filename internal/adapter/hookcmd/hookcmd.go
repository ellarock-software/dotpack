// Package hookcmd contains host-specific command rewrites for hook
// resources. Hook commands are otherwise preserved verbatim by the
// adapters; this package only handles cross-host project-root placeholders.
package hookcmd

import "strings"

// ProjectRootExpr is a shell expression that resolves to the current repo
// root without depending on a Claude-only runtime variable. Codex's official
// hook docs recommend this pattern for repo-local hooks because commands run
// with the session cwd and Codex may start from a subdirectory.
const ProjectRootExpr = "$(git rev-parse --show-toplevel)"

// ForHost rewrites Claude-specific project directory placeholders in a hook
// command into the host expression expected by the destination adapter.
func ForHost(command, projectRootExpr string) string {
	replacer := strings.NewReplacer(
		"${CLAUDE_PROJECT_DIR}", projectRootExpr,
		"$CLAUDE_PROJECT_DIR", projectRootExpr,
	)
	return replacer.Replace(command)
}
