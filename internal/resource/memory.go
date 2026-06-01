package resource

// Memory represents a dotpack memory file (e.g. GEMINI.md, CLAUDE.md).
type Memory struct {
	Name       string
	Body       string
	Raw        []byte
	extensions map[string]any
}

func (m *Memory) Kind() Kind                 { return KindMemory }
func (m *Memory) Extensions() map[string]any { return m.extensions }
func (m *Memory) ResourceName() string       { return m.Name }

func (m *Memory) WithName(name string) *Memory {
	m.Name = name
	return m
}

// ParseMemory parses a memory file. Memory files have no frontmatter.
func ParseMemory(raw []byte) (*Memory, error) {
	return &Memory{
		Body: string(raw),
		Raw:  append([]byte(nil), raw...),
	}, nil
}
