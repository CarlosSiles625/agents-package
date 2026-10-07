package userpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- V1: pure quoting and block generation -----------------------------------

func TestPOSIXQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "'plain'"},
		{"a b", "'a b'"},
		{"a$b", "'a$b'"},
		{"a`b", "'a`b'"},
		{"*.md", "'*.md'"},
		{`quote"inside`, `'quote"inside'`},
		{"a'b", `'a'\''b'`},
		{"", "''"},
	}
	for _, c := range cases {
		if got := POSIXQuote(c.in); got != c.want {
			t.Errorf("POSIXQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPathBlockShape(t *testing.T) {
	dir := `/opt/agent bin's`
	block := PathBlock(dir)
	for _, want := range []string{
		markerBegin,
		markerEnd,
		`case ":${PATH}:" in`,
		"*:" + POSIXQuote(dir) + ":*",
		"export PATH=" + POSIXQuote(dir) + `:"${PATH}"`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("block missing %q:\n%s", want, block)
		}
	}
	if block[len(block)-1] != '\n' {
		t.Fatalf("block must end with a newline: %q", block)
	}
}

// TestPathBlockMembershipWithBash executes the generated snippet in a real shell
// so the literal membership test is proven, not just inspected.
func TestPathBlockMembershipWithBash(t *testing.T) {
	if runtime.GOOS == "windows" {
		// A Windows "bash" on PATH is a WSL stub, not a POSIX shell; replacing
		// PATH makes it fail for reasons unrelated to the snippet. This test
		// asserts the POSIX block through a genuine bash, so it only runs where
		// such a shell exists and is not the WSL launcher.
		t.Skip("POSIX PATH block is validated on POSIX platforms with a real bash; skipped on Windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	const dir = "/opt/agent sync's dir"
	block := PathBlock(dir)
	run := func() string {
		cmd := exec.Command(bash, "-c", block+`printf '%s' "$PATH"`)
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("bash: %v", err)
		}
		return string(out)
	}
	first := run()
	found := false
	for _, p := range strings.Split(first, ":") {
		if p == dir {
			found = true
		}
	}
	if !found {
		t.Fatalf("snippet did not add the directory: PATH=%q", first)
	}
	if second := run(); second != first {
		t.Fatalf("snippet is not idempotent: %q then %q", first, second)
	}
}

// --- V2: profile rendering, malformed markers, idempotency -------------------

func TestRenderProfileAppendsPreservingTail(t *testing.T) {
	block := PathBlock("/opt/x")
	cur := []byte("export A=1") // no trailing newline
	out, changed, err := renderProfile(cur, block)
	if err != nil || !changed {
		t.Fatalf("renderProfile: changed=%v err=%v", changed, err)
	}
	if string(out) != "export A=1\n"+block {
		t.Fatalf("out = %q", out)
	}
}

func TestRenderProfileIdempotent(t *testing.T) {
	block := PathBlock("/opt/x")
	out, changed, err := renderProfile([]byte("before\n"), block)
	if err != nil || !changed {
		t.Fatalf("first render changed=%v err=%v", changed, err)
	}
	out2, changed2, err := renderProfile(out, block)
	if err != nil || changed2 {
		t.Fatalf("second render must be a no-op: changed=%v err=%v", changed2, err)
	}
	if string(out2) != string(out) {
		t.Fatalf("second render altered bytes: %q", out2)
	}
}

func TestRenderProfileReplacesOwnBlockOnly(t *testing.T) {
	old := PathBlock("/opt/old")
	cur := []byte("head\n" + old + "tail\n")
	out, changed, err := renderProfile(cur, PathBlock("/opt/new"))
	if err != nil || !changed {
		t.Fatalf("render changed=%v err=%v", changed, err)
	}
	if !strings.HasPrefix(string(out), "head\n") || !strings.HasSuffix(string(out), "tail\n") {
		t.Fatalf("surrounding text lost: %q", out)
	}
	if strings.Contains(string(out), "/opt/old") {
		t.Fatalf("old block not replaced: %q", out)
	}
}

func TestRenderProfileMalformedFailsClosed(t *testing.T) {
	block := PathBlock("/opt/x")
	cases := map[string]string{
		"begin without end": markerBegin + "\nexport PATH=x\n",
		"end without begin": "text\n" + markerEnd + "\n",
		"duplicated":        markerBegin + "\n" + markerEnd + "\n" + markerBegin + "\n" + markerEnd + "\n",
	}
	for name, cur := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := renderProfile([]byte(cur), block); err == nil {
				t.Fatal("expected an error for malformed markers")
			}
		})
	}
}

// TestApplyProfileMalformedPreservesBytes proves a malformed existing profile is
// never reconstructed: the error aborts before any write.
func TestApplyProfileMalformedPreservesBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bashrc")
	original := markerBegin + "\n# user text without an end marker\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	var prev []Preview
	_, err := applyProfile(path, PathBlock("/opt/x"), Options{DryRun: true}, &prev)
	if err == nil {
		t.Fatal("expected a malformed-marker error")
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Fatalf("malformed profile was modified: %q", got)
	}
}

// --- V2: file fixture, backups, rollback, dry-run ----------------------------

func posixCfg(home, dir, backupDir string) posixConfig {
	return posixConfig{
		Dir:       dir,
		Home:      home,
		ShellFlag: "bash",
		AssumeYes: true,
		BackupDir: backupDir,
	}
}

func TestInstallPOSIXAddsBackupAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "path-backups")
	rc := filepath.Join(home, ".bashrc")
	const secret = "export SECRET=hunter2\n"
	if err := os.WriteFile(rc, []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := "/opt/agent bin"

	rep, err := InstallPOSIX(posixCfg(home, dir, backupDir))
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	got, _ := os.ReadFile(rc)
	if !strings.Contains(string(got), secret) || !strings.Contains(string(got), markerBegin) {
		t.Fatalf(".bashrc = %q", got)
	}
	// Previews must carry only agent-sync's own lines, never unrelated content.
	for _, pv := range rep.Previews {
		for _, l := range append(append([]string{}, pv.Added...), pv.Removed...) {
			if strings.Contains(l, "SECRET") {
				t.Fatalf("preview leaked unrelated profile content: %q", l)
			}
		}
	}
	if len(rep.Backups) != 1 {
		t.Fatalf("backups = %v", rep.Backups)
	}
	if b, _ := os.ReadFile(rep.Backups[0]); string(b) != secret {
		t.Fatalf("backup must hold the exact original bytes, got %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(rep.Backups[0]); fi.Mode().Perm() != 0o600 {
			t.Fatalf("backup mode = %o, want 0600", fi.Mode().Perm())
		}
	}

	// Second run is an idempotent no-op with no new backup.
	rep2, err := InstallPOSIX(posixCfg(home, dir, backupDir))
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(rep2.Backups) != 0 {
		t.Fatalf("idempotent run created backups: %v", rep2.Backups)
	}
	if len(rep2.Previews) == 0 || !rep2.Previews[0].Already {
		t.Fatalf("expected an already-present preview, got %+v", rep2.Previews)
	}
}

func TestInstallPOSIXDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "path-backups")
	rc := filepath.Join(home, ".bashrc")
	before := "export SECRET=hunter2\n"
	if err := os.WriteFile(rc, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := posixCfg(home, "/opt/dry", backupDir)
	cfg.DryRun = true

	rep, err := InstallPOSIX(cfg)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if got, _ := os.ReadFile(rc); string(got) != before {
		t.Fatalf("dry-run wrote the profile: %q", got)
	}
	if _, err := os.Stat(backupDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a backup dir (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".bash_profile")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a profile (err=%v)", err)
	}
	if len(rep.Previews) == 0 || len(rep.Previews[0].Added) == 0 {
		t.Fatalf("dry-run preview missing: %+v", rep.Previews)
	}
}

func TestInstallPOSIXSymlinkProfileRefused(t *testing.T) {
	home := t.TempDir()
	external := filepath.Join(t.TempDir(), "external-bashrc")
	if err := os.WriteFile(external, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(home, ".bashrc")
	if err := os.Symlink(external, rc); err != nil {
		t.Skipf("cannot create a file symlink: %v", err)
	}
	rep, err := InstallPOSIX(posixCfg(home, "/opt/x", filepath.Join(t.TempDir(), "b")))
	if err == nil {
		t.Fatalf("expected a symlink refusal, got %+v", rep)
	}
	if got, _ := os.ReadFile(external); string(got) != "external\n" {
		t.Fatalf("external file changed through the link: %q", got)
	}
}

func TestInstallPOSIXNonInteractiveSkipped(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := posixCfg(home, "/opt/x", filepath.Join(t.TempDir(), "b"))
	cfg.AssumeYes = false
	cfg.Interactive = false

	rep, err := InstallPOSIX(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !rep.Skipped {
		t.Fatalf("non-interactive run must skip, got %+v", rep)
	}
}

func TestInstallPOSIXDeclineIsNoOp(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	before := "x\n"
	if err := os.WriteFile(rc, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := posixCfg(home, "/opt/x", filepath.Join(t.TempDir(), "b"))
	cfg.AssumeYes = false
	cfg.Interactive = true
	cfg.Stdin = strings.NewReader("n\n")
	cfg.Stdout = &strings.Builder{}

	rep, err := InstallPOSIX(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !rep.Skipped {
		t.Fatalf("declined run must skip, got %+v", rep)
	}
	if got, _ := os.ReadFile(rc); string(got) != before {
		t.Fatalf("declined run wrote the profile: %q", got)
	}
}

// --- low-level snapshot / atomic write / rollback ----------------------------

func TestWriteProfileAtomicPreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	snap := fileSnapshot{exists: true, data: []byte("old\n"), mode: fi.Mode().Perm()}
	if err := writeProfileAtomic(path, []byte("new\n"), snap); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new\n" {
		t.Fatalf("content = %q", got)
	}
	fi2, _ := os.Stat(path)
	if fi2.Mode().Perm() != fi.Mode().Perm() {
		t.Fatalf("mode changed: %o -> %o", fi.Mode().Perm(), fi2.Mode().Perm())
	}
}

func TestCheckUnchangedDetectsDrift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	mode := fi.Mode().Perm()

	if err := checkUnchanged(path, fileSnapshot{exists: true, data: []byte("old\n"), mode: mode}); err != nil {
		t.Fatalf("unchanged profile rejected: %v", err)
	}
	if err := checkUnchanged(path, fileSnapshot{exists: true, data: []byte("other\n"), mode: mode}); err == nil {
		t.Fatal("content drift not detected")
	}
	if err := checkUnchanged(path, fileSnapshot{exists: true, data: []byte("old\n"), mode: mode ^ 0o111}); err == nil {
		t.Fatal("mode drift not detected")
	}
	if err := checkUnchanged(filepath.Join(dir, "gone"), fileSnapshot{exists: true, data: []byte("x"), mode: mode}); err == nil {
		t.Fatal("disappearance not detected")
	}
}

func TestRollbackOnlyOwnBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile")
	orig := "orig\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	backup, err := writeBackup(filepath.Join(dir, "backups"), path, []byte(orig))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &receipt{path: path, existed: true, mode: 0o644, backup: backup, newHash: hashBytes([]byte("new\n"))}

	// A later user edit must not be rolled back.
	if err := os.WriteFile(path, []byte("user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rollback(rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "user\n" {
		t.Fatalf("rollback clobbered a user edit: %q", got)
	}

	// Exactly our bytes are restored.
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rollback(rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != orig {
		t.Fatalf("rollback did not restore the original: %q", got)
	}

	// A created file is removed only while it still holds exactly our bytes.
	created := filepath.Join(dir, "created")
	rec2 := &receipt{path: created, existed: false, newHash: hashBytes([]byte("mine"))}
	if err := os.WriteFile(created, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rollback(rec2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created profile not removed: %v", err)
	}
}
