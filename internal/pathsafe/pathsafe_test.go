package pathsafe

import "testing"

func TestValidateRelAccept(t *testing.T) {
	for _, p := range []string{"AGENTS.md", "agents/a.md", "agents/sub/deep/b.md"} {
		if err := ValidateRel(p); err != nil {
			t.Errorf("ValidateRel(%q) = %v, want nil", p, err)
		}
	}
}

func TestValidateRelReject(t *testing.T) {
	for _, p := range []string{
		"", "/abs", `\abs`, "C:/x", `C:\x`,
		"../x", "a/../b", "a/./b", "a//b", "./a",
		`a\b`, "agents/CON.md", "agents/PRN.txt", "x.", "x ", " x",
		"a/b\x01c", "a/b|c.md", "a/b:c.md",
	} {
		if err := ValidateRel(p); err == nil {
			t.Errorf("ValidateRel(%q) = nil, want error", p)
		}
	}
}

func TestCaseCollisions(t *testing.T) {
	if _, _, ok := CaseCollisions([]string{"agents/A.md", "agents/a.md"}); !ok {
		t.Error("expected a case collision")
	}
	if _, _, ok := CaseCollisions([]string{"agents/a.md", "agents/b.md"}); ok {
		t.Error("did not expect a case collision")
	}
}

func TestCheckNoSymlinkParents(t *testing.T) {
	if err := CheckNoSymlinkParents(t.TempDir() + "/does/not/exist"); err != nil {
		t.Errorf("unexpected error for non-existent path: %v", err)
	}
}
