package validator

import (
	"fmt"
	"strings"

	"github.com/ellarock-software/dotpack/internal/resource"
)

func ValidateCommand(c *resource.Command) []ValidationError {
	var errs []ValidationError

	switch {
	case strings.TrimSpace(c.Name) == "":
		errs = append(errs, ValidationError{Field: "name", Message: "required"})
	case !skillNameRE.MatchString(c.Name):
		errs = append(errs, ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("must match %s (lowercase letters, digits, hyphens; must not start with a hyphen)", skillNameRE.String()),
		})
	}

	if strings.TrimSpace(c.Prompt) == "" {
		errs = append(errs, ValidationError{Field: "prompt", Message: "required (command must have a prompt or body)"})
	}

	return errs
}
