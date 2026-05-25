package resource

import (
	"strings"
	"testing"
)

func TestParseHook_SingleEventSingleBinding(t *testing.T) {
	raw := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/bin/echo hi","timeout":5}]}]}}`)
	h, err := ParseHook(raw)
	if err != nil {
		t.Fatalf("ParseHook: %v", err)
	}
	if len(h.Events) != 1 {
		t.Fatalf("expected 1 event; got %d", len(h.Events))
	}
	if h.Events[0].Event != "PreToolUse" {
		t.Errorf("event = %q; want PreToolUse", h.Events[0].Event)
	}
	if len(h.Events[0].Bindings) != 1 {
		t.Fatalf("expected 1 binding; got %d", len(h.Events[0].Bindings))
	}
	b := h.Events[0].Bindings[0]
	if b.Matcher != "Bash" {
		t.Errorf("matcher = %q; want Bash", b.Matcher)
	}
	if len(b.Hooks) != 1 || b.Hooks[0].Command != "/bin/echo hi" {
		t.Errorf("hook command = %v; want /bin/echo hi", b.Hooks[0].Command)
	}
	if !b.Hooks[0].HasTimeout || b.Hooks[0].Timeout != 5 {
		t.Errorf("timeout = %v / has=%v; want 5/true", b.Hooks[0].Timeout, b.Hooks[0].HasTimeout)
	}
}

func TestParseHook_MultiEventSortedByName(t *testing.T) {
	raw := []byte(`{"hooks":{
		"PostToolUse":[{"hooks":[{"type":"command","command":"/bin/post"}]}],
		"PreToolUse":[{"hooks":[{"type":"command","command":"/bin/pre"}]}]
	}}`)
	h, err := ParseHook(raw)
	if err != nil {
		t.Fatalf("ParseHook: %v", err)
	}
	if len(h.Events) != 2 {
		t.Fatalf("expected 2 events; got %d", len(h.Events))
	}
	// Events sorted alphabetically — Post precedes Pre.
	if h.Events[0].Event != "PostToolUse" || h.Events[1].Event != "PreToolUse" {
		t.Errorf("events not sorted: %v", h.Events)
	}
}

func TestParseHook_ExtensionFieldsHoisted(t *testing.T) {
	raw := []byte(`{"hooks":{"PreToolUse":[{
		"matcher":"Bash",
		"async": true,
		"name": "my-hook",
		"hooks":[{
			"type":"command",
			"command":"/bin/foo",
			"description": "fancy"
		}]
	}]}}`)
	h, err := ParseHook(raw)
	if err != nil {
		t.Fatalf("ParseHook: %v", err)
	}
	ext := h.Extensions()
	if ext == nil {
		t.Fatalf("expected extensions to be populated; got nil")
	}
	if ext["async"] != true {
		t.Errorf("ext[async] = %v; want true", ext["async"])
	}
	if ext["name"] != "my-hook" {
		t.Errorf("ext[name] = %v; want my-hook", ext["name"])
	}
	if ext["description"] != "fancy" {
		t.Errorf("ext[description] = %v; want fancy", ext["description"])
	}
}

func TestParseHook_Rejections(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		errPart string
	}{
		{"empty", `{}`, "empty source"},
		{"wrong wrapper", `{"hookz": {"PreToolUse": []}}`, "not \"hooks\""},
		{"multi top-level", `{"hooks": {"PreToolUse": [{"hooks":[{"type":"command","command":"x"}]}]}, "other": 1}`, "multiple top-level"},
		{"empty events map", `{"hooks": {}}`, "empty hooks map"},
		{"event not array", `{"hooks": {"PreToolUse": "foo"}}`, "parse as binding array"},
		{"binding without hooks", `{"hooks": {"PreToolUse": [{"matcher": "Bash"}]}}`, "is required"},
		{"matcher wrong type", `{"hooks": {"PreToolUse": [{"matcher": 1, "hooks":[{"type":"command","command":"x"}]}]}}`, "matcher must be a string"},
		{"timeout float", `{"hooks": {"PreToolUse": [{"hooks":[{"type":"command","command":"x","timeout":5.5}]}]}}`, "timeout must be an integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseHook([]byte(tc.src))
			if err == nil {
				t.Fatalf("expected error containing %q; got nil", tc.errPart)
			}
			if !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errPart)
			}
		})
	}
}

func TestHook_WithName(t *testing.T) {
	h, err := ParseHook([]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"x"}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "" {
		t.Errorf("ParseHook should leave Name empty for the CLI to fill; got %q", h.Name)
	}
	h.WithName("my-hook")
	if h.Name != "my-hook" {
		t.Errorf("WithName: got %q; want my-hook", h.Name)
	}
}
