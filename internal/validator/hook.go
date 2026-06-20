package validator

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ellarock-software/dotpack/internal/resource"
)

// hookNameRE constrains the filename-derived hook bundle name to the
// same kebab/alphanumeric shape used by skill names. The name lands in
// the manifest record ID (claude-code:hook:<name>) and arbitrary
// filename characters would break short-name extraction (uninstall's
// trailing-colon split) and produce ugly IDs on `dotpack list`.
var hookNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// canonicalHookEvents mirrors schema/hook.yaml's canonical_event_names
// list — every event with >= 2-file corpus evidence across the 7
// surveyed files. Authors using a non-canonical event name (or a
// misspelling like "PreToolsUse") are almost certainly making a
// mistake; failing loudly here surfaces the bug at install time
// rather than letting the host silently ignore the binding.
//
// Gemini's BeforeTool / AfterTool are NOT in this set — the translator
// canonicalises Gemini sources to PascalCase per ADR-0012 §5 before
// the validator runs.
var canonicalHookEvents = map[string]struct{}{
	"PreToolUse":         {},
	"PostToolUse":        {},
	"UserPromptSubmit":   {},
	"SessionStart":       {},
	"SessionEnd":         {},
	"SubagentStart":      {},
	"SubagentStop":       {},
	"PermissionRequest":  {},
	"PreCompact":         {},
	"PostCompact":        {},
	"Stop":               {},
	"Notification":       {},
	"PostToolUseFailure": {},
}

// hookTimeoutSecondsSaneCeiling is the shanraisshan-anomaly guard from
// schema/hook.yaml's ecosystem_notes. 600 seconds = 10 minutes; corpus
// values for Claude/Codex range 3–90. Values above this threshold are
// almost certainly a unit-confusion bug (the corpus has 5000 and
// 30000 in a Claude-seconds field — the author thought it was
// milliseconds, so their hooks effectively never timed out).
//
// Deviation from ADR-0012 §6: the ADR text says "the validator emits
// a WARNING when canonical timeout > 600". The validator framework
// has no warnings channel (errors block, nothing else does), so this
// slice TIGHTENS the behaviour to a blocking error per ADR-0011's
// "fail-loud-not-silent" deviation criterion 2. A user who legitimately
// wants a >10-minute timeout (no corpus precedent) must lower the
// value to install; --force does not bypass validation. If the
// warnings channel ever lands (separate slice), this guard reverts
// to a warning and the docstring drops this paragraph.
const hookTimeoutSecondsSaneCeiling = 600

// ValidateHook checks the per-instance invariants ADR-0012 §5–§6 +
// schema/hook.yaml promise to gate at parse-time:
//
//  1. Name is non-empty and kebab-shaped (see hookNameRE).
//  2. Every event name is in canonicalHookEvents (PascalCase per ADR
//     §5; Gemini's BeforeTool/AfterTool are translator-side concerns).
//  3. Every binding has at least one hook-spec.
//  4. Every hook-spec has type="command" (the only value any host
//     parses today per schema/hook.yaml hook_spec.fields[0].notes;
//     other types are spec-tagged "parsed but skipped").
//  5. Every hook-spec has a non-empty command string.
//  6. Timeouts above the sane ceiling are flagged per ADR §6's
//     shanraisshan-anomaly guard.
//
// Extensions are NOT validated here — per-instance lossy detection
// (ADR-0012 §8) is the orchestrator's job, not the validator's.
func ValidateHook(h *resource.Hook) []ValidationError {
	var errs []ValidationError

	switch {
	case strings.TrimSpace(h.Name) == "":
		errs = append(errs, ValidationError{
			Field:   "name",
			Message: "required (filename-derived; the manifest record ID is claude-code:hook:<name>)",
		})
	case !hookNameRE.MatchString(h.Name):
		errs = append(errs, ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("must match %s (lowercase letters, digits, hyphens; first char alphanumeric)", hookNameRE.String()),
		})
	}

	if len(h.Events) == 0 {
		errs = append(errs, ValidationError{
			Field:   "events",
			Message: "no events found (one resource must install at least one binding)",
		})
	}

	for _, ev := range h.Events {
		if _, ok := canonicalHookEvents[ev.Event]; !ok {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("hooks.%s", ev.Event),
				Message: fmt.Sprintf("event name not in schema/hook.yaml canonical_event_names (most likely a typo — Claude+Codex use PascalCase identity; Gemini's BeforeTool/AfterTool is translator-side)"),
			})
			continue
		}
		for bi, b := range ev.Bindings {
			if len(b.Hooks) == 0 {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("hooks.%s[%d].hooks", ev.Event, bi),
					Message: "binding has no hook-spec entries (matcher with no handlers is meaningless)",
				})
				continue
			}
			for si, spec := range b.Hooks {
				field := fmt.Sprintf("hooks.%s[%d].hooks[%d]", ev.Event, bi, si)
				if spec.Type != "command" {
					errs = append(errs, ValidationError{
						Field:   field + ".type",
						Message: fmt.Sprintf("must be \"command\" (got %q; the spec documents `prompt`/`agent` but tags them \"parsed but skipped\" — only command actually runs)", spec.Type),
					})
				}
				if strings.TrimSpace(spec.Command) == "" {
					errs = append(errs, ValidationError{
						Field:   field + ".command",
						Message: "required (the shell command the host executes)",
					})
				}
				if spec.HasTimeout && spec.Timeout > hookTimeoutSecondsSaneCeiling {
					errs = append(errs, ValidationError{
						Field: field + ".timeout",
						Message: fmt.Sprintf(
							"%d seconds exceeds the sane ceiling of %d — almost certainly a unit-confusion bug (Claude/Codex are seconds, Gemini is milliseconds; corpus has 5000/30000 in a Claude-seconds field, see schema/hook.yaml ecosystem_notes). If you really mean a >10-minute timeout, edit the source after the canonicalisation step.",
							spec.Timeout, hookTimeoutSecondsSaneCeiling),
					})
				}
				if spec.HasTimeout && spec.Timeout < 0 {
					errs = append(errs, ValidationError{
						Field:   field + ".timeout",
						Message: fmt.Sprintf("must be non-negative (got %d)", spec.Timeout),
					})
				}
			}
		}
	}

	return errs
}
