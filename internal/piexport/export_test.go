package piexport

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Decode the serialized artifact with a streaming decoder: a Go map would
// hide precisely the ordering regressions these tests must detect.
func artifactRules(t *testing.T, p Policy) []policyRule {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(string(p.JSON())))
	for _, want := range []json.Token{json.Delim('{'), "permission", json.Delim('{')} {
		got, e := d.Token()
		if e != nil || got != want {
			t.Fatalf("invalid JSON token %v (%v)", got, e)
		}
	}
	var rs []policyRule
	for d.More() {
		key, e := d.Token()
		if e != nil {
			t.Fatal(e)
		}
		surface := key.(string)
		value, e := d.Token()
		if e != nil {
			t.Fatal(e)
		}
		if s, ok := value.(string); ok {
			rs = append(rs, policyRule{surface, "*", s})
			continue
		}
		if value != json.Delim('{') {
			t.Fatal("not scalar/map")
		}
		for d.More() {
			pat, e := d.Token()
			if e != nil {
				t.Fatal(e)
			}
			effect, e := d.Token()
			if e != nil {
				t.Fatal(e)
			}
			rs = append(rs, policyRule{surface, pat.(string), effect.(string)})
		}
		if _, e := d.Token(); e != nil {
			t.Fatal(e)
		}
	}
	for range 2 {
		if _, e := d.Token(); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := d.Token(); e != io.EOF {
		t.Fatalf("trailing JSON: %v", e)
	}
	return rs
}

func matches(p, v string) bool {
	r := regexp.QuoteMeta(p)
	r = strings.ReplaceAll(r, `\*`, ".*")
	r = strings.ReplaceAll(r, `\?`, ".")
	return regexp.MustCompile("(?s)^" + r + "$").MatchString(v)
}
func decision(rs []policyRule, surface, value string) string {
	e := "ask"
	for _, r := range rs {
		if matches(r.surface, surface) && matches(r.pattern, value) {
			e = r.effect
		}
	}
	return e
}
func policy(t *testing.T, rs ...Rule) Policy {
	t.Helper()
	p, _, e := Permissions(rs)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func policyYAML(t *testing.T, p Policy) string {
	t.Helper()
	s, err := p.YAML()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPermissionsIndependentGates(t *testing.T) {
	p := policy(t, Rule{"*", "*", "deny"}, Rule{"read", "*", "allow"}, Rule{"read", "*SKILL.md", "deny"}, Rule{"edit", "*", "allow"}, Rule{"external_directory", "*", "ask"})
	rs := artifactRules(t, p)
	for _, c := range []struct{ s, v, w string }{
		{"path", "/repo/src/a.go", "allow"}, {"read", "src/a.go", "allow"}, {"read", "a/b/SKILL.md", "deny"},
		{"edit", "src/a.go", "allow"}, {"write", "src/a.go", "deny"}, {"bash", "git status", "deny"},
		{"external_directory", "/outside/file", "ask"}, {"execute", "*", "deny"}, {"codemode", "*", "deny"},
	} {
		if got := decision(rs, c.s, c.v); got != c.w {
			t.Errorf("%s:%s = %s, want %s", c.s, c.v, got, c.w)
		}
	}
	y := policyYAML(t, p)
	if strings.Contains(y, "permissions:") || strings.Contains(y, "- action:") {
		t.Fatal("Pi frontmatter must be scalar/nested maps")
	}
}

func TestPermissionsExplicitPathAndDirections(t *testing.T) {
	p := policy(t, Rule{"*", "*", "deny"}, Rule{"path", "*", "deny"}, Rule{"path_read", "src/*", "allow"}, Rule{"external_directory", "*", "ask"}, Rule{"external_directory_read", "/cache/*", "allow"})
	rs := artifactRules(t, p)
	if strings.Contains(policyYAML(t, p), "  \"path\": \"allow\"") {
		t.Fatal("neutral path must not replace explicit path rules")
	}
	for _, c := range []struct{ s, v, w string }{
		{"path_read", "src/nested/a.go", "allow"}, {"path_write", "src/nested/a.go", "deny"},
		{"external_directory_read", "/cache/a/b", "allow"}, {"external_directory_write", "/cache/a/b", "ask"},
	} {
		if got := decision(rs, c.s, c.v); got != c.w {
			t.Errorf("%+v got %s", c, got)
		}
	}
}

func TestPermissionsAliasesAndKnownMCP(t *testing.T) {
	p := policy(t, Rule{"*", "*", "deny"}, Rule{"shell", "*", "ask"}, Rule{"shell", "git *", "deny"}, Rule{"shell", "git status*", "allow"}, Rule{"glob", "*", "allow"}, Rule{"list", "*", "allow"}, Rule{"skill", "context7-mcp", "allow"}, Rule{"websearch", "*", "allow"}, Rule{"webfetch", "*", "allow"}, Rule{"context7_query_docs", "*", "allow"}, Rule{"supabase*_execute_sql", "*", "deny"}, Rule{"opencode_list_mcp_resources", "*", "allow"})
	rs := artifactRules(t, p)
	for _, c := range []struct{ s, v, w string }{
		{"bash", "git status --short", "allow"}, {"bash", "git push origin main", "deny"}, {"bash", "npm test", "ask"},
		{"find", "nested/a", "allow"}, {"ls", "nested/a", "allow"}, {"skill", "context7-mcp", "allow"},
		{"web_search", "*", "allow"}, {"fetch_content", "*", "allow"}, {"source_check", "*", "deny"}, {"get_search_content", "*", "deny"},
		{"mcp", "context7_query_docs", "allow"}, {"mcp", "supabase_epy_execute_sql", "deny"}, {"mcp", "supabase_epy_other", "deny"}, {"list_mcp_resources", "*", "allow"},
	} {
		if got := decision(rs, c.s, c.v); got != c.w {
			t.Errorf("%+v got %s", c, got)
		}
	}
	if _, _, e := Permissions([]Rule{{"unknown_mcp_tool", "*", "allow"}}); e == nil {
		t.Fatal("unknown mapping must fail")
	}
	if _, _, e := Permissions([]Rule{{"webfetch", "https://one.example/*", "allow"}}); e == nil {
		t.Fatal("unsupported URL granular grant must fail")
	}
}

func TestPermissionsDuplicateLastPosition(t *testing.T) {
	p := policy(t, Rule{"*", "*", "deny"}, Rule{"shell", "*", "allow"}, Rule{"shell", "git *", "deny"}, Rule{"shell", "*", "allow"})
	rs := artifactRules(t, p)
	if got := decision(rs, "bash", "git push"); got != "allow" {
		t.Fatalf("last duplicate must dominate intervening rule, got %s", got)
	}
	if strings.Index(policyYAML(t, p), "\"git *\"") > strings.Index(policyYAML(t, p), "    \"*\"") {
		t.Fatal("duplicate kept its first insertion position")
	}
	// Grouping interleaved overlapping surfaces would move the final deny ahead
	// of the wildcard allow. The converter must reject instead of guessing.
	_, _, err := Permissions([]Rule{{"read", "*", "deny"}, {"*", "*", "allow"}, {"read", "secret*", "deny"}})
	if err == nil {
		t.Fatal("nonrepresentable universal ordering accepted")
	}
	// JavaScript enumerates integer keys before '*', independent of YAML order.
	_, _, err = Permissions([]Rule{{"*", "*", "deny"}, {"read", "*", "deny"}, {"read", "123", "allow"}})
	if err == nil {
		t.Fatal("unsafe integer-property reordering accepted")
	}
}

func TestPermissionsMissingDefaultAndGenericWildcard(t *testing.T) {
	p := policy(t, Rule{"edit", "*", "deny"}, Rule{"codegraph_*", "*", "deny"}, Rule{"skill", "*", "deny"}, Rule{"skill", "opencode", "allow"})
	rs := artifactRules(t, p)
	for _, c := range []struct{ s, v, w string }{
		{"read", "src/a", "allow"}, {"edit", "src/a", "deny"}, {"write", "src/a", "deny"},
		{"codegraph_anything", "*", "deny"}, {"mcp", "codegraph_codegraph_explore", "deny"}, {"skill", "opencode", "allow"},
	} {
		if got := decision(rs, c.s, c.v); got != c.w {
			t.Errorf("%+v got %s", c, got)
		}
	}
}

func TestGlobOverlapBoundaries(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"a/*", "a/b/c", true}, {"a?", "ab", true}, {"a?", "abc", false}, {"", "*", true}, {"a*", "b*", false},
		{"*x", "y*", true}, {"git *", "git status*", true}, {"a*b", "a*c", false}, {"*", "", true},
	} {
		if got := globOverlap(c.a, c.b); got != c.want {
			t.Errorf("%q/%q=%v", c.a, c.b, got)
		}
	}
}

func fixtureAgent(mode, steps string) string {
	return "---\ndescription: test\nmode: " + mode + "\nmodel: provider/model#variant\nvariant: high\nsteps: " + steps + "\npermissions:\n  - action: \"*\"\n    resource: \"*\"\n    effect: deny\n  - action: edit\n    resource: \"*\"\n    effect: allow\n---\nbody\n"
}

func TestModelsLimitsAndPrimaryArtifacts(t *testing.T) {
	for _, c := range []struct{ steps, want string }{{"0", ""}, {"1", "max_turns: 2"}, {"32", "max_turns: 32"}} {
		out, e := Convert(map[string][]byte{"agents/a.md": []byte(fixtureAgent("subagent", c.steps)), "agents/orchestrator.md": []byte(fixtureAgent("primary", "50"))})
		if e != nil {
			t.Fatal(e)
		}
		a := string(out["agents/a.md"])
		if strings.Contains(a, "\nmodel:") || strings.Contains(a, "\nvariant:") || strings.Contains(a, "\nthinking:") {
			t.Fatal("model fields leaked")
		}
		if c.want == "" && strings.Contains(a, "max_turns:") || c.want != "" && !strings.Contains(a, c.want) {
			t.Fatalf("wrong limit: %s", a)
		}
		if _, ok := out["agents/orchestrator.md"]; ok {
			t.Fatal("primary is loadable as a child")
		}
		if string(out["agent-sync/primary/orchestrator.md"]) != "body\n" {
			t.Fatal("primary body changed")
		}
		if !json.Valid(out["agent-sync/policies/orchestrator.json"]) {
			t.Fatal("invalid root policy")
		}
		if strings.Contains(string(out["agent-sync/report.md"]), "<active_agent>") {
			t.Fatal("fabricated identity")
		}
	}
	for _, steps := range []string{"-1", "\"3\"", "many"} {
		if _, e := Parse([]byte(fixtureAgent("subagent", steps))); e == nil {
			t.Fatalf("invalid numeric steps %q accepted", steps)
		}
	}
}

func TestRepositorySnapshotFailsClosedOnPinnedParserLimitation(t *testing.T) {
	paths, e := filepath.Glob("../../agents/*.md")
	if e != nil {
		t.Fatal(e)
	}
	if len(paths) != 19 {
		t.Fatalf("actual source count %d, want 19", len(paths))
	}
	files := map[string][]byte{}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		files["agents/"+filepath.Base(p)] = b
	}
	out, e := Convert(files)
	if e == nil || !strings.Contains(e.Error(), "agents/git-manager.md") || !strings.Contains(e.Error(), "first colon") || out != nil {
		t.Fatalf("actual snapshot must fail atomically, not lose git-manager policy: outputs=%d error=%v", len(out), e)
	}
	// Only the explicit compatibility declaration enables the full snapshot.
	// Upstream above remains atomic fail-closed; no production agent filtering.
	out, e = ConvertWithParser(files, ParserPatched40V1)
	if e != nil {
		t.Fatal(e)
	}
	loadable := 0
	for p := range out {
		if strings.HasPrefix(p, "agents/") {
			loadable++
		}
	}
	if loadable != 18 {
		t.Fatalf("loadable count %d", loadable)
	}
	if !strings.Contains(string(out["agents/researcher.md"]), "web_search, fetch_content") {
		t.Fatal("researcher extension tools not admitted")
	}
	if !strings.Contains(string(out["agents/memory-curator.md"]), "mcp__engram__mem_save") {
		t.Fatal("curator MCP tools not admitted")
	}
	if !strings.Contains(string(out["agents/database-specialist.md"]), "mcp__supabase*__list_tables") {
		t.Fatal("bounded supabase tool pattern missing")
	}
	if strings.Contains(string(out["agents/implementer.md"]), "edit, write") {
		t.Fatal("edit admitted write")
	}
	for p := range out {
		if !Manifest().Match(p) {
			t.Fatalf("output outside manifest: %s", p)
		}
	}
	if _, ok := out["agents/orchestrator.md"]; ok { t.Fatal("primary became loadable") }
	for _, pattern := range []string{"git push *:*", "git add -- :*", "git add -- * :*", "git add :*", "git add * :*"} {
		if !strings.Contains(string(out["agents/git-manager.md"]), quote(pattern)+":") { t.Fatalf("exact colon key lost: %q", pattern) }
	}
	if !strings.Contains(string(out["agent-sync/report.md"]), "compatibility: patched-40.2.0-v1") || !strings.Contains(string(out["agent-sync/report.md"]), "DECLARATION ONLY") {
		t.Fatal("managed report lacks mode/attestation distinction")
	}
	t.Log("upstream rejects all 17 atomically; explicit patched declaration exports 16 loadable + primary without dropping keys")
}

func TestPatchedParserModeKeyRoundTrip(t *testing.T) {
	for _, key := range []string{`git push *:*`, `git "a:b" C:\repo\*`, `it's:a"b*`, "clé:日本語🧪", "a\u2028:b"} {
		encoded, e := yamlKeyWithParser(key, ParserPatched40V1)
		if e != nil { t.Fatal(e) }
		var decoded string
		if e := json.Unmarshal([]byte(encoded), &decoded); e != nil || decoded != key { t.Fatalf("key roundtrip %q => %q (%v)", key, decoded, e) }
	}
	for _, key := range []string{string([]byte{0xff}), "a\n:b", "a\x00:b"} {
		if _, e := yamlKeyWithParser(key, ParserPatched40V1); e == nil { t.Fatalf("unsafe key accepted %q", key) }
	}
	if _, e := NormalizeParserMode("patched"); e == nil { t.Fatal("unpinned parser mode accepted") }
	if out, e := ConvertWithParser(map[string][]byte{"agents/a.md": []byte(fixtureAgent("subagent", "2"))}, "unknown"); e == nil || out != nil { t.Fatal("unknown mode produced artifacts") }
}

func TestPinnedYAMLParserLiteralKeys(t *testing.T) {
	p := policy(t, Rule{"*", "*", "deny"}, Rule{"read", `**/.ssh\**`, "deny"})
	y := policyYAML(t, p)
	if !strings.Contains(y, `'**/.ssh\**': "deny"`) {
		t.Fatalf("backslash was JSON-escaped in YAML: %s", y)
	}
	p = policy(t, Rule{"shell", "git push *:*", "ask"})
	if _, e := p.YAML(); e == nil || !strings.Contains(e.Error(), "first colon") {
		t.Fatal("quoted colon key silently corrupted")
	}
	if !json.Valid(p.JSON()) {
		t.Fatal("the same key is representable in JSON for a manual primary policy")
	}
	if _, e := yamlKey(`a"b'c`); e == nil {
		t.Fatal("key requiring escape decoding accepted")
	}
}

func TestWebProfileManualContract(t *testing.T) {
	var got map[string]any
	if e := json.Unmarshal(WebProfile, &got); e != nil {
		t.Fatal(e)
	}
	for k, w := range map[string]any{"toolActivation": "eager", "provider": "exa", "allowBrowserCookies": false, "workflow": "none"} {
		if got[k] != w {
			t.Errorf("%s=%v", k, got[k])
		}
	}
	for _, k := range []string{"youtube", "video", "githubClone", "githubPrIssue"} {
		if got[k].(map[string]any)["enabled"] != false {
			t.Errorf("%s not disabled", k)
		}
	}
	if _, ok := got["authFetch"]; ok {
		t.Fatal("auth profile must not be generated")
	}
	for _, k := range []string{"sourceCheck", "getSearchContent"} {
		if got["tools"].(map[string]any)[k].(map[string]any)["enabled"] != false {
			t.Errorf("%s enabled", k)
		}
	}
	fetch := got["fetch"].(map[string]any)
	if fetch["defaultMode"] != "readable" || len(fetch["allowedModes"].([]any)) != 2 {
		t.Fatal("unsafe fetch modes")
	}
	route := got["fetchRouting"].(map[string]any)
	if route["allowRemoteHostedProviders"] != false || len(route["providers"].([]any)) != 1 || route["providers"].([]any)[0] != "http" {
		t.Fatal("unsafe hosted fetch route")
	}
	if got["pdf"].(map[string]any)["provider"] != "unpdf" {
		t.Fatal("cloud PDF parser")
	}
	ssrf := got["ssrf"].(map[string]any)
	if ssrf["trustEnvProxy"] != false || len(ssrf["allowRanges"].([]any)) != 0 {
		t.Fatal("weakened SSRF policy")
	}
	for _, v := range got["commands"].(map[string]any) {
		if v.(map[string]any)["enabled"] != false {
			t.Fatal("live command enabled")
		}
	}
}
