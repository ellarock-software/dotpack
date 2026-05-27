package validator

import (
	"fmt"
	"strings"

	"github.com/ellarock/dotpack/internal/resource"
)

// ValidateRule checks the install identity and body for a Markdown rule.
// Introduction metadata is validated by the canonical source repository;
// dotpack's install-time responsibility is to understand and preserve it.
func ValidateRule(r *resource.Rule) []ValidationError {
	var errs []ValidationError

	name := r.NameOrID()
	switch {
	case strings.TrimSpace(name) == "":
		errs = append(errs, ValidationError{Field: "id/name", Message: "one of id or name is required"})
	case !skillNameRE.MatchString(name):
		errs = append(errs, ValidationError{
			Field:   "id/name",
			Message: fmt.Sprintf("must match %s (lowercase letters, digits, hyphens; must not start with a hyphen)", skillNameRE.String()),
		})
	}

	if got, _ := r.Extensions()["artifact-type"].(string); got != "" && got != "rule" {
		errs = append(errs, ValidationError{Field: "artifact-type", Message: `must be "rule" when present`})
	}

	if strings.TrimSpace(r.Body) == "" {
		errs = append(errs, ValidationError{Field: "body", Message: "required (markdown content after the closing `---` is the rule guidance)"})
	}

	return errs
}
