package piexport

import (
	"os"
	"testing"
)

// parseAgentSource reads the authoritative repository source. These tests pin
// the OpenCode last-match semantics of the ordered rules, independent of the
// Pi export, which intentionally has no mapping for these MCP action names.
func parseAgentSource(t *testing.T, name string) Agent {
	t.Helper()
	b, err := os.ReadFile("../../agents/" + name)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return a
}

// effectiveEffect applies OpenCode's last-match-wins over the ordered list.
func effectiveEffect(a Agent, action, resource string) (string, bool) {
	effect, found := "", false
	for _, r := range a.Rules {
		if matches(r.Action, action) && matches(r.Resource, resource) {
			effect, found = r.Effect, true
		}
	}
	return effect, found
}

func browserListedActions() []string {
	return []string{
		"playwright_browser_navigate",
		"playwright_browser_snapshot",
		"playwright_browser_wait_for",
		"playwright_browser_take_screenshot",
		"playwright_browser_click",
		"playwright_browser_fill_form",
		"playwright_browser_type",
		"playwright_browser_press_key",
		"playwright_browser_tabs",
		"playwright_browser_console_messages",
		"playwright_browser_network_requests",
		"chrome-devtools_list_pages",
		"chrome-devtools_new_page",
		"chrome-devtools_select_page",
		"chrome-devtools_navigate_page",
		"chrome-devtools_take_snapshot",
		"chrome-devtools_wait_for",
		"chrome-devtools_take_screenshot",
		"chrome-devtools_click",
		"chrome-devtools_fill",
		"chrome-devtools_fill_form",
		"chrome-devtools_press_key",
		"chrome-devtools_get_css_styles",
		"chrome-devtools_list_console_messages",
		"chrome-devtools_list_network_requests",
	}
}

var browserUnlistedActions = []string{
	"playwright_browser_run_code_unsafe",
	"playwright_browser_evaluate",
	"playwright_browser_network_request",
	"playwright_browser_storage_state",
	"chrome-devtools_evaluate_script",
	"chrome-devtools_get_network_request",
	"chrome-devtools_unlisted_operation",
}

func TestBrowserRestrictedProfilesPreserveOrderedRules(t *testing.T) {
	for _, name := range []string{"debugger.md", "debugger-fast.md", "debugger-deep.md", "verifier.md", "general-purpose.md"} {
		a := parseAgentSource(t, name)
		for _, action := range browserListedActions() {
			if got, ok := effectiveEffect(a, action, "*"); !ok || got != "ask" {
				t.Errorf("%s: listed %s = %q (matched %v), want ask", name, action, got, ok)
			}
		}
		// The wildcard deny precedes the per-action asks: anything not listed,
		// including code evaluation and network/cookie details, stays denied.
		for _, action := range browserUnlistedActions {
			if got, ok := effectiveEffect(a, action, "https://example.test/"); !ok || got != "deny" {
				t.Errorf("%s: unlisted %s = %q (matched %v), want deny", name, action, got, ok)
			}
		}
		wantFallback := "deny"
		if name == "general-purpose.md" {
			wantFallback = "ask"
		}
		if got, ok := effectiveEffect(a, "some_unlisted_tool", "*"); !ok || got != wantFallback {
			t.Errorf("%s: fallback = %q (matched %v), want %s", name, got, ok, wantFallback)
		}
		if got, ok := effectiveEffect(a, "read", "src/a.go"); !ok || got != "allow" {
			t.Errorf("%s: read = %q (matched %v), want allow", name, got, ok)
		}
		if got, _ := effectiveEffect(a, "execute", "*"); name == "general-purpose.md" {
			if got != "ask" {
				t.Errorf("%s: execute = %q, want ask", name, got)
			}
		} else if got != "allow" {
			t.Errorf("%s: execute = %q, want allow", name, got)
		}
		if got, _ := effectiveEffect(a, "codegraph_codegraph_explore", "*"); name == "general-purpose.md" {
			// No explicit CodeGraph rule: the original *: ask fallback governs.
			if got != "ask" {
				t.Errorf("%s: codegraph = %q, want the ask fallback", name, got)
			}
		} else if got != "allow" {
			t.Errorf("%s: codegraph = %q, want allow", name, got)
		}
	}
}

func TestImplementerBrowserRulesStayAsk(t *testing.T) {
	for _, name := range []string{"implementer.md", "implementer-fast.md", "implementer-deep.md", "database-implementer.md"} {
		a := parseAgentSource(t, name)
		// The pre-existing universal default and its consequences stay intact.
		if got, ok := effectiveEffect(a, "some_unlisted_tool", "*"); !ok || got != "allow" {
			t.Errorf("%s: fallback = %q (matched %v), want allow", name, got, ok)
		}
		for _, action := range browserListedActions() {
			if got, ok := effectiveEffect(a, action, "*"); !ok || got != "ask" {
				t.Errorf("%s: listed %s = %q (matched %v), want ask", name, action, got, ok)
			}
		}
		for _, action := range browserUnlistedActions {
			if got, ok := effectiveEffect(a, action, "*"); !ok || got != "ask" {
				t.Errorf("%s: unlisted %s = %q (matched %v), want ask", name, action, got, ok)
			}
		}
		for _, c := range []struct{ action, resource, want string }{
			{"execute", "*", "allow"},
			{"skill", "context7-mcp", "allow"},
			{"skill", "some-project-skill", "allow"},
			{"skill", "orch-commit", "deny"},
			{"skill", "report", "deny"},
			{"shell", "git push origin main", "deny"},
		} {
			if got, ok := effectiveEffect(a, c.action, c.resource); !ok || got != c.want {
				t.Errorf("%s: %s:%s = %q (matched %v), want %s", name, c.action, c.resource, got, ok, c.want)
			}
		}
	}
}
