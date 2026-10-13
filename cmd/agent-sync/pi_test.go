package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-sync/internal/piexport"
	"agent-sync/internal/state"
	"agent-sync/internal/testsupport"
)

func piAgent(body string) string {
	return "---\ndescription: test agent\nmode: subagent\nmodel: provider/model#high\nsteps: 4\npermissions:\n  - action: \"*\"\n    resource: \"*\"\n    effect: deny\n  - action: read\n    resource: \"*\"\n    effect: allow\n---\n" + body
}

func piOptions(t *testing.T, scope, target, base string) *options {
	t.Helper()
	o := &options{pi: true, target: target, stateDir: base}
	if e := resolvePiOptions(o, scope); e != nil {
		t.Fatal(e)
	}
	return o
}

func TestPiScopesSyncConflictAndManualOnly(t *testing.T) {
	for _, scope := range []string{"global", "project"} {
		t.Run(scope, func(t *testing.T) {
			root := t.TempDir()
			base := filepath.Join(root, "state-base")
			target := filepath.Join(root, "destination")
			o := piOptions(t, scope, target, base)
			remote, commit := e2eRemote(t, map[string]string{
				"agents/a.md":            piAgent("upstream one\n"),
				"agents/orchestrator.md": strings.Replace(piAgent("primary prompt\n"), "mode: subagent", "mode: primary", 1),
				"AGENTS.md":              "source root must not be copied\n", "opencode.json": "must not be copied\n",
			})
			configs := []string{"AGENTS.md", "settings.json", "web-search.json", "opencode.json", "extensions/pi-permission-system/config.json"}
			for _, p := range configs {
				e2eWrite(t, filepath.Join(o.target, p), "foreign "+p+"\n")
			}
			foreign := filepath.Join(o.target, "agents", "foreign.md")
			e2eWrite(t, foreign, "foreign agent\n")
			originalState := filepath.Join(base, "state.json")
			e2eWrite(t, originalState, "OpenCode state sentinel\n")
			common := []string{"--scope", scope, "--target", target, "--state", base}
			args := append([]string{"pi", "install"}, common...)
			args = append(args, "--repo", remote, "--branch", "main", "--yes")
			if c, out, e := e2eRun(t, args...); c != 0 || !strings.Contains(out, "INHERIT") {
				t.Fatalf("install %d: %s %s", c, out, e)
			}
			st := e2eState(t, o.stateDir)
			if st.Commit != commit || st.Target != o.target {
				t.Fatalf("state identity: %+v", st)
			}
			if len(st.ModelPrefs) != 0 {
				t.Fatal("Pi wrote model preferences")
			}
			p := filepath.Join(o.target, "agents", "a.md")
			installed := e2eRead(t, p)
			if strings.Contains(installed, "\nmodel:") || !strings.Contains(installed, "max_turns: 4") {
				t.Fatal("unconverted source")
			}
			if st.Files["agents/a.md"].Hash != state.Hash([]byte(installed)) {
				t.Fatal("baseline is not converted bytes")
			}
			if _, e := os.Stat(filepath.Join(o.target, "agents", "orchestrator.md")); !os.IsNotExist(e) {
				t.Fatal("primary is loadable")
			}
			if got := e2eRead(t, filepath.Join(o.target, "agent-sync", "primary", "orchestrator.md")); got != "primary prompt\n" {
				t.Fatal("primary not separate")
			}
			if c, out, e := e2eRun(t, append([]string{"pi", "status"}, common...)...); c != 0 || strings.Contains(out, "  write") {
				t.Fatalf("status idempotence %d %s %s", c, out, e)
			}
			if c, out, e := e2eRun(t, append(append([]string{"pi", "pull"}, common...), "--yes")...); c != 0 || !strings.Contains(out, "applied 0 change(s)") {
				t.Fatalf("pull idempotence %d %s %s", c, out, e)
			}
			if got := e2eRead(t, p); got != installed {
				t.Fatal("idempotent pull changed bytes")
			}
			local := installed + "local body edit\n"
			e2eWrite(t, p, local)
			if c, _, e := e2eRun(t, append(append([]string{"pi", "pull"}, common...), "--yes")...); c != 0 {
				t.Fatalf("keep local %d %s", c, e)
			}
			if e2eRead(t, p) != local || e2eState(t, o.stateDir).Files["agents/a.md"].Hash != state.Hash([]byte(installed)) {
				t.Fatal("local edit lost or accepted as baseline")
			}
			testsupport.Commit(t, remote, map[string]string{"agents/a.md": piAgent("remote two\n"), "agents/orchestrator.md": strings.Replace(piAgent("changed primary\n"), "mode: subagent", "mode: primary", 1)}, nil, "update")
			if c, _, e := e2eRun(t, append([]string{"pi", "status"}, common...)...); c != 0 {
				t.Fatalf("status fetches? %d %s", c, e)
			}
			if c, _, e := e2eRun(t, append(append([]string{"pi", "pull"}, common...), "--yes")...); c != 2 || !strings.Contains(e, "conflict") {
				t.Fatalf("conflict %d %s", c, e)
			}
			if e2eRead(t, p) != local || e2eRead(t, filepath.Join(o.target, "agent-sync", "primary", "orchestrator.md")) != "primary prompt\n" {
				t.Fatal("conflict wrote target")
			}
			for _, p := range configs {
				if e2eRead(t, filepath.Join(o.target, p)) != "foreign "+p+"\n" {
					t.Fatalf("live/foreign config changed %s", p)
				}
			}
			if e2eRead(t, foreign) != "foreign agent\n" || e2eRead(t, originalState) != "OpenCode state sentinel\n" {
				t.Fatal("unrelated ownership changed")
			}
			t.Logf("%s: commit %s; isolated state %s; install/status/pull/idempotence/local/conflict/manual-only verified", scope, commit, o.stateDir)
		})
	}
}

func TestPiRejectBeforeTargetWritesAndDryRun(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	base := filepath.Join(root, "state")
	o := piOptions(t, "project", target, base)
	bad := strings.Replace(piAgent("body\n"), "action: read", "action: unknown_mcp_tool", 1)
	remote, _ := e2eRemote(t, map[string]string{"agents/good.md": piAgent("good\n"), "agents/bad.md": bad})
	common := []string{"--scope", "project", "--target", target, "--state", base, "--repo", remote, "--yes"}
	if c, _, e := e2eRun(t, append([]string{"pi", "install"}, common...)...); c != 1 || !strings.Contains(e, "no evidence-backed") {
		t.Fatalf("unknown mapping %d %s", c, e)
	}
	if _, e := os.Stat(o.target); !os.IsNotExist(e) {
		t.Fatal("partial export wrote target")
	}
	if _, e := os.Stat(state.Path(o.stateDir)); !os.IsNotExist(e) {
		t.Fatal("unsafe conversion wrote baseline")
	}
	testsupport.Commit(t, remote, map[string]string{"agents/bad.md": piAgent("fixed\n")}, nil, "fix fixture")
	if c, _, e := e2eRun(t, append(append([]string{"pi", "install"}, common...), "--dry-run")...); c != 0 {
		t.Fatalf("dry run %d %s", c, e)
	}
	if _, e := os.Stat(o.target); !os.IsNotExist(e) {
		t.Fatal("dry-run target write")
	}
	if _, e := os.Stat(state.Path(o.stateDir)); !os.IsNotExist(e) {
		t.Fatal("dry-run baseline write")
	}
	// Unrepresentable fallback order must also abort ALL agent exports.
	bad = strings.Replace(piAgent("body\n"), "  - action: \"*\"\n    resource: \"*\"\n    effect: deny\n  - action: read\n    resource: \"*\"\n    effect: allow", "  - action: read\n    resource: \"*\"\n    effect: allow\n  - action: \"*\"\n    resource: \"*\"\n    effect: deny", 1)
	testsupport.Commit(t, remote, map[string]string{"agents/bad.md": bad}, nil, "unsafe order")
	if c, _, e := e2eRun(t, append([]string{"pi", "install"}, common...)...); c != 1 || !strings.Contains(e, "precedence") {
		t.Fatalf("unsafe order %d %s", c, e)
	}
	if _, e := os.Stat(o.target); !os.IsNotExist(e) {
		t.Fatal("unsafe ordering wrote target")
	}
	bad = strings.Replace(piAgent("body\n"), "  - action: read\n    resource: \"*\"\n    effect: allow", "  - action: shell\n    resource: \"git push *:*\"\n    effect: ask", 1)
	testsupport.Commit(t, remote, map[string]string{"agents/bad.md": bad}, nil, "pinned YAML incompatibility")
	if c, _, e := e2eRun(t, append([]string{"pi", "install"}, common...)...); c != 1 || !strings.Contains(e, "first colon") {
		t.Fatalf("unsafe YAML %d %s", c, e)
	}
	if _, e := os.Stat(o.target); !os.IsNotExist(e) {
		t.Fatal("unsupported YAML wrote a partial export")
	}
}

func TestPiCollisionBackupAndRecovery(t *testing.T) {
	for _, scope := range []string{"global", "project"} {
		t.Run(scope, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target")
			base := filepath.Join(root, "state")
			o := piOptions(t, scope, target, base)
			remote, _ := e2eRemote(t, map[string]string{"agents/a.md": piAgent("body\n")})
			p := filepath.Join(o.target, "agents", "a.md")
			e2eWrite(t, p, "unmanaged\n")
			common := []string{"--scope", scope, "--target", target, "--state", base}
			install := append(append([]string{"pi", "install"}, common...), "--repo", remote, "--yes")
			if c, _, e := e2eRun(t, install...); c != 2 {
				t.Fatalf("collision %d %s", c, e)
			}
			if e2eRead(t, p) != "unmanaged\n" {
				t.Fatal("collision overwritten")
			}
			if c, _, e := e2eRun(t, append(install, "--replace", "agents/a.md")...); c != 0 {
				t.Fatalf("replace %d %s", c, e)
			}
			if !e2eBackupContains(t, filepath.Join(o.stateDir, "backups"), "unmanaged\n") {
				t.Fatal("collision backup missing")
			}
			old := e2eRead(t, p)
			newData := "transaction partial write\n"
			e2eWrite(t, p, newData)
			backup := "backups/fixture/old.md"
			e2eWrite(t, filepath.Join(o.stateDir, backup), old)
			st := e2eState(t, o.stateDir)
			st.Journal = &state.Journal{ID: "fixture", Ops: []state.JournalOp{{Path: "agents/a.md", Action: "write", HadOld: true, Backup: backup, NewHash: state.Hash([]byte(newData))}}}
			if e := st.Save(o.stateDir); e != nil {
				t.Fatal(e)
			}
			if c, out, e := e2eRun(t, append([]string{"pi", "status"}, common...)...); c != 0 || !strings.Contains(out, "pending transaction") {
				t.Fatalf("pending status %d %s %s", c, out, e)
			}
			if c, _, e := e2eRun(t, append(append([]string{"pi", "pull"}, common...), "--yes")...); c != 2 {
				t.Fatalf("pending pull %d %s", c, e)
			}
			if c, _, e := e2eRun(t, append([]string{"pi", "recover"}, common...)...); c != 0 {
				t.Fatalf("recover %d %s", c, e)
			}
			if e2eRead(t, p) != old || e2eState(t, o.stateDir).Journal != nil {
				t.Fatal("recovery did not restore/clear")
			}
			// Recovery must leave an edit made after the failed transaction untouched.
			st.Journal = &state.Journal{ID: "fixture", Ops: []state.JournalOp{{Path: "agents/a.md", Action: "write", HadOld: true, Backup: backup, NewHash: state.Hash([]byte(newData))}}}
			if e := st.Save(o.stateDir); e != nil {
				t.Fatal(e)
			}
			e2eWrite(t, p, "post-failure user edit\n")
			if c, _, e := e2eRun(t, append([]string{"pi", "recover"}, common...)...); c != 0 {
				t.Fatalf("recover edited %d %s", c, e)
			}
			if e2eRead(t, p) != "post-failure user edit\n" {
				t.Fatal("recovery clobbered later edit")
			}
			// Even a journal cannot expand ownership to live configuration.
			config := filepath.Join(o.target, "web-search.json")
			e2eWrite(t, config, "live\n")
			st.Journal = &state.Journal{ID: "invalid", Ops: []state.JournalOp{{Path: "web-search.json", Action: "write", NewHash: state.Hash([]byte("live\n"))}}}
			if e := st.Save(o.stateDir); e != nil {
				t.Fatal(e)
			}
			if c, _, e := e2eRun(t, append([]string{"pi", "recover"}, common...)...); c != 2 {
				t.Fatalf("out-of-scope journal %d %s", c, e)
			}
			if e2eRead(t, config) != "live\n" {
				t.Fatal("recovery changed live config")
			}
		})
	}
}

func TestPiScopeOptionsAndIsolation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "global-env"))
	g := piOptions(t, "global", "", filepath.Join(root, "state"))
	p := piOptions(t, "project", root, filepath.Join(root, "state"))
	if g.target != filepath.Join(root, "global-env") || p.target != filepath.Join(root, ".pi") || g.stateDir == p.stateDir {
		t.Fatal("scope/env/state resolution")
	}
	other := piOptions(t, "project", filepath.Join(root, "other"), filepath.Join(root, "state"))
	if other.stateDir == p.stateDir {
		t.Fatal("target states collide")
	}
	for _, args := range [][]string{{"pi", "install"}, {"pi", "status", "--scope", "local"}, {"pi", "models", "--scope", "global"}, {"pi", "install", "--scope", "global", "--add-to-path"}, {"pi", "status", "--scope", "global", "extra"}} {
		if c, _, _ := e2eRun(t, args...); c != 2 {
			t.Fatalf("invalid args accepted %v (%d)", args, c)
		}
	}
	// A substituted target identity must not be applied or recovered.
	st := state.New("", "")
	st.Target = other.target
	if e := st.Save(p.stateDir); e != nil {
		t.Fatal(e)
	}
	if c, _, e := e2eRun(t, "pi", "recover", "--scope", "project", "--target", root, "--state", filepath.Join(root, "state")); c != 2 {
		t.Fatalf("target identity %d %s", c, e)
	}
}

func TestPiParserDeclarationFullSnapshotAndPinnedState(t *testing.T) {
	paths, err := filepath.Glob("../../agents/*.md")
	if err != nil || len(paths) != 17 { t.Fatalf("actual source snapshot: %d files, %v", len(paths), err) }
	files := map[string]string{}
	for _, p := range paths { b, err := os.ReadFile(p); if err != nil { t.Fatal(err) }; files["agents/"+filepath.Base(p)] = string(b) }
	remote, commit := e2eRemote(t, files)
	for _, scope := range []string{"global", "project"} { t.Run(scope, func(t *testing.T) {
		root := t.TempDir(); target := filepath.Join(root,"target"); base := filepath.Join(root,"state")
		o := piOptions(t,scope,target,base)
		configs := []string{"AGENTS.md", "settings.json", "web-search.json", "opencode.json", "extensions/pi-permission-system/config.json"}
		for _, p := range configs { e2eWrite(t, filepath.Join(o.target,p), "sentinel "+p) }
		common := []string{"--scope",scope,"--target",target,"--state",base}
		install := append(append([]string{"pi","install"},common...),"--repo",remote,"--yes")
		if c,_,e := e2eRun(t,install...); c != 1 || !strings.Contains(e,"first colon") { t.Fatalf("upstream must reject actual snapshot: %d %s",c,e) }
		if _,e := os.Stat(filepath.Join(o.target,"agents")); !os.IsNotExist(e) { t.Fatal("upstream wrote partial agents") }
		if _,e := os.Stat(state.Path(o.stateDir)); !os.IsNotExist(e) { t.Fatal("upstream wrote baseline") }
		patched := append(append([]string{},common...),"--permission-parser",string(piexport.ParserPatched40V1))
		if c,out,e := e2eRun(t,append(install,"--permission-parser",string(piexport.ParserPatched40V1))...); c != 0 || !strings.Contains(out,"DECLARATION ONLY") { t.Fatalf("patched declaration %d %s %s",c,out,e) }
		st := e2eState(t,o.stateDir)
		if st.PermissionParser != string(piexport.ParserPatched40V1) || st.Commit != commit { t.Fatalf("mode identity not stored: %+v",st) }
		loadable,e := filepath.Glob(filepath.Join(o.target,"agents","*.md")); if e != nil || len(loadable) != 16 { t.Fatalf("loadable %d %v",len(loadable),e) }
		if _,e := os.Stat(filepath.Join(o.target,"agents","orchestrator.md")); !os.IsNotExist(e) { t.Fatal("loadable primary") }
		if e2eRead(t,filepath.Join(o.target,"agent-sync","primary","orchestrator.md")) == "" { t.Fatal("primary missing") }
		gitAgent := e2eRead(t,filepath.Join(o.target,"agents","git-manager.md"))
		for _,key := range []string{"git push *:*", "git add -- :*", "git add -- * :*", "git add :*", "git add * :*"} { if !strings.Contains(gitAgent,"\""+key+"\":") { t.Fatalf("key lost %q",key) } }
		if !strings.Contains(e2eRead(t,filepath.Join(o.target,"agent-sync","report.md")),"compatibility: patched-40.2.0-v1") { t.Fatal("baseline report mode missing") }
		stateBefore := e2eRead(t,state.Path(o.stateDir))
		for _,command := range []string{"install","pull","status","recover"} {
			if c,_,e := e2eRun(t,append([]string{"pi",command},common...)...); c != 2 || !strings.Contains(e,"compatibility mismatch") { t.Fatalf("implicit mixed mode %s %d %s",command,c,e) }
		}
		if e2eRead(t,state.Path(o.stateDir)) != stateBefore || e2eRead(t,filepath.Join(o.target,"agents","git-manager.md")) != gitAgent { t.Fatal("mode mismatch wrote state/target") }
		if c,out,e := e2eRun(t,append(append([]string{"pi","pull"},patched...),"--yes")...); c != 0 || !strings.Contains(out,"applied 0 change(s)") { t.Fatalf("patched idempotence %d %s %s",c,out,e) }
		old := gitAgent; written := "partial transaction\n"; backup := "backups/parser-fixture/old.md"
		e2eWrite(t,filepath.Join(o.target,"agents","git-manager.md"),written); e2eWrite(t,filepath.Join(o.stateDir,backup),old)
		st.Journal = &state.Journal{ID:"parser-fixture",Ops:[]state.JournalOp{{Path:"agents/git-manager.md",Action:"write",HadOld:true,Backup:backup,NewHash:state.Hash([]byte(written))}}}
		if e := st.Save(o.stateDir); e != nil { t.Fatal(e) }
		if c,_,e := e2eRun(t,append([]string{"pi","recover"},common...)...); c != 2 || !strings.Contains(e,"compatibility mismatch") { t.Fatalf("mixed-mode recovery %d %s",c,e) }
		if c,_,e := e2eRun(t,append([]string{"pi","recover"},patched...)...); c != 0 { t.Fatalf("correct-mode recovery %d %s",c,e) }
		if e2eRead(t,filepath.Join(o.target,"agents","git-manager.md")) != old || e2eState(t,o.stateDir).PermissionParser != string(piexport.ParserPatched40V1) { t.Fatal("recovery lost mode/restoration") }
		for _,p := range configs { if e2eRead(t,filepath.Join(o.target,p)) != "sentinel "+p { t.Fatalf("config changed %s",p) } }
	}) }
}

func TestPiParserLegacyStateAndHelp(t *testing.T) {
	root := t.TempDir(); base := filepath.Join(root,"state"); o := piOptions(t,"project",root,base)
	st := state.New("",""); st.Target = o.target // Legacy state has no parser field.
	if err := st.Save(o.stateDir); err != nil { t.Fatal(err) }
	if c,_,e := e2eRun(t,"pi","status","--scope","project","--target",root,"--state",base); c != 0 { t.Fatalf("legacy upstream %d %s",c,e) }
	before := e2eRead(t,state.Path(o.stateDir))
	if c,_,e := e2eRun(t,"pi","install","--scope","project","--target",root,"--state",base,"--permission-parser","patched-40.2.0-v1"); c != 2 || !strings.Contains(e,"compatibility mismatch") { t.Fatalf("legacy auto migration %d %s",c,e) }
	if e2eRead(t,state.Path(o.stateDir)) != before { t.Fatal("legacy mismatch rewrote state") }
	for _, args := range [][]string{{"pi","help"},{"pi","install","--help"}} {
		if c,out,e := e2eRun(t,args...); c != 0 || !strings.Contains(out,"runtime-verify") || !strings.Contains(out,"default upstream") { t.Fatalf("declaration help %d %s %s",c,out,e) }
	}
	if c,_,_ := e2eRun(t,"pi","status","--scope","project","--permission-parser","unknown"); c != 2 { t.Fatal("unknown parser selected") }
}
