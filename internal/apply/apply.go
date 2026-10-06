// Package apply executes a plan against the destination with a recoverable
// journal. Before any write it revalidates that the destination still matches
// what was planned, then records intent plus backups, then writes. A failed or
// interrupted run leaves a journal that blocks further operations until an
// explicit recovery restores the backups. Recovery never overwrites changes
// made after the transaction.
package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-sync/internal/planner"
	"agent-sync/internal/state"
	"agent-sync/internal/targetfs"
)

// ErrJournalPending is returned when a transaction must be recovered first.
var ErrJournalPending = errors.New("a pending transaction journal exists; run 'agent-sync recover' first")

// Apply performs the plan's writes and deletes and updates the state baseline.
func Apply(ctx context.Context, target, stateDir string, st *state.State, plan planner.Plan) error {
	if st.Journal != nil {
		return ErrJournalPending
	}
	actions := plan.Executable()

	// Revalidate every path before touching anything.
	for _, e := range actions {
		exists, data, err := targetfs.Read(target, e.Path)
		if err != nil {
			return err
		}
		var hash string
		if exists {
			hash = state.Hash(data)
		}
		if exists != e.Local.Exists || hash != e.Local.Hash {
			return fmt.Errorf("target %q changed since planning; aborting before any write", e.Path)
		}
	}

	if len(actions) == 0 {
		// Nothing to execute, but baseline still advances to the planned commit.
		updateFiles(st, plan)
		st.Commit = plan.Commit
		return st.Save(stateDir)
	}

	// Record intent and backups, then persist the journal before writing.
	id := time.Now().UTC().Format("20060102T150405.000000000Z")
	var ops []state.JournalOp
	for i, e := range actions {
		op := state.JournalOp{Path: e.Path, HadOld: e.Local.Exists}
		switch e.Kind {
		case planner.KindWrite:
			op.Action = "write"
			op.NewHash = state.Hash(e.Content)
		case planner.KindDelete:
			op.Action = "delete"
		}
		if e.Local.Exists {
			rel := filepath.Join("backups", id, fmt.Sprintf("%03d_%s", i, strings.ReplaceAll(e.Path, "/", "__")))
			if err := targetfs.Validate(target, e.Path); err != nil {
				return err
			}
			if err := copyFile(filepath.Join(target, filepath.FromSlash(e.Path)), filepath.Join(stateDir, rel)); err != nil {
				return err
			}
			op.Backup = rel
		}
		ops = append(ops, op)
	}
	st.Journal = &state.Journal{ID: id, Ops: ops}
	if err := st.Save(stateDir); err != nil {
		return err
	}

	// Execute.
	for _, e := range actions {
		switch e.Kind {
		case planner.KindWrite:
			if err := targetfs.WriteAtomic(target, e.Path, e.Content); err != nil {
				return err
			}
		case planner.KindDelete:
			if err := targetfs.Remove(target, e.Path); err != nil {
				return err
			}
		}
	}

	// Commit: update baseline, clear journal.
	updateFiles(st, plan)
	st.Commit = plan.Commit
	st.Journal = nil
	return st.Save(stateDir)
}

func updateFiles(st *state.State, plan planner.Plan) {
	for _, e := range plan.Entries {
		switch e.Kind {
		case planner.KindWrite:
			st.Files[e.Path] = state.FileState{Exists: true, Hash: state.Hash(e.Content)}
		case planner.KindDelete:
			delete(st.Files, e.Path)
		case planner.KindAdopt:
			st.Files[e.Path] = state.FileState{Exists: true, Hash: e.Remote.Hash}
		case planner.KindKeep:
			// The baseline must track the accepted remote commit (including
			// absence), never the local copy. Recording a kept local edit as the
			// baseline would make the next remote change look like a clean
			// update and silently overwrite that local edit.
			if e.Remote.Exists {
				st.Files[e.Path] = state.FileState{Exists: true, Hash: e.Remote.Hash}
			} else {
				delete(st.Files, e.Path)
			}
		}
	}
}

// Recover rolls back a pending journal. For every operation it only restores
// the backup when the destination still holds exactly what the failed run
// wrote (or deleted); anything changed afterwards is left untouched.
func Recover(ctx context.Context, target, stateDir string, st *state.State) error {
	if st.Journal == nil {
		return nil
	}
	for i := len(st.Journal.Ops) - 1; i >= 0; i-- {
		op := st.Journal.Ops[i]
		exists, data, err := targetfs.Read(target, op.Path)
		if err != nil {
			return err
		}
		var hash string
		if exists {
			hash = state.Hash(data)
		}
		switch op.Action {
		case "write":
			// Only undo when the file still matches what we wrote.
			if exists && hash == op.NewHash {
				if op.HadOld {
					if err := restore(stateDir, op.Backup, target, op.Path); err != nil {
						return err
					}
				} else if err := targetfs.Remove(target, op.Path); err != nil {
					return err
				}
			}
		case "delete":
			if !exists && op.HadOld {
				if err := restore(stateDir, op.Backup, target, op.Path); err != nil {
					return err
				}
			}
		}
	}
	st.Journal = nil
	return st.Save(stateDir)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func restore(stateDir, backupRel, target, rel string) error {
	if backupRel == "" {
		return fmt.Errorf("missing backup for %q", rel)
	}
	if err := targetfs.Validate(target, rel); err != nil {
		return err
	}
	dst := filepath.Join(target, filepath.FromSlash(rel))
	data, err := os.ReadFile(filepath.Join(stateDir, backupRel))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
