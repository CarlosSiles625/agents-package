package manifest

import "testing"

func TestDefaultMatch(t *testing.T) {
	m := Default()
	if err := m.Validate(); err != nil {
		t.Fatalf("default manifest invalid: %v", err)
	}
	yes := []string{"AGENTS.md", "agents/a.md", "agents/sub/deep/b.md"}
	for _, p := range yes {
		if !m.Match(p) {
			t.Errorf("expected %q to match", p)
		}
	}
	no := []string{
		"README.md",
		"skills/x.md",
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
