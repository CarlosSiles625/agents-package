package apply_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agent-sync/internal/apply"
	"agent-sync/internal/manifest"
	"agent-sync/internal/modelpref"
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestApplyInterruptedPrefWriteKeepsBaseline covers REC-1: a genuinely
// interrupted Apply (it persists its journal, then fails while replacing a
// preference-managed file) must leave the previous preference, receipt and raw
// baseline on disk, and a later Recover must preserve a post-failure user edit
// while restoring the file the interrupted run did write.
func TestApplyInterruptedPrefWriteKeepsBaseline(t *testing.T) {
	target := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")

	const plainOld, plainNew = "plain-old", "plain-new"
	raw := []byte("---\nmodel: upstreamA\n---\nbody\n")
	local := []byte("---\nmodel: openai/gpt-4\n---\nbody\n")
	newRaw := []byte("---\nmodel: upstreamB\n---\nbody2\n")

	write(t, filepath.Join(target, "AGENTS.md"), plainOld)
	write(t, filepath.Join(target, "agents", "a.md"), string(local))

	pref := modelpref.Pref{Mode: modelpref.ModeExplicit, Model: "openai/gpt-4"}
	if _, rec, err := modelpref.Render(raw, pref); err != nil {
		t.Fatal(err)
	} else {
		pref.Receipt = rec
	}
	newRendered, newRec, err := modelpref.Render(newRaw, pref)
	if err != nil {
		t.Fatal(err)
	}
	newPref := pref
	newPref.Receipt = newRec

	st := state.New("repo", "main")
	st.Files = map[string]state.FileState{
		"AGENTS.md":   {Exists: true, Hash: state.Hash([]byte(plainOld))},
		"agents/a.md": {Exists: true, Hash: state.Hash(raw)},
	}
	st.ModelPrefs = map[string]modelpref.Pref{"agents/a.md": pref}

	plan := planner.Plan{Commit: "c2", Entries: []planner.Entry{
		{
			Path:    "AGENTS.md",
			Kind:    planner.KindWrite,
			Local:   planner.Val{Exists: true, Hash: state.Hash([]byte(plainOld))},
			Content: []byte(plainNew),
			Remote:  planner.Val{Exists: true, Hash: state.Hash([]byte(plainNew))},
		},
		{
			Path:    "agents/a.md",
			Kind:    planner.KindWrite,
			Local:   planner.Val{Exists: true, Hash: state.Hash(local)},
			Content: newRendered,
			RawHash: state.Hash(newRaw),
			Remote:  planner.Val{Exists: true, Hash: state.Hash(newRaw)},
			Pref:    &newPref,
		},
	}}

	// A read-only destination makes the second atomic replacement fail after
	// the journal was persisted: the run is interrupted mid-write.
	agentPath := filepath.Join(target, "agents", "a.md")
	if err := os.Chmod(agentPath, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agentPath, 0o644) })

	if err := apply.Apply(context.Background(), target, stateDir, st, plan); err == nil {
		t.Skip("filesystem permits replacing a read-only file; cannot interrupt the write deterministically here")
	}

	// The previous preference/receipt and raw baseline must be intact on disk,
	// with the journal recorded.
	onDisk, exists, err := state.Load(stateDir)
	if err != nil || !exists {
		t.Fatalf("reload after interruption: exists=%v err=%v", exists, err)
	}
	if onDisk.Journal == nil {
		t.Fatal("interrupted apply must leave its journal on disk")
	}
	if onDisk.Files["agents/a.md"].Hash != state.Hash(raw) {
		t.Fatalf("raw baseline advanced despite interruption: %+v", onDisk.Files["agents/a.md"])
	}
	if got := onDisk.ModelPrefs["agents/a.md"]; got != pref {
		t.Fatalf("preference/receipt changed despite interruption: %+v want %+v", got, pref)
	}
	if got := readFile(t, filepath.Join(target, "AGENTS.md")); got != plainNew {
		t.Fatalf("first write should have happened before the failure, got %q", got)
	}

	// The user repairs the managed file after the failed run.
	if err := os.Chmod(agentPath, 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, agentPath, "user-edit\n")

	if err := apply.Recover(context.Background(), target, stateDir, onDisk); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got := readFile(t, agentPath); got != "user-edit\n" {
		t.Fatalf("recover overwrote the post-failure user edit: %q", got)
	}
	if got := readFile(t, filepath.Join(target, "AGENTS.md")); got != plainOld {
		t.Fatalf("recover did not restore the prior file: %q", got)
	}
	final, _, err := state.Load(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if final.Journal != nil {
		t.Fatal("journal not cleared after recovery")
	}
	if final.ModelPrefs["agents/a.md"] != pref {
		t.Fatalf("preference changed across recovery: %+v", final.ModelPrefs["agents/a.md"])
	}
}
