package apply_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agent-sync/internal/apply"
	"agent-sync/internal/manifest"
	"agent-sync/internal/planner"
	"agent-sync/internal/state"
)

func remoteFile(content string) map[string]planner.Remote {
	return map[string]planner.Remote{
		"AGENTS.md": {Exists: true, Hash: state.Hash([]byte(content)), Data: []byte(content)},
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestApplyWritesAndRecords(t *testing.T) {
	target := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	st := state.New("repo", "main")
	plan, err := planner.Build(manifest.Default(), st.Files, nil, remoteFile("v1"), planner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	plan.Commit = "c1"
	if err := apply.Apply(context.Background(), target, stateDir, st, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(target, "AGENTS.md"))
	if string(data) != "v1" {
		t.Fatalf("content = %q", data)
	}
	got, exists, err := state.Load(stateDir)
	if err != nil || !exists {
		t.Fatalf("reload: exists=%v err=%v", exists, err)
	}
	if got.Journal != nil {
		t.Fatal("journal should be cleared after success")
	}
	if got.Commit != "c1" || got.Files["AGENTS.md"].Hash != state.Hash([]byte("v1")) {
		t.Fatalf("state = %+v", got)
	}
}

func TestApplyRevalidatesBeforeWriting(t *testing.T) {
	target := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	write(t, filepath.Join(target, "AGENTS.md"), "orig")
	st := state.New("repo", "main")
	baseline := map[string]state.FileState{"AGENTS.md": {Exists: true, Hash: state.Hash([]byte("orig"))}}
	local := map[string][]byte{"AGENTS.md": []byte("orig")}
	plan, err := planner.Build(manifest.Default(), baseline, local, remoteFile("new"), planner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Executable()) != 1 {
		t.Fatalf("expected one write, got %d", len(plan.Executable()))
	}
	// Something changes between planning and applying.
	write(t, filepath.Join(target, "AGENTS.md"), "tampered")

	if err := apply.Apply(context.Background(), target, stateDir, st, plan); err == nil {
		t.Fatal("expected revalidation failure")
	}
	data, _ := os.ReadFile(filepath.Join(target, "AGENTS.md"))
	if string(data) != "tampered" {
		t.Fatalf("file was modified: %q", data)
	}
	if st.Journal != nil {
		t.Fatal("no journal should be persisted when revalidation fails")
	}
}

func TestJournalPendingBlocks(t *testing.T) {
	target := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	st := state.New("repo", "main")
	st.Journal = &state.Journal{ID: "x", Ops: []state.JournalOp{{Path: "AGENTS.md", Action: "write"}}}
	err := apply.Apply(context.Background(), target, stateDir, st, planner.Plan{})
	if !errors.Is(err, apply.ErrJournalPending) {
		t.Fatalf("err = %v, want ErrJournalPending", err)
	}
}

func TestRecoverRestoresBackup(t *testing.T) {
	target := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	write(t, filepath.Join(target, "AGENTS.md"), "new")
	backupRel := filepath.Join("backups", "tx", "000_AGENTS.md")
	write(t, filepath.Join(stateDir, backupRel), "old")
	st := state.New("repo", "main")
	st.Journal = &state.Journal{ID: "tx", Ops: []state.JournalOp{
		{Path: "AGENTS.md", Action: "write", HadOld: true, Backup: backupRel, NewHash: state.Hash([]byte("new"))},
	}}
	if err := apply.Recover(context.Background(), target, stateDir, st); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(target, "AGENTS.md"))
	if string(data) != "old" {
		t.Fatalf("restored = %q, want old", data)
	}
	if st.Journal != nil {
		t.Fatal("journal should be cleared")
	}
}

func TestRecoverDoesNotOverwriteNewerChanges(t *testing.T) {
	target := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	write(t, filepath.Join(target, "AGENTS.md"), "user-change")
	backupRel := filepath.Join("backups", "tx", "000_AGENTS.md")
	write(t, filepath.Join(stateDir, backupRel), "old")
	st := state.New("repo", "main")
	st.Journal = &state.Journal{ID: "tx", Ops: []state.JournalOp{
		{Path: "AGENTS.md", Action: "write", HadOld: true, Backup: backupRel, NewHash: state.Hash([]byte("new"))},
	}}
	if err := apply.Recover(context.Background(), target, stateDir, st); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(target, "AGENTS.md"))
	if string(data) != "user-change" {
		t.Fatalf("recover overwrote a newer change: %q", data)
	}
}

func TestRecoverRemovesCreatedFile(t *testing.T) {
	target := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	write(t, filepath.Join(target, "AGENTS.md"), "created")
	st := state.New("repo", "main")
	st.Journal = &state.Journal{ID: "tx", Ops: []state.JournalOp{
		{Path: "AGENTS.md", Action: "write", HadOld: false, NewHash: state.Hash([]byte("created"))},
	}}
	if err := apply.Recover(context.Background(), target, stateDir, st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("expected file to be removed, stat err = %v", err)
	}
}

func TestRecoverRestoresDeletedFile(t *testing.T) {
	target := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	backupRel := filepath.Join("backups", "tx", "000_AGENTS.md")
	write(t, filepath.Join(stateDir, backupRel), "original")
	st := state.New("repo", "main")
	st.Journal = &state.Journal{ID: "tx", Ops: []state.JournalOp{
		{Path: "AGENTS.md", Action: "delete", HadOld: true, Backup: backupRel},
	}}
	if err := apply.Recover(context.Background(), target, stateDir, st); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(target, "AGENTS.md"))
	if string(data) != "original" {
		t.Fatalf("restored = %q", data)
	}
}
