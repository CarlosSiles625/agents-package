package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-sync/internal/state"
	"agent-sync/internal/testsupport"
)

// e2eRun invokes the CLI entry point in-process.
func e2eRun(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut strings.Builder
	code = run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// e2eRemote creates a local git repository fixture with the given files.
func e2eRemote(t *testing.T, files map[string]string) (dir, commit string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "remote")
	commit = testsupport.InitRepo(t, dir, files)
	return dir, commit
}

func e2eWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func e2eRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func e2eState(t *testing.T, dir string) *state.State {
	t.Helper()
	st, _, err := state.Load(dir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	return st
}

// e2eBackupContains reports whether any file under dir holds content.
func e2eBackupContains(t *testing.T, dir, content string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if data, err := os.ReadFile(p); err == nil && string(data) == content {
			found = true
		}
		return nil
	})
	return found
}

func TestE2EInstallFresh(t *testing.T) {
	remote, commit := e2eRemote(t, map[string]string{
		"AGENTS.md":   "# root\n",
		"agents/a.md": "a\n",
	})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	foreign := filepath.Join(target, "skills", "keep.md")
	e2eWrite(t, foreign, "keep\n")

	code, out, errOut := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "# root\n" {
		t.Fatalf("AGENTS.md = %q", got)
	}
	if got := e2eRead(t, filepath.Join(target, "agents", "a.md")); got != "a\n" {
		t.Fatalf("agents/a.md = %q", got)
	}
	if got := e2eRead(t, foreign); got != "keep\n" {
		t.Fatalf("foreign file changed: %q", got)
	}
	if !strings.Contains(out, "done:") {
		t.Fatalf("stdout = %s", out)
	}
	st := e2eState(t, stateDir)
	if st.Commit != commit {
		t.Fatalf("commit = %q, want %q", st.Commit, commit)
	}
	if !st.Files["AGENTS.md"].Exists || st.Files["AGENTS.md"].Hash != state.Hash([]byte("# root\n")) {
		t.Fatalf("state files = %+v", st.Files)
	}
}

func TestE2EAdoptIdentical(t *testing.T) {
	remote, _ := e2eRemote(t, map[string]string{"AGENTS.md": "same\n"})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	e2eWrite(t, filepath.Join(target, "AGENTS.md"), "same\n")

	code, out, errOut := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "same\n" {
		t.Fatalf("content = %q", got)
	}
	if !strings.Contains(out, "adopt") {
		t.Fatalf("stdout = %s", out)
	}
	if st := e2eState(t, stateDir); st.Files["AGENTS.md"].Hash != state.Hash([]byte("same\n")) {
		t.Fatalf("state files = %+v", st.Files)
	}
}

func TestE2EReplaceBlockedThenAdopted(t *testing.T) {
	remote, _ := e2eRemote(t, map[string]string{"AGENTS.md": "remote\n"})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	e2eWrite(t, filepath.Join(target, "AGENTS.md"), "local\n")

	code, _, errOut := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes")
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "local\n" {
		t.Fatalf("blocked run modified the file: %q", got)
	}

	code, _, errOut = e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes", "--replace", "AGENTS.md")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "remote\n" {
		t.Fatalf("replace content = %q", got)
	}
	if !e2eBackupContains(t, filepath.Join(stateDir, "backups"), "local\n") {
		t.Fatal("expected a backup of the replaced file")
	}
}

func TestE2ELocalChangeKeptWhenRemoteUnchanged(t *testing.T) {
	remote, _ := e2eRemote(t, map[string]string{"AGENTS.md": "v1\n"})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	if code, _, e := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("install code = %d, %s", code, e)
	}
	e2eWrite(t, filepath.Join(target, "AGENTS.md"), "local-edit\n")

	code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes")
	if code != 0 {
		t.Fatalf("pull code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "local-edit\n" {
		t.Fatalf("local change was overwritten: %q", got)
	}
	if st := e2eState(t, stateDir); st.Files["AGENTS.md"].Hash != state.Hash([]byte("v1\n")) {
		t.Fatalf("baseline must stay at the accepted remote, got: %+v", st.Files)
	}
}

func TestE2EConflictAbortsAllBeforeWriting(t *testing.T) {
	remote, _ := e2eRemote(t, map[string]string{
		"AGENTS.md":   "v1\n",
		"agents/a.md": "a1\n",
	})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	if code, _, e := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("install code = %d, %s", code, e)
	}

	// Remote changes both files; the local copy of one also diverged.
	testsupport.Commit(t, remote, map[string]string{
		"AGENTS.md":   "v2\n",
		"agents/a.md": "a2\n",
	}, nil, "remote update")
	e2eWrite(t, filepath.Join(target, "agents", "a.md"), "local-a\n")

	code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes")
	if code != 2 {
		t.Fatalf("pull code = %d, want 2; stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "v1\n" {
		t.Fatalf("abort-all failed: AGENTS.md = %q, want v1", got)
	}
	if got := e2eRead(t, filepath.Join(target, "agents", "a.md")); got != "local-a\n" {
		t.Fatalf("conflicting local file changed: %q", got)
	}
}

func TestE2EDryRunWritesNothing(t *testing.T) {
	remote, baseCommit := e2eRemote(t, map[string]string{"AGENTS.md": "v1\n"})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	if code, _, e := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("install code = %d, %s", code, e)
	}
	testsupport.Commit(t, remote, map[string]string{"AGENTS.md": "v2\n"}, nil, "remote update")

	code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--dry-run", "--yes")
	if code != 0 {
		t.Fatalf("dry-run code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "v1\n" {
		t.Fatalf("dry-run wrote target: %q", got)
	}
	if st := e2eState(t, stateDir); st.Commit != baseCommit {
		t.Fatalf("dry-run changed state commit to %q", st.Commit)
	}
}

func TestE2EDeleteGatingLeavesForeignIntact(t *testing.T) {
	remote, _ := e2eRemote(t, map[string]string{
		"AGENTS.md":   "v1\n",
		"agents/a.md": "a1\n",
	})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	if code, _, e := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("install code = %d, %s", code, e)
	}
	foreign := filepath.Join(target, "skills", "keep.md")
	e2eWrite(t, foreign, "keep\n")

	testsupport.Commit(t, remote, nil, []string{"agents/a.md"}, "drop a")

	code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes")
	if code != 2 {
		t.Fatalf("pull without --allow-delete code = %d, want 2; stderr = %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(target, "agents", "a.md")); err != nil {
		t.Fatalf("file deleted without --allow-delete: %v", err)
	}

	code, _, errOut = e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes", "--allow-delete")
	if code != 0 {
		t.Fatalf("pull with --allow-delete code = %d, stderr = %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(target, "agents", "a.md")); !os.IsNotExist(err) {
		t.Fatalf("expected agents/a.md deleted, stat err = %v", err)
	}
	if got := e2eRead(t, foreign); got != "keep\n" {
		t.Fatalf("foreign file changed: %q", got)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "v1\n" {
		t.Fatalf("AGENTS.md = %q", got)
	}
}

func TestE2ERecoverPendingTransaction(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")

	// Simulate an interrupted run: AGENTS.md was overwritten and agents/a.md deleted.
	e2eWrite(t, filepath.Join(target, "AGENTS.md"), "new\n")
	writeBackup := filepath.Join("backups", "tx", "000_AGENTS.md")
	e2eWrite(t, filepath.Join(stateDir, writeBackup), "old\n")
	deleteBackup := filepath.Join("backups", "tx", "001_agents__a.md")
	e2eWrite(t, filepath.Join(stateDir, deleteBackup), "a-old\n")

	st := state.New("", "")
	st.Commit = "abc"
	st.Files = map[string]state.FileState{
		"AGENTS.md":   {Exists: true, Hash: state.Hash([]byte("old\n"))},
		"agents/a.md": {Exists: true, Hash: state.Hash([]byte("a-old\n"))},
	}
	st.Journal = &state.Journal{ID: "tx", Ops: []state.JournalOp{
		{Path: "AGENTS.md", Action: "write", HadOld: true, Backup: writeBackup, NewHash: state.Hash([]byte("new\n"))},
		{Path: "agents/a.md", Action: "delete", HadOld: true, Backup: deleteBackup},
	}}
	if err := st.Save(stateDir); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := e2eRun(t, "recover", "--target", target, "--state", stateDir)
	if code != 0 {
		t.Fatalf("recover code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "old\n" {
		t.Fatalf("AGENTS.md not restored: %q", got)
	}
	if got := e2eRead(t, filepath.Join(target, "agents", "a.md")); got != "a-old\n" {
		t.Fatalf("deleted file not restored: %q", got)
	}
	if !strings.Contains(out, "recovered") {
		t.Fatalf("stdout = %s", out)
	}
	if st := e2eState(t, stateDir); st.Journal != nil {
		t.Fatal("journal not cleared")
	}
}

func TestE2ERecoverNothingPending(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	code, out, errOut := e2eRun(t, "recover", "--target", target, "--state", stateDir)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "nothing to recover") {
		t.Fatalf("stdout = %s", out)
	}
}

// TestE2EWriteThroughLinkParentRefused covers the case where a managed parent
// directory is a symlink/junction to a directory outside the destination: the
// install must be refused and the external directory must stay untouched.
func TestE2EWriteThroughLinkParentRefused(t *testing.T) {
	remote, _ := e2eRemote(t, map[string]string{"agents/new.md": "remote\n"})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	external := filepath.Join(t.TempDir(), "external")
	e2eWrite(t, filepath.Join(external, "keep.md"), "keep\n")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	testsupport.MakeDirLink(t, filepath.Join(target, "agents"), external)

	code, _, errOut := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes")
	if code == 0 {
		t.Fatalf("install should have been refused; stderr = %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(external, "new.md")); !os.IsNotExist(err) {
		t.Fatalf("external file was written through the link: %v", err)
	}
	if got := e2eRead(t, filepath.Join(external, "keep.md")); got != "keep\n" {
		t.Fatalf("external file changed: %q", got)
	}
}

// TestE2EKeptLocalChangeDoesNotBecomeBaseline covers the baseline regression: a
// kept local edit must not become the baseline, otherwise a later remote change
// would silently overwrite it instead of conflicting.
func TestE2EKeptLocalChangeDoesNotBecomeBaseline(t *testing.T) {
	remote, baseCommit := e2eRemote(t, map[string]string{"AGENTS.md": "A\n"})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	if code, _, e := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("install code = %d, %s", code, e)
	}
	e2eWrite(t, filepath.Join(target, "AGENTS.md"), "L\n")

	// First pull: remote unchanged, so the local edit is kept.
	if code, _, e := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("pull code = %d, %s", code, e)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "L\n" {
		t.Fatalf("local edit lost: %q", got)
	}
	st := e2eState(t, stateDir)
	if st.Files["AGENTS.md"].Hash != state.Hash([]byte("A\n")) {
		t.Fatalf("baseline captured the local edit instead of the remote: %+v", st.Files["AGENTS.md"])
	}
	if st.Commit != baseCommit {
		t.Fatalf("commit = %q, want %q", st.Commit, baseCommit)
	}

	// status (no fetch) must still report the local modification as kept.
	code, out, errOut := e2eRun(t, "status", "--target", target, "--state", stateDir)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "keep") || strings.Contains(out, "write") {
		t.Fatalf("status should show a kept local edit, got:\n%s", out)
	}

	// A later remote change must conflict, never silently overwrite the edit.
	testsupport.Commit(t, remote, map[string]string{"AGENTS.md": "R\n"}, nil, "remote update")
	code, _, errOut = e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes")
	if code != 2 {
		t.Fatalf("second pull code = %d, want 2 (conflict); stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "AGENTS.md")); got != "L\n" {
		t.Fatalf("local edit overwritten by remote: %q", got)
	}
}

