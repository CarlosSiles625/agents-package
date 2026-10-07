package modelpref_test

import (
	"bytes"
	"testing"

	"agent-sync/internal/modelpref"
)

func explicit(ref string) modelpref.Pref {
	return modelpref.Pref{Mode: modelpref.ModeExplicit, Model: ref}
}

func inheritPref() modelpref.Pref { return modelpref.Pref{Mode: modelpref.ModeInherit} }

// TestRenderUndoRoundTrip covers V6/V7: every supported edit is reversible byte
// for byte, across LF and CRLF endings, comments, quoting and an absent
// frontmatter block.
func TestRenderUndoRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		pref modelpref.Pref
	}{
		{"no frontmatter explicit", "body\n", explicit("openai/gpt-4")},
		{"no frontmatter empty file", "", explicit("openai/gpt-4")},
		{"frontmatter no model explicit", "---\ntitle: x\n---\nbody\n", explicit("openai/gpt-4")},
		{"frontmatter model explicit", "---\ntitle: x\nmodel: old\ndesc: y\n---\nbody\n", explicit("openai/gpt-4")},
		{"frontmatter only model explicit", "---\nmodel: old\n---\nbody\n", explicit("openai/gpt-4")},
		{"frontmatter model inherit", "---\ntitle: x\nmodel: old\ndesc: y\n---\nbody\n", inheritPref()},
		{"frontmatter only model inherit", "---\nmodel: old\n---\nbody\n", inheritPref()},
		{"frontmatter no model inherit", "---\ntitle: x\n---\nbody\n", inheritPref()},
		{"crlf explicit", "---\r\ntitle: x\r\nmodel: old\r\n---\r\nbody\r\n", explicit("openai/gpt-4")},
		{"crlf only model inherit", "---\r\nmodel: old\r\n---\r\nbody\r\n", inheritPref()},
		{"quoted scalar", "---\nmodel: 'old'\n---\nbody\n", explicit("openai/gpt-4#high")},
		{"trailing comment", "---\nmodel: old # keep\n---\nbody\n", explicit("openai/gpt-4")},
		{"body contains dashes", "---\nmodel: old\n---\n---\nnot frontmatter\n", explicit("openai/gpt-4")},
		{"no trailing newline", "---\nmodel: old\n---\nbody", explicit("openai/gpt-4")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered, rec, err := modelpref.Render([]byte(tc.raw), tc.pref)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			got, err := modelpref.Undo(rendered, rec)
			if err != nil {
				t.Fatalf("Undo: %v", err)
			}
			if !bytes.Equal(got, []byte(tc.raw)) {
				t.Fatalf("round trip mismatch:\n got %q\nwant %q\nrendered %q", got, tc.raw, rendered)
			}
			if err := modelpref.Verify(rendered, rec); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

// TestRenderPreservesEverythingButTheModelLine verifies the byte-exact
// preservation contract: only the scalar of the top-level model key changes.
func TestRenderPreservesEverythingButTheModelLine(t *testing.T) {
	raw := "---\n# comment\ntitle: 'x'\nmodel: old # keep\nafter: 1\n---\n# body\n"
	want := "---\n# comment\ntitle: 'x'\nmodel: openai/gpt-4 # keep\nafter: 1\n---\n# body\n"
	got, _, err := modelpref.Render([]byte(raw), explicit("openai/gpt-4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("rendered = %q, want %q", got, want)
	}

	raw = "---\ntitle: x\nmodel: old\nafter: 1\n---\nbody\n"
	want = "---\ntitle: x\nafter: 1\n---\nbody\n"
	got, _, err = modelpref.Render([]byte(raw), inheritPref())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("inherit rendered = %q, want %q", got, want)
	}
}

func TestModelOf(t *testing.T) {
	model, ok, err := modelpref.ModelOf([]byte("---\nmodel: 'openai/gpt-4'\n---\nbody\n"))
	if err != nil || !ok || model != "openai/gpt-4" {
		t.Fatalf("ModelOf = %q, %v, %v", model, ok, err)
	}
	if _, ok, err := modelpref.ModelOf([]byte("body\n")); err != nil || ok {
		t.Fatalf("absent model: ok=%v err=%v", ok, err)
	}
	if _, _, err := modelpref.ModelOf([]byte("---\nmodel: x\n")); err == nil {
		t.Fatal("expected unterminated frontmatter error")
	}
	// A YAML plain scalar "model:value" (no space) is not a mapping key.
	if _, ok, err := modelpref.ModelOf([]byte("model:value\n")); err != nil || ok {
		t.Fatalf("model:value should not be a key: ok=%v err=%v", ok, err)
	}
}

// TestRejectsUnsupportedStructures covers V7: duplicates, merge keys, complex
// values, aliases/tags and unterminated quotes are refused, never guessed at.
func TestRejectsUnsupportedStructures(t *testing.T) {
	bad := []struct {
		name string
		raw  string
	}{
		{"duplicate model", "---\nmodel: a\nmodel: b\n---\n"},
		{"merge key", "---\n<<: *base\n---\n"},
		{"merge key inline", "---\n<<: {a: 1}\n---\n"},
		{"flow mapping", "---\nmodel: {a: 1}\n---\n"},
		{"flow sequence", "---\nmodel: [a]\n---\n"},
		{"anchor", "---\nmodel: &anchor x\n---\n"},
		{"alias", "---\nmodel: *anchor\n---\n"},
		{"block literal", "---\nmodel: |\n---\n"},
		{"block folded", "---\nmodel: >\n---\n"},
		{"tag", "---\nmodel: !tag x\n---\n"},
		{"unterminated quote", "---\nmodel: 'x\n---\n"},
		{"unterminated frontmatter", "---\nmodel: x\n"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := modelpref.Render([]byte(tc.raw), explicit("openai/gpt-4")); err == nil {
				t.Fatalf("Render accepted %q", tc.raw)
			}
			if _, _, err := modelpref.ModelOf([]byte(tc.raw)); err == nil {
				t.Fatalf("ModelOf accepted %q", tc.raw)
			}
		})
	}
}

func TestValidateRef(t *testing.T) {
	good := []string{"openai/gpt-4", "anthropic/claude-3-5", "opencode-go/qwen#high", "a/b#c"}
	for _, ref := range good {
		if err := modelpref.ValidateRef(ref); err != nil {
			t.Errorf("ValidateRef(%q) = %v", ref, err)
		}
	}
	bad := []string{"", "abc", "/x", "x/", "x/#v", "x/a#", "x/a b", "x a/b"}
	for _, ref := range bad {
		if err := modelpref.ValidateRef(ref); err == nil {
			t.Errorf("ValidateRef(%q) = nil, want error", ref)
		}
	}
}

// TestUndoFailsWhenModelFieldEdited covers V8: a hand-edited model scalar or a
// duplicated key makes Undo fail closed, while a body-only edit still reverses.
func TestUndoFailsWhenModelFieldEdited(t *testing.T) {
	raw := "---\ntitle: x\n---\nbody\n"
	rendered, rec, err := modelpref.Render([]byte(raw), explicit("openai/gpt-4"))
	if err != nil {
		t.Fatal(err)
	}

	tampered := bytes.Replace(rendered, []byte("openai/gpt-4"), []byte("openai/manual"), 1)
	if err := modelpref.Verify(tampered, rec); err == nil {
		t.Fatal("Verify should fail after a manual model edit")
	}
	if _, err := modelpref.Undo(tampered, rec); err == nil {
		t.Fatal("Undo should fail after a manual model edit")
	}

	dup := bytes.Replace(rendered, []byte("---\nbody\n"), []byte("model: extra\n---\nbody\n"), 1)
	if _, err := modelpref.Undo(dup, rec); err == nil {
		t.Fatal("Undo should fail after a duplicated model key")
	}
}

func TestUndoPreservesBodyEdit(t *testing.T) {
	raw := "---\ntitle: x\n---\nbody\n"
	rendered, rec, err := modelpref.Render([]byte(raw), explicit("openai/gpt-4"))
	if err != nil {
		t.Fatal(err)
	}
	edited := append(append([]byte{}, rendered...), []byte("extra line\n")...)
	if err := modelpref.Verify(edited, rec); err != nil {
		t.Fatalf("a body-only edit must still verify: %v", err)
	}
	got, err := modelpref.Undo(edited, rec)
	if err != nil {
		t.Fatal(err)
	}
	if want := raw + "extra line\n"; string(got) != want {
		t.Fatalf("Undo = %q, want %q", got, want)
	}
}

func TestRenderRejectsInvalidPref(t *testing.T) {
	if _, _, err := modelpref.Render([]byte("body\n"), modelpref.Pref{Mode: modelpref.ModeExplicit}); err == nil {
		t.Fatal("explicit pref without a model must be rejected")
	}
	if _, _, err := modelpref.Render([]byte("body\n"), modelpref.Pref{Mode: modelpref.ModeInherit, Model: "x/y"}); err == nil {
		t.Fatal("inherit pref with a model must be rejected")
	}
}
