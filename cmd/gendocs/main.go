package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type FullSchema struct {
	Kind                 string    `yaml:"kind"`
	Version              int       `yaml:"dotpack_schema_version"`
	Template             Template  `yaml:"template"`
	Fields               []Field   `yaml:"fields"`
	EcosystemNotes       []string  `yaml:"ecosystem_notes"`
	DeliberatelyExcluded []Concept `yaml:"deliberately_excluded"`
}

type Template struct {
	Shape    string `yaml:"shape"`
	Filename string `yaml:"filename"`
	Body     string `yaml:"body"`
}

type Field struct {
	Name      string      `yaml:"name"`
	Type      string      `yaml:"type"`
	Required  interface{} `yaml:"required"`
	AppearsIn string      `yaml:"appears_in"`
	Notes     string      `yaml:"notes"`
}

type Concept struct {
	CanonicalConcept string `yaml:"canonical_concept"`
	Aliases          []struct {
		Host      string `yaml:"host"`
		FieldName string `yaml:"field_name"`
	} `yaml:"aliases"`
	FieldNames               []string `yaml:"field_names"`
	LossyWhenDropped         *bool    `yaml:"lossy_when_dropped"`
	CanonicalisesTo          string   `yaml:"canonicalises_to"`
	AppearedIn               string   `yaml:"appeared_in"`
	Reason                   string   `yaml:"reason"`
	BodySectionNormalisation *struct {
		Reason string `yaml:"reason"`
	} `yaml:"body_section_normalisation"`
}

func main() {
	// go generate runs this from the schema/ directory
	schemaDir := "."
	docsDir := filepath.Join("..", "docs", "schemas")

	if err := os.MkdirAll(docsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create docs dir: %v\n", err)
		os.Exit(1)
	}

	files, err := os.ReadDir(schemaDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read schema dir: %v\n", err)
		os.Exit(1)
	}

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".yaml") {
			continue
		}

		path := filepath.Join(schemaDir, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read %s: %v\n", path, err)
			os.Exit(1)
		}

		var s FullSchema
		if err := yaml.Unmarshal(data, &s); err != nil {
			fmt.Fprintf(os.Stderr, "failed to parse %s: %v\n", path, err)
			os.Exit(1)
		}

		md := generateMarkdown(s)

		outPath := filepath.Join(docsDir, strings.TrimSuffix(file.Name(), ".yaml")+".md")
		if err := os.WriteFile(outPath, []byte(md), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", outPath, err)
			os.Exit(1)
		}
		fmt.Printf("Generated %s\n", outPath)
	}

	// Create an index file.
	indexContent := "# dotpack Documentation\n\n"
	indexContent += "dotpack validates portable `.agents` resources and translates them into host-native agent configuration files.\n\n"
	indexContent += "Use this documentation to inspect the schema contracts, architecture decisions, and optional lifecycle hardening behavior.\n\n"
	indexContent += "## Start Here\n\n"
	indexContent += "- [Project README](https://github.com/ellarock-software/dotpack#readme)\n"
	indexContent += "- [Schema reference](schemas/skill.md)\n"
	indexContent += "- [Optional Sponsio lifecycle hardening](SPONSIO_INSTALL_INSTRUCTIONS.md)\n"
	indexContent += "- [Architecture decisions](adr/0001-empirically-derived-schema-via-corpus-survey.md)\n\n"
	indexContent += "## Schemas\n\n"
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".yaml") {
			kind := strings.TrimSuffix(file.Name(), ".yaml")
			indexContent += fmt.Sprintf("- [%s Schema](schemas/%s.md)\n", strings.Title(kind), kind)
		}
	}
	// Generate index.md
	os.WriteFile(filepath.Join("..", "docs", "index.md"), []byte(indexContent), 0644)

	// Generate mkdocs.yml
	generateMkDocsYML(files, filepath.Join("..", "docs", "adr"))
}

func generateMkDocsYML(schemaFiles []os.DirEntry, adrDir string) {
	var sb strings.Builder
	sb.WriteString("site_name: dotpack Schema Documentation\n")
	sb.WriteString("site_description: Documentation for the dotpack universal agent schemas\n")
	sb.WriteString("use_directory_urls: false\n")
	sb.WriteString("theme:\n  name: material\n  features:\n    - navigation.sections\n    - navigation.tabs\n    - search.suggest\n    - search.highlight\n\n")
	sb.WriteString("markdown_extensions:\n  - tables\n  - admonition\n  - attr_list\n  - def_list\n  - md_in_html\n\n")

	sb.WriteString("nav:\n")
	sb.WriteString("  - Home: index.md\n")
	sb.WriteString("  - Schemas:\n")
	for _, file := range schemaFiles {
		if strings.HasSuffix(file.Name(), ".yaml") {
			kind := strings.TrimSuffix(file.Name(), ".yaml")
			sb.WriteString(fmt.Sprintf("    - %s: schemas/%s.md\n", strings.Title(kind), kind))
		}
	}

	sb.WriteString("  - Optional Hardening:\n")
	sb.WriteString("    - Sponsio Lifecycle: SPONSIO_INSTALL_INSTRUCTIONS.md\n")
	sb.WriteString("    - Testing Sponsio: SPONSIO_TEST_INSTRUCTIONS.md\n")

	sb.WriteString("  - Standards & Architecture (ADR):\n")
	adrFiles, err := os.ReadDir(adrDir)
	if err == nil {
		for _, file := range adrFiles {
			if strings.HasSuffix(file.Name(), ".md") {
				// E.g. 0012-agents-cli-adapter... -> ADR-0012
				title := file.Name()
				parts := strings.SplitN(file.Name(), "-", 2)
				if len(parts) > 0 {
					title = "ADR " + parts[0]
				}
				sb.WriteString(fmt.Sprintf("    - %s: adr/%s\n", title, file.Name()))
			}
		}
	}

	sb.WriteString("  - Archive:\n")
	sb.WriteString("    - Archived Context: archive/CONTEXT-archived-2026-05-26.md\n")
	sb.WriteString("    - Archived ADR 0001: archive/adr/0001-llm-only-trust-gate-for-translated-resources.md\n")
	sb.WriteString("    - Archived ADR 0002: archive/adr/0002-pluggable-dotpack-agent-cmd-not-anthropic-sdk.md\n")
	sb.WriteString("    - Archived ADR 0004: archive/adr/0004-workdir-filesystem-handoff-agent-interface.md\n")
	sb.WriteString("    - Archived ADR 0006: archive/adr/0006-local-cache-plus-opt-in-upstream-pr.md\n")

	os.WriteFile(filepath.Join("..", "mkdocs.yml"), []byte(sb.String()), 0644)
}

func generateMarkdown(s FullSchema) string {
	var sb strings.Builder

	title := strings.Title(s.Kind)
	sb.WriteString(fmt.Sprintf("# %s Schema\n\n", title))

	sb.WriteString(fmt.Sprintf("**Version:** `%d`\n\n", s.Version))

	sb.WriteString("## Template\n\n")
	sb.WriteString(fmt.Sprintf("- **Shape:** `%s`\n", s.Template.Shape))
	sb.WriteString(fmt.Sprintf("- **Filename:** `%s`\n", s.Template.Filename))
	if s.Template.Body != "" {
		sb.WriteString(fmt.Sprintf("- **Body:** `%s`\n", s.Template.Body))
	}
	sb.WriteString("\n")

	if len(s.Fields) > 0 {
		sb.WriteString("## Fields\n\n")
		sb.WriteString("| Name | Type | Required | Notes |\n")
		sb.WriteString("| --- | --- | --- | --- |\n")
		for _, f := range s.Fields {
			reqStr := "No"
			if b, ok := f.Required.(bool); ok {
				if b {
					reqStr = "**Yes**"
				}
			} else if s, ok := f.Required.(string); ok {
				reqStr = fmt.Sprintf("`%s`", s)
			}
			notes := strings.ReplaceAll(strings.TrimSpace(f.Notes), "\n", " ")
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n", f.Name, f.Type, reqStr, notes))
		}
		sb.WriteString("\n")
	}

	if len(s.EcosystemNotes) > 0 {
		sb.WriteString("## Ecosystem Notes\n\n")
		for _, note := range s.EcosystemNotes {
			sb.WriteString(fmt.Sprintf("- %s\n", strings.TrimSpace(note)))
		}
		sb.WriteString("\n")
	}

	if len(s.DeliberatelyExcluded) > 0 {
		sb.WriteString("## Deliberately Excluded Concepts\n\n")
		for _, c := range s.DeliberatelyExcluded {
			if c.BodySectionNormalisation != nil {
				sb.WriteString("### Body Section Normalisation\n\n")
				sb.WriteString(fmt.Sprintf("%s\n\n", strings.TrimSpace(c.BodySectionNormalisation.Reason)))
				continue
			}

			sb.WriteString(fmt.Sprintf("### Concept: `%s`\n\n", c.CanonicalConcept))
			if c.Reason != "" {
				sb.WriteString(fmt.Sprintf("%s\n\n", strings.TrimSpace(c.Reason)))
			}

			if len(c.Aliases) > 0 {
				sb.WriteString("**Aliases:**\n\n")
				sb.WriteString("| Host | Field Name |\n")
				sb.WriteString("| --- | --- |\n")
				for _, a := range c.Aliases {
					sb.WriteString(fmt.Sprintf("| `%s` | `%s` |\n", a.Host, a.FieldName))
				}
				sb.WriteString("\n")
			}

			if len(c.FieldNames) > 0 {
				sb.WriteString(fmt.Sprintf("**Field Names:** `%s`\n\n", strings.Join(c.FieldNames, "`, `")))
			}
		}
	}

	result := sb.String()

	// Automatically hyperlink ADR references (e.g., ADR-0012)
	adrRegex := regexp.MustCompile(`ADR-(\d{4})`)
	// We'll read the docs/adr directory to map numbers to full filenames
	adrDir := filepath.Join("..", "docs", "adr")
	files, err := os.ReadDir(adrDir)
	if err == nil {
		adrMap := make(map[string]string)
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".md") {
				// extract number from "0012-agents-cli-adapter..."
				parts := strings.SplitN(f.Name(), "-", 2)
				if len(parts) > 0 {
					adrMap[parts[0]] = f.Name()
				}
			}
		}

		result = adrRegex.ReplaceAllStringFunc(result, func(match string) string {
			num := match[4:] // extract 0012
			if filename, exists := adrMap[num]; exists {
				return fmt.Sprintf("[%s](../adr/%s)", match, filename)
			}
			return match
		})
	}

	return result
}
