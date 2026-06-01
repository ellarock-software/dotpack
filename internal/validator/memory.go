package validator

import (
	"strings"

	"github.com/ellarock/dotpack/internal/resource"
)

func ValidateMemory(m *resource.Memory) []ValidationError {
	var errs []ValidationError

	if strings.TrimSpace(m.Name) == "" {
		errs = append(errs, ValidationError{Field: "name", Message: "required"})
	}

	return errs
}
