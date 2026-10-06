// Command agent-sync installs and updates the global OpenCode agent
// instructions (AGENTS.md and agents/**/*.md) from a git repository into the
// destination directory, keeping a recoverable baseline and never touching
// files it does not own.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agent-sync/internal/apply"
	"agent-sync/internal/manifest"
	"agent-sync/internal/pathsafe"
	"agent-sync/internal/planner"
	"agent-sync/internal/source"
	"agent-sync/internal/state"
	"agent-sync/internal/targetfs"
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
	case "install", "pull":
		return runSync(ctx, args[0], args[1:], stdin, stdout, stderr)
	case "status":
		return runStatus(ctx, args[1:], stdout, stderr)
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
  agent-sync recover [flags]   roll back a pending transaction

Flags:
  --repo <path|url>   git repository (required unless already recorded)
  --branch <name>     git branch (default "main")
  --target <dir>      destination directory
  --state <dir>       state directory
  --dry-run           preview only; never writes target or state
  --yes               assume yes for changes (not deletions)
  --allow-delete      permit deletions
  --replace <path>    adopt a differing unmanaged file (repeatable)
`)
}

type options struct {
	repo        string
	branch      string
	target      string
	stateDir    string
	dryRun      bool
	yes         bool
	allowDelete bool
	replace     multiFlag
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
	fs := newFlags(name, o)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
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

	m := manifest.Default()
	plan, err := buildPlan(ctx, o, m, st, src, commit)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	printPlan(stdout, plan)

	if cs := plan.OfKind(planner.KindConflict); len(cs) > 0 {
		fmt.Fprintf(stderr, "error: %d conflict(s); aborting before writing anything\n", len(cs))
		return 2
	}
	if bs := plan.OfKind(planner.KindBlocked); len(bs) > 0 {
		fmt.Fprintf(stderr, "error: %d unmanaged file(s) differ; re-run with --replace <path> to overwrite them with a backup\n", len(bs))
		return 2
	}
	if o.dryRun {
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
	if err := apply.Apply(ctx, o.target, o.stateDir, st, plan); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "done: applied %d change(s) at commit %s\n", len(plan.Executable()), commit)
	return 0
}

func buildPlan(ctx context.Context, o *options, m manifest.Manifest, st *state.State, src *source.Source, commit string) (planner.Plan, error) {
	remote, err := buildRemote(ctx, src, commit, m)
	if err != nil {
		return planner.Plan{}, err
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
	plan, err := planner.Build(m, st.Files, local, remote, planner.Options{Replace: replace})
	if err != nil {
		return planner.Plan{}, err
	}
	plan.Commit = commit
	return plan, nil
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
	m := manifest.Default()
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
