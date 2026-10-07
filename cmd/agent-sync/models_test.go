package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-sync/internal/modelpref"
	"agent-sync/internal/state"
	"agent-sync/internal/testsupport"
)

const catalogJSON = `{"openai":{"id":"openai","name":"OpenAI","models":{"gpt-4":{"id":"gpt-4","name":"GPT-4","cost":{"input":1,"output":2}}}}}`

func catalogServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, catalogJSON)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func e2eRunIn(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut strings.Builder
	code = run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func installAgent(t *testing.T, files map[string]string) (remote, target, stateDir string) {
	t.Helper()
	remote, _ = e2eRemote(t, files)
	target = filepath.Join(t.TempDir(), "target")
	stateDir = filepath.Join(t.TempDir(), "state")
	if code, _, errOut := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("install code = %d, stderr = %s", code, errOut)
	}
	return remote, target, stateDir
}

func setExplicit(t *testing.T, target, stateDir, agent, ref string, extra ...string) {
	t.Helper()
	args := append([]string{"models", "set"}, extra...)
	args = append(args, "--offline", "--target", target, "--state", stateDir, agent, ref)
	if code, _, errOut := e2eRun(t, args...); code != 0 {
		t.Fatalf("models set code = %d, stderr = %s", code, errOut)
	}
}

// TestModelsSetExplicitSurvivesPull covers V9/V12: a non-interactive set
// injects the model, records the upstream-raw baseline and is re-applied on a
// later pull when only the body changes.
func TestModelsSetExplicitSurvivesPull(t *testing.T) {
	remote, target, stateDir := installAgent(t, map[string]string{"agents/a.md": "# a\n"})
	setExplicit(t, target, stateDir, "agents/a.md", "openai/gpt-4")

	agentPath := filepath.Join(target, "agents", "a.md")
	if got, want := e2eRead(t, agentPath), "---\nmodel: openai/gpt-4\n---\n# a\n"; got != want {
		t.Fatalf("after set = %q, want %q", got, want)
	}
	st := e2eState(t, stateDir)
	pref, ok := st.ModelPrefs["agents/a.md"]
	if !ok || pref.Mode != modelpref.ModeExplicit || pref.Model != "openai/gpt-4" {
		t.Fatalf("pref = %+v (present=%v)", pref, ok)
	}
	if st.Files["agents/a.md"].Hash != state.Hash([]byte("# a\n")) {
		t.Fatalf("baseline must record upstream raw, got %+v", st.Files["agents/a.md"])
	}

	testsupport.Commit(t, remote, map[string]string{"agents/a.md": "# a v2\n"}, nil, "body update")
	if code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("pull code = %d, stderr = %s", code, errOut)
	}
	if got, want := e2eRead(t, agentPath), "---\nmodel: openai/gpt-4\n---\n# a v2\n"; got != want {
		t.Fatalf("after pull = %q, want %q", got, want)
	}
	if st := e2eState(t, stateDir); st.Files["agents/a.md"].Hash != state.Hash([]byte("# a v2\n")) {
		t.Fatalf("baseline after pull = %+v", st.Files["agents/a.md"])
	}
}

// TestModelsInheritThenResetAfterBaselineAdvance covers V9: inherit removes the
// upstream model; after the baseline advances, reset restores the new upstream
// model (checking the receipt is refreshed).
func TestModelsInheritThenResetAfterBaselineAdvance(t *testing.T) {
	remote, target, stateDir := installAgent(t, map[string]string{
		"agents/a.md": "---\nmodel: upstream\n---\nbody\n",
	})
	agentPath := filepath.Join(target, "agents", "a.md")

	if code, _, errOut := e2eRun(t, "models", "inherit", "--target", target, "--state", stateDir, "agents/a.md"); code != 0 {
		t.Fatalf("inherit code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, agentPath); got != "body\n" {
		t.Fatalf("after inherit = %q", got)
	}
	if st := e2eState(t, stateDir); st.ModelPrefs["agents/a.md"].Mode != modelpref.ModeInherit {
		t.Fatalf("pref = %+v", st.ModelPrefs["agents/a.md"])
	}

	testsupport.Commit(t, remote, map[string]string{"agents/a.md": "---\nmodel: upstream2\n---\nbody2\n"}, nil, "update")
	if code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("pull code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, agentPath); got != "body2\n" {
		t.Fatalf("inherit not kept across pull: %q", got)
	}

	if code, _, errOut := e2eRun(t, "models", "reset", "--target", target, "--state", stateDir, "agents/a.md"); code != 0 {
		t.Fatalf("reset code = %d, stderr = %s", code, errOut)
	}
	if got, want := e2eRead(t, agentPath), "---\nmodel: upstream2\n---\nbody2\n"; got != want {
		t.Fatalf("after reset = %q, want %q", got, want)
	}
	st := e2eState(t, stateDir)
	if _, ok := st.ModelPrefs["agents/a.md"]; ok {
		t.Fatalf("reset should clear the preference, got %+v", st.ModelPrefs)
	}
	if st.Files["agents/a.md"].Hash != state.Hash([]byte("---\nmodel: upstream2\n---\nbody2\n")) {
		t.Fatalf("baseline after reset = %+v", st.Files["agents/a.md"])
	}
}

// TestModelsPrefHiddenUpstreamModelUpdate covers FIX-1: when upstream changes
// only its `model:` field but the stored preference renders exactly the same
// bytes, the pull must keep the file AND refresh the receipt to the advanced
// raw baseline. Otherwise a later body-only update conflicts and reset restores
// the old upstream model.
func TestModelsPrefHiddenUpstreamModelUpdate(t *testing.T) {
	cases := []struct {
		name    string
		mode    string // "explicit" or "inherit"
		ref     string
		initial string // rendered local bytes after the preference is applied
		body    string // rendered local bytes after the later body update
	}{
		{
			name:    "explicit",
			mode:    "explicit",
			ref:     "openai/gpt-4",
			initial: "---\nmodel: openai/gpt-4\n---\nbody\n",
			body:    "---\nmodel: openai/gpt-4\n---\nbody2\n",
		},
		{
			name:    "inherit",
			mode:    "inherit",
			initial: "body\n",
			body:    "body2\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remote, target, stateDir := installAgent(t, map[string]string{
				"agents/a.md": "---\nmodel: upstreamA\n---\nbody\n",
			})
			agentPath := filepath.Join(target, "agents", "a.md")

			if tc.mode == "explicit" {
				setExplicit(t, target, stateDir, "agents/a.md", tc.ref)
			} else if code, _, errOut := e2eRun(t, "models", "inherit",
				"--target", target, "--state", stateDir, "agents/a.md"); code != 0 {
				t.Fatalf("inherit code = %d, stderr = %s", code, errOut)
			}
			if got := e2eRead(t, agentPath); got != tc.initial {
				t.Fatalf("after preference = %q, want %q", got, tc.initial)
			}

			// Upstream changes ONLY the model field; the preference hides it,
			// so the rendered file does not change.
			const rawB = "---\nmodel: upstreamB\n---\nbody\n"
			testsupport.Commit(t, remote, map[string]string{"agents/a.md": rawB}, nil, "model-only update")
			if code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes"); code != 0 {
				t.Fatalf("pull code = %d, stderr = %s", code, errOut)
			}
			if got := e2eRead(t, agentPath); got != tc.initial {
				t.Fatalf("pull changed the rendered file: %q", got)
			}

			st := e2eState(t, stateDir)
			if st.Files["agents/a.md"].Hash != state.Hash([]byte(rawB)) {
				t.Fatalf("baseline after hidden update = %+v", st.Files["agents/a.md"])
			}
			pref, ok := st.ModelPrefs["agents/a.md"]
			if !ok {
				t.Fatal("preference lost across the hidden update")
			}
			undone, err := modelpref.Undo([]byte(tc.initial), pref.Receipt)
			if err != nil {
				t.Fatalf("refresh receipt: %v", err)
			}
			if string(undone) != rawB {
				t.Fatalf("receipt undoes to %q, want new upstream raw %q", undone, rawB)
			}

			// A later upstream body update must be accepted without a conflict.
			const rawBody2 = "---\nmodel: upstreamB\n---\nbody2\n"
			testsupport.Commit(t, remote, map[string]string{"agents/a.md": rawBody2}, nil, "body update")
			if code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes"); code != 0 {
				t.Fatalf("body pull code = %d, stderr = %s", code, errOut)
			}
			if got := e2eRead(t, agentPath); got != tc.body {
				t.Fatalf("after body pull = %q, want %q", got, tc.body)
			}

			// Reset restores the NEW upstream model, not the previous one.
			if code, _, errOut := e2eRun(t, "models", "reset",
				"--target", target, "--state", stateDir, "agents/a.md"); code != 0 {
				t.Fatalf("reset code = %d, stderr = %s", code, errOut)
			}
			if got, want := e2eRead(t, agentPath), rawBody2; got != want {
				t.Fatalf("after reset = %q, want %q", got, want)
			}
		})
	}
}

// TestDeleteRetainsOrphanPreference covers V9: deleting a managed agent removes
// the baseline entry but keeps the preference so it is re-applied if the agent
// returns upstream.
func TestDeleteRetainsOrphanPreference(t *testing.T) {
	remote, target, stateDir := installAgent(t, map[string]string{"agents/a.md": "# a\n"})
	setExplicit(t, target, stateDir, "agents/a.md", "openai/gpt-4")

	testsupport.Commit(t, remote, nil, []string{"agents/a.md"}, "drop agent")
	if code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes", "--allow-delete"); code != 0 {
		t.Fatalf("pull code = %d, stderr = %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(target, "agents", "a.md")); !os.IsNotExist(err) {
		t.Fatalf("agent file should be deleted, stat err = %v", err)
	}
	st := e2eState(t, stateDir)
	if _, ok := st.Files["agents/a.md"]; ok {
		t.Fatalf("baseline entry should be removed, got %+v", st.Files)
	}
	if pref, ok := st.ModelPrefs["agents/a.md"]; !ok || pref.Model != "openai/gpt-4" {
		t.Fatalf("orphan preference should be retained, got %+v (present=%v)", pref, ok)
	}
}

// TestHandEditedModelAbortsPull covers V8: a manual model change makes the run
// fail closed and leaves the local file untouched, while a body-only edit does
// not (TestBodyEditPreservedWithModel).
func TestHandEditedModelAbortsPull(t *testing.T) {
	remote, target, stateDir := installAgent(t, map[string]string{"agents/a.md": "# a\n"})
	setExplicit(t, target, stateDir, "agents/a.md", "openai/gpt-4")

	agentPath := filepath.Join(target, "agents", "a.md")
	edited := strings.Replace(e2eRead(t, agentPath), "openai/gpt-4", "openai/manual", 1)
	e2eWrite(t, agentPath, edited)

	testsupport.Commit(t, remote, map[string]string{"agents/a.md": "# a v2\n"}, nil, "update")
	if code, _, _ := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes"); code == 0 {
		t.Fatal("pull should refuse a hand-edited model field")
	}
	if got := e2eRead(t, agentPath); got != edited {
		t.Fatalf("local file was modified: %q", got)
	}
}

func TestBodyEditPreservedWithModel(t *testing.T) {
	_, target, stateDir := installAgent(t, map[string]string{"agents/a.md": "# a\n"})
	setExplicit(t, target, stateDir, "agents/a.md", "openai/gpt-4")

	agentPath := filepath.Join(target, "agents", "a.md")
	edited := e2eRead(t, agentPath) + "extra\n"
	e2eWrite(t, agentPath, edited)

	if code, _, errOut := e2eRun(t, "pull", "--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("pull code = %d, stderr = %s", code, errOut)
	}
	got := e2eRead(t, agentPath)
	if got != edited || !strings.Contains(got, "model: openai/gpt-4") {
		t.Fatalf("kept file = %q, want %q", got, edited)
	}
}

// TestModelsDryRunWritesNothing covers V10: a dry-run model change computes but
// never touches the target, state or lock.
func TestModelsDryRunWritesNothing(t *testing.T) {
	_, target, stateDir := installAgent(t, map[string]string{"agents/a.md": "# a\n"})
	agentPath := filepath.Join(target, "agents", "a.md")
	before := e2eRead(t, agentPath)

	code, _, errOut := e2eRun(t, "models", "set", "--dry-run", "--offline",
		"--target", target, "--state", stateDir, "agents/a.md", "openai/gpt-4")
	if code != 0 {
		t.Fatalf("dry-run code = %d, stderr = %s", code, errOut)
	}
	if after := e2eRead(t, agentPath); after != before {
		t.Fatalf("dry-run wrote the target: %q", after)
	}
	if st := e2eState(t, stateDir); len(st.ModelPrefs) != 0 {
		t.Fatalf("dry-run stored a preference: %+v", st.ModelPrefs)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.lock")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a lock (err=%v)", err)
	}
}

// TestSyncDryRunCreatesNoStateDir covers V10 for the sync path.
func TestSyncDryRunCreatesNoStateDir(t *testing.T) {
	remote, _ := e2eRemote(t, map[string]string{"AGENTS.md": "# root\n"})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "fresh-state")

	if code, _, errOut := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--dry-run", "--yes"); code != 0 {
		t.Fatalf("dry-run code = %d, stderr = %s", code, errOut)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created the state dir (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(target, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote the target (err=%v)", err)
	}
}

// TestTargetMismatchRefused covers V3: state bound to one destination refuses a
// different one.
func TestTargetMismatchRefused(t *testing.T) {
	_, _, stateDir := installAgent(t, map[string]string{"AGENTS.md": "# root\n"})
	other := filepath.Join(t.TempDir(), "other")
	code, _, errOut := e2eRun(t, "status", "--target", other, "--state", stateDir)
	if code == 0 {
		t.Fatal("status should refuse a mismatched target")
	}
	if !strings.Contains(errOut, "belongs to target") {
		t.Fatalf("stderr = %q", errOut)
	}
}

// TestModelsCatalogVerification covers V2: a reachable catalog refuses unknown
// references; --offline and an unreachable catalog store them unverified.
func TestModelsCatalogVerification(t *testing.T) {
	srv := catalogServer(t)
	_, target, stateDir := installAgent(t, map[string]string{"agents/a.md": "# a\n"})

	code, _, errOut := e2eRun(t, "models", "set", "--catalog", srv.URL,
		"--target", target, "--state", stateDir, "agents/a.md", "openai/notreal")
	if code != 2 || !strings.Contains(errOut, "not in the model catalog") {
		t.Fatalf("unknown ref: code=%d stderr=%q", code, errOut)
	}

	code, _, errOut = e2eRun(t, "models", "set", "--offline",
		"--target", target, "--state", stateDir, "agents/a.md", "openai/notreal")
	if code != 0 {
		t.Fatalf("offline code = %d, stderr = %q", code, errOut)
	}

	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := closed.URL
	closed.Close()
	code, _, errOut = e2eRun(t, "models", "set", "--catalog", url,
		"--target", target, "--state", stateDir, "agents/a.md", "openai/other")
	if code != 0 || !strings.Contains(errOut, "unverified") {
		t.Fatalf("unreachable catalog: code=%d stderr=%q", code, errOut)
	}
}

// TestModelsSelectInteractive covers V12: the interactive flow prompts for the
// agent, provider, model and optional variant and applies the choice.
func TestModelsSelectInteractive(t *testing.T) {
	srv := catalogServer(t)
	_, target, stateDir := installAgent(t, map[string]string{"agents/a.md": "# a\n"})

	code, out, errOut := e2eRunIn(t, "agents/a.md\nopenai\ngpt-4\n\n",
		"models", "select", "--catalog", srv.URL, "--target", target, "--state", stateDir)
	if code != 0 {
		t.Fatalf("select code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "agents", "a.md")); !strings.Contains(got, "model: openai/gpt-4") {
		t.Fatalf("selected file = %q", got)
	}
}

// TestSyncConfirmDeclinedAborts covers V12: without --yes and with no stdin the
// destructive run aborts before writing the target.
func TestSyncConfirmDeclinedAborts(t *testing.T) {
	remote, _ := e2eRemote(t, map[string]string{"AGENTS.md": "# root\n"})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")

	code, out, errOut := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "aborted") {
		t.Fatalf("stdout = %q, want aborted", out)
	}
	if _, err := os.Stat(filepath.Join(target, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("target written after abort")
	}
	if _, err := os.Stat(state.Path(stateDir)); !os.IsNotExist(err) {
		t.Fatal("state written after abort")
	}
}

// TestRecoverKeepsModelPreferences covers V4: recovery rolls back the files but
// preserves the stored preferences.
func TestRecoverKeepsModelPreferences(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	agentPath := filepath.Join(target, "agents", "a.md")
	e2eWrite(t, agentPath, "new\n")

	backupRel := filepath.Join("backups", "tx", "000_agents__a.md")
	e2eWrite(t, filepath.Join(stateDir, backupRel), "old\n")

	st := state.New("", "")
	st.Files = map[string]state.FileState{"agents/a.md": {Exists: true, Hash: state.Hash([]byte("old\n"))}}
	st.ModelPrefs = map[string]modelpref.Pref{"agents/a.md": {Mode: modelpref.ModeExplicit, Model: "openai/gpt-4"}}
	st.Journal = &state.Journal{ID: "tx", Ops: []state.JournalOp{
		{Path: "agents/a.md", Action: "write", HadOld: true, Backup: backupRel, NewHash: state.Hash([]byte("new\n"))},
	}}
	if err := st.Save(stateDir); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := e2eRun(t, "recover", "--target", target, "--state", stateDir); code != 0 {
		t.Fatalf("recover code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, agentPath); got != "old\n" {
		t.Fatalf("restored file = %q", got)
	}
	saved := e2eState(t, stateDir)
	if saved.Journal != nil {
		t.Fatal("journal should be cleared")
	}
	if pref, ok := saved.ModelPrefs["agents/a.md"]; !ok || pref.Model != "openai/gpt-4" {
		t.Fatalf("preference lost across recovery: %+v (present=%v)", pref, ok)
	}
}

// TestOnlyManifestScopeManaged covers V11: files outside the manifest are never
// read, written or recorded, and are rejected by the models command.
func TestOnlyManifestScopeManaged(t *testing.T) {
	remote, _ := e2eRemote(t, map[string]string{
		"AGENTS.md":     "# root\n",
		"agents/a.md":   "a\n",
		"opencode.json": `{"secret":"upstream"}`,
		"skills/s.md":   "skill\n",
	})
	target := filepath.Join(t.TempDir(), "target")
	stateDir := filepath.Join(t.TempDir(), "state")
	e2eWrite(t, filepath.Join(target, "opencode.json"), `{"local":true}`)

	if code, _, errOut := e2eRun(t, "install", "--repo", remote, "--branch", "main",
		"--target", target, "--state", stateDir, "--yes"); code != 0 {
		t.Fatalf("install code = %d, stderr = %s", code, errOut)
	}
	if got := e2eRead(t, filepath.Join(target, "opencode.json")); got != `{"local":true}` {
		t.Fatalf("out-of-scope config was modified: %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "skills", "s.md")); !os.IsNotExist(err) {
		t.Fatal("out-of-scope skill was written")
	}
	for p := range e2eState(t, stateDir).Files {
		if p != "AGENTS.md" && p != "agents/a.md" {
			t.Fatalf("state manages out-of-scope path %q", p)
		}
	}
	if code, _, _ := e2eRun(t, "models", "set", "--offline", "--target", target, "--state", stateDir,
		"skills/s.md", "openai/gpt-4"); code == 0 {
		t.Fatal("models set accepted an out-of-scope path")
	}
}
