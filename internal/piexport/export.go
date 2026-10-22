package piexport

import (
	_ "embed"
	"fmt"
	"path"
	"sort"
	"strings"

	"agent-sync/internal/manifest"
	"agent-sync/internal/pathsafe"
)

//go:embed web-search.global.manual.json
var WebProfile []byte

// Manifest does not include AGENTS.md or any live Pi/OpenCode configuration.
func Manifest() manifest.Manifest {
	return manifest.Manifest{Version: manifest.CurrentVersion, Include: []string{
		"agents/**/*.md", "agent-sync/primary/**/*.md", "agent-sync/policies/**/*.json",
		"agent-sync/report.md", "agent-sync/web-search.global.manual.json",
	}}
}

// Convert validates ALL inputs before returning the converted snapshot. The
// caller plans/applies only these bytes; its baseline is never the source YAML.
func Convert(files map[string][]byte) (map[string][]byte, error) {
	return ConvertWithParser(files, ParserUpstream)
}

func ConvertWithParser(files map[string][]byte, mode ParserMode) (map[string][]byte, error) {
	mode, err := NormalizeParserMode(mode)
	if err != nil { return nil, err }
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string][]byte{}
	names := map[string]string{}
	var report strings.Builder
	report.WriteString("# Pi export — MANUAL configuration\n\nModels: INHERIT the active Pi model; model, variant and thinking are not exported.\n")
	fmt.Fprintf(&report, "Permission parser compatibility: %s. This mode is part of the managed baseline and persisted state identity.\n", mode)
	if mode == ParserPatched40V1 {
		report.WriteString("DECLARATION ONLY: selecting patched-40.2.0-v1 declares the user already applied and SHA-256 verified the version-pinned parser patch. agent-sync does NOT install, activate or runtime-verify it. See patches/pi-permission-system/40.2.0/README.md in the agent-sync source repository. A different host parser must not load this export.\n\n")
	}
	report.WriteString("Numeric steps adapt to max_turns (minimum 2); zero/missing omits the limit. Pi counts successful model turns, not OpenCode steps; callers/runtime defaults may override these values.\n\n")
	report.WriteString("Install/review @gotgenes/pi-subagents@23.4.0, @gotgenes/pi-permission-system@40.2.0 and pi-web-access@0.39.0 manually (Pi >=1, Node >=22.19). Nothing here activates a config.\n")
	report.WriteString("Children must retain the permission extension: no excludedExtensionPackages or yoloMode. Runtime supplies active_agent identity and forwards asks; no fabricated primary identity. Children cannot recurse; root delegation is unaffected.\n\n")
	report.WriteString("Review policies/*.json MANUALLY before applying a primary policy to Pi's live permission config. Global policy affects other sessions; project policy requires project trust. Never merge blindly. Primary prompts below are outside the loadable agents directory; select them with --append-system-prompt, not an agent tag.\n\n")
	report.WriteString("Review web-search.global.manual.json MANUALLY and apply only to the GLOBAL web-search.json selected by pi-web-access (PI_CODING_AGENT_DIR/XDG/legacy precedence). It is required for the approved web capability profile, even with project agents; it affects other sessions. Never overwrite an existing config without review. Restart Pi after manual application.\n")
	report.WriteString("Web profile: Exa search, direct HTTP(S) fetch, local unpdf parsing of remote PDFs; no cookie auth, video/YouTube, GitHub clone/gh specialization, hosted fetchers or answer models. includeContent may fetch HTTP(S) result URLs internally. source_check/get_search_content are disabled; long inline content can be truncated. Grants alone do not enforce these side-effect limits. No sandbox is provided.\n")
	report.WriteString("Unset BOTH PI_ALLOW_BROWSER_COOKIES and FEYNMAN_ALLOW_BROWSER_COOKIES before starting; either =1 overrides allowBrowserCookies:false. Only submit HTTP(S) URLs, never local paths/uploads. Do not enable credential commands, proxies, authFetch or other side-effect features in the manual profile.\n\n")
	count := 0
	for _, f := range keys {
		if !strings.HasPrefix(f, "agents/") || !strings.HasSuffix(f, ".md") {
			continue
		}
		if err := pathsafe.ValidateRel(f); err != nil {
			return nil, err
		}
		a, err := Parse(files[f])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if a.Mode == "subagent" {
			for _, r := range a.Rules {
				if r.Action == "*" && r.Resource == "*" && r.Effect == "allow" {
					return nil, fmt.Errorf("%s: explicit universal allow (action %q, resource %q) cannot be represented by the closed Pi subagent tool list; refusing instead of dropping future/MCP tools", f, r.Action, r.Resource)
				}
			}
		}
		name := strings.TrimSuffix(path.Base(f), ".md")
		if old, ok := names[strings.ToLower(name)]; ok {
			return nil, fmt.Errorf("duplicate Pi agent name in %q and %q", old, f)
		}
		names[strings.ToLower(name)] = f
		p, notes, err := Permissions(a.Rules)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		count++
		fmt.Fprintf(&report, "## %s — %s — model INHERIT\n\n", f, a.Mode)
		if a.Mode == "primary" {
			rel := strings.TrimPrefix(f, "agents/")
			prompt := "agent-sync/primary/" + rel
			out[prompt] = []byte(a.Body)
			out["agent-sync/policies/"+strings.TrimSuffix(rel, ".md")+".json"] = p.JSON()
			fmt.Fprintf(&report, "From the Pi directory containing this report's parent: `env -u PI_ALLOW_BROWSER_COOKIES -u FEYNMAN_ALLOW_BROWSER_COOKIES pi --append-system-prompt %s`\n\n", prompt)
			report.WriteString("Primary steps are reported only; append-system-prompt does not enforce a root turn ceiling. The policy remains MANUAL, not per-agent frontmatter.\n")
		} else {
			yaml, err := p.YAMLWithParser(mode)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			var b strings.Builder
			fmt.Fprintf(&b, "---\ndescription: %s\ntools: %s\n", quote(a.Description), quote(toolCSV(a.Rules)))
			if a.Steps > 0 {
				fmt.Fprintf(&b, "max_turns: %d\n", max(2, a.Steps))
			}
			b.WriteString(yaml)
			b.WriteString("---\n")
			b.WriteString(a.Body)
			b.WriteString("\n\n## Pi host adaptation\n\nInherit the active Pi model. Use bash/find/ls for shell/glob/list, web_search/fetch_content for websearch/webfetch. Execute/Code Mode is not authorized; call only admitted MCP tools directly (servers must use direct exposure). Missing capabilities must be reported, never replaced by shell or broader tool grants. Ask unresolved questions in your final response to the parent.\n")
			for _, n := range notes {
				fmt.Fprintf(&b, "- %s\n", n)
			}
			out[f] = []byte(b.String())
		}
		fmt.Fprintf(&report, "Source numeric steps: %d.\n", a.Steps)
		for _, n := range notes {
			fmt.Fprintf(&report, "- %s\n", n)
		}
		report.WriteByte('\n')
	}
	if count == 0 {
		return nil, fmt.Errorf("no source agents to convert")
	}
	fmt.Fprintf(&report, "Converted %d source agents. Tool availability is not runtime-verified. Exact resource aliases require the host's native list_mcp_resources/read_mcp_resource. MCP allowlists use exact/bounded mcp__server__tool patterns; servers must be connected with direct exposure before spawning (not codemode). Wildcard matches cross '/'. edit never grants write. Missing source * explicitly uses allow; source denies and external_directory remain effective.\n", count)
	out["agent-sync/report.md"] = []byte(report.String())
	out["agent-sync/web-search.global.manual.json"] = append([]byte(nil), WebProfile...)
	return out, nil
}

// The explicit complete allowlist admits no Code Mode, shell substitute or
// recursion tool. Permissions still narrow these tools per invocation.
func toolCSV(rules []Rule) string {
	tools := []string{"read", "bash", "edit", "grep", "find", "ls"}
	seen := map[string]bool{}
	for _, t := range tools {
		seen[t] = true
	}
	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			tools = append(tools, t)
		}
	}
	for _, r := range rules {
		if r.Effect == "deny" {
			continue
		}
		switch r.Action {
		case "write":
			add("write")
		case "websearch", "web_search":
			add("web_search")
		case "webfetch", "fetch_content":
			add("fetch_content")
		case "source_check", "get_search_content":
			add(r.Action)
		case "opencode_list_mcp_resources":
			add("list_mcp_resources")
		case "opencode_read_mcp_resource":
			add("read_mcp_resource")
		case "codegraph_*":
			add("mcp__codegraph__*")
		default:
			// Permissions already validated this finite, evidence-backed set.
			for _, server := range []string{"context7", "codegraph", "engram"} {
				prefix := server + "_"
				if strings.HasPrefix(r.Action, prefix) {
					add("mcp__" + server + "__" + strings.TrimPrefix(r.Action, prefix))
				}
			}
			if strings.HasPrefix(r.Action, "supabase*_") {
				add("mcp__supabase*__" + strings.TrimPrefix(r.Action, "supabase*_"))
			}
		}
	}
	return strings.Join(tools, ", ")
}
