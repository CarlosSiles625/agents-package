// Command agent-sync installs and updates the global OpenCode agent
// instructions (AGENTS.md, agents/**/*.md and skills/<id>/SKILL.md) from a git repository into the
// destination directory, keeping a recoverable baseline and never touching
// files it does not own.
package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"agent-sync/internal/apply"
	"agent-sync/internal/manifest"
	"agent-sync/internal/modelpref"
	"agent-sync/internal/pathsafe"
	"agent-sync/internal/piexport"
	"agent-sync/internal/planner"
	"agent-sync/internal/source"
	"agent-sync/internal/state"
	"agent-sync/internal/targetfs"
	"agent-sync/internal/userpath"
)

// addToUserPath and stdinIsTerminal are seams replaced by tests so that no test
// ever touches the real registry, shell profiles or console.
var (
	addToUserPath   = userpath.Add
	stdinIsTerminal = userpath.StdinIsTerminal
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	ctx := context.Background()
	switch args[0] {
	case "pi":
		return runPi(ctx, args[1:], stdin, stdout, stderr)
	case "install", "pull":
		return runSync(ctx, args[0], args[1:], stdin, stdout, stderr)
	case "status":
		return runStatus(ctx, args[1:], stdout, stderr)
	case "models":
		return runModels(ctx, args[1:], stdin, stdout, stderr)
	case "recover":
		return runRecover(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `agent-sync - sync global OpenCode agent instructions from git

Usage:
  agent-sync install [flags]   fetch and apply (first-time friendly)
  agent-sync pull    [flags]   fetch and apply
  agent-sync status  [flags]   show state and the pending plan (no fetch)
  agent-sync models  <sub>     list/select/set/inherit/reset/status model choices
  agent-sync recover [flags]   roll back a pending transaction
  agent-sync pi install|pull|status|recover --scope global|project [flags]

Flags:
  --repo <path|url>   git repository (required unless already recorded)
  --branch <name>     git branch (default "main")
  --target <dir>      destination directory
  --state <dir>       state directory
  --dry-run           preview only; never writes target or state
  --yes               assume yes for changes (not deletions)
  --allow-delete      permit deletions
  --replace <path>    adopt a differing unmanaged file (repeatable)

Install-only flags:
  --add-to-path       add the executable's directory to the user PATH (no prompt)
  --shell bash|zsh    shell to configure the user PATH (Linux only; default: $SHELL)
`)
}

type options struct {
	pi          bool
	permissionParser piexport.ParserMode
	repo        string
	branch      string
	target      string
	stateDir    string
	dryRun      bool
	yes         bool
	allowDelete bool
	replace     multiFlag
	addToPath   bool
	shell       string
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func newFlags(name string, o *options) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.repo, "repo", "", "git repository path or URL")
	fs.StringVar(&o.branch, "branch", "", "git branch")
	fs.StringVar(&o.target, "target", "", "destination directory")
	fs.StringVar(&o.stateDir, "state", "", "state directory")
	fs.BoolVar(&o.dryRun, "dry-run", false, "preview without writing")
	fs.BoolVar(&o.yes, "yes", false, "assume yes for non-destructive changes")
	fs.BoolVar(&o.allowDelete, "allow-delete", false, "allow deletions")
	fs.Var(&o.replace, "replace", "adopt a differing unmanaged file (repeatable)")
	return fs
}

// newInstallFlags adds the PATH flags that are valid only for "install"; other
// commands keep the base flag set so such flags are rejected instead of being
// silently ignored.
func newInstallFlags(o *options) *flag.FlagSet {
	fs := newFlags("install", o)
	fs.BoolVar(&o.addToPath, "add-to-path", false, "add the executable's directory to the user PATH")
	fs.StringVar(&o.shell, "shell", "", "shell to configure the user PATH (bash or zsh; Linux only)")
	return fs
}

// validateInstallFlags prevalidates install-only flags before any agent file is
// written.
func validateInstallFlags(o *options) error {
	if o.shell == "" {
		return nil
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("--shell is only valid on Linux")
	}
	if _, err := userpath.ParseShell(o.shell); err != nil {
		return err
	}
	return nil
}

func defaultTarget() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config/opencode"
	}
	return filepath.Join(home, ".config", "opencode")
}

func defaultState() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".local/state/agent-sync"
	}
	return filepath.Join(home, ".local", "state", "agent-sync")
}

func isRemoteRepo(repo string) bool {
	return strings.Contains(repo, "://") || (strings.Contains(repo, "@") && strings.Contains(repo, ":"))
}

func runSync(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	o := &options{}
	var fs *flag.FlagSet
	if name == "install" {
		fs = newInstallFlags(o)
	} else {
		fs = newFlags(name, o)
	}
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if name == "install" {
		if err := validateInstallFlags(o); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
	}
	return runSyncOptions(ctx, name, o, stdin, stdout, stderr)
}

func runSyncOptions(ctx context.Context, name string, o *options, stdin io.Reader, stdout, stderr io.Writer) int {
	if o.stateDir == "" {
		o.stateDir = defaultState()
	}
	if o.target == "" {
		o.target = defaultTarget()
	}

	st, exists, err := state.Load(o.stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if st.Journal != nil {
		fmt.Fprintln(stderr, "error:", apply.ErrJournalPending)
		return 2
	}
	if o.repo == "" {
		o.repo = st.Repo
	}
	if o.branch == "" {
		o.branch = st.Branch
	}
	if o.branch == "" {
		o.branch = "main"
	}
	if o.repo == "" {
		fmt.Fprintln(stderr, "error: no repository configured; pass --repo")
		return 2
	}
	if err := source.ValidateRepoBranch(o.repo, o.branch); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if err := checkCritical(o); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if !exists {
		fmt.Fprintln(stderr, "note: no existing state; treating as a fresh install")
	}
	if !o.dryRun {
		lock, err := state.Acquire(o.stateDir)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		defer lock.Release()
		// Reload under the lock so a concurrent writer is never clobbered.
		st, exists, err = state.Load(o.stateDir)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if st.Journal != nil {
			fmt.Fprintln(stderr, "error:", apply.ErrJournalPending)
			return 2
		}
	}
	if err := checkTargetIdentity(st, o.target); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if o.pi {
		if err := checkPiCompatibility(o, st); err != nil { fmt.Fprintln(stderr, "error:", err); return 2 }
	}

	repoDir := filepath.Join(o.stateDir, "repo")
	if o.dryRun {
		tmp, err := os.MkdirTemp("", "agent-sync-dryrun-")
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		defer os.RemoveAll(tmp)
		repoDir = filepath.Join(tmp, "repo")
	}

	src := source.New(o.repo, o.branch, repoDir)
	commit, err := src.Fetch(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	m := syncManifest(o)
	plan, err := buildPlan(ctx, o, m, st, src, commit)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	printPlan(stdout, plan)
	if o.pi {
		printPiNotice(stdout, o)
	}

	if cs := plan.OfKind(planner.KindConflict); len(cs) > 0 {
		fmt.Fprintf(stderr, "error: %d conflict(s); aborting before writing anything\n", len(cs))
		return 2
	}
	if bs := plan.OfKind(planner.KindBlocked); len(bs) > 0 {
		fmt.Fprintf(stderr, "error: %d unmanaged file(s) differ; re-run with --replace <path> to overwrite them with a backup\n", len(bs))
		return 2
	}
	if o.dryRun {
		if name == "install" && !o.pi {
			return addPathAfterInstall(o, stdin, stdout, stderr)
		}
		return 0
	}
	if ds := plan.Deletes(); len(ds) > 0 && !o.allowDelete {
		fmt.Fprintf(stderr, "error: %d deletion(s) planned; pass --allow-delete to proceed\n", len(ds))
		return 2
	}
	if !o.yes {
		if !confirm(stdin, stdout) {
			fmt.Fprintln(stdout, "aborted")
			return 0
		}
	}

	st.Repo = source.Redact(o.repo)
	st.Branch = o.branch
	if o.pi { st.PermissionParser = string(o.permissionParser) }
	if err := apply.Apply(ctx, o.target, o.stateDir, st, plan); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "done: applied %d change(s) at commit %s\n", len(plan.Executable()), commit)
	if name == "install" && !o.pi {
		// The PATH step runs only after the agent files were applied cleanly.
		// A failure here leaves the verified agent install in place and exits
		// with a distinct code so the caller can tell the two apart.
		if code := addPathAfterInstall(o, stdin, stdout, stderr); code != 0 {
			return code
		}
	}
	return 0
}

// pathBackupDir is the private, user-owned directory that receives raw backups
// of any PATH/profile bytes agent-sync changes. It is named explicitly so the
// location is documented and the original value/type can be restored by hand.
func pathBackupDir(stateDir string) string {
	return filepath.Join(stateDir, "path-backups")
}

// addPathAfterInstall performs the optional PATH installation for "install".
// It is called only after a successful agent apply (never for pull/status), and
// a dry-run never writes, backs up or reads the registry through the non-dry
// path. An explicit --add-to-path authorizes the write without a prompt; without
// it the action is only offered on a real terminal and is a silent success
// otherwise. --yes never consents to the PATH change.
func addPathAfterInstall(o *options, stdin io.Reader, stdout, stderr io.Writer) int {
	explicit := o.addToPath
	interactive := stdinIsTerminal()

	if o.dryRun {
		if !explicit {
			return 0
		}
		dir, err := userpath.ExecutableDir()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		rep, err := addToUserPath(dir, userpath.Options{Shell: o.shell, DryRun: true})
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		printPathReport(stdout, rep)
		return 0
	}

	if !explicit && !interactive {
		// No terminal to ask and no explicit opt-in: leave PATH untouched and
		// do not read the registry or any profile.
		fmt.Fprintln(stdout, "note: PATH unchanged; pass --add-to-path to add the executable's directory")
		return 0
	}

	dir, err := userpath.ExecutableDir()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	rep, aerr := addToUserPath(dir, userpath.Options{
		Shell:       o.shell,
		AssumeYes:   explicit,
		Interactive: interactive,
		Stdin:       stdin,
		Stdout:      stdout,
		BackupDir:   pathBackupDir(o.stateDir),
	})
	if aerr != nil {
		if explicit {
			fmt.Fprintln(stderr, "error: adding the executable's directory to PATH failed:", aerr)
			fmt.Fprintln(stderr, "note: the agent files were installed; PATH is unchanged")
			return 1
		}
		fmt.Fprintln(stderr, "warning: adding the executable's directory to PATH failed:", aerr)
		return 0
	}
	printPathReport(stdout, rep)
	return 0
}

// printPathReport prints only agent-sync's own generated lines and the file
// names it changes. It never echoes the surrounding profile or registry
// content, which may contain unrelated entries or secrets.
func printPathReport(w io.Writer, rep userpath.Report) {
	if rep.Dir != "" {
		fmt.Fprintf(w, "PATH directory: %s\n", rep.Dir)
	}
	for _, pv := range rep.Previews {
		switch {
		case pv.Already:
			fmt.Fprintf(w, "  already present in %s\n", pv.Target)
		case pv.Create:
			fmt.Fprintf(w, "  create %s\n", pv.Target)
		default:
			fmt.Fprintf(w, "  update %s\n", pv.Target)
		}
		for _, l := range pv.Added {
			fmt.Fprintf(w, "    + %s\n", l)
		}
		for _, l := range pv.Removed {
			fmt.Fprintf(w, "    - %s\n", l)
		}
	}
	for _, b := range rep.Backups {
		fmt.Fprintf(w, "  backup: %s\n", b)
	}
	if rep.Skipped {
		fmt.Fprintln(w, "  skipped")
	}
	if rep.Note != "" {
		fmt.Fprintln(w, rep.Note)
	}
}

func buildPlan(ctx context.Context, o *options, m manifest.Manifest, st *state.State, src *source.Source, commit string) (planner.Plan, error) {
	sourceManifest := m
	if o.pi {
		sourceManifest = manifest.Manifest{Version: manifest.CurrentVersion, Include: []string{"agents/**/*.md"}}
	}
	remote, err := buildRemote(ctx, src, commit, sourceManifest)
	if err != nil {
		return planner.Plan{}, err
	}
	if o.pi {
		if err := checkPiCompatibility(o, st); err != nil {
			return planner.Plan{}, err
		}
		files := map[string][]byte{}
		for p, r := range remote {
			files[p] = r.Data
		}
		converted, err := piexport.ConvertWithParser(files, o.permissionParser)
		if err != nil {
			return planner.Plan{}, err
		}
		remote = map[string]planner.Remote{}
		for p, data := range converted {
			remote[p] = planner.Remote{Exists: true, Hash: state.Hash(data), Data: data}
		}
	}
	local, err := buildLocal(o.target, m)
	if err != nil {
		return planner.Plan{}, err
	}
	replace := map[string]bool{}
	for _, p := range o.replace {
		if err := pathsafe.ValidateRel(p); err != nil {
			return planner.Plan{}, err
		}
		replace[p] = true
	}
	plan, err := planner.Build(m, st.Files, mergeLocal(local, st), remote, planner.Options{Replace: replace})
	if err != nil {
		return planner.Plan{}, err
	}
	if !o.pi {
		if err := applyPrefs(st, &plan, remote, local); err != nil {
			return planner.Plan{}, err
		}
	} else {
		// Avoid backing up/replacing identical converted files on every pull.
		// The common planner's historical OpenCode behavior is unchanged.
		for i := range plan.Entries {
			e := &plan.Entries[i]
			if e.Kind == planner.KindWrite && e.Local == e.Remote {
				e.Kind = planner.KindKeep
				e.Content = nil
			}
		}
	}
	plan.Commit = commit
	return plan, nil
}

// mergeLocal returns the local bytes the three-way merge should compare. For a
// path with a stored model preference the edit agent-sync injected (or removed)
// is reversed, so the merge runs on edit-free bytes and never mistakes its own
// model field for a user change. The caller keeps the actual bytes for
// revalidation and rendering.
func mergeLocal(local map[string][]byte, st *state.State) map[string][]byte {
	if len(st.ModelPrefs) == 0 {
		return local
	}
	out := make(map[string][]byte, len(local))
	for k, v := range local {
		out[k] = v
	}
	for p, pref := range st.ModelPrefs {
		data, ok := local[p]
		if !ok {
			continue
		}
		raw, err := modelpref.Undo(data, pref.Receipt)
		if err != nil {
			// Fail closed: the model field no longer matches the recorded edit.
			// mergeLocal cannot return an error, so mark the path with bytes that
			// can never equal a real hash and force a conflict instead.
			out[p] = nil
			continue
		}
		out[p] = raw
	}
	return out
}

// applyPrefs renders or clears the stored model preference on the plan. It also
// keeps Apply's revalidation hash pointing at the real (rendered) local file
// rather than the edit-free bytes used for the merge decision.
func applyPrefs(st *state.State, plan *planner.Plan, remote map[string]planner.Remote, local map[string][]byte) error {
	for i := range plan.Entries {
		e := &plan.Entries[i]
		pref, has := st.ModelPrefs[e.Path]
		if !has {
			continue
		}
		if _, err := modelpref.Undo(local[e.Path], pref.Receipt); err != nil && local[e.Path] != nil {
			return fmt.Errorf("managed agent %q was edited by hand (the model field no longer matches the stored receipt); refusing to sync: %w", e.Path, err)
		}
		if data, ok := local[e.Path]; ok {
			e.Local = planner.Val{Exists: true, Hash: state.Hash(data)}
		} else {
			e.Local = planner.Val{Exists: false}
		}
		switch e.Kind {
		case planner.KindWrite:
			rm, ok := remote[e.Path]
			if !ok || !rm.Exists {
				e.ClearPref = true
				continue
			}
			rendered, rec, err := modelpref.Render(rm.Data, pref)
			if err != nil {
				return fmt.Errorf("rendering model preference for %q: %w", e.Path, err)
			}
			if data, ok := local[e.Path]; ok && bytes.Equal(data, rendered) {
				// The file already holds exactly this rendering; keep it. The
				// receipt from rendering the new remote is valid because the
				// equality proves the actual file is byte-for-byte what that
				// rendering produced, and it must describe the advanced raw
				// baseline. Keeping the previous receipt would undo to the old
				// upstream body/model and cause a false conflict later.
				p := pref
				p.Receipt = rec
				e.Kind = planner.KindKeep
				e.Content = nil
				e.Pref = &p
				continue
			}
			p := pref
			p.Receipt = rec
			e.Content = rendered
			e.RawHash = state.Hash(rm.Data)
			e.Pref = &p
		case planner.KindKeep:
			// Local wins; retain the existing preference, whose receipt already
			// describes the actual file. Never regenerate it from remote.
			p := pref
			e.Pref = &p
			if fs, ok := st.Files[e.Path]; ok {
				e.RawHash = fs.Hash
			}
		case planner.KindDelete:
			// The managed file is removed, but the stored preference is kept as
			// an orphan: if the agent reappears upstream the choice is re-applied
			// instead of being silently forgotten.
		}
	}
	return nil
}

func buildRemote(ctx context.Context, src *source.Source, commit string, m manifest.Manifest) (map[string]planner.Remote, error) {
	files, err := src.ListFiles(ctx, commit)
	if err != nil {
		return nil, err
	}
	out := map[string]planner.Remote{}
	for _, f := range files {
		if !m.Match(f) {
			continue
		}
		if err := pathsafe.ValidateRel(f); err != nil {
			return nil, err
		}
		data, ok, err := src.ReadFile(ctx, commit, f)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out[f] = planner.Remote{Exists: true, Hash: state.Hash(data), Data: data}
	}
	return out, nil
}

func buildLocal(target string, m manifest.Manifest) (map[string][]byte, error) {
	rels, err := targetfs.List(target, m)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, rel := range rels {
		exists, data, err := targetfs.Read(target, rel)
		if err != nil {
			return nil, err
		}
		if exists {
			out[rel] = data
		}
	}
	return out, nil
}

func checkCritical(o *options) error {
	for _, d := range []string{o.stateDir, o.target} {
		if err := pathsafe.CheckNoSymlinkParents(d); err != nil {
			return err
		}
	}
	if !isRemoteRepo(o.repo) && o.repo != "" {
		if err := pathsafe.CheckNoSymlinkParents(o.repo); err != nil {
			return err
		}
	}
	return nil
}

func printPlan(w io.Writer, plan planner.Plan) {
	changes := 0
	for _, e := range plan.Entries {
		if e.Kind == planner.KindIgnore {
			continue
		}
		changes++
		fmt.Fprintf(w, "  %-8s %s\n", e.Kind, e.Path)
	}
	if changes == 0 {
		fmt.Fprintln(w, "  (no changes)")
	}
}

func confirm(stdin io.Reader, stdout io.Writer) bool {
	fmt.Fprint(stdout, "proceed? [y/N] ")
	r := bufio.NewReader(stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func runStatus(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o := &options{}
	fs := newFlags("status", o)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	return runStatusOptions(ctx, o, stdout, stderr)
}

func runStatusOptions(ctx context.Context, o *options, stdout, stderr io.Writer) int {
	if o.stateDir == "" {
		o.stateDir = defaultState()
	}
	if o.target == "" {
		o.target = defaultTarget()
	}
	st, exists, err := state.Load(o.stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := checkTargetIdentity(st, o.target); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	repo := o.repo
	if repo == "" {
		repo = st.Repo
	}
	branch := o.branch
	if branch == "" {
		branch = st.Branch
	}
	if branch == "" {
		branch = "main"
	}
	fmt.Fprintf(stdout, "target:  %s\n", o.target)
	fmt.Fprintf(stdout, "state:   %s\n", o.stateDir)
	fmt.Fprintf(stdout, "repo:    %s\n", source.Redact(repo))
	fmt.Fprintf(stdout, "branch:  %s\n", branch)
	fmt.Fprintf(stdout, "baseline commit: %s\n", st.Commit)
	if o.pi {
		printPiNotice(stdout, o)
	}

	if st.Journal != nil {
		fmt.Fprintf(stdout, "pending transaction: %s (%d op(s)); run 'agent-sync recover'\n", st.Journal.ID, len(st.Journal.Ops))
		return 0
	}
	if !exists {
		fmt.Fprintln(stdout, "(no state yet)")
	}
	if repo == "" {
		return 0
	}
	src := source.New(repo, branch, filepath.Join(o.stateDir, "repo"))
	remoteCommit, err := src.LastCommit(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "last known remote: (none)\n")
		return 0
	}
	fmt.Fprintf(stdout, "last known remote: %s\n", remoteCommit)
	m := syncManifest(o)
	plan, err := buildPlan(ctx, o, m, st, src, remoteCommit)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintln(stdout, "plan (against last known remote, no fetch):")
	printPlan(stdout, plan)
	return 0
}

func runRecover(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o := &options{}
	fs := newFlags("recover", o)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	return runRecoverOptions(ctx, o, stdout, stderr)
}

func runRecoverOptions(ctx context.Context, o *options, stdout, stderr io.Writer) int {
	if o.stateDir == "" {
		o.stateDir = defaultState()
	}
	if o.target == "" {
		o.target = defaultTarget()
	}
	st, _, err := state.Load(o.stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := checkTargetIdentity(st, o.target); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if st.Journal == nil {
		fmt.Fprintln(stdout, "nothing to recover")
		return 0
	}
	lock, err := state.Acquire(o.stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	defer lock.Release()
	st, _, err = state.Load(o.stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if o.pi {
		if err := checkPiCompatibility(o, st); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		if err := checkTargetIdentity(st, o.target); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
	}
	if st.Journal == nil {
		fmt.Fprintln(stdout, "nothing to recover")
		return 0
	}
	if err := apply.Recover(ctx, o.target, o.stateDir, st); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintln(stdout, "recovered: pending transaction rolled back")
	return 0
}
