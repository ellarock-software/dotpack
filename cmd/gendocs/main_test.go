package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainGeneratesSchemaDocsIndexAndMkDocs(t *testing.T) {
	root := t.TempDir()
	schemaDir := filepath.Join(root, "schema")
	if err := os.MkdirAll(filepath.Join(root, "docs", "adr"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "adr", "0012-example.md"), []byte("# ADR\n"), 0o644); err != nil {
		t.Fatalf("write adr: %v", err)
	}
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir schema: %v", err)
	}
	schema := `kind: widget
dotpack_schema_version: 7
template:
  shape: file_with_frontmatter
  filename: WIDGET.md
  body: required
fields:
  - name: name
    type: string
    required: true
    notes: |
      Name field references ADR-0012.
  - name: mode
    type: string
    required: optional
    notes: Mode field.
ecosystem_notes:
  - Uses support files.
deliberately_excluded:
  - canonical_concept: host_only
    aliases:
      - host: example
        field_name: example-field
    field_names: [metadata]
    reason: |
      Host-only field.
  - body_section_normalisation:
      reason: |
        Body normalization note.
`
	if err := os.WriteFile(filepath.Join(schemaDir, "widget.yaml"), []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(schemaDir, "ignore.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write ignored: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	if err := os.Chdir(schemaDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	main()

	doc := mustRead(t, filepath.Join(root, "docs", "schemas", "widget.md"))
	for _, want := range []string{
		"# Widget Schema",
		"**Version:** `7`",
		"| `name` | `string` | **Yes** |",
		"| `mode` | `string` | `optional` |",
		"[ADR-0012](../adr/0012-example.md)",
		"### Body Section Normalisation",
		"**Aliases:**",
		"**Field Names:** `metadata`",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("generated doc missing %q:\n%s", want, doc)
		}
	}
	index := mustRead(t, filepath.Join(root, "docs", "index.md"))
	if !strings.Contains(index, "- [Widget Schema](schemas/widget.md)") {
		t.Fatalf("index missing widget schema:\n%s", index)
	}
	mkdocs := mustRead(t, filepath.Join(root, "mkdocs.yml"))
	if !strings.Contains(mkdocs, "    - Widget: schemas/widget.md") || !strings.Contains(mkdocs, "ADR 0012: adr/0012-example.md") {
		t.Fatalf("mkdocs missing nav entries:\n%s", mkdocs)
	}
}

func TestGenerateMarkdownWithoutOptionalSections(t *testing.T) {
	got := generateMarkdown(FullSchema{
		Kind:    "minimal",
		Version: 1,
		Template: Template{
			Shape:    "raw",
			Filename: "MIN.md",
		},
	})
	if !strings.Contains(got, "# Minimal Schema") || strings.Contains(got, "## Fields") {
		t.Fatalf("unexpected minimal markdown:\n%s", got)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
