package resource

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// Command represents a dotpack command.
type Command struct {
	Name         string
	Description  string
	Model        string
	AllowedTools []string
	Prompt       string
	Raw          []byte
	extensions   map[string]any
}

func (c *Command) Kind() Kind                 { return KindCommand }
func (c *Command) Extensions() map[string]any { return c.extensions }
func (c *Command) ResourceName() string       { return c.Name }

func (c *Command) WithName(name string) *Command {
	c.Name = name
	return c
}

// ParseCommand parses a command from either a markdown file (with YAML frontmatter)
// or a TOML file.
func ParseCommand(raw []byte) (*Command, error) {
	if len(raw) >= 3 && string(raw[:3]) == "---" {
		return parseCommandMarkdown(raw)
	}
	return parseCommandTOML(raw)
}

func parseCommandMarkdown(raw []byte) (*Command, error) {
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("command: %w", err)
	}

	fields := map[string]any{}
	if err := yaml.Unmarshal(front, &fields); err != nil {
		return nil, fmt.Errorf("command: parse frontmatter: %w", err)
	}

	cmd := &Command{Prompt: strings.TrimSpace(string(body)), Raw: append([]byte(nil), raw...)}
	for key, val := range fields {
		switch key {
		case "name":
			cmd.Name, _ = val.(string)
		case "description":
			cmd.Description, _ = val.(string)
		case "model":
			cmd.Model, _ = val.(string)
		case "allowed-tools":
			tools, err := normaliseTools(val)
			if err != nil {
				return nil, fmt.Errorf("command: allowed-tools: %w", err)
			}
			cmd.AllowedTools = tools
		default:
			if cmd.extensions == nil {
				cmd.extensions = map[string]any{}
			}
			cmd.extensions[key] = val
		}
	}
	return cmd, nil
}

func parseCommandTOML(raw []byte) (*Command, error) {
	fields := map[string]any{}
	if err := toml.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("command: toml parse: %w", err)
	}

	cmd := &Command{Raw: append([]byte(nil), raw...)}
	for key, val := range fields {
		switch key {
		case "name":
			cmd.Name, _ = val.(string)
		case "prompt":
			cmd.Prompt, _ = val.(string)
		case "description":
			cmd.Description, _ = val.(string)
		case "model":
			cmd.Model, _ = val.(string)
		case "allowed-tools":
			tools, err := normaliseTools(val)
			if err != nil {
				return nil, fmt.Errorf("command: allowed-tools: %w", err)
			}
			cmd.AllowedTools = tools
		default:
			if cmd.extensions == nil {
				cmd.extensions = map[string]any{}
			}
			cmd.extensions[key] = val
		}
	}
	return cmd, nil
}
