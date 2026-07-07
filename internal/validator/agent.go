package validator

import (
	"fmt"
	"strings"

	"github.com/ellarock-software/dotpack/internal/resource"
)

// ValidateAgent checks the universal-core invariants for an Agent:
// name required + slug-shaped (same regex as skill — schema/agent.yaml's
// note: "Lowercase letters, digits, hyphens (Gemini spec also allows
// underscores; Claude convention is hyphens only)"), description
// required, body required.
//
// model and tools are pass-through universal-core fields with no shape
// constraints worth gating install on — model strings vary (short alias
// like "sonnet" or full ID "claude-sonnet-4-20250514"), and tools is
// normalised to []string at parse time so any non-string-shaped input
// already errored before we got here. Extensions are not checked here
// — per-instance lossy detection is the orchestrator's job.
func ValidateAgent(a *resource.Agent) []ValidationError {
	var errs []ValidationError

	switch {
	case strings.TrimSpace(a.Name) == "":
		errs = append(errs, ValidationError{Field: "name", Message: "required"})
	case !skillNameRE.MatchString(a.Name):
		errs = append(errs, ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("must match %s (lowercase letters, digits, hyphens; must not start with a hyphen)", skillNameRE.String()),
		})
	}

	if strings.TrimSpace(a.Description) == "" {
		errs = append(errs, ValidationError{Field: "description", Message: "required"})
	}

	if strings.TrimSpace(a.Body) == "" {
		errs = append(errs, ValidationError{Field: "body", Message: "required (markdown body becomes the agent's system prompt)"})
	}

	return errs
}
