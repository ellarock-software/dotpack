// Package all blank-imports every concrete adapter package so their
// init() self-registrations run (ADR-0014). Commands import this package
// once instead of naming each host; onboarding a new host is a one-line
// blank import here plus the host's own package — no edit to CLI core.
//
// This is the ONLY package that may import the concrete adapter
// sub-packages for registration. internal/adapter/registry must stay
// free of these imports to avoid an import cycle (the concretes depend
// on registry, not the reverse).
package all

import (
	// Per-host adapters (each self-registers in init()).
	_ "github.com/ellarock-software/dotpack/internal/adapter/antigravity"
	_ "github.com/ellarock-software/dotpack/internal/adapter/claudecode"
	_ "github.com/ellarock-software/dotpack/internal/adapter/codex"
	_ "github.com/ellarock-software/dotpack/internal/adapter/gemini"
	_ "github.com/ellarock-software/dotpack/internal/adapter/opencode"
)
