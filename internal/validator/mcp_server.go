package validator

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ellarock-software/dotpack/internal/resource"
)

// mcpServerNameRE constrains server names to JSON/TOML identifier shape.
// .mcp.json keys are arbitrary strings (JSON), but TOML's bare-key shape
// (codex) and the corpus convention (kebab + alphanumerics) both want a
// stricter constraint. Allowing arbitrary keys would break TOML emit
// when codex lands; rejecting now is the lower-friction choice (a
// non-conforming source can always be renamed at import time).
var mcpServerNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// ValidateMCPServer checks the per-instance invariants ADR-0010 +
// ADR-0012 §7 promise to gate at parse-time:
//
//  1. Name is non-empty and slug-shaped (see mcpServerNameRE).
//  2. Transport discriminator: exactly one of {Command, URL} is set
//     (stdio XOR HTTP). Both → ambiguous; neither → no transport.
//  3. When Command is set, Args is required per the corpus invariant
//     ("appears_in: 20/20 entries when command present"). Empty array
//     is allowed (an executable with no args is valid); nil/absent is
//     not.
//
// Extensions are NOT validated here — per-instance lossy detection
// (ADR-0012 §8) is the orchestrator's job, not the validator's. The
// validator's contract is "the resource is structurally well-formed";
// "the install would drop semantically load-bearing fields on this
// host" is a runtime concern surfaced via LossyError.
func ValidateMCPServer(m *resource.MCPServer) []ValidationError {
	var errs []ValidationError

	switch {
	case strings.TrimSpace(m.Name) == "":
		errs = append(errs, ValidationError{Field: "name", Message: "required (the JSON/TOML map key for the server entry)"})
	case !mcpServerNameRE.MatchString(m.Name):
		errs = append(errs, ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("must match %s (alphanumerics + _ + -; first char non-separator)", mcpServerNameRE.String()),
		})
	}

	hasCommand := strings.TrimSpace(m.Command) != ""
	hasURL := strings.TrimSpace(m.URL) != ""
	switch {
	case hasCommand && hasURL:
		errs = append(errs, ValidationError{
			Field:   "command/url",
			Message: "exactly one transport allowed: command (stdio) XOR url (HTTP). Both set — pick one.",
		})
	case !hasCommand && !hasURL:
		errs = append(errs, ValidationError{
			Field:   "command/url",
			Message: "no transport: set command (stdio) or url (HTTP)",
		})
	}

	if hasCommand && m.Args == nil {
		// Per schema/mcp-server.yaml: "Always present when command is
		// present (even when conceptually empty — corpus has no entry
		// with command but no args)". An explicit empty array passes;
		// nil/absent fails so the source clearly declares "no args"
		// rather than relying on omission.
		errs = append(errs, ValidationError{
			Field:   "args",
			Message: "required when command is set (use [] for an explicit no-args invocation)",
		})
	}

	return errs
}
