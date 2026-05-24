package schema

import (
	"sort"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/resource"
)

// LossyExtensions implements ADR-0016 §8 per-instance lossy detection.
// For each key in extensions, look up the matching canonical_concept;
// emit a LossyReason when the concept exists and the target host is
// NOT in its aliases (and lossy_when_dropped is true), OR when the
// concept does not exist (unknown field — lossy by default per the
// failure-mode-safety argument in ADR-0016 §Why).
//
// Pass-through metadata (lossy_when_dropped: false) is skipped: dropping
// it changes nothing observable, so requiring --allow-lossy would be
// over-strict friction. Concepts that the target host natively supports
// are skipped (the adapter is expected to emit them).
//
// Keys are iterated in sorted order so the returned slice is stable
// across runs — the orchestrator surfaces it to the user and persists
// it implicitly (e.g. through the LossyError formatter); a stable order
// keeps error messages, logs, and any future manifest field reproducible.
func LossyExtensions(kind resource.Kind, hostID string, extensions map[string]any) ([]adapter.LossyReason, error) {
	if len(extensions) == 0 {
		return nil, nil
	}
	s, err := Load(kind)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(extensions))
	for k := range extensions {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var reasons []adapter.LossyReason
	for _, k := range keys {
		c := s.LookupExtension(k)
		if c == nil {
			reasons = append(reasons, adapter.LossyReason{
				FieldPath:        k,
				CanonicalConcept: "", // unknown — no schema entry claims this field
				SupportedHosts:   nil,
			})
			continue
		}
		if !c.IsLossyWhenDropped() {
			continue
		}
		if c.SupportsHost(hostID) {
			continue
		}
		reasons = append(reasons, adapter.LossyReason{
			FieldPath:        k,
			CanonicalConcept: c.CanonicalConcept,
			SupportedHosts:   c.SupportingHosts(),
		})
	}
	return reasons, nil
}
