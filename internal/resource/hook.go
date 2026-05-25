package resource

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Hook mirrors schema/hook.yaml's two-level nested config-fragment
// shape: a top-level event map ("PreToolUse" → []Binding) plus a list
// of bindings (matcher + []HookSpec) per event. Bindings hold the
// matcher pattern and the hook-spec leaves the host actually executes.
//
// Universal core (the fields adapters always carry): event names
// (PascalCase per ADR-0016 §5; Claude/Codex identity, Gemini's
// BeforeTool/AfterTool aliased per schema cross_ecosystem_event_aliases),
// matcher (optional), and the hook-spec quartet of type, command,
// timeout, statusMessage, env.
//
// Extensions captures host-only fields the §8 lossy-detection algorithm
// inspects — Gemini's hook-execution-mode flags (async, once) and the
// hook-registry identifiers (name, description) per schema
// deliberately_excluded. Extension fields are stored alongside their
// owning binding-or-spec record so the adapter that natively supports
// them can re-emit them; the orchestrator's §8 walk inspects the flat
// per-resource map.
//
// Name is filename-derived (loadResource strips .hook.json / .json /
// .hook from the source basename). Hooks have no in-source name field
// (no frontmatter, no map-key wrapper like mcp-server), so the filesystem
// encodes identity. Empty Name fails validation downstream.
type Hook struct {
	Name       string
	Events     []EventBinding
	Raw        []byte
	extensions map[string]any
}

// EventBinding is one event with its ordered list of matcher-groups.
// Event name is canonical (PascalCase per ADR-0016 §5); the schema's
// cross_ecosystem_event_aliases drives per-host rewriting in the
// adapter's emit function (identity on claude+codex; Gemini-specific
// for BeforeTool/AfterTool).
type EventBinding struct {
	Event    string
	Bindings []Binding
}

// Binding is one matcher-group within an event. Matcher is the regex/
// glob filter ("Bash", "Edit|Write", "^mcp__.*$") applied against the
// tool name (or tool name / file path for Gemini); absence is "match
// everything" per schema.binding.fields[0].notes.
type Binding struct {
	Matcher string
	Hooks   []HookSpec
}

// HookSpec is the leaf handler. Type is always "command" in the
// corpus (Codex documents prompt + agent handlers but tags them
// "parsed but skipped"; only command actually runs). Timeout is in
// canonical seconds — Gemini's milliseconds get a *1000 rewrite at
// emit time per ADR-0016 §6.
type HookSpec struct {
	Type          string
	Command       string
	Timeout       int  // 0 = unset (timeout is optional per schema)
	HasTimeout    bool // distinguishes "explicit 0" from "absent"
	StatusMessage string
	Env           map[string]string
}

// Extensions returns the host-extension fields the schema's §8 lossy
// detection walks. Returns nil when the source carried only universal
// core. Keys mirror their source spellings (e.g., `async`, `once`,
// `name`, `description` for Gemini) so the schema's aliases array
// finds the canonical concept regardless of host of origin.
func (h *Hook) Extensions() map[string]any { return h.extensions }

// WithExtensions sets the extension map and returns the receiver,
// dropping Raw so future byte-pass-through emits cannot lie about a
// stale Raw vs mutated extensions (same shape as Skill.WithExtensions /
// MCPServer.WithExtensions).
func (h *Hook) WithExtensions(ext map[string]any) *Hook {
	h.extensions = ext
	h.Raw = nil
	return h
}

// WithName sets Name post-parse. ParseHook leaves Name empty because
// hook source carries no in-source name; the CLI's loadResource
// derives the name from the source filename and calls WithName before
// returning. This keeps ParseHook a pure function of the source bytes
// (callable from tests + future translators without a filesystem
// dependency).
func (h *Hook) WithName(name string) *Hook {
	h.Name = name
	return h
}

// ParseHook parses a $.hooks-shaped fragment into a *Hook. The expected
// source shape is a copy-pastable slice of a real .claude/settings.json
// (or .gemini/settings.json after translation): a top-level "hooks"
// wrapper key containing an event-name → binding-list map.
//
//	{
//	  "hooks": {
//	    "PreToolUse": [
//	      { "matcher": "Bash", "hooks": [{ "type": "command", ... }] }
//	    ]
//	  }
//	}
//
// Multi-event sources are accepted (one resource can install bindings
// to several events). Empty events map is rejected (zero merged keys
// would be an install that does nothing).
//
// Hook-spec fields not in the universal core (type, command, timeout,
// statusMessage, env) land in Extensions for the schema-driven §8
// lossy check. Gemini's `async`/`once` and registry `name`/`description`
// are the corpus-attested cases per schema/hook.yaml's
// deliberately_excluded entries.
func ParseHook(raw []byte) (*Hook, error) {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("hook: parse JSON: %w", err)
	}
	if len(wrapper) == 0 {
		return nil, fmt.Errorf("hook: empty source (expected {\"hooks\": {\"<Event>\": [...]}})")
	}
	if len(wrapper) > 1 {
		keys := sortedTopKeys(wrapper)
		return nil, fmt.Errorf("hook: source has multiple top-level keys %v; expected only \"hooks\"", keys)
	}
	hooksRaw, ok := wrapper["hooks"]
	if !ok {
		keys := sortedTopKeys(wrapper)
		return nil, fmt.Errorf("hook: top-level key %v is not \"hooks\" (per schema/hook.yaml template.source_locations the wrapper is $.hooks)", keys)
	}

	var events map[string]json.RawMessage
	if err := json.Unmarshal(hooksRaw, &events); err != nil {
		return nil, fmt.Errorf("hook: parse hooks value as event map: %w", err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("hook: empty hooks map (one resource must install at least one binding)")
	}

	hook := &Hook{Raw: append([]byte(nil), raw...)}
	ext := map[string]any{}

	// Sort events for stable emit order (manifest selectors are
	// content-hash so order doesn't change identity, but the install
	// plan's deterministic order matters for cache_key stability and
	// test assertions).
	names := make([]string, 0, len(events))
	for k := range events {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, event := range names {
		bindRaw := events[event]
		var bindings []json.RawMessage
		if err := json.Unmarshal(bindRaw, &bindings); err != nil {
			return nil, fmt.Errorf("hook: %s: parse as binding array: %w", event, err)
		}
		if len(bindings) == 0 {
			return nil, fmt.Errorf("hook: %s: empty binding array (an event listed with no bindings is meaningless)", event)
		}
		eb := EventBinding{Event: event}
		for i, br := range bindings {
			var b map[string]any
			if err := json.Unmarshal(br, &b); err != nil {
				return nil, fmt.Errorf("hook: %s[%d]: parse as binding object: %w", event, i, err)
			}
			binding, bindExt, err := parseBinding(event, i, b)
			if err != nil {
				return nil, err
			}
			for k, v := range bindExt {
				ext[k] = v
			}
			eb.Bindings = append(eb.Bindings, binding)
		}
		hook.Events = append(hook.Events, eb)
	}
	if len(ext) > 0 {
		hook.extensions = ext
	}
	return hook, nil
}

// parseBinding walks one binding object. Returns the typed Binding and
// any extension fields hoisted from the binding or its hook-spec
// leaves. Extension field keys are flattened into the per-resource
// extensions map — the §8 algorithm walks a flat map keyed by source
// field name and consults schema/hook.yaml's aliases to find the
// canonical concept; nesting would require a schema shape we don't
// have.
func parseBinding(event string, idx int, b map[string]any) (Binding, map[string]any, error) {
	var binding Binding
	ext := map[string]any{}
	for k, v := range b {
		switch k {
		case "matcher":
			s, ok := v.(string)
			if !ok {
				return Binding{}, nil, fmt.Errorf("hook: %s[%d].matcher must be a string; got %T", event, idx, v)
			}
			binding.Matcher = s
		case "hooks":
			arr, ok := v.([]any)
			if !ok {
				return Binding{}, nil, fmt.Errorf("hook: %s[%d].hooks must be an array; got %T", event, idx, v)
			}
			for j, item := range arr {
				m, ok := item.(map[string]any)
				if !ok {
					return Binding{}, nil, fmt.Errorf("hook: %s[%d].hooks[%d] must be an object; got %T", event, idx, j, item)
				}
				spec, specExt, err := parseHookSpec(event, idx, j, m)
				if err != nil {
					return Binding{}, nil, err
				}
				for ek, ev := range specExt {
					ext[ek] = ev
				}
				binding.Hooks = append(binding.Hooks, spec)
			}
		default:
			// Binding-level extension (Gemini's async / once /
			// registry identifiers land here per schema/hook.yaml's
			// deliberately_excluded entries).
			ext[k] = v
		}
	}
	if len(binding.Hooks) == 0 {
		return Binding{}, nil, fmt.Errorf("hook: %s[%d].hooks is required and must contain at least one hook-spec", event, idx)
	}
	return binding, ext, nil
}

// parseHookSpec walks one hook-spec leaf. Type, command, timeout,
// statusMessage, env are universal core; anything else lands in
// extensions for §8 inspection.
func parseHookSpec(event string, bindIdx, specIdx int, m map[string]any) (HookSpec, map[string]any, error) {
	var spec HookSpec
	ext := map[string]any{}
	for k, v := range m {
		switch k {
		case "type":
			s, ok := v.(string)
			if !ok {
				return HookSpec{}, nil, fmt.Errorf("hook: %s[%d].hooks[%d].type must be a string; got %T", event, bindIdx, specIdx, v)
			}
			spec.Type = s
		case "command":
			s, ok := v.(string)
			if !ok {
				return HookSpec{}, nil, fmt.Errorf("hook: %s[%d].hooks[%d].command must be a string; got %T", event, bindIdx, specIdx, v)
			}
			spec.Command = s
		case "timeout":
			// JSON numbers decode as float64; demand a whole number.
			switch n := v.(type) {
			case float64:
				if n != float64(int(n)) {
					return HookSpec{}, nil, fmt.Errorf("hook: %s[%d].hooks[%d].timeout must be an integer; got %v", event, bindIdx, specIdx, v)
				}
				spec.Timeout = int(n)
				spec.HasTimeout = true
			default:
				return HookSpec{}, nil, fmt.Errorf("hook: %s[%d].hooks[%d].timeout must be an integer; got %T", event, bindIdx, specIdx, v)
			}
		case "statusMessage":
			s, ok := v.(string)
			if !ok {
				return HookSpec{}, nil, fmt.Errorf("hook: %s[%d].hooks[%d].statusMessage must be a string; got %T", event, bindIdx, specIdx, v)
			}
			spec.StatusMessage = s
		case "env":
			obj, ok := v.(map[string]any)
			if !ok {
				return HookSpec{}, nil, fmt.Errorf("hook: %s[%d].hooks[%d].env must be a string-to-string map; got %T", event, bindIdx, specIdx, v)
			}
			spec.Env = make(map[string]string, len(obj))
			for ek, ev := range obj {
				s, ok := ev.(string)
				if !ok {
					return HookSpec{}, nil, fmt.Errorf("hook: %s[%d].hooks[%d].env[%q] must be a string; got %T", event, bindIdx, specIdx, ek, ev)
				}
				spec.Env[ek] = s
			}
		default:
			// Hook-spec-level extension (Gemini's hook registry name /
			// description per schema/hook.yaml).
			ext[k] = v
		}
	}
	return spec, ext, nil
}
