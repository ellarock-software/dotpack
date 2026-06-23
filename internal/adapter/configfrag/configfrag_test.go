package configfrag

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
)

type fakeResource struct {
	kind resource.Kind
}

func (f fakeResource) Kind() resource.Kind { return f.kind }
func (f fakeResource) Extensions() map[string]any {
	return nil
}

func TestNewRejectsInvalidPolicies(t *testing.T) {
	validFiles := ScopeFiles{User: func(d dirs.Dirs) (string, error) { return filepath.Join(d.HomeDir, "settings.json"), nil }}
	emit := func(r resource.Resource) ([]MergedFragment, error) {
		return []MergedFragment{{Path: "$.x", Value: "y"}}, nil
	}

	cases := []struct {
		name string
		p    Policy
		want string
	}{
		{name: "empty host", p: Policy{}, want: "HostID"},
		{name: "nil emit", p: Policy{HostID: "h", Kinds: map[resource.Kind]KindConfig{resource.KindHook: {Format: FormatJSON, Files: validFiles}}}, want: "Emit"},
		{name: "no files", p: Policy{HostID: "h", Kinds: map[resource.Kind]KindConfig{resource.KindHook: {Format: FormatJSON, Emit: emit}}}, want: "Files"},
		{name: "unknown format", p: Policy{HostID: "h", Kinds: map[resource.Kind]KindConfig{resource.KindHook: {Format: "yaml", Files: validFiles, Emit: emit}}}, want: "Format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected panic")
				}
				if !strings.Contains(fmt.Sprint(r), tc.want) {
					t.Fatalf("panic %q does not contain %q", r, tc.want)
				}
			}()
			New(dirs.Dirs{HomeDir: t.TempDir()}, tc.p)
		})
	}
}

func TestPlanResolvesScopeAndFragments(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	a := New(dirs.Dirs{HomeDir: home, ProjectHome: project}, Policy{
		HostID: "test-host",
		Kinds: map[resource.Kind]KindConfig{
			resource.KindMCPServer: {
				Format: FormatJSON,
				Files: ScopeFiles{
					User:    func(d dirs.Dirs) (string, error) { return filepath.Join(d.HomeDir, "user.json"), nil },
					Project: func(d dirs.Dirs) (string, error) { return filepath.Join(d.ProjectHome, "project.json"), nil },
				},
				Emit: func(r resource.Resource) ([]MergedFragment, error) {
					return []MergedFragment{
						{Path: "$.one", Value: "a"},
						{Path: "$.two", Value: "b", Op: adapter.MergedKeyAppend},
					}, nil
				},
			},
		},
	})

	if a.HostID() != "test-host" {
		t.Fatalf("HostID = %q", a.HostID())
	}
	plan, err := a.Plan(fakeResource{kind: resource.KindMCPServer}, adapter.ScopeProject)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Files) != 0 {
		t.Fatalf("configfrag plan should not contain file writes: %+v", plan.Files)
	}
	if len(plan.MergedKeys) != 2 {
		t.Fatalf("MergedKeys len = %d", len(plan.MergedKeys))
	}
	for _, mk := range plan.MergedKeys {
		if mk.File != filepath.Join(project, "project.json") {
			t.Errorf("merged key file = %q", mk.File)
		}
	}
	if plan.MergedKeys[1].Op != adapter.MergedKeyAppend {
		t.Errorf("append op not preserved: %+v", plan.MergedKeys[1])
	}
}

func TestPlanErrors(t *testing.T) {
	a := New(dirs.Dirs{HomeDir: t.TempDir()}, Policy{
		HostID: "test-host",
		Kinds: map[resource.Kind]KindConfig{
			resource.KindHook: {
				Format: FormatJSON,
				Files:  ScopeFiles{Project: func(d dirs.Dirs) (string, error) { return "", fmt.Errorf("missing project") }},
				Emit: func(r resource.Resource) ([]MergedFragment, error) {
					return []MergedFragment{{Path: "$.hooks", Value: "x"}}, nil
				},
			},
			resource.KindMCPServer: {
				Format: FormatJSON,
				Files:  ScopeFiles{User: func(d dirs.Dirs) (string, error) { return filepath.Join(d.HomeDir, "x.json"), nil }},
				Emit:   func(r resource.Resource) ([]MergedFragment, error) { return nil, nil },
			},
		},
	})

	if _, err := a.Plan(fakeResource{kind: resource.KindSkill}, adapter.ScopeUser); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("unsupported kind error = %v", err)
	}
	if _, err := a.Plan(fakeResource{kind: resource.KindHook}, adapter.ScopeUser); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("unsupported scope error = %v", err)
	}
	if _, err := a.Plan(fakeResource{kind: resource.KindHook}, adapter.ScopeProject); err == nil || !strings.Contains(err.Error(), "missing project") {
		t.Fatalf("resolver error = %v", err)
	}
	if _, err := a.Plan(fakeResource{kind: resource.KindMCPServer}, adapter.ScopeUser); err == nil || !strings.Contains(err.Error(), "no fragments") {
		t.Fatalf("empty emit error = %v", err)
	}
}
