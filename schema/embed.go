// Package schema loads dotpack's per-kind YAML schemas (skill, agent,
// command, memory, hook, mcp-server, rule) and implements the ADR-0016 §8
// per-instance lossy-detection algorithm.
//
// The schema YAML files in this directory are embedded into the binary
// via //go:embed so the installed dotpack is self-contained — no
// runtime filesystem lookup against the source tree.
//
// Adapters and the orchestrator both consume this package: adapters use
// it to decide which extension fields to emit (drop vs preserve), the
// orchestrator uses it to compute per-instance LossyReasons. Both reads
// resolve against the same data, so the "lossy on this host" question
// has one answer regardless of which side asks it.
package schema

import "embed"

//go:embed *.yaml
var files embed.FS
