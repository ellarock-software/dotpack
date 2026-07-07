package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/adapter/registry"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/orchestrator"
	"github.com/ellarock-software/dotpack/internal/resource"
)

// fakeAdapter is a host that exists in NO core switchboard — it is born
// entirely inside this test and registered through the public registry
// API. Its presence here, installing through the unmodified orchestrator,
// is the proof that the architecture is open: onboarding a host touches
// neither internal/cli nor internal/orchestrator (ADR-0014).
type fakeAdapter struct{ root string }

func (f *fakeAdapter) HostID() string { return "fake-host-registry-test" }

func (f *fakeAdapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	named := r.(resource.Named)
	return adapter.InstallPlan{
		Files: []adapter.FileWrite{{
			Path:    filepath.Join(f.root, named.ResourceName()+".fake"),
			Content: []byte("from fake adapter"),
			Mode:    0o644,
		}},
	}, nil
}

// TestFakeAdapterInstallsThroughUnchangedCore registers a brand-new host
// via the public API and drives a real install through orchestrator —
// with zero edits to install.go / registry.go production code.
func TestFakeAdapterInstallsThroughUnchangedCore(t *testing.T) {
	restore := registry.Snapshot()
	defer restore()

	out := t.TempDir()
	registry.RegisterAdapter("fake-host-registry-test", func(d dirs.Dirs) adapter.Adapter {
		return &fakeAdapter{root: out}
	})

	if !registry.IsAdapter("fake-host-registry-test") {
		t.Fatal("registered fake host is not reported by IsAdapter")
	}
	found := false
	for _, id := range registry.AdapterHostIDs() {
		if id == "fake-host-registry-test" {
			found = true
		}
	}
	if !found {
		t.Fatal("registered fake host absent from AdapterHostIDs")
	}

	a, err := registry.Build("fake-host-registry-test", dirs.Dirs{})
	if err != nil {
		t.Fatalf("Build fake host: %v", err)
	}

	d := dirs.Dirs{DotpackHome: t.TempDir(), ProjectHome: t.TempDir()}
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	inst := orchestrator.NewInstaller(d, a, mf)

	res := &resource.Skill{Name: "demo", Description: "a demo skill", Body: "body"}
	result, err := inst.Install(res, adapter.ScopeUser, orchestrator.InstallOptions{
		Source:     "file:///dev/null",
		TargetRoot: out,
	})
	if err != nil {
		t.Fatalf("install through fake adapter: %v", err)
	}
	if result.Record.Agent != "fake-host-registry-test" {
		t.Fatalf("manifest record agent = %q; want fake-host-registry-test", result.Record.Agent)
	}
	wrote := filepath.Join(out, "demo.fake")
	if b, err := os.ReadFile(wrote); err != nil || string(b) != "from fake adapter" {
		t.Fatalf("fake adapter file = %q err=%v; want written", string(b), err)
	}
}

// TestSnapshotRestoreIsolatesRegistration proves a fake registration does
// not leak across tests.
func TestSnapshotRestoreIsolatesRegistration(t *testing.T) {
	restore := registry.Snapshot()
	registry.RegisterAdapter("ephemeral-host", func(d dirs.Dirs) adapter.Adapter { return &fakeAdapter{} })
	if !registry.IsAdapter("ephemeral-host") {
		t.Fatal("ephemeral host should be registered before restore")
	}
	restore()
	if registry.IsAdapter("ephemeral-host") {
		t.Fatal("ephemeral host should be gone after restore")
	}
}

// TestDuplicateRegistrationPanics pins the fail-fast guarantee.
func TestDuplicateRegistrationPanics(t *testing.T) {
	restore := registry.Snapshot()
	defer restore()
	registry.RegisterAdapter("dup-host", func(d dirs.Dirs) adapter.Adapter { return &fakeAdapter{} })
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate RegisterAdapter did not panic")
		}
	}()
	registry.RegisterAdapter("dup-host", func(d dirs.Dirs) adapter.Adapter { return &fakeAdapter{} })
}

// TestValidateRejectsWriterNotInSubs pins the umbrella cross-reference
// check that the prior install.go init() carried.
func TestValidateRejectsWriterNotInSubs(t *testing.T) {
	restore := registry.Snapshot()
	defer restore()

	registry.RegisterAdapter("sub-a", func(d dirs.Dirs) adapter.Adapter { return &fakeAdapter{} })
	registry.RegisterAdapter("sub-b", func(d dirs.Dirs) adapter.Adapter { return &fakeAdapter{} })

	// Writer "sub-b" is NOT in Subs — lossy aggregation would miss it.
	registry.RegisterUmbrella("bad-umbrella", registry.Umbrella{
		Subs:    []string{"sub-a"},
		Writers: map[resource.Kind][]string{resource.KindSkill: {"sub-b"}},
	})
	if err := registry.Validate(); err == nil {
		t.Fatal("Validate accepted an umbrella whose writer is not in subs")
	}

	// An unknown sub is also rejected.
	registry.RegisterUmbrella("bad-umbrella-2", registry.Umbrella{
		Subs:    []string{"nope"},
		Writers: map[resource.Kind][]string{resource.KindSkill: {"nope"}},
	})
	if err := registry.Validate(); err == nil {
		t.Fatal("Validate accepted an umbrella referencing an unregistered sub")
	}
}
