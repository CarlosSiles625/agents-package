package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"agent-sync/internal/apply"
	"agent-sync/internal/catalog"
	"agent-sync/internal/manifest"
	"agent-sync/internal/modelpref"
	"agent-sync/internal/pathsafe"
	"agent-sync/internal/planner"
	"agent-sync/internal/state"
	"agent-sync/internal/targetfs"
)

func modelsUsage(w io.Writer) {
	fmt.Fprint(w, `agent-sync models - choose the model used by managed agents

Usage:
  agent-sync models list   [--catalog URL] [--provider ID]   list the public catalog
  agent-sync models status [--target DIR] [--state DIR]      show stored preferences
  agent-sync models set    <agent> <provider/model[#variant]> [--yes] [--dry-run]
  agent-sync models inherit <agent>                           [--yes] [--dry-run]
  agent-sync models reset  <agent>                            [--yes] [--dry-run]
  agent-sync models select [<agent> [<provider/model>]]       pick interactively

Model choices are stored in the agent-sync state and re-applied on every pull, so
the injected model survives upstream updates while the body stays synced. The
reference is verified against the public models.dev catalog unless --offline is
given. Only managed agents (present in the baseline) can be changed.
`)
}

func runModels(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		modelsUsage(stderr)
		return 2
	}
	switch args[0] {
	case "list":
		return runModelsList(ctx, args[1:], stdout, stderr)
	case "status":
		return runModelsStatus(ctx, args[1:], stdout, stderr)
	case "set":
		return runModelsMutate(ctx, "explicit", args[1:], stdout, stderr)
	case "inherit":
		return runModelsMutate(ctx, "inherit", args[1:], stdout, stderr)
	case "reset":
		return runModelsMutate(ctx, "reset", args[1:], stdout, stderr)
	case "select":
		return runModelsSelect(ctx, args[1:], stdin, stdout, stderr)
	case "help", "-h", "--help":
		modelsUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown models subcommand %q\n", args[0])
		modelsUsage(stderr)
		return 2
	}
}

// checkTargetIdentity refuses to operate when the state directory was bound to
// a different destination. Legacy state (no recorded target) is accepted and
// bound on the next write.
func checkTargetIdentity(st *state.State, target string) error {
	if st.Target == "" {
		return nil
	}
	if !state.SamePath(st.Target, state.NormalizeTarget(target)) {
		return fmt.Errorf("state belongs to target %q, not %q", st.Target, target)
	}
	return nil
}

func runModelsList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("models list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	catalogURL := fs.String("catalog", catalog.DefaultURL, "catalog URL")
	provider := fs.String("provider", "", "show only this provider's models")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	c, err := catalog.Fetch(ctx, *catalogURL)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if *provider == "" {
		for _, id := range c.ProviderIDs() {
			fmt.Fprintf(stdout, "%s\t%s\n", id, c.Providers[id].Name)
		}
		return 0
	}
	p, ok := c.Providers[*provider]
	if !ok {
		fmt.Fprintf(stderr, "error: unknown provider %q\n", *provider)
		return 1
	}
	fmt.Fprintf(stdout, "provider %s (%s)\n", p.ID, p.Name)
	for _, id := range c.ModelIDs(*provider) {
		m := p.Models[id]
		name := m.Name
		if name == "" {
			name = id
		}
		fmt.Fprintf(stdout, "  %-40s %-24s %s\n", id, name, catalog.CostString(m.Cost))
	}
	return 0
}

func runModelsStatus(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o := &options{}
	fs := newFlags("models status", o)
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
	if err := checkTargetIdentity(st, o.target); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "target: %s\n", o.target)
	if len(st.ModelPrefs) == 0 {
		fmt.Fprintln(stdout, "no model preferences")
		return 0
	}
	paths := make([]string, 0, len(st.ModelPrefs))
	for p := range st.ModelPrefs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		pref := st.ModelPrefs[p]
		line := fmt.Sprintf("  %s  %s", p, pref.Mode)
		if pref.Model != "" {
			line += " " + pref.Model
		}
		exists, data, rerr := targetfs.Read(o.target, p)
		switch {
		case rerr != nil:
			line += "  (local error: " + rerr.Error() + ")"
		case !exists:
			line += "  (missing locally)"
		default:
			if verr := modelpref.Verify(data, pref.Receipt); verr != nil {
				line += "  (receipt mismatch: " + verr.Error() + ")"
			} else if model, ok, merr := modelpref.ModelOf(data); merr == nil {
				if ok {
					line += "  current=" + model
				} else {
					line += "  current=(no model)"
				}
			}
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}

// resolveAgentPath maps a user-supplied agent name to a managed relative path.
// A bare name ("review") becomes "agents/review.md"; a full relative path
// ("AGENTS.md", "agents/sub/x.md") is used as-is. Anything outside the manifest
// is rejected.
func resolveAgentPath(arg string, m manifest.Manifest) (string, error) {
	if arg == "" {
		return "", errors.New("agent name is empty")
	}
	cand := filepath.ToSlash(arg)
	if !strings.Contains(cand, "/") && !strings.HasSuffix(strings.ToLower(cand), ".md") {
		cand = "agents/" + cand + ".md"
	}
	if err := pathsafe.ValidateRel(cand); err != nil {
		return "", err
	}
	if !m.Match(cand) {
		return "", fmt.Errorf("%q is not a managed agent path", arg)
	}
	return cand, nil
}

func modelMutateFlags(name string) (*options, *string, *bool, *flag.FlagSet) {
	o := &options{}
	fs := newFlags(name, o)
	catalogURL := fs.String("catalog", catalog.DefaultURL, "catalog URL")
	offline := fs.Bool("offline", false, "do not verify against the catalog")
	return o, catalogURL, offline, fs
}

func runModelsMutate(ctx context.Context, action string, args []string, stdout, stderr io.Writer) int {
	o, catalogURL, offline, fs := modelMutateFlags("models " + action)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	pos := fs.Args()
	if len(pos) < 1 {
		fmt.Fprintf(stderr, "error: models %s requires an <agent>\n", action)
		return 2
	}
	agentArg := pos[0]
	ref := ""
	if action == "explicit" {
		if len(pos) < 2 {
			fmt.Fprintln(stderr, "error: models set requires <agent> <provider/model>")
			return 2
		}
		ref = pos[1]
	} else if len(pos) > 1 {
		fmt.Fprintf(stderr, "error: models %s takes only an <agent>\n", action)
		return 2
	}
	return mutateModels(ctx, action, agentArg, ref, o, *catalogURL, *offline, stdout, stderr)
}

// verifyModel checks a reference against the catalog. When the catalog cannot
// be reached the reference is stored unverified; when it is reachable but the
// reference is unknown the operation is refused.
func verifyModel(ctx context.Context, catalogURL string, offline bool, ref string, stderr io.Writer) error {
	if offline || catalogURL == "" {
		return nil
	}
	c, err := catalog.Fetch(ctx, catalogURL)
	if err != nil {
		fmt.Fprintf(stderr, "warning: model catalog unavailable (%v); storing unverified reference\n", err)
		return nil
	}
	if !c.Verifies(ref) {
		return fmt.Errorf("%q is not in the model catalog; pass --offline to store it unverified", ref)
	}
	return nil
}

// computeModelChange derives the bytes to write and the preference to store for
// one agent. It runs on the state as loaded, and reads the current local file.
func computeModelChange(st *state.State, target, rel, action, ref string) (newBytes []byte, pref modelpref.Pref, clear bool, localHash string, err error) {
	exists, local, err := targetfs.Read(target, rel)
	if err != nil {
		return nil, modelpref.Pref{}, false, "", err
	}
	if !exists {
		return nil, modelpref.Pref{}, false, "", fmt.Errorf("agent file %q is missing from the target", rel)
	}
	localHash = state.Hash(local)
	rawForEdit := local
	if prev, has := st.ModelPrefs[rel]; has {
		raw, uerr := modelpref.Undo(local, prev.Receipt)
		if uerr != nil {
			return nil, modelpref.Pref{}, false, "", fmt.Errorf("agent %q was edited by hand (the model field no longer matches the recorded receipt); refusing to overwrite it: %w", rel, uerr)
		}
		rawForEdit = raw
	}
	switch action {
	case "explicit":
		pref = modelpref.Pref{Mode: modelpref.ModeExplicit, Model: ref}
		out, rec, rerr := modelpref.Render(rawForEdit, pref)
		if rerr != nil {
			return nil, pref, false, "", rerr
		}
		pref.Receipt = rec
		return out, pref, false, localHash, nil
	case "inherit":
		pref = modelpref.Pref{Mode: modelpref.ModeInherit}
		out, rec, rerr := modelpref.Render(rawForEdit, pref)
		if rerr != nil {
			return nil, pref, false, "", rerr
		}
		pref.Receipt = rec
		return out, pref, false, localHash, nil
	case "reset":
		if _, has := st.ModelPrefs[rel]; !has {
			return nil, modelpref.Pref{}, false, "", fmt.Errorf("agent %q has no model preference to reset", rel)
		}
		return rawForEdit, modelpref.Pref{}, true, localHash, nil
	default:
		return nil, modelpref.Pref{}, false, "", fmt.Errorf("unknown action %q", action)
	}
}

func mutateModels(ctx context.Context, action, agentArg, ref string, o *options, catalogURL string, offline bool, stdout, stderr io.Writer) int {
	if o.stateDir == "" {
		o.stateDir = defaultState()
	}
	if o.target == "" {
		o.target = defaultTarget()
	}
	m := manifest.Agents()
	rel, err := resolveAgentPath(agentArg, m)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if action == "explicit" {
		if err := modelpref.ValidateRef(ref); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		if err := verifyModel(ctx, catalogURL, offline, ref, stderr); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
	}

	st, exists, err := state.Load(o.stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if !exists {
		fmt.Fprintln(stderr, "error: no state yet; run 'agent-sync install' first")
		return 2
	}
	if st.Journal != nil {
		fmt.Fprintln(stderr, "error:", apply.ErrJournalPending)
		return 2
	}
	if err := checkTargetIdentity(st, o.target); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if fs, ok := st.Files[rel]; !ok || !fs.Exists {
		fmt.Fprintf(stderr, "error: %q is not a managed agent (no matching baseline); only files agent-sync installed can be changed\n", rel)
		return 2
	}

	if o.dryRun {
		// Compute without locking or writing anything.
		newBytes, _, _, _, cerr := computeModelChange(st, o.target, rel, action, ref)
		if cerr != nil {
			fmt.Fprintln(stderr, "error:", cerr)
			return 2
		}
		_ = newBytes
		fmt.Fprintf(stdout, "dry-run: would %s model for %s\n", actionVerb(action), rel)
		return 0
	}

	lock, err := state.Acquire(o.stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	defer lock.Release()

	// Reload and recompute under the lock so a concurrent writer cannot be lost.
	st, exists, err = state.Load(o.stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if !exists || st.Journal != nil {
		fmt.Fprintln(stderr, "error: state changed while preparing; re-run")
		return 1
	}
	if err := checkTargetIdentity(st, o.target); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if fs, ok := st.Files[rel]; !ok || !fs.Exists {
		fmt.Fprintf(stderr, "error: %q is not a managed agent\n", rel)
		return 2
	}

	newBytes, pref, clear, localHash, err := computeModelChange(st, o.target, rel, action, ref)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}

	e := planner.Entry{
		Path:      rel,
		Kind:      planner.KindWrite,
		Local:     planner.Val{Exists: true, Hash: localHash},
		Content:   newBytes,
		RawHash:   st.Files[rel].Hash,
		ClearPref: clear,
	}
	if clear {
		e.Pref = nil
	} else {
		p := pref
		e.Pref = &p
	}
	plan := planner.Plan{Commit: st.Commit, Entries: []planner.Entry{e}}
	if err := apply.Apply(ctx, o.target, o.stateDir, st, plan); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if clear {
		fmt.Fprintf(stdout, "done: reset model for %s\n", rel)
	} else {
		fmt.Fprintf(stdout, "done: %s model for %s\n", actionVerb(action), rel)
	}
	return 0
}

func actionVerb(action string) string {
	switch action {
	case "explicit":
		return "set"
	case "inherit":
		return "inherit"
	case "reset":
		return "reset"
	}
	return action
}

func runModelsSelect(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	o, catalogURL, offline, fs := modelMutateFlags("models select")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	pos := fs.Args()
	if len(pos) >= 2 {
		return mutateModels(ctx, "explicit", pos[0], pos[1], o, *catalogURL, *offline, stdout, stderr)
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
	if !exists {
		fmt.Fprintln(stderr, "error: no state yet; run 'agent-sync install' first")
		return 2
	}
	agents := make([]string, 0, len(st.Files))
	agentScope := manifest.Agents()
	for p, f := range st.Files {
		if f.Exists && agentScope.Match(p) {
			agents = append(agents, p)
		}
	}
	sort.Strings(agents)
	if len(agents) == 0 {
		fmt.Fprintln(stderr, "error: no managed agents to select")
		return 2
	}
	r := bufio.NewReader(stdin)
	var agent string
	if len(pos) == 1 {
		rel, rerr := resolveAgentPath(pos[0], manifest.Agents())
		if rerr != nil {
			fmt.Fprintln(stderr, "error:", rerr)
			return 2
		}
		agent = rel
	} else {
		fmt.Fprintln(stdout, "managed agents:")
		for _, a := range agents {
			fmt.Fprintf(stdout, "  %s\n", a)
		}
		line, err := prompt(r, stdout, stderr, "agent")
		if err != nil {
			return 1
		}
		rel, rerr := resolveAgentPath(line, manifest.Agents())
		if rerr != nil {
			fmt.Fprintln(stderr, "error:", rerr)
			return 2
		}
		agent = rel
	}

	c, err := catalog.Fetch(ctx, *catalogURL)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	line, err := prompt(r, stdout, stderr, "provider (e.g. opencode-go)")
	if err != nil {
		return 1
	}
	provID := strings.TrimSpace(line)
	if _, ok := c.Providers[provID]; !ok {
		fmt.Fprintf(stderr, "error: unknown provider %q\n", provID)
		return 2
	}
	for _, id := range c.ModelIDs(provID) {
		fmt.Fprintf(stdout, "  %s\n", id)
	}
	line, err = prompt(r, stdout, stderr, "model")
	if err != nil {
		return 1
	}
	model := strings.TrimSpace(line)
	if model == "" {
		fmt.Fprintln(stderr, "error: a model id is required")
		return 2
	}
	if _, ok := c.Providers[provID].Models[model]; !ok {
		fmt.Fprintf(stderr, "error: model %q is not in provider %q\n", model, provID)
		return 2
	}
	variant, err := prompt(r, stdout, stderr, "variant (optional)")
	if err != nil {
		return 1
	}
	ref := provID + "/" + model
	if v := strings.TrimSpace(variant); v != "" {
		ref += "#" + v
	}
	return mutateModels(ctx, "explicit", agent, ref, o, *catalogURL, true, stdout, stderr)
}

func prompt(r *bufio.Reader, stdout, stderr io.Writer, label string) (string, error) {
	fmt.Fprintf(stdout, "%s: ", label)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(stderr, "error: no input")
		return "", err
	}
	return strings.TrimSpace(line), nil
}
