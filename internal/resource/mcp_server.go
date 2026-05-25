package resource

import (
	"encoding/json"
	"fmt"
	"sort"
)

// MCPServer mirrors schema/mcp-server.yaml's universal core (command,
// args, url, env) plus host-extension fields the per-instance lossy
// check inspects (access via Extensions()).
//
// Name is the server name — the JSON/TOML map key in the source file,
// promoted to a field on the canonical struct so adapters can use it for
// path composition ($.mcpServers.<Name> / mcp_servers.<Name>) and the
// manifest record ID derivation (claude-code:mcp-server:<Name>) without
// re-reading the source.
//
// Transport: ADR-0014 + ADR-0016 §7 discriminate by field presence —
// stdio is (Command, Args), HTTP is URL. The validator enforces "exactly
// one of Command/URL is present" + "Args required when Command present"
// at parse-time gate; the adapter emits whichever transport fields the
// validated resource carries.
//
// Env is the stdio process env-var map (per schema/mcp-server.yaml's
// env field). Codex spec extends with `env_vars` (per-var source =
// local|remote) and `bearer_token_env_var`; those are extensions per the
// schema's deliberately_excluded list, not universal core.
//
// Source format is the wrapper-style fragment a user can copy-paste from
// a real .mcp.json (per advisor's resolution of Branch A): exactly one
// mcpServers entry with the server name as the map key. Multi-entry
// sources are rejected — one resource = one server.
type MCPServer struct {
	Name       string
	Command    string
	Args       []string
	URL        string
	Env        map[string]string
	Raw        []byte
	extensions map[string]any
}

// Kind returns KindMCPServer so *MCPServer satisfies the Resource interface.
func (m *MCPServer) Kind() Kind { return KindMCPServer }

// ResourceName returns the server name. Identity for the manifest ID
// (claude-code:mcp-server:<name>); also the JSON/TOML map key the
// configfrag adapter writes under.
func (m *MCPServer) ResourceName() string { return m.Name }

// Extensions returns the host-extension fields the schema's §8 lossy
// detection walks. Returns nil when the source carried only universal
// core. The map's keys are the source's original spellings (e.g.,
// `http_headers` or `headers`) — the schema's aliases array names both
// when applicable, so lossy detection finds the canonical concept
// regardless of which spelling the author used.
func (m *MCPServer) Extensions() map[string]any { return m.extensions }

// WithExtensions sets the extension map and returns the receiver. Drops
// Raw so a future byte-pass-through emit cannot lie about a stale Raw
// vs. mutated extensions (same shape as Skill.WithExtensions /
// Agent.WithExtensions).
func (m *MCPServer) WithExtensions(ext map[string]any) *MCPServer {
	m.extensions = ext
	m.Raw = nil
	return m
}

// ParseMCPServer parses a .mcp.json-style fragment into an *MCPServer.
// Accepts the wrapper shape {"mcpServers": {"<name>": {...fields...}}}
// per advisor's resolution of Branch A — copy-paste from real .mcp.json
// works with zero edits. The wrapper key MUST be `mcpServers` (camelCase,
// the canonical name per ADR-0016 §5); codex's snake_case `mcp_servers`
// is a TOML-emit concern, not a JSON-source concern.
//
// Multi-entry sources are rejected: dotpack's resource = one server
// install unit. Splitting a multi-entry .mcp.json into N resources is a
// translator-level concern (out of MVP).
//
// Fields the schema names as universal core (command, args, url, env)
// are extracted into typed fields; everything else lands in extensions
// for the §8 lossy check to inspect (codex's enabled_tools, http_headers,
// bearer_token_env_var, etc. — see schema/mcp-server.yaml's
// deliberately_excluded entries).
func ParseMCPServer(raw []byte) (*MCPServer, error) {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("mcp-server: parse JSON: %w", err)
	}
	if len(wrapper) == 0 {
		return nil, fmt.Errorf("mcp-server: empty source (expected {\"mcpServers\": {\"<name>\": {...}}})")
	}
	if len(wrapper) > 1 {
		keys := sortedTopKeys(wrapper)
		return nil, fmt.Errorf("mcp-server: source has multiple top-level keys %v; expected only mcpServers", keys)
	}
	serversRaw, ok := wrapper["mcpServers"]
	if !ok {
		keys := sortedTopKeys(wrapper)
		return nil, fmt.Errorf("mcp-server: top-level key %v is not \"mcpServers\" (per ADR-0016 §5 the canonical wrapper is camelCase mcpServers)", keys)
	}

	var servers map[string]json.RawMessage
	if err := json.Unmarshal(serversRaw, &servers); err != nil {
		return nil, fmt.Errorf("mcp-server: parse mcpServers value as map: %w", err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("mcp-server: mcpServers map is empty (one resource = one server entry)")
	}
	if len(servers) > 1 {
		names := sortedTopKeys(servers)
		return nil, fmt.Errorf("mcp-server: source has %d server entries %v; one resource = one server (split the source into separate files or use a translator)", len(servers), names)
	}

	var name string
	var entryRaw json.RawMessage
	for k, v := range servers {
		name = k
		entryRaw = v
	}

	var entry map[string]any
	if err := json.Unmarshal(entryRaw, &entry); err != nil {
		return nil, fmt.Errorf("mcp-server: parse mcpServers[%q] value as object: %w", name, err)
	}

	srv := &MCPServer{Name: name, Raw: append([]byte(nil), raw...)}
	ext := map[string]any{}
	for k, v := range entry {
		switch k {
		case "command":
			if s, ok := v.(string); ok {
				srv.Command = s
			} else {
				return nil, fmt.Errorf("mcp-server: %s.command must be a string; got %T", name, v)
			}
		case "args":
			arr, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("mcp-server: %s.args must be a string array; got %T", name, v)
			}
			// Pre-allocate so an explicit "args": [] yields a non-nil empty
			// slice. ValidateMCPServer distinguishes nil-vs-empty: nil/absent
			// fails ("args required when command is set"); explicit []
			// passes ("explicit no-args invocation"). Without this pre-alloc
			// the loop never runs for [] and srv.Args stays nil, silently
			// flipping the validator outcome opposite to what the docstring
			// promises — hostile-review #1.
			srv.Args = make([]string, 0, len(arr))
			for i, item := range arr {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("mcp-server: %s.args[%d] must be a string; got %T", name, i, item)
				}
				srv.Args = append(srv.Args, s)
			}
		case "url":
			if s, ok := v.(string); ok {
				srv.URL = s
			} else {
				return nil, fmt.Errorf("mcp-server: %s.url must be a string; got %T", name, v)
			}
		case "env":
			obj, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("mcp-server: %s.env must be a string-to-string map; got %T", name, v)
			}
			srv.Env = make(map[string]string, len(obj))
			for ek, ev := range obj {
				s, ok := ev.(string)
				if !ok {
					return nil, fmt.Errorf("mcp-server: %s.env[%q] must be a string; got %T", name, ek, ev)
				}
				srv.Env[ek] = s
			}
		default:
			ext[k] = v
		}
	}
	if len(ext) > 0 {
		srv.extensions = ext
	}
	return srv, nil
}

// sortedTopKeys returns the keys of m in lexical order for stable error
// messages. Generic over json.RawMessage and any since both call sites
// want the same stable rendering.
func sortedTopKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
