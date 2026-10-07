// Package userpath adds the running executable's directory to the user's PATH.
//
// It is deliberately narrow: on Windows it edits only the HKCU\Environment
// "Path" value through the registry API; on Linux it edits only the user's
// shell profiles. It never copies or moves the binary, never reads unrelated
// environment variables, never rewrites unrelated entries or bytes, and never
// parses the rest of a profile. A dry-run performs no writes of any kind: no
// backups, directories, registry keys, locks or caches.
package userpath

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Options controls one PATH installation.
type Options struct {
	// Shell is an explicit POSIX shell name ("bash" or "zsh"). It is rejected
	// on Windows, where the shell is irrelevant.
	Shell string
	// DryRun previews the change without any write, backup, directory or lock.
	DryRun bool
	// AssumeYes skips the interactive consent prompt (set by --add-to-path).
	AssumeYes bool
	// Interactive reports whether a terminal is available for prompts.
	Interactive bool
	// Stdin/Stdout are used only for prompts.
	Stdin  io.Reader
	Stdout io.Writer
	// BackupDir receives private, user-owned raw backups of anything changed.
	// It is required for real writes and is never created during a dry-run.
	BackupDir string
}

// Preview describes the safe, own-lines-only change to one target. Added and
// Removed only ever contain lines agent-sync itself generated or removed; the
// rest of the target is never echoed.
type Preview struct {
	Target  string
	Create  bool
	Added   []string
	Removed []string
	Already bool
}

// Report summarizes a PATH installation (or dry-run).
type Report struct {
	Dir      string
	Previews []Preview
	Backups  []string
	Skipped  bool
	Note     string
}

// ExecutableDir returns the directory containing the running executable,
// resolving symlinks when possible so the stable, real location is used
// regardless of the current working directory. If the path cannot be resolved
// the unresolved path is used rather than failing: it is still a stable
// directory as long as the binary is not moved (documented limitation).
func ExecutableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating the running executable: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return filepath.Dir(exe), nil
}

// ParseShell validates an explicit POSIX shell name.
func ParseShell(s string) (string, error) {
	switch s {
	case "bash", "zsh":
		return s, nil
	case "":
		return "", fmt.Errorf("no shell given")
	default:
		return "", fmt.Errorf("unsupported shell %q; use bash or zsh", s)
	}
}
