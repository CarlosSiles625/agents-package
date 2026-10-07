package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agent-sync/internal/state"
	"agent-sync/internal/testsupport"
	"agent-sync/internal/userpath"
)

// e2eRun invokes the CLI entry point in-process. It replaces the userpath seams
// with safe, inert defaults so no test ever reads the real console, registry or
// shell profiles: the environment reports no terminal and any unexpected Add
// call fails the test instead of touching the host.
func e2eRun(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	e2eSeams(t, false, e2eNoAdd(t))
	return e2eInvoke(args...)
}

// e2eNoAdd returns an addToUserPath seam that fails the test if it is ever
// called, so a test can prove the backend is never reached.
func e2eNoAdd(t *testing.T) func(string, userpath.Options) (userpath.Report, error) {
	t.Helper()
	return func(string, userpath.Options) (userpath.Report, error) {
		t.Errorf("userpath.Add called unexpectedly in this test")
		return userpath.Report{}, fmt.Errorf("userpath.Add not allowed in this test")
	}
}

// e2eSeams replaces the package-level userpath seams for the duration of the
// test and restores them via t.Cleanup. The package's tests run serially (no
// t.Parallel), so swapping these globals is race-free.
func e2eSeams(t *testing.T, tty bool, add func(string, userpath.Options) (userpath.Report, error)) {
	t.Helper()
	prevTTY, prevAdd := stdinIsTerminal, addToUserPath
	stdinIsTerminal = func() bool { return tty }
	addToUserPath = add
	t.Cleanup(func() { stdinIsTerminal, addToUserPath = prevTTY, prevAdd })
}

// e2eInvoke runs the CLI in-process with empty stdin and captures its output.
func e2eInvoke(args ...string) (code int, stdout, stderr string) {
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

// --- V5: install --add-to-path / --shell CLI seam coverage -------------------

// e2ePathInstall creates a fresh one-file remote and runs "install" against it
// with the supplied userpath seams, returning the exit code, captured output,
// the target dir and the state dir. Each call gets its own temp repository and
// state; no real registry, profile or console is touched.
func e2ePathInstall(t *testing.T, tty bool, add func(string, userpath.Options) (userpath.Report, error), extra ...string) (code int, stdout, stderr, target, stateDir string) {
	t.Helper()
	e2eSeams(t, tty, add)
	remote, _ := e2eRemote(t, map[string]string{"AGENTS.md": "# root\n"})
	target = filepath.Join(t.TempDir(), "target")
	stateDir = filepath.Join(t.TempDir(), "state")
	args := append([]string{"install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir}, extra...)
	code, stdout, stderr = e2eInvoke(args...)
	return code, stdout, stderr, target, stateDir
}

// TestE2EInstallPathExplicitAddsWithoutTTY proves --add-to-path authorizes the
// PATH write with AssumeYes and without a terminal prompt.
func TestE2EInstallPathExplicitAddsWithoutTTY(t *testing.T) {
	var got userpath.Options
	called := false
	add := func(_ string, o userpath.Options) (userpath.Report, error) {
		called, got = true, o
		return userpath.Report{Dir: "X"}, nil
	}
	code, _, errOut, target, _ := e2ePathInstall(t, false, add, "--add-to-path", "--yes")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !called {
		t.Fatal("addToUserPath was not called for --add-to-path")
	}
	if !got.AssumeYes || got.Interactive {
		t.Fatalf("options = %+v, want AssumeYes=true and Interactive=false", got)
	}
	if content := e2eRead(t, filepath.Join(target, "AGENTS.md")); content != "# root\n" {
		t.Fatalf("agent file not applied: %q", content)
	}
}

// TestE2EInstallPathNoFlagNoTTYNoBackend proves that with no --add-to-path and
// no terminal the run aborts at the apply confirmation and the PATH backend is
// never reached.
func TestE2EInstallPathNoFlagNoTTYNoBackend(t *testing.T) {
	code, out, errOut, target, _ := e2ePathInstall(t, false, e2eNoAdd(t))
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "aborted") {
		t.Fatalf("stdout = %s", out)
	}
	if _, err := os.Stat(filepath.Join(target, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("aborted run wrote agent files (err=%v)", err)
	}
}

// TestE2EInstallPathYesAloneNoTTYNoBackend proves --yes does not consent to the
// PATH change: on a non-terminal the step is a silent no-op that never calls the
// backend, while the agent install still succeeds.
func TestE2EInstallPathYesAloneNoTTYNoBackend(t *testing.T) {
	code, out, errOut, target, _ := e2ePathInstall(t, false, e2eNoAdd(t), "--yes")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "PATH unchanged") {
		t.Fatalf("stdout = %s", out)
	}
	if content := e2eRead(t, filepath.Join(target, "AGENTS.md")); content != "# root\n" {
		t.Fatalf("agent files not applied: %q", content)
	}
}

// TestE2EInstallPathExplicitFailureKeepsAgents proves an explicit PATH failure
// exits 1 while leaving the successfully applied agent files in place.
func TestE2EInstallPathExplicitFailureKeepsAgents(t *testing.T) {
	add := func(string, userpath.Options) (userpath.Report, error) {
		return userpath.Report{}, errors.New("backend boom")
	}
	code, _, errOut, target, _ := e2ePathInstall(t, false, add, "--add-to-path", "--yes")
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr = %s", code, errOut)
	}
	if content := e2eRead(t, filepath.Join(target, "AGENTS.md")); content != "# root\n" {
		t.Fatalf("agent files were not installed before the PATH failure: %q", content)
	}
	if !strings.Contains(errOut, "agent files were installed") {
		t.Fatalf("stderr should note the agent install survived: %s", errOut)
	}
}

// TestE2EInstallPathDryRunNoWrites proves a dry-run passes DryRun=true (and no
// backup dir) to the backend and creates no state.
func TestE2EInstallPathDryRunNoWrites(t *testing.T) {
	var got userpath.Options
	called := false
	add := func(_ string, o userpath.Options) (userpath.Report, error) {
		called, got = true, o
		return userpath.Report{Dir: "X", Note: "would append"}, nil
	}
	code, _, errOut, target, stateDir := e2ePathInstall(t, false, add, "--add-to-path", "--yes", "--dry-run")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !called || !got.DryRun {
		t.Fatalf("dry-run must call the backend with DryRun=true, got called=%v opts=%+v", called, got)
	}
	if got.BackupDir != "" {
		t.Fatalf("dry-run must not configure a backup dir, got %q", got.BackupDir)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "path-backups")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created PATH state (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(target, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote agent files (err=%v)", err)
	}
}

// TestE2EInstallPathInteractiveDeclineStillSucceeds proves that when the
// interactive step declines (backend reports no change) the install still
// succeeds with the agent files applied.
func TestE2EInstallPathInteractiveDeclineStillSucceeds(t *testing.T) {
	var got userpath.Options
	called := false
	add := func(_ string, o userpath.Options) (userpath.Report, error) {
		called, got = true, o
		return userpath.Report{Skipped: true}, nil
	}
	code, _, errOut, target, _ := e2ePathInstall(t, true, add, "--yes")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !called {
		t.Fatal("interactive PATH step did not call the backend")
	}
	if got.AssumeYes || !got.Interactive {
		t.Fatalf("options = %+v, want a real prompt (AssumeYes=false, Interactive=true)", got)
	}
	if content := e2eRead(t, filepath.Join(target, "AGENTS.md")); content != "# root\n" {
		t.Fatalf("agent files not applied: %q", content)
	}
}

// TestE2EInstallOnlyFlagsRejectedElsewhere proves --add-to-path and --shell are
// rejected (not silently ignored) outside "install".
func TestE2EInstallOnlyFlagsRejectedElsewhere(t *testing.T) {
	if code, _, _ := e2eInvoke("pull", "--add-to-path"); code != 2 {
		t.Fatalf("pull --add-to-path code = %d, want 2", code)
	}
	if code, _, _ := e2eInvoke("status", "--shell", "bash"); code != 2 {
		t.Fatalf("status --shell code = %d, want 2", code)
	}
}

// TestE2EInstallShellRejectedOnWindows proves an explicit --shell is rejected
// before any agent file is written on Windows.
func TestE2EInstallShellRejectedOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("--shell rejection is Windows-specific (POSIX accepts bash/zsh)")
	}
	code, _, errOut, target, _ := e2ePathInstall(t, false, e2eNoAdd(t), "--shell", "bash")
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(target, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("--shell rejection still wrote agent files (err=%v)", err)
	}
}

