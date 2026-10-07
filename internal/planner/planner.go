// Package planner computes the three-way merge between the baseline (B), the
// local destination (L) and the remote commit (R) for every managed path, and
// returns an ordered plan. It never writes anything.
//
// Rules:
//   - L == B  -> apply remote (write R, or delete when R is absent)
//   - R == B  -> keep local
//   - L == R  -> converged (keep)
//   - otherwise (all three differ, or both L and R diverged from B) -> conflict,
//     and the caller must abort the whole run before writing.
//
// For paths with no baseline (a fresh install, or state lost/corrupt) nothing
// is assumed to be owned: identical local/remote files are adopted, local-only
// files are left untouched, and differing files block unless the path is
// explicitly named with --replace (which backs up the local file first).
package planner

import (
	"fmt"
	"sort"

	"agent-sync/internal/manifest"
	"agent-sync/internal/modelpref"
	"agent-sync/internal/pathsafe"
	"agent-sync/internal/state"
)

// Kind is the planned action for one path.
type Kind string

const (
	KindWrite    Kind = "write"
	KindDelete   Kind = "delete"
	KindAdopt    Kind = "adopt"
	KindKeep     Kind = "keep"
	KindIgnore   Kind = "ignore"
	KindConflict Kind = "conflict"
	KindBlocked  Kind = "blocked"
)

// Val is a presence/hash pair for baseline, local or remote.
type Val struct {
	Exists bool
	Hash   string
}

func (v Val) equal(o Val) bool { return v.Exists == o.Exists && v.Hash == o.Hash }

// Remote is a remote file at the planned commit.
type Remote struct {
	Exists bool
	Hash   string
	Data   []byte
}

// Entry is one planned path.
type Entry struct {
	Path     string
	Kind     Kind
	Baseline Val
	Local    Val
	Remote   Val
	Content  []byte // bytes to write (remote raw, or rendered when a preference applies)

	// RawHash is the upstream-raw baseline hash to record for a write. It is
	// set when the written Content differs from the raw upstream bytes because
	// of a model preference; when empty the baseline is Hash(Content).
	RawHash string

	// Pref, when non-nil, is the model preference to store for this path.
	// ClearPref removes any stored preference instead.
	Pref      *modelpref.Pref
	ClearPref bool
}

// Options configures planning.
type Options struct {
	Replace map[string]bool // explicit --replace paths
}

// Plan is the ordered set of planned entries plus the target commit.
type Plan struct {
	Commit  string
	Entries []Entry
}

// OfKind returns the entries of a given kind.
func (p Plan) OfKind(k Kind) []Entry {
	var out []Entry
	for _, e := range p.Entries {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

// Executable returns the entries that modify the destination.
func (p Plan) Executable() []Entry {
	var out []Entry
	for _, e := range p.Entries {
		if e.Kind == KindWrite || e.Kind == KindDelete {
			out = append(out, e)
		}
	}
	return out
}

// Deletes returns the deletion entries.
func (p Plan) Deletes() []Entry { return p.OfKind(KindDelete) }

// Build computes the plan.
func Build(m manifest.Manifest, baseline map[string]state.FileState, local map[string][]byte, remote map[string]Remote, opts Options) (Plan, error) {
	if err := m.Validate(); err != nil {
		return Plan{}, err
	}

	set := make(map[string]struct{}, len(baseline)+len(local)+len(remote))
	for k := range baseline {
		set[k] = struct{}{}
	}
	for k := range local {
		set[k] = struct{}{}
	}
	for k := range remote {
		set[k] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if a, b, ok := pathsafe.CaseCollisions(keys); ok {
		return Plan{}, fmt.Errorf("case-insensitive path collision between %q and %q", a, b)
	}

	var plan Plan
	for _, p := range keys {
		if err := pathsafe.ValidateRel(p); err != nil {
			return Plan{}, err
		}
		if !m.Match(p) {
			continue // outside the declared scope; never touch
		}

		bfs, inBaseline := baseline[p]
		var b Val
		if inBaseline {
			b = Val{Exists: bfs.Exists, Hash: bfs.Hash}
		}
		var l Val
		if data, ok := local[p]; ok {
			l = Val{Exists: true, Hash: state.Hash(data)}
		}
		var r Val
		var content []byte
		if rm, ok := remote[p]; ok {
			r = Val{Exists: true, Hash: rm.Hash}
			content = rm.Data
		}

		e := Entry{Path: p, Baseline: b, Local: l, Remote: r}
		if inBaseline {
			switch {
			case l.equal(b):
				if r.Exists {
					e.Kind = KindWrite
				} else {
					e.Kind = KindDelete
				}
			case r.equal(b):
				e.Kind = KindKeep
			case l.equal(r):
				e.Kind = KindKeep
			default:
				e.Kind = KindConflict
			}
		} else {
			switch {
			case l.Exists && r.Exists:
				switch {
				case l.equal(r):
					e.Kind = KindAdopt
				case opts.Replace[p]:
					e.Kind = KindWrite
				default:
					e.Kind = KindBlocked
				}
			case !l.Exists && r.Exists:
				e.Kind = KindWrite
			default:
				e.Kind = KindIgnore
			}
		}
		if e.Kind == KindWrite {
			e.Content = content
		}
		plan.Entries = append(plan.Entries, e)
	}
	return plan, nil
}
