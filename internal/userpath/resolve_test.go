package userpath

import (
	"path"
	"path/filepath"
	"testing"
)

func TestBashProfilesPrecedence(t *testing.T) {
	home := "/home/u"
	cases := []struct {
		name   string
		exists []string
		want   string
	}{
		{"none falls back to bash_profile", nil, ".bash_profile"},
		{"bash_profile first", []string{".bash_profile", ".bash_login", ".profile"}, ".bash_profile"},
		{"bash_login when no bash_profile", []string{".bash_login", ".profile"}, ".bash_login"},
		{"profile when only profile", []string{".profile"}, ".profile"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := map[string]bool{}
			for _, n := range tc.exists {
				set[filepath.Join(home, n)] = true
			}
			exists := func(p string) bool { return set[p] }
			got := bashProfiles(home, exists)
			if len(got) != 2 || got[0] != filepath.Join(home, ".bashrc") {
				t.Fatalf("profiles = %v", got)
			}
			if want := filepath.Join(home, tc.want); got[1] != want {
				t.Fatalf("login profile = %q, want %q", got[1], want)
			}
		})
	}
}

func TestZshProfileDir(t *testing.T) {
	home := "/home/u"
	if dir, err := zshProfileDir(home, "", false); err != nil || dir != home {
		t.Fatalf("unset ZDOTDIR = %q, %v", dir, err)
	}
	if dir, err := zshProfileDir(home, "", true); err != nil || dir != home {
		t.Fatalf("empty ZDOTDIR = %q, %v", dir, err)
	}
	if dir, err := zshProfileDir(home, "/abs/zdot", true); err != nil || dir != "/abs/zdot" {
		t.Fatalf("absolute ZDOTDIR = %q, %v", dir, err)
	}
	if _, err := zshProfileDir(home, "relative/zdot", true); err == nil {
		t.Fatal("relative ZDOTDIR must be rejected")
	}
}

func TestZshProfiles(t *testing.T) {
	got := zshProfiles("/abs/zdot")
	want := []string{path.Join("/abs/zdot", ".zshrc"), path.Join("/abs/zdot", ".zprofile")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("zshProfiles = %v, want %v", got, want)
	}
}

func TestDetectShell(t *testing.T) {
	// detectShell parses a POSIX $SHELL value: the basename must be exactly
	// "bash" or "zsh". Windows shell paths (e.g. bash.exe) are intentionally not
	// recognized here because on Windows the PATH is handled through the
	// registry, never through $SHELL.
	cases := map[string]string{
		"/bin/bash":    "bash",
		"/usr/bin/zsh": "zsh",
	}
	for in, want := range cases {
		got, ok := detectShell(in)
		if !ok || got != want {
			t.Errorf("detectShell(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := detectShell("/bin/fish"); ok {
		t.Error("fish must not be detected")
	}
}
