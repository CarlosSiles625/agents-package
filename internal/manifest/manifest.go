// Package manifest defines the versioned, explicit allow-list of files that
// agent-sync is permitted to manage. The default manifest includes only the
// top-level AGENTS.md, markdown files under agents/, and one-level skills
// (skills/<id>/SKILL.md). Everything else (other skill files, MCP
// configuration, opencode.json, credentials, ...) is explicitly excluded.
package manifest

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"agent-sync/internal/pathsafe"
)

// CurrentVersion is the manifest schema version understood by this binary.
const CurrentVersion = 1

// Manifest is the explicit, versioned set of include patterns.
type Manifest struct {
	Version int      `json:"version"`
	Include []string `json:"include"`
}

// Default returns the built-in manifest: AGENTS.md, agents/**/*.md and
// skills/<id>/SKILL.md only.
func Default() Manifest {
	return Manifest{
		Version: CurrentVersion,
		Include: []string{"AGENTS.md", "agents/**/*.md", "skills/*/SKILL.md"},
	}
}

// Agents returns the subset of the default manifest that holds agent
// definitions (agents/**/*.md). Model preferences apply only to these paths.
func Agents() Manifest {
	return Manifest{
		Version: CurrentVersion,
		Include: []string{"agents/**/*.md"},
	}
}

// Decode parses a manifest from JSON.
func Decode(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("decoding manifest: %w", err)
	}
	return m, m.Validate()
}

// Validate checks the schema version and the include patterns.
func (m Manifest) Validate() error {
	if m.Version != CurrentVersion {
		return fmt.Errorf("manifest: unsupported version %d", m.Version)
	}
	if len(m.Include) == 0 {
		return fmt.Errorf("manifest: include list is empty")
	}
	for _, pat := range m.Include {
		if err := validatePattern(pat); err != nil {
			return fmt.Errorf("manifest: %w", err)
		}
	}
	return nil
}

func validatePattern(pat string) error {
	if pat == "" {
		return fmt.Errorf("empty pattern")
	}
	if strings.HasPrefix(pat, "/") || strings.HasPrefix(pat, `\`) || strings.Contains(pat, `\`) {
		return fmt.Errorf("pattern %q must be a relative forward-slash path", pat)
	}
	if len(pat) >= 2 && pat[1] == ':' {
		return fmt.Errorf("pattern %q must not contain a drive letter", pat)
	}
	for _, seg := range strings.Split(pat, "/") {
		if seg == "" {
			return fmt.Errorf("pattern %q has an empty segment", pat)
		}
		if seg == "**" {
			continue
		}
		if seg == "." || seg == ".." {
			return fmt.Errorf("pattern %q contains a traversal segment", pat)
		}
		if strings.TrimRight(seg, ".") != seg {
			return fmt.Errorf("pattern %q has a trailing dot", pat)
		}
		if strings.ContainsAny(seg, `<>:"|?`) {
			return fmt.Errorf("pattern %q contains a non-portable character", pat)
		}
	}
	return nil
}

// Match reports whether rel is a portable regular path covered by the
// manifest. Non-portable paths never match.
func (m Manifest) Match(rel string) bool {
	if pathsafe.ValidateRel(rel) != nil {
		return false
	}
	for _, pat := range m.Include {
		if match(pat, rel) {
			return true
		}
	}
	return false
}

func match(pattern, name string) bool {
	return matchSegs(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegs(p, n []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			if len(p) == 1 {
				return true
			}
			for i := 0; i <= len(n); i++ {
				if matchSegs(p[1:], n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 {
			return false
		}
		ok, err := path.Match(p[0], n[0])
		if err != nil || !ok {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}
