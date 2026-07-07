package validator

import (
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/resource"
)

func validHook(t *testing.T) *resource.Hook {
	t.Helper()
	h, err := resource.ParseHook([]byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/bin/echo"}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	h.WithName("bash-guard")
	return h
}

func TestValidateHook_HappyPath(t *testing.T) {
	if errs := ValidateHook(validHook(t)); len(errs) > 0 {
		t.Errorf("expected no validation errors; got %v", errs)
	}
}

func TestValidateHook_NameRequired(t *testing.T) {
	h := validHook(t)
	h.WithName("")
	errs := ValidateHook(h)
	if !containsField(errs, "name") {
		t.Errorf("expected name error; got %v", errs)
	}
}

func TestValidateHook_NameShape(t *testing.T) {
	h := validHook(t)
	h.WithName("Bad_Name") // capitals + underscore not in the kebab regex
	errs := ValidateHook(h)
	if !containsField(errs, "name") {
		t.Errorf("expected name shape error; got %v", errs)
	}
}

func TestValidateHook_UnknownEvent(t *testing.T) {
	h, _ := resource.ParseHook([]byte(`{"hooks":{"PreToolsUse":[{"hooks":[{"type":"command","command":"x"}]}]}}`))
	h.WithName("typo-hook")
	errs := ValidateHook(h)
	if !containsSubstring(errs, "PreToolsUse") {
		t.Errorf("expected typo'd event surfaced; got %v", errs)
	}
}

func TestValidateHook_TypeMustBeCommand(t *testing.T) {
	h, _ := resource.ParseHook([]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"prompt","command":"x"}]}]}}`))
	h.WithName("prompt-hook")
	errs := ValidateHook(h)
	if !containsSubstring(errs, ".type") {
		t.Errorf("expected type error; got %v", errs)
	}
}

func TestValidateHook_CommandRequired(t *testing.T) {
	h, _ := resource.ParseHook([]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":""}]}]}}`))
	h.WithName("blank-hook")
	errs := ValidateHook(h)
	if !containsSubstring(errs, ".command") {
		t.Errorf("expected command-required error; got %v", errs)
	}
}

func TestValidateHook_TimeoutSaneCeiling(t *testing.T) {
	h, _ := resource.ParseHook([]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"x","timeout":5000}]}]}}`))
	h.WithName("slow-hook")
	errs := ValidateHook(h)
	if !containsSubstring(errs, "unit-confusion") {
		t.Errorf("expected timeout sane-ceiling error mentioning unit-confusion; got %v", errs)
	}
}

func TestValidateHook_TimeoutNegative(t *testing.T) {
	h, _ := resource.ParseHook([]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"x","timeout":-1}]}]}}`))
	h.WithName("neg-hook")
	errs := ValidateHook(h)
	if !containsSubstring(errs, "non-negative") {
		t.Errorf("expected non-negative error; got %v", errs)
	}
}

func containsField(errs []ValidationError, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}

func containsSubstring(errs []ValidationError, needle string) bool {
	for _, e := range errs {
		if strings.Contains(e.Field, needle) || strings.Contains(e.Message, needle) {
			return true
		}
	}
	return false
}
