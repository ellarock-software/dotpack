package validator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/internal/validator"
)

func TestValidateSkill_TracerBulletFixturePasses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	skill, err := resource.ParseSkill(raw)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if errs := validator.ValidateSkill(skill); len(errs) != 0 {
		t.Fatalf("ValidateSkill: got %v, want no errors", errs)
	}
}

func TestValidateSkill_EmptyNameIsRequired(t *testing.T) {
	skill := &resource.Skill{Description: "d", Body: "b"}
	errs := validator.ValidateSkill(skill)
	if !hasField(errs, "name") {
		t.Errorf("expected name validation error; got %v", errs)
	}
}

func TestValidateSkill_EmptyDescriptionIsRequired(t *testing.T) {
	skill := &resource.Skill{Name: "ok", Body: "b"}
	errs := validator.ValidateSkill(skill)
	if !hasField(errs, "description") {
		t.Errorf("expected description validation error; got %v", errs)
	}
}

func TestValidateSkill_EmptyBodyIsRequired(t *testing.T) {
	skill := &resource.Skill{Name: "ok", Description: "d"}
	errs := validator.ValidateSkill(skill)
	if !hasField(errs, "body") {
		t.Errorf("expected body validation error; got %v", errs)
	}
}

func TestValidateSkill_NameSlugRegex(t *testing.T) {
	// schema/skill.yaml: "Letters, numbers, and hyphens only per Gemini CLI spec"
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"ok-name", false},
		{"ok123", false},
		{"Foo", true},     // uppercase
		{"foo_bar", true}, // underscore
		{"foo bar", true}, // space
		{"foo/bar", true}, // slash (path-injection risk)
		{"-leading-hyphen", true},
		{"", true}, // empty (covered by required check; should still flag)
	}
	for _, tc := range cases {
		t.Run("name="+tc.name, func(t *testing.T) {
			skill := &resource.Skill{Name: tc.name, Description: "d", Body: "b"}
			errs := validator.ValidateSkill(skill)
			gotErr := hasField(errs, "name")
			if gotErr != tc.wantErr {
				t.Errorf("name=%q: gotErr=%v, wantErr=%v (errs=%v)", tc.name, gotErr, tc.wantErr, errs)
			}
		})
	}
}

func hasField(errs []validator.ValidationError, field string) bool {
	for _, e := range errs {
		if strings.EqualFold(e.Field, field) {
			return true
		}
	}
	return false
}
