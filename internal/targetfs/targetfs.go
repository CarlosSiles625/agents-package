// Package targetfs performs symlink-safe, path-validated reads and writes of
// managed files inside the destination directory. Foreign files are never
// followed or modified.
package targetfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agent-sync/internal/manifest"
	"agent-sync/internal/pathsafe"
)

func abs(target, rel string) string {
	return filepath.Join(target, filepath.FromSlash(rel))
}

// isLink reports whether p is a symlink or, on Windows, a directory junction or
// other reparse point. Readlink succeeds only for links and reparse points, so
// it also catches Windows junctions that Go does not mark as ModeSymlink.
func isLink(p string) bool {
	if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	if _, err := os.Readlink(p); err == nil {
		return true
	}
	return false
}

// Validate rejects a managed path if any component of it under target is a
// symlink or other reparse point. It runs before every read, write, delete,
// backup and recovery operation so that a link can never redirect a managed
// path outside the destination. Missing components are fine: nothing below them
// can exist yet.
func Validate(target, rel string) error {
	if err := pathsafe.ValidateRel(rel); err != nil {
		return err
	}
	cur := target
	for _, seg := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, seg)
		if _, err := os.Lstat(cur); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if isLink(cur) {
			return fmt.Errorf("refusing to access %q through symlink %q", rel, cur)
		}
	}
	return nil
}

// Read returns the content of a managed path. exists is false when the file is
// absent. Symlinks and non-regular files are rejected.
func Read(target, rel string) (exists bool, data []byte, err error) {
	if err := Validate(target, rel); err != nil {
		return false, nil, err
	}
	p := abs(target, rel)
	fi, err := os.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil
		}
		return false, nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return false, nil, fmt.Errorf("refusing to read symlink %q", p)
	}
	if !fi.Mode().IsRegular() {
		return false, nil, fmt.Errorf("not a regular file: %q", p)
	}
	data, err = os.ReadFile(p)
	if err != nil {
		return false, nil, err
	}
	return true, data, nil
}

// WriteAtomic writes data to a managed path via a temporary file and rename.
func WriteAtomic(target, rel string, data []byte) error {
	if err := Validate(target, rel); err != nil {
		return err
	}
	p := abs(target, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".agent-sync-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, p); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Remove deletes a managed path, rejecting symlinks. A missing file is fine.
func Remove(target, rel string) error {
	if err := Validate(target, rel); err != nil {
		return err
	}
	p := abs(target, rel)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List walks the target and returns the managed relative paths present,
// filtered by the manifest. Symlinks and non-regular files are skipped.
func List(target string, m manifest.Manifest) ([]string, error) {
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	err := filepath.WalkDir(target, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if isLink(p) {
			return nil
		}
		rel, err := filepath.Rel(target, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !m.Match(rel) {
			return nil
		}
		if err := pathsafe.ValidateRel(rel); err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	return out, err
}
