// Package piexport converts the repository's deliberately small agent format
// into Pi artifacts. It does not install extensions or write live configuration.
package piexport

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Rule struct {
	Action, Resource, Effect string
}

type Agent struct {
	Description, Mode string
	Steps             int
	Rules             []Rule
	Body              string
}

// Parse accepts scalar frontmatter and the ordered permissions list used by
// this repository, not arbitrary YAML. Unsupported syntax fails before apply.
func Parse(data []byte) (Agent, error) {
	var a Agent
	s := string(data)
	lines := strings.SplitAfter(s, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return a, fmt.Errorf("missing YAML frontmatter")
	}
	offset, end := len(lines[0]), -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == "---" {
			end = i
			a.Body = s[offset+len(lines[i]):]
			break
		}
		offset += len(lines[i])
	}
	if end < 0 {
		return a, fmt.Errorf("unterminated frontmatter")
	}
	seen := map[string]bool{}
	inRules := false
	var fields map[string]string
	finish := func() error {
		if fields == nil {
			return nil
		}
		if len(fields) != 3 || fields["action"] == "" || fields["resource"] == "" || fields["effect"] == "" {
			return fmt.Errorf("permission requires action, resource and effect")
		}
		a.Rules = append(a.Rules, Rule{fields["action"], fields["resource"], fields["effect"]})
		fields = nil
		return nil
	}
	for i := 1; i < end; i++ {
		line := strings.TrimRight(lines[i], "\r\n")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "\t") {
			return a, fmt.Errorf("line %d: tabs unsupported", i+1)
		}
		if strings.HasPrefix(line, " ") {
			if !inRules {
				return a, fmt.Errorf("line %d: unexpected nesting", i+1)
			}
			if strings.HasPrefix(line, "  - ") {
				if err := finish(); err != nil {
					return a, err
				}
				fields = map[string]string{}
				line = strings.TrimPrefix(line, "  - ")
			} else if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ") && fields != nil {
				line = strings.TrimPrefix(line, "    ")
			} else {
				return a, fmt.Errorf("line %d: unsupported permission indentation", i+1)
			}
			k, v, err := scalarField(line)
			if err != nil {
				return a, fmt.Errorf("line %d: %w", i+1, err)
			}
			if k != "action" && k != "resource" && k != "effect" {
				return a, fmt.Errorf("unknown permission field %q", k)
			}
			if _, ok := fields[k]; ok {
				return a, fmt.Errorf("duplicate permission field %q", k)
			}
			fields[k] = v
			continue
		}
		if err := finish(); err != nil {
			return a, err
		}
		inRules = false
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return a, fmt.Errorf("line %d: expected scalar field", i+1)
		}
		k := parts[0]
		if seen[k] {
			return a, fmt.Errorf("duplicate field %q", k)
		}
		seen[k] = true
		if k == "permissions" {
			if strings.TrimSpace(parts[1]) != "" {
				return a, fmt.Errorf("permissions must be an ordered list")
			}
			inRules = true
			continue
		}
		_, v, err := scalarField(line)
		if err != nil {
			return a, err
		}
		switch k {
		case "description":
			a.Description = v
		case "mode":
			a.Mode = v
		case "model", "variant": // Intentionally omitted: inherit the active Pi model.
		case "steps":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || strings.TrimSpace(parts[1]) != v {
				return a, fmt.Errorf("steps must be a non-negative numeric scalar")
			}
			a.Steps = n
		default:
			return a, fmt.Errorf("unsupported frontmatter field %q", k)
		}
	}
	if err := finish(); err != nil {
		return a, err
	}
	if a.Description == "" {
		return a, fmt.Errorf("description is required")
	}
	if a.Mode != "primary" && a.Mode != "subagent" {
		return a, fmt.Errorf("unsupported mode %q", a.Mode)
	}
	return a, nil
}

func scalarField(line string) (string, string, error) {
	p := strings.SplitN(line, ":", 2)
	if len(p) != 2 || p[0] == "" {
		return "", "", fmt.Errorf("expected scalar field")
	}
	v := strings.TrimSpace(p[1])
	if v == "" {
		return "", "", fmt.Errorf("empty scalar %q", p[0])
	}
	if strings.HasPrefix(v, "\"") {
		var s string
		if err := json.Unmarshal([]byte(v), &s); err != nil {
			return "", "", fmt.Errorf("invalid quoted scalar %q", p[0])
		}
		v = s
	} else if strings.HasPrefix(v, "'") {
		if len(v) < 2 || v[len(v)-1] != '\'' {
			return "", "", fmt.Errorf("invalid quoted scalar")
		}
		v = strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	} else if strings.ContainsAny(v[:1], "[{&!|>") || strings.Contains(v, " #") {
		return "", "", fmt.Errorf("unsupported scalar syntax for %q", p[0])
	}
	if strings.ContainsAny(v, "\r\n\x00") {
		return "", "", fmt.Errorf("multiline/control scalar unsupported")
	}
	return p[0], v, nil
}
