package validator_test

import (
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/internal/validator"
)

func TestValidateCommandMemoryMCPAndRuleCoverage(t *testing.T) {
	if errs := validator.ValidateCommand(&resource.Command{Name: "deploy", Prompt: "ship"}); len(errs) != 0 {
		t.Fatalf("ValidateCommand happy path: %v", errs)
	}
	for _, cmd := range []*resource.Command{
		{Prompt: "ship"},
		{Name: "Bad Name", Prompt: "ship"},
		{Name: "deploy"},
	} {
		if errs := validator.ValidateCommand(cmd); len(errs) == 0 {
			t.Fatalf("ValidateCommand(%+v) expected errors", cmd)
		}
	}

	if errs := validator.ValidateMemory(&resource.Memory{Name: "AGENTS.md"}); len(errs) != 0 {
		t.Fatalf("ValidateMemory happy path: %v", errs)
	}
	if errs := validator.ValidateMemory(&resource.Memory{}); !hasField(errs, "name") {
		t.Fatalf("ValidateMemory missing name errors = %v; want name", errs)
	}

	if errs := validator.ValidateMCPServer(&resource.MCPServer{Name: "github", Command: "npx", Args: []string{}}); len(errs) != 0 {
		t.Fatalf("ValidateMCPServer stdio happy path: %v", errs)
	}
	if errs := validator.ValidateMCPServer(&resource.MCPServer{Name: "remote", URL: "https://example.com"}); len(errs) != 0 {
		t.Fatalf("ValidateMCPServer http happy path: %v", errs)
	}
	for _, server := range []*resource.MCPServer{
		{Command: "npx", Args: []string{}},
		{Name: "-bad", Command: "npx", Args: []string{}},
		{Name: "both", Command: "npx", Args: []string{}, URL: "https://example.com"},
		{Name: "neither"},
		{Name: "missingargs", Command: "npx"},
	} {
		if errs := validator.ValidateMCPServer(server); len(errs) == 0 {
			t.Fatalf("ValidateMCPServer(%+v) expected errors", server)
		}
	}

	if errs := validator.ValidateRule((&resource.Rule{Name: "rule", Body: "body"}).WithExtensions(map[string]any{"artifact-type": "rule"})); len(errs) != 0 {
		t.Fatalf("ValidateRule happy path: %v", errs)
	}
	for _, rule := range []*resource.Rule{
		{Body: "body"},
		{Name: "Bad Name", Body: "body"},
		(&resource.Rule{Name: "rule", Body: "body"}).WithExtensions(map[string]any{"artifact-type": "agent"}),
		{Name: "rule"},
	} {
		if errs := validator.ValidateRule(rule); len(errs) == 0 {
			t.Fatalf("ValidateRule(%+v) expected errors", rule)
		}
	}

	for _, hook := range []*resource.Hook{
		{Name: "empty"},
		{Name: "empty-binding", Events: []resource.EventBinding{{Event: "PreToolUse", Bindings: []resource.Binding{{}}}}},
	} {
		if errs := validator.ValidateHook(hook); len(errs) == 0 {
			t.Fatalf("ValidateHook(%+v) expected errors", hook)
		}
	}
}

func TestValidationErrorString(t *testing.T) {
	err := validator.ValidationError{Field: "name", Message: "required"}
	if got := err.Error(); !strings.Contains(got, "name") || !strings.Contains(got, "required") {
		t.Fatalf("ValidationError.Error() = %q", got)
	}
}
