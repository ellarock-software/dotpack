// Package all wires every shipped skill gate into the registry with a
// single blank import, mirroring internal/adapter/all (ADR-0014).
//
// internal/cli imports this package once; adding a gate means adding one
// line here and touching no CLI core.
//
// Note the asymmetry: the "skillspector" gate registers from
// internal/cli rather than from its own package, because the
// implementation it wraps is still shared with the scan-skills and
// baseline-skills commands. That is recorded as debt in ADR-0016.
package all

import (
	_ "github.com/ellarock-software/dotpack/internal/skillgate/delta"
)
