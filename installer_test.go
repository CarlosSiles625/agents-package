// Package agent_sync_test drives the root install wrappers (install.ps1 and
// install.sh) end to end with fake go/git tools and a fake resulting CLI, so no
// test ever compiles the real module, touches the real user PATH, registry or
// shell profiles.
package agent_sync_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// --- fake resulting CLI (this test binary re-entered as the installed tool) ---

func TestMain(m *testing.M) {
	if os.Getenv("AGENT_SYNC_FAKE_CLI") == "1" {
		os.Exit(runFakeCLI())
	}
	os.Exit(m.Run())
}

// runFakeCLI records its argv and working directory, then exits with the code
// asked for through the environment. It never examines the test flags.
func runFakeCLI() int {
	args := os.Args[1:]
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(0)
		}
		b.WriteString(a)
	}
	if out := os.Getenv("AGENT_SYNC_FAKE_CLI_OUT"); out != "" {
		_ = os.WriteFile(out, []byte(b.String()), 0o644)
	}
	if cwd := os.Getenv("AGENT_SYNC_FAKE_CLI_CWD"); cwd != "" {
		if wd, err := os.Getwd(); err == nil {
			_ = os.WriteFile(cwd, []byte(wd), 0o644)
		}
	}
	code := 0
	if s := os.Getenv("AGENT_SYNC_FAKE_CLI_EXIT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			code = n
		}
	}
	return code
}

// --- fixtures ---

type fixture struct {
	t       *testing.T
	base    string
	root    string
	fakeDir string
	binDir  string
	gopath  string
	home    string
	cliOut  string
	cliCwd  string
	marker  string
	script  string
}

func newFixture(t *testing.T, scriptName string) *fixture {
	t.Helper()
	base := filepath.Join(t.TempDir(), "base with space's")
	mustMkdir(t, base)
	f := &fixture{
		t:       t,
		base:    base,
		root:    filepath.Join(base, "checkout with space's"),
		fakeDir: filepath.Join(base, "fake tools"),
		binDir:  filepath.Join(base, "bin out's"),
		gopath:  filepath.Join(base, "go path's"),
		home:    filepath.Join(base, "home's"),
		cliOut:  filepath.Join(base, "cli args.bin"),
		cliCwd:  filepath.Join(base, "cli cwd.txt"),
		marker:  filepath.Join(base, "go install marker.txt"),
	}
	mustMkdir(t, f.root)
	mustMkdir(t, f.fakeDir)
	mustMkdir(t, f.home)
	mustWrite(t, filepath.Join(f.root, "go.mod"), "module fixture\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(f.root, "cmd", "agent-sync", "placeholder.txt"), "fixture\n")

	// Copy the real script into the fixture checkout so it resolves the fixture
	// as its root, wherever the caller's working directory is.
	src := filepath.Join(repoRoot(t), scriptName)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	f.script = filepath.Join(f.root, scriptName)
	mustWrite(t, f.script, string(data))
	if scriptName == "install.sh" {
		if err := os.Chmod(f.script, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) testBinary() string {
	f.t.Helper()
	exe, err := os.Executable()
	if err != nil {
		f.t.Fatalf("os.Executable: %v", err)
	}
	return exe
}

// env builds the child environment: temp HOME/GOBIN, fake tools first on PATH,
// and the fake-go/fake-CLI controls.
func (f *fixture) env(extra map[string]string) []string {
	cliName := "agent-sync.exe"
	if runtime.GOOS != "windows" {
		cliName = "agent-sync"
	}
	kv := map[string]string{
		"FAKE_GO_VERSION":         "1.27.1",
		"FAKE_GOBIN":              f.binDir,
		"FAKE_GOPATH":             f.gopath,
		"FAKE_GO_BIN_DIR":         f.binDir,
		"FAKE_CLI_NAME":           cliName,
		"FAKE_CLI_SOURCE":         f.testBinary(),
		"FAKE_GO_INSTALL_MARKER":  f.marker,
		"AGENT_SYNC_FAKE_CLI":     "1",
		"AGENT_SYNC_FAKE_CLI_OUT": f.cliOut,
		"AGENT_SYNC_FAKE_CLI_CWD": f.cliCwd,
		"USERPROFILE":             f.home,
		"HOME":                    f.home,
		"GOBIN":                   f.binDir,
		"GOPATH":                  f.gopath,
		"PATH":                    f.fakeDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	for k, v := range extra {
		kv[k] = v
	}
	return envWith(os.Environ(), kv)
}

func envWith(base []string, kv map[string]string) []string {
	norm := func(s string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(s)
		}
		return s
	}
	want := map[string]bool{}
	for k := range kv {
		want[norm(k)] = true
	}
	out := make([]string, 0, len(base)+len(kv))
	for _, e := range base {
		k := e
		if i := strings.IndexByte(e, '='); i >= 0 {
			k = e[:i]
		}
		if want[norm(k)] {
			continue
		}
		out = append(out, e)
	}
	for k, v := range kv {
		out = append(out, k+"="+v)
	}
	return out
}

type runResult struct {
	code   int
	stdout string
	stderr string
}

func (f *fixture) runPS(interpreter, callerDir string, extra map[string]string, args ...string) runResult {
	f.t.Helper()
	full := append([]string{"-NoProfile", "-NonInteractive", "-File", f.script}, args...)
	return f.exec(interpreter, callerDir, extra, nil, full...)
}

// psQuoteLiteral renders s as a single-quoted PowerShell string literal, the
// same quoting the interactive prompt would produce. It is used only by tests to
// hand the script an exact value; the wrapper itself never evaluates user input.
func psQuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// runPSCommand runs the script through -Command with caller-supplied argument
// expressions. Unlike -File, this lets a test pass a raw double quote to -Repo
// unchanged even on Windows PowerShell 5.1, where -File re-parses the native
// command line.
func (f *fixture) runPSCommand(interpreter, callerDir string, extra map[string]string, argExprs ...string) runResult {
	f.t.Helper()
	command := "& " + psQuoteLiteral(f.script)
	if len(argExprs) > 0 {
		command += " " + strings.Join(argExprs, " ")
	}
	full := []string{"-NoProfile", "-NonInteractive", "-Command", command}
	return f.exec(interpreter, callerDir, extra, nil, full...)
}

func (f *fixture) runSh(callerDir, stdin string, extra map[string]string, args ...string) runResult {
	f.t.Helper()
	var in *strings.Reader
	if stdin != "" {
		in = strings.NewReader(stdin)
	}
	return f.exec(f.script, callerDir, extra, in, args...)
}

func (f *fixture) exec(name, callerDir string, extra map[string]string, stdin *strings.Reader, args ...string) runResult {
	f.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = callerDir
	cmd.Env = f.env(extra)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			f.t.Fatalf("running %s: %v", name, err)
		}
		code = ee.ExitCode()
	}
	return runResult{code: code, stdout: out.String(), stderr: errOut.String()}
}

func (f *fixture) readCLIArgs() []string {
	f.t.Helper()
	data, err := os.ReadFile(f.cliOut)
	if err != nil {
		f.t.Fatalf("fake CLI was not invoked: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	return strings.Split(string(data), "\x00")
}

func (f *fixture) readCLICwd() string {
	f.t.Helper()
	data, err := os.ReadFile(f.cliCwd)
	if err != nil {
		f.t.Fatalf("fake CLI cwd was not recorded: %v", err)
	}
	return string(data)
}

func (f *fixture) cliInvocations() (int, bool) {
	data, err := os.ReadFile(f.cliOut)
	if err != nil {
		return 0, false
	}
	if len(data) == 0 {
		return 0, true
	}
	return len(strings.Split(string(data), "\x00")), true
}

// writeWindowsFakes installs fake go/git .cmd shims.
func (f *fixture) writeWindowsFakes() {
	mustWrite(f.t, filepath.Join(f.fakeDir, "go.cmd"), fakeGoCmd)
	mustWrite(f.t, filepath.Join(f.fakeDir, "git.cmd"), fakeGitCmd)
}

// writePOSIXFakes installs fake go/git shell shims.
func (f *fixture) writePOSIXFakes() {
	mustWriteMode(f.t, filepath.Join(f.fakeDir, "go"), fakeGoSh, 0o755)
	mustWriteMode(f.t, filepath.Join(f.fakeDir, "git"), fakeGitSh, 0o755)
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			m[p] = "dir"
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		m[p] = fmt.Sprintf("file:%d:%v", info.Size(), info.Mode())
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return m
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	mustWriteMode(t, path, content, 0o644)
}

func mustWriteMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func callerDir(t *testing.T, f *fixture) string {
	t.Helper()
	d := filepath.Join(f.base, "caller dir")
	mustMkdir(t, d)
	return d
}

// --- Windows install.ps1 ---

func psInterpreters(t *testing.T) []string {
	t.Helper()
	var found []string
	seen := map[string]bool{}
	for _, name := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		key := strings.ToLower(filepath.Base(p))
		if seen[key] {
			continue
		}
		seen[key] = true
		found = append(found, p)
	}
	sort.Strings(found)
	return found
}

func requireWindowsPS(t *testing.T) []string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("install.ps1 targets Windows PowerShell")
	}
	exes := psInterpreters(t)
	if len(exes) == 0 {
		t.Skip("no native PowerShell interpreter (pwsh/powershell) found on PATH")
	}
	return exes
}

func TestInstallPs1RepoForwardingExact(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		t.Run(filepath.Base(exe), func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			repo := `C:\agents dir\it's $HOME; v2.git`
			dir := callerDir(t, f)

			res := f.runPS(exe, dir, nil, "-Repo", repo)
			if res.code != 0 {
				t.Fatalf("exit %d\nstdout=%s\nstderr=%s", res.code, res.stdout, res.stderr)
			}
			want := []string{"install", "--repo", repo}
			if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
				t.Fatalf("cli argv = %q, want %q", got, want)
			}
			if got := f.readCLICwd(); !samePath(got, dir) {
				t.Fatalf("cli cwd = %q, want %q", got, dir)
			}
			if _, err := os.Stat(filepath.Join(f.binDir, "agent-sync.exe")); err != nil {
				t.Fatalf("installed binary missing in GOBIN: %v", err)
			}
			if !strings.Contains(res.stdout, f.binDir) {
				t.Fatalf("stdout does not report the binary directory %q:\n%s", f.binDir, res.stdout)
			}
		})
	}
}

// TestInstallPs1ExplicitRepoSkipsDefaultPreflight pins that an explicit -Repo
// bypasses the default-source preflight even when this checkout is not a usable
// git source: the CLI runs with the URL verbatim and git is never asked to
// resolve the checkout.
func TestInstallPs1ExplicitRepoSkipsDefaultPreflight(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		t.Run(filepath.Base(exe), func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			res := f.runPS(exe, callerDir(t, f), map[string]string{"FAKE_GIT": "nongit"},
				"-Repo", "https://host/agents.git")
			if res.code != 0 {
				t.Fatalf("exit %d\nstdout=%s\nstderr=%s", res.code, res.stdout, res.stderr)
			}
			want := []string{"install", "--repo", "https://host/agents.git"}
			if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
				t.Fatalf("cli argv = %q, want %q", got, want)
			}
		})
	}
}

// TestInstallPs1DefaultSourceUsesCheckout covers the omitted -Repo path: the
// committed "main" branch of this checkout is validated, then the CLI is given
// this checkout's own root (a path with spaces and apostrophes) verbatim.
func TestInstallPs1DefaultSourceUsesCheckout(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		t.Run(filepath.Base(exe), func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			dir := callerDir(t, f)

			res := f.runPS(exe, dir, map[string]string{"FAKE_GIT": "ok"})
			if res.code != 0 {
				t.Fatalf("exit %d\nstdout=%s\nstderr=%s", res.code, res.stdout, res.stderr)
			}
			want := []string{"install", "--repo", f.root}
			if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
				t.Fatalf("cli argv = %q, want %q", got, want)
			}
			if got := f.readCLICwd(); !samePath(got, dir) {
				t.Fatalf("cli cwd = %q, want %q", got, dir)
			}
			if _, err := os.Stat(filepath.Join(f.binDir, "agent-sync.exe")); err != nil {
				t.Fatalf("installed binary missing in GOBIN: %v", err)
			}
		})
	}
}

// TestInstallPs1DefaultSourceNonMainBranchNotes keeps the non-main branch
// advisory: the install still runs against the committed "main" branch but says
// which branch the checkout is actually on.
func TestInstallPs1DefaultSourceNonMainBranchNotes(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		t.Run(filepath.Base(exe), func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			res := f.runPS(exe, callerDir(t, f), map[string]string{
				"FAKE_GIT":        "ok",
				"FAKE_GIT_BRANCH": "feature/x",
			})
			if res.code != 0 {
				t.Fatalf("exit %d\nstdout=%s\nstderr=%s", res.code, res.stdout, res.stderr)
			}
			if !strings.Contains(res.stdout, "note:") || !strings.Contains(res.stdout, "feature/x") {
				t.Fatalf("missing non-main branch note:\n%s", res.stdout)
			}
			want := []string{"install", "--repo", f.root}
			if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
				t.Fatalf("cli argv = %q, want %q", got, want)
			}
		})
	}
}

// TestInstallPs1DefaultSourcePreflightFailures pins that an unusable default
// source aborts before any build or CLI call, so a non-git checkout, a missing
// "main" branch, or a committed tree without eligible agents cannot silently
// install a wrong or mutable source.
func TestInstallPs1DefaultSourcePreflightFailures(t *testing.T) {
	cases := []struct {
		name string
		mode string
	}{
		{"not-a-git-repo", "nongit"},
		{"no-main-branch", "nomain"},
		{"no-agent-markdown", "noagents"},
		{"non-md-only", "onlynonmd"},
		{"enclosing-parent-repo", "parent"},
		{"uppercase-md-not-eligible", "uppercase"},
	}
	for _, exe := range requireWindowsPS(t) {
		for _, tc := range cases {
			t.Run(filepath.Base(exe)+"/"+tc.name, func(t *testing.T) {
				f := newFixture(t, "install.ps1")
				f.writeWindowsFakes()
				res := f.runPS(exe, callerDir(t, f), map[string]string{"FAKE_GIT": tc.mode})
				if res.code == 0 {
					t.Fatalf("expected a preflight failure, got exit 0\nstdout=%s", res.stdout)
				}
				if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
					t.Fatalf("go install ran despite a failed preflight (marker present: %v)", err)
				}
				if _, ok := f.cliInvocations(); ok {
					t.Fatal("the CLI was invoked despite a failed preflight")
				}
			})
		}
	}
}

func TestInstallPs1PathFlagOnlyWhenExplicit(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		t.Run(filepath.Base(exe)+"/default", func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			res := f.runPS(exe, callerDir(t, f), nil, "-Repo", "https://host/agents.git")
			if res.code != 0 {
				t.Fatalf("exit %d stderr=%s", res.code, res.stderr)
			}
			want := []string{"install", "--repo", "https://host/agents.git"}
			if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
				t.Fatalf("cli argv = %q, want %q", got, want)
			}
		})
		t.Run(filepath.Base(exe)+"/add-to-path", func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			res := f.runPS(exe, callerDir(t, f), nil, "-Repo", "https://host/agents.git", "-AddToPath")
			if res.code != 0 {
				t.Fatalf("exit %d stderr=%s", res.code, res.stderr)
			}
			want := []string{"install", "--repo", "https://host/agents.git", "--add-to-path"}
			if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
				t.Fatalf("cli argv = %q, want %q", got, want)
			}
		})
	}
}

func TestInstallPs1DryRunMakesNoChanges(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		t.Run(filepath.Base(exe), func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			// Create the caller directory before the snapshot: it is a fixture
			// artifact, not something the dry-run may or may not create.
			dir := callerDir(t, f)
			before := snapshotTree(t, f.base)

			res := f.runPS(exe, dir, nil, "-Repo", "https://host/agents.git", "-DryRun")
			if res.code != 0 {
				t.Fatalf("exit %d stderr=%s", res.code, res.stderr)
			}
			if !strings.Contains(res.stdout, "dry-run") {
				t.Fatalf("dry-run output missing:\n%s", res.stdout)
			}
			if _, ok := f.cliInvocations(); ok {
				t.Fatal("dry-run invoked the CLI")
			}
			if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatalf("dry-run ran go install (marker present: %v)", err)
			}
			if _, err := os.Stat(f.binDir); !os.IsNotExist(err) {
				t.Fatalf("dry-run created the bin directory: %v", err)
			}
			if after := snapshotTree(t, f.base); !reflect.DeepEqual(before, after) {
				t.Fatalf("dry-run changed the filesystem\nbefore=%v\nafter=%v", before, after)
			}
		})
	}
}

// TestInstallPs1RejectsBlankEmptyAndUnknown covers an explicitly bound but
// unusable -Repo and unknown arguments. An omitted -Repo is no longer invalid:
// it selects the default checkout source and is covered by the default-source
// tests above.
func TestInstallPs1RejectsBlankEmptyAndUnknown(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		cases := []struct {
			name string
			args []string
		}{
			{"blank", []string{"-Repo", "   "}},
			{"empty", []string{"-Repo", ""}},
			{"unknown", []string{"-Repo", "https://host/a.git", "-Bogus"}},
		}
		for _, tc := range cases {
			t.Run(filepath.Base(exe)+"/"+tc.name, func(t *testing.T) {
				f := newFixture(t, "install.ps1")
				f.writeWindowsFakes()
				res := f.runPS(exe, callerDir(t, f), nil, tc.args...)
				if res.code == 0 {
					t.Fatalf("expected failure, got exit 0\nstdout=%s", res.stdout)
				}
				if _, ok := f.cliInvocations(); ok {
					t.Fatal("CLI was invoked despite invalid arguments")
				}
			})
		}
	}
}

// TestInstallPs1RejectsRawDoubleQuoteInRepo covers the native argv
// serialization risk: a -Repo value carrying a raw double quote could be
// re-parsed into extra CLI flags (here --yes/--add-to-path) by PowerShell 5.1,
// so the script must reject it before running `go install` or the CLI.
func TestInstallPs1RejectsRawDoubleQuoteInRepo(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		t.Run(filepath.Base(exe), func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			// Passed via -Command with a single-quoted literal so Windows
			// PowerShell 5.1 delivers the raw double quote to -Repo; -File may
			// re-parse the native command line first.
			payload := `https://host/a" --yes --add-to-path "`

			res := f.runPSCommand(exe, callerDir(t, f), nil,
				"-Repo", psQuoteLiteral(payload))

			if res.code == 0 {
				t.Fatalf("expected the raw double quote to be rejected, got exit 0\nstdout=%s", res.stdout)
			}
			if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatalf("go install ran for a rejected repo (marker present: %v)", err)
			}
			if _, ok := f.cliInvocations(); ok {
				t.Fatal("the CLI was invoked for a rejected repo")
			}
			if !strings.Contains(strings.ToLower(res.stderr), "quote") {
				t.Fatalf("error should name the rejected character:\nstdout=%s\nstderr=%s", res.stdout, res.stderr)
			}
		})
	}
}

func TestInstallPs1ToolFailurePaths(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		t.Run(filepath.Base(exe)+"/go-install-fails", func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			res := f.runPS(exe, callerDir(t, f), map[string]string{"FAKE_GO_INSTALL_FAIL": "1"}, "-Repo", "https://host/a.git")
			if res.code == 0 {
				t.Fatalf("expected failure, got 0\nstdout=%s", res.stdout)
			}
			if _, ok := f.cliInvocations(); ok {
				t.Fatal("CLI was invoked after a failed go install")
			}
		})
		t.Run(filepath.Base(exe)+"/cli-exit-propagates", func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			res := f.runPS(exe, callerDir(t, f), map[string]string{"AGENT_SYNC_FAKE_CLI_EXIT": "5"}, "-Repo", "https://host/a.git")
			if res.code != 5 {
				t.Fatalf("exit = %d, want 5\nstderr=%s", res.code, res.stderr)
			}
		})
		t.Run(filepath.Base(exe)+"/old-go", func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			res := f.runPS(exe, callerDir(t, f), map[string]string{"FAKE_GO_VERSION": "1.26.0"}, "-Repo", "https://host/a.git")
			if res.code == 0 {
				t.Fatal("expected the old Go version to be rejected")
			}
			if _, ok := f.cliInvocations(); ok {
				t.Fatal("CLI was invoked with an unsupported Go version")
			}
		})
		t.Run(filepath.Base(exe)+"/prerelease-go", func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			res := f.runPS(exe, callerDir(t, f), map[string]string{"FAKE_GO_VERSION": "1.28rc1"}, "-Repo", "https://host/a.git")
			if res.code == 0 {
				t.Fatal("expected the prerelease Go version to be rejected")
			}
		})
		t.Run(filepath.Base(exe)+"/no-tools", func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			empty := filepath.Join(f.base, "empty path dir")
			mustMkdir(t, empty)
			res := f.runPS(exe, callerDir(t, f), map[string]string{"PATH": empty}, "-Repo", "https://host/a.git")
			if res.code == 0 {
				t.Fatal("expected a failure when Go and Git are absent")
			}
		})
	}
}

func TestInstallPs1GOPATHFallback(t *testing.T) {
	for _, exe := range requireWindowsPS(t) {
		t.Run(filepath.Base(exe), func(t *testing.T) {
			f := newFixture(t, "install.ps1")
			f.writeWindowsFakes()
			wantBin := filepath.Join(f.gopath, "bin")
			res := f.runPS(exe, callerDir(t, f), map[string]string{
				"FAKE_GOBIN":      "",
				"FAKE_GO_BIN_DIR": wantBin,
			}, "-Repo", "https://host/a.git")
			if res.code != 0 {
				t.Fatalf("exit %d stderr=%s", res.code, res.stderr)
			}
			if _, ok := f.cliInvocations(); !ok {
				t.Fatal("CLI was not invoked for the GOPATH fallback case")
			}
			if !strings.Contains(res.stdout, wantBin) {
				t.Fatalf("stdout does not report %q:\n%s", wantBin, res.stdout)
			}
			if _, err := os.Stat(filepath.Join(wantBin, "agent-sync.exe")); err != nil {
				t.Fatalf("binary not installed under GOPATH/bin: %v", err)
			}
		})
	}
}

// --- POSIX install.sh ---

// genuineBash returns a real bash suitable for parsing install.sh. On Windows
// the WSL launcher is ignored in favour of Git Bash, which is a genuine bash.
func genuineBash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		for _, p := range []string{
			`C:\Program Files\Git\bin\bash.exe`,
			`C:\Program Files\Git\usr\bin\bash.exe`,
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		return ""
	}
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	return ""
}

func TestInstallShSyntaxParses(t *testing.T) {
	bash := genuineBash(t)
	if bash == "" {
		t.Skip("no genuine bash found (Windows WSL bash is not used as a substitute)")
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// Feed the script on stdin so no shell-specific path translation applies.
	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(string(data))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n install.sh: %v\n%s", err, out)
	}
}

func requireLinuxSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh runtime test requires a native Linux/macOS host; Windows bash is a WSL launcher, not a faithful environment")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
}

func TestInstallShRepoForwardingExact(t *testing.T) {
	requireLinuxSh(t)
	f := newFixture(t, "install.sh")
	f.writePOSIXFakes()
	dir := callerDir(t, f)
	repo := `/srv/agents dir/it's $HOME; v2.git`

	res := f.runSh(dir, "", nil, "--repo", repo)
	if res.code != 0 {
		t.Fatalf("exit %d\nstdout=%s\nstderr=%s", res.code, res.stdout, res.stderr)
	}
	want := []string{"install", "--repo", repo, "--shell", "zsh"}
	if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cli argv = %q, want %q", got, want)
	}
	if got := f.readCLICwd(); !samePath(got, dir) {
		t.Fatalf("cli cwd = %q, want %q", got, dir)
	}
	if _, err := os.Stat(filepath.Join(f.binDir, "agent-sync")); err != nil {
		t.Fatalf("installed binary missing in GOBIN: %v", err)
	}
}

// TestInstallShExplicitRepoSkipsDefaultPreflight mirrors the PowerShell test:
// an explicit --repo bypasses the default-source preflight entirely.
func TestInstallShExplicitRepoSkipsDefaultPreflight(t *testing.T) {
	requireLinuxSh(t)
	f := newFixture(t, "install.sh")
	f.writePOSIXFakes()
	res := f.runSh(callerDir(t, f), "", map[string]string{"FAKE_GIT": "nongit"},
		"--repo", "https://host/agents.git")
	if res.code != 0 {
		t.Fatalf("exit %d\nstdout=%s\nstderr=%s", res.code, res.stdout, res.stderr)
	}
	want := []string{"install", "--repo", "https://host/agents.git", "--shell", "zsh"}
	if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cli argv = %q, want %q", got, want)
	}
}

// TestInstallShDefaultSourceUsesCheckout covers the omitted --repo path: the
// committed "main" branch is validated and the CLI receives this checkout's own
// root verbatim (a path with spaces and apostrophes).
func TestInstallShDefaultSourceUsesCheckout(t *testing.T) {
	requireLinuxSh(t)
	f := newFixture(t, "install.sh")
	f.writePOSIXFakes()
	dir := callerDir(t, f)

	res := f.runSh(dir, "", map[string]string{"FAKE_GIT": "ok"})
	if res.code != 0 {
		t.Fatalf("exit %d\nstdout=%s\nstderr=%s", res.code, res.stdout, res.stderr)
	}
	want := []string{"install", "--repo", f.root, "--shell", "zsh"}
	if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cli argv = %q, want %q", got, want)
	}
	if got := f.readCLICwd(); !samePath(got, dir) {
		t.Fatalf("cli cwd = %q, want %q", got, dir)
	}
}

// TestInstallShDefaultSourceNonMainBranchNotes keeps the non-main branch
// advisory while still sourcing the committed "main" branch.
func TestInstallShDefaultSourceNonMainBranchNotes(t *testing.T) {
	requireLinuxSh(t)
	f := newFixture(t, "install.sh")
	f.writePOSIXFakes()
	res := f.runSh(callerDir(t, f), "", map[string]string{
		"FAKE_GIT":        "ok",
		"FAKE_GIT_BRANCH": "feature/x",
	})
	if res.code != 0 {
		t.Fatalf("exit %d\nstdout=%s\nstderr=%s", res.code, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "note:") || !strings.Contains(res.stdout, "feature/x") {
		t.Fatalf("missing non-main branch note:\n%s", res.stdout)
	}
}

// TestInstallShDefaultSourcePreflightFailures pins that an unusable default
// source aborts before any build or CLI call.
func TestInstallShDefaultSourcePreflightFailures(t *testing.T) {
	requireLinuxSh(t)
	cases := []struct {
		name string
		mode string
	}{
		{"not-a-git-repo", "nongit"},
		{"no-main-branch", "nomain"},
		{"no-agent-markdown", "noagents"},
		{"non-md-only", "onlynonmd"},
		{"enclosing-parent-repo", "parent"},
		{"uppercase-md-not-eligible", "uppercase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "install.sh")
			f.writePOSIXFakes()
			res := f.runSh(callerDir(t, f), "", map[string]string{"FAKE_GIT": tc.mode})
			if res.code == 0 {
				t.Fatalf("expected a preflight failure, got exit 0\nstdout=%s", res.stdout)
			}
			if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatalf("go install ran despite a failed preflight (marker present: %v)", err)
			}
			if _, ok := f.cliInvocations(); ok {
				t.Fatal("the CLI was invoked despite a failed preflight")
			}
		})
	}
}

func TestInstallShPathFlagOnlyWhenExplicit(t *testing.T) {
	requireLinuxSh(t)
	f := newFixture(t, "install.sh")
	f.writePOSIXFakes()
	res := f.runSh(callerDir(t, f), "", nil, "--repo", "https://host/agents.git", "--add-to-path")
	if res.code != 0 {
		t.Fatalf("exit %d stderr=%s", res.code, res.stderr)
	}
	want := []string{"install", "--repo", "https://host/agents.git", "--add-to-path", "--shell", "zsh"}
	if got := f.readCLIArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cli argv = %q, want %q", got, want)
	}
}

func TestInstallShDryRunMakesNoChanges(t *testing.T) {
	requireLinuxSh(t)
	f := newFixture(t, "install.sh")
	f.writePOSIXFakes()
	// Create the caller directory before the snapshot: it is a fixture
	// artifact, not something the dry-run may or may not create.
	dir := callerDir(t, f)
	before := snapshotTree(t, f.base)

	res := f.runSh(dir, "", nil, "--repo", "https://host/agents.git", "--dry-run")
	if res.code != 0 {
		t.Fatalf("exit %d stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "dry-run") {
		t.Fatalf("dry-run output missing:\n%s", res.stdout)
	}
	if _, ok := f.cliInvocations(); ok {
		t.Fatal("dry-run invoked the CLI")
	}
	if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatalf("dry-run ran go install (marker present: %v)", err)
	}
	if after := snapshotTree(t, f.base); !reflect.DeepEqual(before, after) {
		t.Fatalf("dry-run changed the filesystem\nbefore=%v\nafter=%v", before, after)
	}
}

// TestInstallShRejectsBlankEmptyAndUnknown covers an explicitly bound but
// unusable --repo and malformed arguments. An omitted --repo is no longer
// invalid: it selects the default checkout source, covered above.
func TestInstallShRejectsBlankEmptyAndUnknown(t *testing.T) {
	requireLinuxSh(t)
	cases := []struct {
		name string
		args []string
	}{
		{"blank", []string{"--repo", "   "}},
		{"empty", []string{"--repo", ""}},
		{"unknown", []string{"--repo", "https://host/a.git", "--bogus"}},
		{"repo-without-value", []string{"--repo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "install.sh")
			f.writePOSIXFakes()
			res := f.runSh(callerDir(t, f), "", nil, tc.args...)
			if res.code == 0 {
				t.Fatalf("expected failure, got 0\nstdout=%s", res.stdout)
			}
			if _, ok := f.cliInvocations(); ok {
				t.Fatal("CLI was invoked despite invalid arguments")
			}
		})
	}
}

func TestInstallShToolFailurePaths(t *testing.T) {
	requireLinuxSh(t)
	t.Run("go-install-fails", func(t *testing.T) {
		f := newFixture(t, "install.sh")
		f.writePOSIXFakes()
		res := f.runSh(callerDir(t, f), "", map[string]string{"FAKE_GO_INSTALL_FAIL": "1"}, "--repo", "https://host/a.git")
		if res.code == 0 {
			t.Fatalf("expected failure, got 0\nstdout=%s", res.stdout)
		}
		if _, ok := f.cliInvocations(); ok {
			t.Fatal("CLI was invoked after a failed go install")
		}
	})
	t.Run("cli-exit-propagates", func(t *testing.T) {
		f := newFixture(t, "install.sh")
		f.writePOSIXFakes()
		res := f.runSh(callerDir(t, f), "", map[string]string{"AGENT_SYNC_FAKE_CLI_EXIT": "5"}, "--repo", "https://host/a.git")
		if res.code != 5 {
			t.Fatalf("exit = %d, want 5\nstderr=%s", res.code, res.stderr)
		}
	})
	t.Run("old-go", func(t *testing.T) {
		f := newFixture(t, "install.sh")
		f.writePOSIXFakes()
		res := f.runSh(callerDir(t, f), "", map[string]string{"FAKE_GO_VERSION": "1.26.0"}, "--repo", "https://host/a.git")
		if res.code == 0 {
			t.Fatal("expected the old Go version to be rejected")
		}
	})
	t.Run("gopath-fallback", func(t *testing.T) {
		f := newFixture(t, "install.sh")
		f.writePOSIXFakes()
		wantBin := filepath.Join(f.gopath, "bin")
		res := f.runSh(callerDir(t, f), "", map[string]string{
			"FAKE_GOBIN":      "",
			"FAKE_GO_BIN_DIR": wantBin,
		}, "--repo", "https://host/a.git")
		if res.code != 0 {
			t.Fatalf("exit %d stderr=%s", res.code, res.stderr)
		}
		if !strings.Contains(res.stdout, wantBin) {
			t.Fatalf("stdout does not report %q:\n%s", wantBin, res.stdout)
		}
	})
	t.Run("no-tools", func(t *testing.T) {
		f := newFixture(t, "install.sh")
		empty := filepath.Join(f.base, "empty path dir")
		mustMkdir(t, empty)
		res := f.runSh(callerDir(t, f), "", map[string]string{"PATH": empty}, "--repo", "https://host/a.git")
		if res.code == 0 {
			t.Fatal("expected a failure when Go and Git are absent")
		}
	})
}

const fakeGoCmd = `@echo off
setlocal
if "%~1"=="version" (
  if defined FAKE_GO_VERSION (echo go version go%FAKE_GO_VERSION% windows/amd64) else (echo go version go1.27.1 windows/amd64)
  exit /b 0
)
if "%~1"=="env" (
  if "%~2"=="GOBIN" (echo(%FAKE_GOBIN%& exit /b 0)
  if "%~2"=="GOPATH" (echo(%FAKE_GOPATH%& exit /b 0)
  exit /b 1
)
if "%~1"=="install" (
  if "%FAKE_GO_INSTALL_FAIL%"=="1" (echo fake go install failed 1>&2& exit /b 1)
  if defined FAKE_GO_INSTALL_MARKER (> "%FAKE_GO_INSTALL_MARKER%" echo installed)
  if not exist "%FAKE_GO_BIN_DIR%" mkdir "%FAKE_GO_BIN_DIR%"
  copy /y "%FAKE_CLI_SOURCE%" "%FAKE_GO_BIN_DIR%\%FAKE_CLI_NAME%" >nul
  exit /b 0
)
echo unknown fake go invocation 1>&2
exit /b 1
`

const fakeGoSh = `#!/bin/sh
set -eu
if [ "$1" = "version" ]; then
  printf 'go version go%s linux/amd64\n' "${FAKE_GO_VERSION:-1.27.1}"
  exit 0
fi
if [ "$1" = "env" ]; then
  case "${2:-}" in
    GOBIN) printf '%s\n' "${FAKE_GOBIN:-}" ;;
    GOPATH) printf '%s\n' "${FAKE_GOPATH:-}" ;;
    *) exit 1 ;;
  esac
  exit 0
fi
if [ "$1" = "install" ]; then
  if [ "${FAKE_GO_INSTALL_FAIL:-}" = "1" ]; then
    echo 'fake go install failed' >&2
    exit 1
  fi
  if [ -n "${FAKE_GO_INSTALL_MARKER:-}" ]; then
    echo installed > "$FAKE_GO_INSTALL_MARKER"
  fi
  mkdir -p "$FAKE_GO_BIN_DIR"
  cp "$FAKE_CLI_SOURCE" "$FAKE_GO_BIN_DIR/$FAKE_CLI_NAME"
  chmod +x "$FAKE_GO_BIN_DIR/$FAKE_CLI_NAME"
  exit 0
fi
echo 'unknown fake go invocation' >&2
exit 1
`

// fakeGitCmd answers only what the wrappers' default-source preflight asks of
// git: `git -C <dir> rev-parse --show-toplevel`, `... rev-parse --verify
// refs/heads/main`, `... ls-tree ... -- agents`, and `... rev-parse --abbrev-ref
// HEAD`. FAKE_GIT selects the scenario ("ok", "nongit", "nomain", "noagents",
// "onlynonmd", "parent", "uppercase"); it defaults to "nongit" so a fixture that
// never opts in is not a git checkout. "parent" makes the reported top-level a
// parent of the checkout (a subdirectory of an enclosing repository) and
// "uppercase" lists only "agents/a.MD" (a case mismatch). FAKE_GIT_BRANCH
// overrides the reported current branch. The shim never creates a repository or
// a commit.
const fakeGitCmd = `@echo off
setlocal
if not defined FAKE_GIT set "FAKE_GIT=nongit"
if "%~3"=="rev-parse" goto revparse
if "%~3"=="ls-tree" goto lstree
echo git version 2.0.0.fake
exit /b 0

:revparse
if "%~4"=="--show-toplevel" goto toplevel
if "%~4"=="--verify" goto verify
if "%~4"=="--abbrev-ref" goto branch
exit /b 1

:toplevel
if "%FAKE_GIT%"=="nongit" exit /b 1
if "%FAKE_GIT%"=="parent" (
  echo(%~2\..
  exit /b 0
)
echo(%~2
exit /b 0

:verify
if "%FAKE_GIT%"=="nomain" exit /b 1
exit /b 0

:branch
if defined FAKE_GIT_BRANCH (
  echo(%FAKE_GIT_BRANCH%
) else (
  echo(main
)
exit /b 0

:lstree
if "%FAKE_GIT%"=="noagents" exit /b 0
if "%FAKE_GIT%"=="onlynonmd" (
  echo(agents/readme.txt
  echo(agents/notes
  exit /b 0
)
if "%FAKE_GIT%"=="uppercase" (
  echo(agents/a.MD
  exit /b 0
)
echo(agents/example.md
exit /b 0
`

// fakeGitSh is the POSIX counterpart of fakeGitCmd; it reads the same FAKE_GIT
// and FAKE_GIT_BRANCH controls.
const fakeGitSh = `#!/bin/sh
mode="${FAKE_GIT:-nongit}"
case "${3:-}" in
  rev-parse)
    case "${4:-}" in
      --show-toplevel)
        [ "$mode" = "nongit" ] && exit 1
        if [ "$mode" = "parent" ]; then printf '%s\n' "${2:-}/.."; else printf '%s\n' "${2:-}"; fi
        exit 0 ;;
      --verify)
        [ "$mode" = "nomain" ] && exit 1
        exit 0 ;;
      --abbrev-ref)
        printf '%s\n' "${FAKE_GIT_BRANCH:-main}"
        exit 0 ;;
    esac
    exit 1 ;;
  ls-tree)
    case "$mode" in
      noagents) : ;;
      onlynonmd) printf '%s\n' 'agents/readme.txt' 'agents/notes' ;;
      uppercase) printf '%s\n' 'agents/a.MD' ;;
      *) printf '%s\n' 'agents/example.md' ;;
    esac
    exit 0 ;;
esac
echo 'git version 2.0.0.fake'
exit 0
`
