package state_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"agent-sync/internal/state"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := state.New("repo", "main")
	st.Commit = "abc"
	st.Files["AGENTS.md"] = state.FileState{Exists: true, Hash: state.Hash([]byte("x"))}
	if err := st.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, exists, err := state.Load(dir)
	if err != nil || !exists {
		t.Fatalf("Load: exists=%v err=%v", exists, err)
	}
	if got.Commit != "abc" {
		t.Fatalf("commit = %q", got.Commit)
	}
	if got.Files["AGENTS.md"].Hash != state.Hash([]byte("x")) {
		t.Fatalf("files = %+v", got.Files)
	}
}

func TestLoadMissing(t *testing.T) {
	got, exists, err := state.Load(t.TempDir())
	if err != nil || exists {
		t.Fatalf("Load: exists=%v err=%v", exists, err)
	}
	if got.Files == nil {
		t.Fatal("Files map must be non-nil")
	}
}

func TestLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(state.Path(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.Load(dir); err == nil {
		t.Fatal("expected an error for corrupt state")
	}
}

func TestHashVector(t *testing.T) {
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := state.Hash(nil); got != want {
		t.Fatalf("Hash(nil) = %s, want %s", got, want)
	}
}

// TestLoadMigratesV1 covers V3: a version 1 file loads, is upgraded in memory
// and is written back as version 2 without losing the recorded files.
func TestLoadMigratesV1(t *testing.T) {
	dir := t.TempDir()
	v1 := `{"version":1,"repo":"r","branch":"main","commit":"c","files":{"AGENTS.md":{"exists":true,"hash":"h"}}}`
	if err := os.WriteFile(state.Path(dir), []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	st, exists, err := state.Load(dir)
	if err != nil || !exists {
		t.Fatalf("Load: exists=%v err=%v", exists, err)
	}
	if st.Version != state.CurrentVersion {
		t.Fatalf("in-memory version = %d, want %d", st.Version, state.CurrentVersion)
	}
	if st.ModelPrefs == nil {
		t.Fatal("ModelPrefs must be non-nil after migration")
	}
	if st.Target != "" || st.Files["AGENTS.md"].Hash != "h" {
		t.Fatalf("migrated state = %+v", st)
	}
	if err := st.Save(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(state.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if string(m["version"]) != "2" {
		t.Fatalf("persisted version = %s, want 2", m["version"])
	}
}

func TestLoadRejectsUnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(state.Path(dir), []byte(`{"version":99,"files":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.Load(dir); err == nil {
		t.Fatal("expected an error for an unsupported state version")
	}
}

// TestLockSerializesWithoutTakeover covers V5: a second acquire fails closed
// with ErrLocked (and a diagnostic), and the lock is reusable after release.
func TestLockSerializesWithoutTakeover(t *testing.T) {
	dir := t.TempDir()
	l1, err := state.Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := state.Acquire(dir); !errors.Is(err, state.ErrLocked) {
		t.Fatalf("second Acquire err = %v, want ErrLocked", err)
	} else if !strings.Contains(err.Error(), "owner") {
		t.Fatalf("lock diagnostic = %q, want owner/age", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	l2, err := state.Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if err := l2.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if err := l2.Release(); err != nil {
		t.Fatalf("Release must be idempotent: %v", err)
	}
}
