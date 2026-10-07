package userpath

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// bashProfiles returns the profiles agent-sync edits for bash: ~/.bashrc plus
// the first existing login profile (~/.bash_profile, ~/.bash_login, ~/.profile),
// falling back to creating ~/.bash_profile when none exists. This mirrors the
// order bash itself uses to pick a login file.
func bashProfiles(home string, exists func(string) bool) []string {
	out := []string{filepath.Join(home, ".bashrc")}
	for _, n := range []string{".bash_profile", ".bash_login", ".profile"} {
		p := filepath.Join(home, n)
		if exists(p) {
			return append(out, p)
		}
	}
	return append(out, filepath.Join(home, ".bash_profile"))
}

// zshProfileDir resolves the directory that holds the zsh startup files. When
// ZDOTDIR is set it must be absolute; a relative value is rejected rather than
// guessed.
func zshProfileDir(home, zdotdir string, zdotdirSet bool) (string, error) {
	if !zdotdirSet || zdotdir == "" {
		return home, nil
	}
	if !strings.HasPrefix(zdotdir, "/") {
		return "", fmt.Errorf("ZDOTDIR %q is not absolute; refusing to guess where the zsh profiles are", zdotdir)
	}
	return zdotdir, nil
}

func zshProfiles(dir string) []string {
	return []string{path.Join(dir, ".zshrc"), path.Join(dir, ".zprofile")}
}

// detectShell recognises bash or zsh from a $SHELL path basename.
func detectShell(shellPath string) (string, bool) {
	base := strings.ToLower(path.Base(strings.ReplaceAll(shellPath, `\`, "/")))
	switch base {
	case "bash":
		return "bash", true
	case "zsh":
		return "zsh", true
	}
	return "", false
}

// resolveProfiles maps a shell to the concrete profile files to edit, using the
// real filesystem only to test login-profile existence.
func resolveProfiles(shell string, cfg posixConfig) ([]string, error) {
	exists := func(p string) bool {
		_, err := os.Lstat(p)
		return err == nil
	}
	switch shell {
	case "bash":
		return bashProfiles(cfg.Home, exists), nil
	case "zsh":
		dir, err := zshProfileDir(cfg.Home, cfg.Zdotdir, cfg.ZdotdirSet)
		if err != nil {
			return nil, err
		}
		return zshProfiles(dir), nil
	}
	return nil, fmt.Errorf("unsupported shell %q", shell)
}
