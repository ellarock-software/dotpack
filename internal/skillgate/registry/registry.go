// Package registry is the open skill-gate registry (ADR-0016), built on
// the same shape as the adapter registry in ADR-0014.
//
// Adding a gate is mechanical and local to the gate's own package: the
// sub-package calls Register from an init(), and a single blank import
// in internal/skillgate/all wires it into every command. The mandatory
// enforcement funnel in internal/cli consults this registry through
// Build and never names a concrete gate.
//
// Import-cycle safety: this package imports ONLY {skillgate, dirs} — a
// strict subset of what every concrete gate imports — so any gate
// sub-package can import it without a cycle. It MUST NOT import a
// concrete gate sub-package.
//
// Gate SELECTION is deliberately not part of this package's surface
// beyond DefaultName. Which gate runs is an operator decision resolved
// by internal/cli from a flag or an environment variable, never from the
// package being installed: a source that could choose its own gate could
// choose the weakest one.
package registry

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillgate"
)

// defaultName is the gate enforced when the operator selects none.
//
// It is the delta gate: absolute gating on a high-recall, low-precision
// scanner forces whole-package bypasses, and a permanent bypass is worse
// than a noisy gate. See ADR-0016.
const defaultName = "skillgate"

// Factory constructs a gate from resolved Dirs. The closure indirection
// matches the adapter registry: a gate's New(d) returns its concrete
// type, and Go has no return-type variance.
type Factory func(dirs.Dirs) skillgate.Gate

var (
	mu    sync.Mutex
	gates = map[string]Factory{}
)

// Register registers a gate factory under name. Called from each gate
// sub-package's init(). Panics on a duplicate, empty name, or nil
// factory — two packages claiming the same gate is a programmer error
// that must fail at process start rather than silently shadow, because
// the shadowed gate might be the stricter one.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if strings.TrimSpace(name) == "" {
		panic("skillgate registry: Register called with an empty gate name")
	}
	if f == nil {
		panic(fmt.Sprintf("skillgate registry: Register(%q) called with a nil factory", name))
	}
	if _, exists := gates[name]; exists {
		panic(fmt.Sprintf("skillgate registry: duplicate gate %q", name))
	}
	gates[name] = f
}

// Build constructs the named gate. An unknown name is an error listing
// what is registered; it is never a silent fall back to the default,
// because a typo'd --skill-gate must not quietly run a different gate
// than the operator asked for.
func Build(name string, d dirs.Dirs) (skillgate.Gate, error) {
	mu.Lock()
	f, ok := gates[name]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown skill gate %q; registered gates: %s", name, strings.Join(Names(), ", "))
	}
	return f(d), nil
}

// Has reports whether name is registered.
func Has(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	_, ok := gates[name]
	return ok
}

// Names returns the registered gate names, sorted. Used for help text
// and for error messages.
func Names() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(gates))
	for name := range gates {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// DefaultName is the gate enforced when the operator selects none.
func DefaultName() string { return defaultName }

// Validate checks the registry is coherent. Like the adapter registry's
// Validate it is an explicit call rather than an init(), so it runs
// after every gate package's init() has completed: the caller
// (internal/cli) invokes it from its own init(), and Go runs imported
// packages' init()s first.
func Validate() error {
	mu.Lock()
	defer mu.Unlock()
	if len(gates) == 0 {
		return fmt.Errorf("skillgate registry: no gates registered; internal/skillgate/all must be imported")
	}
	if _, ok := gates[defaultName]; !ok {
		names := make([]string, 0, len(gates))
		for name := range gates {
			names = append(names, name)
		}
		sort.Strings(names)
		return fmt.Errorf("skillgate registry: default gate %q is not registered; registered gates: %s", defaultName, strings.Join(names, ", "))
	}
	for name, f := range gates {
		if f == nil {
			return fmt.Errorf("skillgate registry: gate %q has a nil factory", name)
		}
	}
	return nil
}

// Snapshot captures the current registry state and returns a restore
// func. It is the test seam for registering a fake gate without leaking
// into other tests: defer the returned closure to roll back. Production
// code never calls this.
func Snapshot() func() {
	mu.Lock()
	defer mu.Unlock()
	saved := make(map[string]Factory, len(gates))
	for k, v := range gates {
		saved[k] = v
	}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		gates = saved
	}
}
