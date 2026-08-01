package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillgate"
)

type fakeGate struct{ name string }

func (f fakeGate) Name() string { return f.name }

func (f fakeGate) Run(context.Context, skillgate.Request) (skillgate.Verdict, error) {
	return skillgate.Verdict{Gate: f.name, Pass: true}, nil
}

func register(t *testing.T, name string) {
	t.Helper()
	Register(name, func(dirs.Dirs) skillgate.Gate { return fakeGate{name: name} })
}

func TestRegisterAndBuildRoundTrip(t *testing.T) {
	defer Snapshot()()
	register(t, "fake-a")

	g, err := Build("fake-a", dirs.Dirs{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Name() != "fake-a" {
		t.Fatalf("Build returned gate %q, want fake-a", g.Name())
	}
}

func TestRegisterPanicsOnDuplicate(t *testing.T) {
	defer Snapshot()()
	register(t, "fake-dup")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("registering a duplicate gate did not panic; the shadowed gate could be the stricter one")
		}
		if !strings.Contains(toString(r), "fake-dup") {
			t.Errorf("panic %v does not name the duplicate gate", r)
		}
	}()
	register(t, "fake-dup")
}

func TestRegisterPanicsOnEmptyNameOrNilFactory(t *testing.T) {
	t.Run("empty name", func(t *testing.T) {
		defer Snapshot()()
		defer func() {
			if recover() == nil {
				t.Fatal("empty gate name did not panic")
			}
		}()
		Register("", func(dirs.Dirs) skillgate.Gate { return fakeGate{} })
	})
	t.Run("nil factory", func(t *testing.T) {
		defer Snapshot()()
		defer func() {
			if recover() == nil {
				t.Fatal("nil factory did not panic")
			}
		}()
		Register("fake-nil", nil)
	})
}

// A typo'd --skill-gate must error, never silently fall back to the
// default: the operator would believe a gate ran that did not.
func TestBuildRejectsUnknownGateAndListsRegistered(t *testing.T) {
	defer Snapshot()()
	register(t, "fake-a")
	register(t, "fake-b")

	_, err := Build("skilgate", dirs.Dirs{})
	if err == nil {
		t.Fatal("Build of an unknown gate returned no error")
	}
	msg := err.Error()
	for _, want := range []string{"skilgate", "fake-a", "fake-b"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

func TestNamesAreSorted(t *testing.T) {
	defer Snapshot()()
	for _, n := range []string{"zeta", "alpha", "mid"} {
		register(t, n)
	}
	got := Names()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("Names() is not sorted: %v", got)
		}
	}
}

func TestHasReportsRegistration(t *testing.T) {
	defer Snapshot()()
	register(t, "fake-a")
	if !Has("fake-a") {
		t.Error("Has(fake-a) = false after registration")
	}
	if Has("absent") {
		t.Error("Has(absent) = true")
	}
}

// The default must be the delta gate. If this changes, it is a breaking
// change to every adopter's install path and wants an explicit decision,
// not a silent edit.
func TestDefaultNameIsTheDeltaGate(t *testing.T) {
	if DefaultName() != "skillgate" {
		t.Fatalf("DefaultName() = %q, want \"skillgate\"", DefaultName())
	}
}

func TestValidateRequiresTheDefaultToBeRegistered(t *testing.T) {
	t.Run("empty registry", func(t *testing.T) {
		restore := Snapshot()
		defer restore()
		gates = map[string]Factory{}
		if err := Validate(); err == nil {
			t.Fatal("Validate accepted an empty registry")
		}
	})

	t.Run("default missing", func(t *testing.T) {
		restore := Snapshot()
		defer restore()
		gates = map[string]Factory{}
		register(t, "only-other")
		err := Validate()
		if err == nil {
			t.Fatal("Validate accepted a registry without the default gate")
		}
		if !strings.Contains(err.Error(), DefaultName()) {
			t.Errorf("error %q does not name the missing default", err)
		}
	})

	t.Run("default present", func(t *testing.T) {
		restore := Snapshot()
		defer restore()
		gates = map[string]Factory{}
		register(t, DefaultName())
		if err := Validate(); err != nil {
			t.Fatalf("Validate rejected a valid registry: %v", err)
		}
	})
}

func TestSnapshotRollsBackRegistrations(t *testing.T) {
	restore := Snapshot()
	register(t, "temporary")
	if !Has("temporary") {
		t.Fatal("registration did not take effect")
	}
	restore()
	if Has("temporary") {
		t.Fatal("Snapshot did not roll back the registration; gates leak between tests")
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return ""
}
