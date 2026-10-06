// Package testsupport provides git fixtures for tests. It is only used by
// tests and never by the shipped binary.
package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// MakeDirLink creates a directory link from link to target. It prefers a real
// symlink and falls back to a Windows directory junction (which needs no
// elevation); it skips the test when neither can be created.
func MakeDirLink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err == nil {
			return
		}
		t.Skipf("cannot create symlink or junction: %v: %s", err, strings.TrimSpace(string(out)))
	}
	t.Skip("cannot create a directory symlink on this platform")
}

// Git runs a git command and fails the test on error.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// InitRepo initialises a git repository with branch main and commits files.
func InitRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	Git(t, "", "init", "-b", "main", dir)
	return Commit(t, dir, files, nil, "initial")
}

// Commit writes/removes files and creates a commit, returning the new HEAD.
func Commit(t *testing.T, dir string, write map[string]string, remove []string, msg string) string {
	t.Helper()
	for name, content := range write {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range remove {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-m", msg)
	return strings.TrimSpace(Git(t, dir, "rev-parse", "HEAD"))
}
