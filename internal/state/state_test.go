package state_test

import (
	"os"
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
