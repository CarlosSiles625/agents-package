package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agent-sync/internal/manifest"
	"agent-sync/internal/piexport"
	"agent-sync/internal/state"
)

func syncManifest(o *options) manifest.Manifest {
	if o.pi {
		return piexport.Manifest()
	}
	return manifest.Default()
}

func printPiNotice(w io.Writer, o *options) {
	fmt.Fprintln(w, "Pi models: INHERIT the active Pi model (model/variant omitted)")
	fmt.Fprintf(w, "Pi target: %s; isolated state: %s\n", o.target, o.stateDir)
	fmt.Fprintln(w, "MANUAL artifacts only: agent-sync/report.md, primary prompts, root policies and global web profile. No live config or extension installation.")
	fmt.Fprintf(w, "permission parser compatibility: %s\n", o.permissionParser)
	if o.permissionParser == piexport.ParserPatched40V1 {
		fmt.Fprintln(w, "DECLARATION ONLY: user applied and SHA-256 verified patched-40.2.0-v1; agent-sync does not runtime-verify or install the patch.")
	}
}

func piUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-sync pi install|pull|status|recover --scope global|project [--target DIR] [--repo PATH|URL] [--branch NAME] [--state BASE] [--dry-run] [--yes] [--replace PATH] [--allow-delete]")
	fmt.Fprintln(w, "  --permission-parser upstream|patched-40.2.0-v1 (default upstream)")
	fmt.Fprintln(w, "Patched selection DECLARES that you applied and SHA-256 verified the pinned patch; this CLI does not install or runtime-verify it. Repeat the same selection for pull/status/recover; state rejects mixed modes.")
}

func runPi(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		piUsage(stdout)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	name := args[0]
	if name != "install" && name != "pull" && name != "status" && name != "recover" {
		fmt.Fprintf(stderr, "unknown Pi command %q\n", name)
		return 2
	}
	o := &options{pi: true}
	fs := newFlags("pi "+name, o)
	var scope string
	fs.StringVar(&scope, "scope", "", "required: global or project")
	parser := string(piexport.ParserUpstream)
	fs.StringVar(&parser, "permission-parser", parser, "upstream or patched-40.2.0-v1; patched declares user-applied, SHA-verified patch, not runtime verification")
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp { piUsage(stdout); return 0 }
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "error: unexpected positional arguments")
		return 2
	}
	o.permissionParser = piexport.ParserMode(parser)
	if err := resolvePiOptions(o, scope); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if err := checkCritical(o); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	st, _, err := state.Load(o.stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := checkPiCompatibility(o, st); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	switch name {
	case "status":
		return runStatusOptions(ctx, o, stdout, stderr)
	case "recover":
		return runRecoverOptions(ctx, o, stdout, stderr)
	default:
		return runSyncOptions(ctx, name, o, stdin, stdout, stderr)
	}
}

// Do not namespace modes into separate locks for the same physical target.
// Pin compatibility in the existing state and reject an implicit mode switch.
func checkPiCompatibility(o *options, st *state.State) error {
	if err := checkPiState(st); err != nil { return err }
	selected, err := piexport.NormalizeParserMode(o.permissionParser)
	if err != nil { return err }
	stored, err := piexport.NormalizeParserMode(piexport.ParserMode(st.PermissionParser))
	if err != nil { return fmt.Errorf("invalid Pi parser compatibility in state: %w", err) }
	populated := st.PermissionParser != "" || len(st.Files) != 0 || st.Journal != nil || st.Commit != "" || st.Target != "" || st.Repo != "" || st.Branch != ""
	if populated && selected != stored {
		return fmt.Errorf("Pi parser compatibility mismatch: state is %s, selected %s; repeat --permission-parser %s; mode migration is not automatic", stored, selected, stored)
	}
	return nil
}

func checkPiState(st *state.State) error {
	if len(st.ModelPrefs) != 0 {
		return fmt.Errorf("Pi state must not contain OpenCode model preferences")
	}
	m := piexport.Manifest()
	for p := range st.Files {
		if !m.Match(p) {
			return fmt.Errorf("non-Pi path %q in Pi state", p)
		}
	}
	if st.Journal != nil {
		for _, op := range st.Journal.Ops {
			if !m.Match(op.Path) {
				return fmt.Errorf("non-Pi journal path %q; refusing recovery outside the Pi manifest", op.Path)
			}
		}
	}
	return nil
}

// --target is a project root for project scope, a Pi agent directory for global
// scope. Namespace state by scope AND normalized effective destination, even
// when --state supplies a custom base: OpenCode state cannot be reused by Pi.
func resolvePiOptions(o *options, scope string) error {
	mode, err := piexport.NormalizeParserMode(o.permissionParser)
	if err != nil { return err }
	o.permissionParser = mode
	if scope != "global" && scope != "project" {
		return fmt.Errorf("--scope must be global or project")
	}
	if scope == "project" {
		if o.target == "" {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			o.target = wd
		}
		o.target = filepath.Join(o.target, ".pi")
	} else if o.target == "" {
		o.target = os.Getenv("PI_CODING_AGENT_DIR")
		if o.target == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			o.target = filepath.Join(home, ".pi", "agent")
		}
		if o.target == "~" || strings.HasPrefix(o.target, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			o.target = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(o.target, "~"), "/"))
		}
	}
	o.target = state.NormalizeTarget(o.target)
	if o.stateDir == "" {
		o.stateDir = defaultState()
	}
	o.stateDir = filepath.Join(o.stateDir, "pi", scope+"-"+state.Hash([]byte(o.target))[:16])
	return nil
}
