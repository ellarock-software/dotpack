package validator_test

import (
	"testing"

	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/internal/validator"
)

func TestValidateAgent_HappyPath(t *testing.T) {
	a := &resource.Agent{
		Name:        "ok-agent",
		Description: "Use when ...",
		Body:        "system prompt body",
	}
	if errs := validator.ValidateAgent(a); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestValidateAgent_NameRequired(t *testing.T) {
	a := &resource.Agent{Description: "d", Body: "b"}
	errs := validator.ValidateAgent(a)
	if !hasField(errs, "name") {
		t.Errorf("expected name error; got %v", errs)
	}
}

func TestValidateAgent_NameSlug(t *testing.T) {
	a := &resource.Agent{Name: "Bad Name!", Description: "d", Body: "b"}
	errs := validator.ValidateAgent(a)
	if !hasField(errs, "name") {
		t.Errorf("expected name slug error; got %v", errs)
	}
}

func TestValidateAgent_DescriptionRequired(t *testing.T) {
	a := &resource.Agent{Name: "ok", Body: "b"}
	errs := validator.ValidateAgent(a)
	if !hasField(errs, "description") {
		t.Errorf("expected description error; got %v", errs)
	}
}

func TestValidateAgent_BodyRequired(t *testing.T) {
	a := &resource.Agent{Name: "ok", Description: "d"}
	errs := validator.ValidateAgent(a)
	if !hasField(errs, "body") {
		t.Errorf("expected body error; got %v", errs)
	}
}
