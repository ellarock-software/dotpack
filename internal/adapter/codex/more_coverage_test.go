package codex

import "testing"

func TestCodexHookEventValueDefinesHooksShapes(t *testing.T) {
	cases := []struct {
		value any
		want  bool
	}{
		{nil, false},
		{[]any{}, false},
		{[]any{map[string]any{}}, true},
		{map[string]any{}, false},
		{map[string]any{"x": "y"}, true},
		{"", false},
		{"x", true},
		{1, true},
	}
	for _, tc := range cases {
		if got := codexHookEventValueDefinesHooks(tc.value); got != tc.want {
			t.Fatalf("codexHookEventValueDefinesHooks(%#v)=%v; want %v", tc.value, got, tc.want)
		}
	}
}
