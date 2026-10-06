// Package pathsafe provides validation and safe access helpers for the
// portable, regular, relative paths that agent-sync is allowed to manage.
//
// It rejects absolute paths, traversal, drive letters, backslashes, control
// characters, characters that are illegal on Windows, trailing dots/spaces,
// Windows reserved device names and case-insensitive collisions. It also
// refuses to traverse symlinks in the parents of critical directories or to
// read/write a managed path that is itself a symlink.
package pathsafe

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// reservedNames are the Windows reserved device names (matched
// case-insensitively, including when followed by an extension).
var reservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidateRel validates a manifest/target relative path. The path must be a
// portable, regular, normalized relative path using forward slashes.
func ValidateRel(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.ContainsRune(p, 0) {
		return fmt.Errorf("path contains a NUL byte")
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return fmt.Errorf("path must be relative: %q", p)
	}
	if len(p) >= 2 && p[1] == ':' {
		return fmt.Errorf("path must not contain a drive letter: %q", p)
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("path must use forward slashes: %q", p)
	}
	cleaned := path.Clean(p)
	if cleaned != p || cleaned == "." || cleaned == ".." {
		return fmt.Errorf("path is not normalized: %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if err := validateSegment(seg); err != nil {
			return fmt.Errorf("%q: %w", p, err)
		}
	}
	return nil
}

func validateSegment(seg string) error {
	if seg == "" {
		return fmt.Errorf("empty path segment")
	}
	if seg == "." || seg == ".." {
		return fmt.Errorf("path traversal segment %q", seg)
	}
	if strings.TrimRight(seg, ".") != seg {
		return fmt.Errorf("segment %q has a trailing dot", seg)
	}
	if strings.TrimRight(seg, " ") != seg || strings.HasPrefix(seg, " ") {
		return fmt.Errorf("segment %q has a leading or trailing space", seg)
	}
	for _, r := range seg {
		if r < 0x20 {
			return fmt.Errorf("segment %q contains a control character", seg)
		}
		if strings.ContainsRune(`<>:"|?*`, r) {
			return fmt.Errorf("segment %q contains a character that is not portable", seg)
		}
	}
	base := seg
	if i := strings.IndexByte(seg, '.'); i >= 0 {
		base = seg[:i]
	}
	if reservedNames[strings.ToLower(base)] {
		return fmt.Errorf("segment %q is a reserved Windows device name", seg)
	}
	return nil
}

// CaseCollisions returns two paths that differ only by case, if any. Such
// paths collide on case-insensitive filesystems.
func CaseCollisions(paths []string) (string, string, bool) {
	seen := make(map[string]string, len(paths))
	for _, p := range paths {
		k := strings.ToLower(p)
		if prev, ok := seen[k]; ok && prev != p {
			return prev, p, true
		}
		seen[k] = p
	}
	return "", "", false
}

// CheckNoSymlinkParents fails if any existing component of p (including p)
// is a symlink. This protects the critical state/source/target roots.
func CheckNoSymlinkParents(p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	vol := filepath.VolumeName(abs)
	rest := strings.TrimPrefix(abs[len(vol):], string(os.PathSeparator))
	cur := vol + string(os.PathSeparator)
	for _, part := range strings.Split(rest, string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symlink", cur)
		}
	}
	return nil
}
