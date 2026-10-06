package targetfs_test

import (
	"os"
	"path/filepath"
	"testing"

	"agent-sync/internal/targetfs"
	"agent-sync/internal/testsupport"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAllowsPlainNestedPath(t *testing.T) {
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "agents", "a.md"), "a\n")
	if err := targetfs.Validate(target, "agents/a.md"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	exists, data, err := targetfs.Read(target, "agents/a.md")
	if err != nil || !exists || string(data) != "a\n" {
		t.Fatalf("Read = %v, %q, %v", exists, data, err)
	}
}

// TestLinkParentIsRejected verifies that a managed path whose parent is a
// symlink or Windows junction is refused for reads, writes and deletes, and
// that the link target outside the destination is never modified.
func TestLinkParentIsRejected(t *testing.T) {
	target := t.TempDir()
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "keep.md"), "keep\n")
	testsupport.MakeDirLink(t, filepath.Join(target, "agents"), external)

	if err := targetfs.Validate(target, "agents/new.md"); err == nil {
		t.Fatal("Validate should reject a link parent")
	}
	if _, _, err := targetfs.Read(target, "agents/new.md"); err == nil {
		t.Fatal("Read should reject a link parent")
	}
	if err := targetfs.WriteAtomic(target, "agents/new.md", []byte("x")); err == nil {
		t.Fatal("WriteAtomic should reject a link parent")
	}
	if _, err := os.Stat(filepath.Join(external, "new.md")); !os.IsNotExist(err) {
		t.Fatalf("external file was created through the link: %v", err)
	}
	if err := targetfs.Remove(target, "agents/keep.md"); err == nil {
		t.Fatal("Remove should reject a link parent")
	}
	if data, err := os.ReadFile(filepath.Join(external, "keep.md")); err != nil || string(data) != "keep\n" {
		t.Fatalf("external file changed: %q, %v", data, err)
	}
}
