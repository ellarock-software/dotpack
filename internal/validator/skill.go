// Package validator holds per-kind validators that gate installs on
// schema-level invariants (required fields, slug shapes, discriminated
// transports, etc.). Per the handoff note, logic is per-kind rather
// than a generic walker — the kind schemas are non-uniform.
package validator

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ellarock/dotpack/internal/resource"
)

// ValidationError reports one schema-invariant violation, with a field
// path the CLI can surface to the user.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// skillNameRE encodes schema/skill.yaml's name constraint: lowercase
// letters, digits, and hyphens; must start with a letter or digit.
// Per the schema note: "Letters, numbers, and hyphens only per Gemini
// CLI spec (other ecosystems converge on the same constraint)."
var skillNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidateSkill checks the universal-core invariants for a Skill: name
// required + slug-shaped, description required, body required. License
// is optional and pass-through (no validation). Extensions are not
// checked here — per-instance lossy detection (ADR-0016 §8) is the
// orchestrator's job, not the validator's.
func ValidateSkill(s *resource.Skill) []ValidationError {
	var errs []ValidationError

	switch {
	case strings.TrimSpace(s.Name) == "":
		errs = append(errs, ValidationError{Field: "name", Message: "required"})
	case !skillNameRE.MatchString(s.Name):
		errs = append(errs, ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("must match %s (lowercase letters, digits, hyphens; must not start with a hyphen)", skillNameRE.String()),
		})
	}

	if strings.TrimSpace(s.Description) == "" {
		errs = append(errs, ValidationError{Field: "description", Message: "required"})
	}

	if strings.TrimSpace(s.Body) == "" {
		errs = append(errs, ValidationError{Field: "body", Message: "required (markdown content after the closing `---` is the skill's instructions)"})
	}

	return errs
}
