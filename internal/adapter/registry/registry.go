// Package registry is the host-extensible adapter + umbrella registry
// (ADR-0014). It replaces the hard-coded adapterFactories /
// umbrellaFactories maps that previously lived in internal/cli/install.go
// and forced every new host to edit CLI core.
//
// Onboarding a host is now mechanical and local to the host's own
// package: the adapter sub-package calls RegisterAdapter from an init(),
// and a single blank import in internal/adapter/all wires it into every
// command. The CLI flow (install/uninstall/list/sync) consults this
// registry through Build / IsAdapter / IsUmbrella / AdapterHostIDs and
// never names a concrete host.
//
// Import-cycle safety: this package imports ONLY {adapter, dirs,
// resource} — a strict superset of what internal/adapter imports — so
// every adapter sub-package (which already imports those three) can
// import it without a cycle. It MUST NOT import any concrete adapter
// sub-package; the concrete packages depend on it, not the reverse. The
// blank-import aggregation lives in internal/adapter/all.
package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
)

// Factory constructs a per-host adapter from resolved Dirs. The closure
// indirection is mandatory: each host's New(d) returns its concrete
// *Adapter, not adapter.Adapter, and Go has no return-type variance —
// the wrapper unifies the value type the registry stores.
type Factory func(dirs.Dirs) adapter.Adapter

// Umbrella is the per-umbrella declaration (ADR-0012 §1 + §10), held as
// data rather than code. Subs is the sub-adapter HostID set the umbrella
// fans out to for lossy aggregation. Writers is the per-kind canonical
// writer HostID list: a file-drop kind with a shared convergence path
// names ONE writer (write-once); kinds whose hosts each own a distinct
// file name every sub-adapter (fan-out). Kinds absent from Writers are
// explicitly unsupported under the umbrella.
type Umbrella struct {
	Subs    []string
	Writers map[resource.Kind][]string
}

var (
	mu        sync.Mutex
	adapters  = map[string]Factory{}
	umbrellas = map[string]Umbrella{}
)

// RegisterAdapter registers a per-host adapter factory under hostID.
// Called from each adapter sub-package's init(). Panics on a duplicate
// hostID — two packages claiming the same host is a programmer error
// that must fail at process start, not silently shadow.
func RegisterAdapter(hostID string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if hostID == "" {
		panic("registry: RegisterAdapter called with empty hostID")
	}
	if f == nil {
		panic(fmt.Sprintf("registry: RegisterAdapter(%q) called with nil factory", hostID))
	}
	if _, dup := adapters[hostID]; dup {
		panic(fmt.Sprintf("registry: duplicate adapter registration for hostID %q", hostID))
	}
	adapters[hostID] = f
}

// RegisterUmbrella registers an umbrella under name. Called from the
// umbrella's own init(). Panics on a duplicate name. Cross-references to
// sub-adapters are NOT checked here (registration order across init()s
// is not guaranteed); call Validate once after all init()s complete.
func RegisterUmbrella(name string, u Umbrella) {
	mu.Lock()
	defer mu.Unlock()
	if name == "" {
		panic("registry: RegisterUmbrella called with empty name")
	}
	if _, dup := umbrellas[name]; dup {
		panic(fmt.Sprintf("registry: duplicate umbrella registration for name %q", name))
	}
	umbrellas[name] = u
}

// Build constructs the per-host adapter registered under hostID.
func Build(hostID string, d dirs.Dirs) (adapter.Adapter, error) {
	mu.Lock()
	f, ok := adapters[hostID]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", hostID)
	}
	return f(d), nil
}

// IsAdapter reports whether hostID is a registered per-host adapter.
func IsAdapter(hostID string) bool {
	mu.Lock()
	defer mu.Unlock()
	_, ok := adapters[hostID]
	return ok
}

// IsUmbrella reports whether name is a registered umbrella.
func IsUmbrella(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	_, ok := umbrellas[name]
	return ok
}

// AdapterHostIDs returns every registered per-host HostID in sorted
// order. Layout-driven callers (reconcile scan, CLI help) iterate this
// instead of a hard-coded host list.
func AdapterHostIDs() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(adapters))
	for id := range adapters {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// LookupUmbrella returns the umbrella declaration for name.
func LookupUmbrella(name string) (Umbrella, bool) {
	mu.Lock()
	defer mu.Unlock()
	u, ok := umbrellas[name]
	return u, ok
}

// BuildUmbrella resolves an umbrella name to its constructed sub-adapter
// set and per-kind writer adapters. Mirrors the prior install.go
// buildUmbrella. ok is false for an unknown name. Assumes Validate has
// run (every referenced HostID resolves); a missing factory here would
// be an internal error masked as a nil entry, which Validate prevents.
func BuildUmbrella(name string, d dirs.Dirs) (subs []adapter.Adapter, writers map[resource.Kind][]adapter.Adapter, ok bool) {
	mu.Lock()
	u, found := umbrellas[name]
	defer mu.Unlock()
	if !found {
		return nil, nil, false
	}
	subs = make([]adapter.Adapter, 0, len(u.Subs))
	for _, hostID := range u.Subs {
		if f, has := adapters[hostID]; has {
			subs = append(subs, f(d))
		}
	}
	writers = make(map[resource.Kind][]adapter.Adapter, len(u.Writers))
	for kind, hostIDs := range u.Writers {
		for _, hostID := range hostIDs {
			if f, has := adapters[hostID]; has {
				writers[kind] = append(writers[kind], f(d))
			}
		}
	}
	return subs, writers, true
}

// Validate checks every umbrella's cross-references against the
// registered adapter set. It reproduces the fail-fast guarantees the
// prior install.go init() carried, but is an explicit call (not an
// init()) so it runs AFTER all adapter/umbrella registrations complete —
// the caller (internal/cli) invokes it from its own init(), and Go runs
// imported packages' init()s first. Returns a descriptive error; the
// caller decides whether to panic.
func Validate() error {
	mu.Lock()
	defer mu.Unlock()
	for name, u := range umbrellas {
		subSet := map[string]bool{}
		for _, sub := range u.Subs {
			if _, ok := adapters[sub]; !ok {
				return fmt.Errorf("umbrella %q references unknown sub-adapter %q (not registered)", name, sub)
			}
			subSet[sub] = true
		}
		for kind, writers := range u.Writers {
			if len(writers) == 0 {
				return fmt.Errorf("umbrella %q writers[%q] has no writers", name, kind)
			}
			for _, w := range writers {
				if _, ok := adapters[w]; !ok {
					return fmt.Errorf("umbrella %q writers[%q] includes %q which is not registered", name, kind, w)
				}
				if !subSet[w] {
					return fmt.Errorf("umbrella %q writers[%q] includes %q which is not in subs %v — lossy aggregation would not cover the writer's emits", name, kind, w, u.Subs)
				}
			}
		}
	}
	return nil
}

// Snapshot captures the current registry state and returns a restore
// func. It is the test seam for registering a fake adapter/umbrella
// without leaking into other tests: defer the returned closure to roll
// back. Production code never calls this.
func Snapshot() func() {
	mu.Lock()
	defer mu.Unlock()
	savedAdapters := make(map[string]Factory, len(adapters))
	for k, v := range adapters {
		savedAdapters[k] = v
	}
	savedUmbrellas := make(map[string]Umbrella, len(umbrellas))
	for k, v := range umbrellas {
		savedUmbrellas[k] = v
	}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		adapters = savedAdapters
		umbrellas = savedUmbrellas
	}
}
