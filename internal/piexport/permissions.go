package piexport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type policyRule struct{ surface, pattern, effect string }
type surfaceRules struct {
	name  string
	rules []policyRule
}
type Policy struct{ groups []surfaceRules }

// Permissions preserves rule precedence, including overlapping tool wildcards.
// A nested map cannot represent arbitrary interleaving. Only commuting rules
// may change order; otherwise conversion rejects the entire source snapshot.
func Permissions(rules []Rule) (Policy, []string, error) {
	var converted []policyRule
	var notes []string
	explicitPath, mixedExternal := false, false
	for _, r := range rules {
		if r.Action == "path" || r.Action == "path_read" || r.Action == "path_write" {
			explicitPath = true
		}
		if r.Action == "external_directory_read" || r.Action == "external_directory_write" {
			mixedExternal = true
		}
	}
	for _, r := range rules {
		if r.Effect != "allow" && r.Effect != "ask" && r.Effect != "deny" {
			return Policy{}, nil, fmt.Errorf("invalid effect %q", r.Effect)
		}
		if r.Resource == "" {
			return Policy{}, nil, fmt.Errorf("empty resource")
		}
		surface, pattern := r.Action, r.Resource
		switch r.Action {
		case "*":
			if r.Resource != "*" {
				return Policy{}, nil, fmt.Errorf("universal action requires universal resource")
			}
		case "shell":
			surface = "bash"
			if strings.ContainsAny(pattern, ";&|\n") {
				return Policy{}, nil, fmt.Errorf("bash chain pattern %q cannot preserve matching semantics", pattern)
			}
		case "glob":
			surface = "find"
		case "list":
			surface = "ls"
		case "subagent":
			surface = "task"
		case "websearch":
			if pattern != "*" {
				return Policy{}, nil, fmt.Errorf("websearch resource %q has no safe query mapping", pattern)
			}
			surface = "web_search"
		case "webfetch":
			if pattern != "*" {
				return Policy{}, nil, fmt.Errorf("webfetch resource %q has no safe URL mapping", pattern)
			}
			surface = "fetch_content"
		case "opencode_list_mcp_resources":
			surface = "list_mcp_resources"
			notes = append(notes, "list_mcp_resources requires that exact host tool; it is not installed by agent-sync")
		case "opencode_read_mcp_resource":
			surface = "read_mcp_resource"
			notes = append(notes, "read_mcp_resource requires that exact host tool; it is not installed by agent-sync")
		case "execute":
			notes = append(notes, "execute is unavailable: use only the permitted read-only MCP tools directly; no Code Mode/shell bypass grant")
			continue
		case "question":
			notes = append(notes, "question is unavailable in native Pi; return the question to the parent/user")
			continue
		case "context7_resolve_library_id", "context7_query_docs", "codegraph_codegraph_explore",
			"engram_mem_current_project", "engram_mem_context", "engram_mem_search", "engram_mem_get_observation",
			"engram_mem_save", "engram_mem_update", "engram_mem_judge", "engram_mem_compare", "engram_mem_session_summary",
			"supabase*_list_tables", "supabase*_list_extensions", "supabase*_list_migrations", "supabase*_get_advisors",
			"supabase*_get_logs", "supabase*_apply_migration", "supabase*_execute_sql", "supabase*_generate_typescript_types":
			if pattern != "*" {
				return Policy{}, nil, fmt.Errorf("MCP tool %q with resource %q has no evidence-backed input mapping", r.Action, pattern)
			}
			surface, pattern = "mcp", r.Action
			notes = append(notes, "MCP target "+r.Action+" requires a matching configured server/tool; availability is not probed")
		case "codegraph_*":
			if pattern != "*" {
				return Policy{}, nil, fmt.Errorf("codegraph wildcard has no safe resource mapping for %q", pattern)
			}
			converted = append(converted, policyRule{"mcp", "codegraph_*", r.Effect})
		case "read", "write", "edit", "grep", "skill", "path", "path_read", "path_write",
			"external_directory", "external_directory_read", "external_directory_write",
			"bash", "find", "ls", "task", "web_search", "fetch_content", "source_check", "get_search_content":
		default:
			return Policy{}, nil, fmt.Errorf("no evidence-backed permission mapping for %q", r.Action)
		}
		if surface == "path" || (surface == "external_directory" && mixedExternal) {
			converted = append(converted, policyRule{surface + "_read", pattern, r.Effect}, policyRule{surface + "_write", pattern, r.Effect})
		} else {
			converted = append(converted, policyRule{surface, pattern, r.Effect})
		}
	}
	// Earlier identical keys are completely shadowed by the last identical key.
	// Delete them and insert the surviving key at its LAST position (not first).
	last := map[[2]string]int{}
	for i, r := range converted {
		last[[2]string{r.surface, r.pattern}] = i
	}
	var normalized []policyRule
	for i, r := range converted {
		if last[[2]string{r.surface, r.pattern}] == i {
			normalized = append(normalized, r)
		}
	}
	converted = normalized
	var p Policy
	// Pi compiles the universal fallback first regardless of object key order.
	fallback := policyRule{"*", "*", "allow"}
	for _, r := range converted {
		if r.surface == "*" {
			fallback = r
		}
	}
	p.groups = append(p.groups, surfaceRules{"*", []policyRule{fallback}})
	if fallback.effect == "allow" && !hasSurface(converted, "*") {
		notes = append(notes, "source omitted the universal default: explicit *: allow preserves OpenCode's permissive fallback (not Pi's ask default)")
	}
	if !explicitPath {
		p.groups = append(p.groups, surfaceRules{"path", []policyRule{{"path", "*", "allow"}}})
	}
	for _, r := range converted {
		if r.surface == "*" {
			continue
		}
		i := -1
		for j := range p.groups {
			if p.groups[j].name == r.surface {
				i = j
				break
			}
		}
		if i == -1 {
			p.groups = append(p.groups, surfaceRules{r.surface, nil})
			i = len(p.groups) - 1
		}
		p.groups[i].rules = append(p.groups[i].rules, r)
	}
	// Model JavaScript's integer-property enumeration, not just YAML order.
	var flattened []policyRule
	for _, g := range p.groups {
		rs := append([]policyRule(nil), g.rules...)
		sort.SliceStable(rs, func(i, j int) bool {
			a, ai := arrayIndex(rs[i].pattern)
			b, bi := arrayIndex(rs[j].pattern)
			if ai && bi {
				return a < b
			}
			return ai && !bi
		})
		for _, r := range rs {
			for _, original := range converted {
				if original == r {
					flattened = append(flattened, r)
					break
				}
			}
		}
	}
	positions := map[policyRule]int{}
	for i, r := range flattened {
		positions[r] = i
	}
	for i, a := range converted {
		for _, b := range converted[i+1:] {
			if positions[a] > positions[b] && a.effect != b.effect && globOverlap(a.surface, b.surface) && globOverlap(a.pattern, b.pattern) {
				return Policy{}, nil, fmt.Errorf("ordered rules %s:%s and %s:%s cannot be represented by a nested map without changing precedence", a.surface, a.pattern, b.surface, b.pattern)
			}
		}
	}
	// Capability adaptations are explicit restrictions, never execution grants.
	for _, name := range []string{"write", "execute", "codemode", "source_check", "get_search_content"} {
		if (name == "write" || name == "source_check" || name == "get_search_content") && hasSurface(converted, name) {
			continue
		}
		p.groups = append(p.groups, surfaceRules{name, []policyRule{{name, "*", "deny"}}})
	}
	return p, notes, nil
}

func hasSurface(rs []policyRule, name string) bool {
	for _, r := range rs {
		if r.surface == name {
			return true
		}
	}
	return false
}
func arrayIndex(s string) (uint64, bool) {
	n, e := strconv.ParseUint(s, 10, 32)
	return n, e == nil && n < 4294967295 && strconv.FormatUint(n, 10) == s
}

// globOverlap decides whether two * / ? wildcard languages intersect. Stars
// consume any character, including '/', as in Pi (not filepath.Match).
func globOverlap(a, b string) bool {
	x, y := []rune(a), []rune(b)
	type pair struct{ i, j int }
	q := []pair{{0, 0}}
	seen := map[pair]bool{}
	for len(q) > 0 {
		p := q[0]
		q = q[1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		if p.i == len(x) && p.j == len(y) {
			return true
		}
		if p.i < len(x) && x[p.i] == '*' {
			q = append(q, pair{p.i + 1, p.j})
		}
		if p.j < len(y) && y[p.j] == '*' {
			q = append(q, pair{p.i, p.j + 1})
		}
		if p.i == len(x) || p.j == len(y) {
			continue
		}
		ca, cb := x[p.i], y[p.j]
		if ca == cb || ca == '*' || cb == '*' || ca == '?' || cb == '?' {
			i, j := p.i, p.j
			if ca != '*' {
				i++
			}
			if cb != '*' {
				j++
			}
			q = append(q, pair{i, j})
		}
	}
	return false
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// The pinned permission plugin splits keys at the FIRST colon, even inside
// quotes, and only strips outer quotes (no escape decoding). Use literal YAML
// quoting where it is lossless and reject anything it cannot actually load.
func yamlKey(s string) (string, error) {
	if !utf8.ValidString(s) { return "", fmt.Errorf("invalid UTF-8 in YAML permission key") }
	if strings.Contains(s, ":") {
		return "", fmt.Errorf("pi-permission-system@40.2.0 cannot load YAML permission key %q: its parser splits at the first colon, including quoted keys", s)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("control character in YAML permission key")
		}
	}
	if !strings.ContainsAny(s, "\"\\") {
		return "\"" + s + "\"", nil
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'", nil
	}
	return "", fmt.Errorf("YAML permission key %q requires escape decoding unsupported by pi-permission-system@40.2.0", s)
}

func (p Policy) YAML() (string, error) {
	return p.YAMLWithParser(ParserUpstream)
}

func yamlKeyWithParser(s string, mode ParserMode) (string, error) {
	if mode == ParserUpstream { return yamlKey(s) }
	if !utf8.ValidString(s) { return "", fmt.Errorf("invalid UTF-8 in YAML permission key") }
	for _, r := range s {
		if r < 0x20 || r == 0x7f { return "", fmt.Errorf("control character in YAML permission key") }
	}
	// JSON double-quoted keys are exactly the pinned patch's decoded subset.
	// Backslashes, quotes, Unicode and ':' are never replaced by wildcards.
	return quote(s), nil
}

func (p Policy) YAMLWithParser(mode ParserMode) (string, error) {
	mode, err := NormalizeParserMode(mode)
	if err != nil { return "", err }
	var b strings.Builder
	b.WriteString("permission:\n")
	for _, g := range p.groups {
		key, err := yamlKeyWithParser(g.name, mode)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "  %s:", key)
		if len(g.rules) == 1 && g.rules[0].pattern == "*" {
			fmt.Fprintf(&b, " %s\n", quote(g.rules[0].effect))
			continue
		}
		b.WriteByte('\n')
		for _, r := range g.rules {
			key, err := yamlKeyWithParser(r.pattern, mode)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "    %s: %s\n", key, quote(r.effect))
		}
	}
	return b.String(), nil
}
func (p Policy) JSON() []byte {
	var b bytes.Buffer
	b.WriteString("{\n  \"permission\": {\n")
	for i, g := range p.groups {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "    %s: ", quote(g.name))
		if len(g.rules) == 1 && g.rules[0].pattern == "*" {
			b.WriteString(quote(g.rules[0].effect))
			continue
		}
		b.WriteString("{")
		for j, r := range g.rules {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "\n      %s: %s", quote(r.pattern), quote(r.effect))
		}
		b.WriteString("\n    }")
	}
	b.WriteString("\n  }\n}\n")
	return b.Bytes()
}
