package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/resource"
)

type umbrellaFakeAdapter struct {
	host string
	plan adapter.InstallPlan
	err  error
}

func (f umbrellaFakeAdapter) HostID() string { return f.host }
func (f umbrellaFakeAdapter) Plan(resource.Resource, adapter.Scope) (adapter.InstallPlan, error) {
	return f.plan, f.err
}

func TestUmbrellaInstallWritesFilesMergedKeysStaleRemovalsAndManifest(t *testing.T) {
	tmp := t.TempDir()
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	target := filepath.Join(tmp, "skill", "SKILL.md")
	stale := filepath.Join(tmp, "stale.md")
	config := filepath.Join(tmp, "settings.json")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	writer := umbrellaFakeAdapter{host: "codex", plan: adapter.InstallPlan{
		Files:       []adapter.FileWrite{{Path: target, Content: []byte("content"), Mode: 0o600}},
		RemoveFiles: []adapter.FileRemove{{Path: stale}, {}},
		MergedKeys:  []adapter.MergedKeyWrite{{File: config, Path: "$.mcpServers.github", Value: map[string]any{"command": "npx"}}},
		TargetDir:   filepath.Dir(target),
	}}
	u := NewUmbrellaInstaller(dirs.Dirs{}, "agents-cli", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {writer},
	}, store)
	u.now = func() time.Time { return time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC) }

	result, err := u.Install(&resource.Skill{Name: "skill", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Source: "file:///tmp/SKILL.md"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Record.Agent != "agents-cli" || result.Record.ID != "agents-cli:skill:skill" {
		t.Fatalf("umbrella record identity wrong: %+v", result.Record)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target file not written: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale file should be removed; stat=%v", err)
	}
	if _, err := os.Stat(config); err != nil {
		t.Fatalf("merged-key config not written: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Installs) != 1 || loaded.Installs[0].MergedKeys[0].SHA256 == "" {
		t.Fatalf("manifest missing umbrella merged-key claim: %+v", loaded.Installs)
	}
}

func TestUmbrellaInstallUnsupportedLossyCollisionAndPlanErrors(t *testing.T) {
	tmp := t.TempDir()
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	target := filepath.Join(tmp, "skill", "SKILL.md")
	writer := umbrellaFakeAdapter{host: "codex", plan: adapter.InstallPlan{
		Files: []adapter.FileWrite{{Path: target, Content: []byte("content")}},
	}}
	u := NewUmbrellaInstaller(dirs.Dirs{}, "agents-cli", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {writer},
	}, store)

	_, err := u.Install(&resource.Agent{Name: "agent", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "not supported under umbrella") {
		t.Fatalf("unsupported error = %v; want umbrella unsupported", err)
	}

	lossySub := umbrellaFakeAdapter{host: "gemini-cli", plan: writer.plan}
	lossyUmbrella := NewUmbrellaInstaller(dirs.Dirs{}, "agents-cli", []adapter.Adapter{lossySub}, map[resource.Kind][]adapter.Adapter{resource.KindSkill: {lossySub}}, store)
	_, err = lossyUmbrella.Install((&resource.Skill{Name: "s", Description: "d", Body: "b"}).WithExtensions(map[string]any{"allowed-tools": "Read"}), adapter.ScopeUser, InstallOptions{})
	var lossy *LossyError
	if !errors.As(err, &lossy) || lossy.Host != "agents-cli" {
		t.Fatalf("lossy error = %T %v; want umbrella LossyError", err, err)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err = u.Install(&resource.Skill{Name: "skill", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{})
	var collision *CollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("collision error = %T %v; want CollisionError", err, err)
	}

	planErr := errors.New("plan failed")
	badWriter := umbrellaFakeAdapter{host: "codex", err: planErr}
	badUmbrella := NewUmbrellaInstaller(dirs.Dirs{}, "agents-cli", []adapter.Adapter{badWriter}, map[resource.Kind][]adapter.Adapter{resource.KindSkill: {badWriter}}, store)
	_, err = badUmbrella.Install(&resource.Skill{Name: "bad", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{})
	if !errors.Is(err, planErr) {
		t.Fatalf("plan error = %v; want wrapped %v", err, planErr)
	}
}

func TestUmbrellaAggregatePlansAndLossyHelpers(t *testing.T) {
	tmp := t.TempDir()
	first := umbrellaFakeAdapter{host: "codex", plan: adapter.InstallPlan{TargetDir: filepath.Join(tmp, "one")}}
	second := umbrellaFakeAdapter{host: "gemini-cli", plan: adapter.InstallPlan{TargetDir: filepath.Join(tmp, "two")}}
	u := NewUmbrellaInstaller(dirs.Dirs{}, "agents-cli", []adapter.Adapter{first, second}, nil, manifest.NewStore(filepath.Join(tmp, "installs.yaml")))

	_, err := u.aggregatePlans(&resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeUser, []adapter.Adapter{first, second})
	if err == nil || !strings.Contains(err.Error(), "multiple target dirs") {
		t.Fatalf("aggregatePlans error = %v; want multiple target dirs", err)
	}

	reasons, err := u.aggregateLossy(&resource.Skill{Name: "plain", Description: "d", Body: "b"})
	if err != nil || len(reasons) != 0 {
		t.Fatalf("aggregateLossy no extensions = %v, %v; want none", reasons, err)
	}
	reasons, err = u.aggregateLossy((&resource.Skill{Name: "lossy", Description: "d", Body: "b"}).WithExtensions(map[string]any{"allowed-tools": "Read"}))
	if err != nil {
		t.Fatalf("aggregateLossy: %v", err)
	}
	if len(reasons) != 1 || reasons[0].FieldPath != "allowed-tools" {
		t.Fatalf("deduped lossy reasons = %+v; want allowed-tools once", reasons)
	}
}
