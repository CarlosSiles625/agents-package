package manifest

import "testing"

func TestDefaultMatch(t *testing.T) {
	m := Default()
	if err := m.Validate(); err != nil {
		t.Fatalf("default manifest invalid: %v", err)
	}
	yes := []string{"AGENTS.md", "agents/a.md", "agents/sub/deep/b.md", "skills/orch-commit/SKILL.md"}
	for _, p := range yes {
		if !m.Match(p) {
			t.Errorf("expected %q to match", p)
		}
	}
	no := []string{
		"README.md",
		"skills/x.md",
		"skills/SKILL.md",
		"skills/x/ref.md",
		"skills/x/skill.md",
		"skills/a/b/SKILL.md",
		"skills/x/references/SKILL.md",
		"agents/x.txt",
		"agents/../AGENTS.md",
		"../AGENTS.md",
		"/etc/passwd",
		"C:/AGENTS.md",
		`agents\a.md`,
		"agents/CON.md",
		"agents/bad./x.md",
		"opencode.json",
		"AGENTS.md.bak",
	}
	for _, p := range no {
		if m.Match(p) {
			t.Errorf("expected %q NOT to match", p)
		}
	}
}

func TestValidateRejectsBadPatterns(t *testing.T) {
	if err := (Manifest{Version: 1, Include: []string{"../secret.md"}}).Validate(); err == nil {
		t.Error("expected traversal pattern to be rejected")
	}
	if err := (Manifest{Version: 99, Include: []string{"AGENTS.md"}}).Validate(); err == nil {
		t.Error("expected bad version to be rejected")
	}
	if err := (Manifest{Version: 1}).Validate(); err == nil {
		t.Error("expected empty include to be rejected")
	}
}

func TestAgentsManifestExcludesNonAgents(t *testing.T) {
	m := Agents()
	if err := m.Validate(); err != nil {
		t.Fatalf("agents manifest invalid: %v", err)
	}
	if !m.Match("agents/a.md") || !m.Match("agents/sub/b.md") {
		t.Error("agents manifest must match agent files")
	}
	for _, p := range []string{"AGENTS.md", "skills/x/SKILL.md", "README.md"} {
		if m.Match(p) {
			t.Errorf("agents manifest must not match %q", p)
		}
	}
}
