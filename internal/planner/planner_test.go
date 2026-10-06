package planner_test

import (
	"testing"

	"agent-sync/internal/manifest"
	"agent-sync/internal/planner"
	"agent-sync/internal/state"
)

func h(s string) string { return state.Hash([]byte(s)) }

func one(t *testing.T, baseline map[string]state.FileState, local map[string][]byte, remote map[string]planner.Remote, replace map[string]bool) planner.Entry {
	t.Helper()
	plan, err := planner.Build(manifest.Default(), baseline, local, remote, planner.Options{Replace: replace})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(plan.Entries))
	}
	return plan.Entries[0]
}

func base(hs string) map[string]state.FileState {
	return map[string]state.FileState{"AGENTS.md": {Exists: true, Hash: hs}}
}

func TestApplyRemoteWhenLocalUnchanged(t *testing.T) {
	e := one(t, base(h("b")), map[string][]byte{"AGENTS.md": []byte("b")},
		map[string]planner.Remote{"AGENTS.md": {Exists: true, Hash: h("r"), Data: []byte("r")}}, nil)
	if e.Kind != planner.KindWrite || string(e.Content) != "r" {
		t.Fatalf("got %v content %q, want write r", e.Kind, e.Content)
	}
}

func TestKeepLocalWhenRemoteUnchanged(t *testing.T) {
	e := one(t, base(h("b")), map[string][]byte{"AGENTS.md": []byte("l")},
		map[string]planner.Remote{"AGENTS.md": {Exists: true, Hash: h("b"), Data: []byte("b")}}, nil)
	if e.Kind != planner.KindKeep {
		t.Fatalf("got %v, want keep", e.Kind)
	}
}

func TestConvergenceKeeps(t *testing.T) {
	e := one(t, base(h("b")), map[string][]byte{"AGENTS.md": []byte("x")},
		map[string]planner.Remote{"AGENTS.md": {Exists: true, Hash: h("x"), Data: []byte("x")}}, nil)
	if e.Kind != planner.KindKeep {
		t.Fatalf("got %v, want keep", e.Kind)
	}
}

func TestConflictAborts(t *testing.T) {
	e := one(t, base(h("b")), map[string][]byte{"AGENTS.md": []byte("l")},
		map[string]planner.Remote{"AGENTS.md": {Exists: true, Hash: h("r"), Data: []byte("r")}}, nil)
	if e.Kind != planner.KindConflict {
		t.Fatalf("got %v, want conflict", e.Kind)
	}
}

func TestManagedDelete(t *testing.T) {
	e := one(t, base(h("b")), map[string][]byte{"AGENTS.md": []byte("b")}, nil, nil)
	if e.Kind != planner.KindDelete {
		t.Fatalf("got %v, want delete", e.Kind)
	}
}

func TestAdoptIdenticalUnmanaged(t *testing.T) {
	e := one(t, nil, map[string][]byte{"AGENTS.md": []byte("x")},
		map[string]planner.Remote{"AGENTS.md": {Exists: true, Hash: h("x"), Data: []byte("x")}}, nil)
	if e.Kind != planner.KindAdopt {
		t.Fatalf("got %v, want adopt", e.Kind)
	}
}

func TestUnmanagedDifferBlocks(t *testing.T) {
	e := one(t, nil, map[string][]byte{"AGENTS.md": []byte("x")},
		map[string]planner.Remote{"AGENTS.md": {Exists: true, Hash: h("y"), Data: []byte("y")}}, nil)
	if e.Kind != planner.KindBlocked {
		t.Fatalf("got %v, want blocked", e.Kind)
	}
}

func TestUnmanagedDifferWithReplaceWrites(t *testing.T) {
	e := one(t, nil, map[string][]byte{"AGENTS.md": []byte("x")},
		map[string]planner.Remote{"AGENTS.md": {Exists: true, Hash: h("y"), Data: []byte("y")}},
		map[string]bool{"AGENTS.md": true})
	if e.Kind != planner.KindWrite || string(e.Content) != "y" {
		t.Fatalf("got %v %q, want write y", e.Kind, e.Content)
	}
}

func TestNewRemoteFileWrites(t *testing.T) {
	e := one(t, nil, nil,
		map[string]planner.Remote{"AGENTS.md": {Exists: true, Hash: h("y"), Data: []byte("y")}}, nil)
	if e.Kind != planner.KindWrite {
		t.Fatalf("got %v, want write", e.Kind)
	}
}

func TestForeignLocalOnlyIgnored(t *testing.T) {
	e := one(t, nil, map[string][]byte{"AGENTS.md": []byte("x")}, nil, nil)
	if e.Kind != planner.KindIgnore {
		t.Fatalf("got %v, want ignore", e.Kind)
	}
}

func TestOutOfScopeNeverTouched(t *testing.T) {
	plan, err := planner.Build(manifest.Default(), nil,
		map[string][]byte{"README.md": []byte("x"), "opencode.json": []byte("{}")},
		map[string]planner.Remote{"skills/s.md": {Exists: true, Hash: h("z"), Data: []byte("z")}}, planner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 0 {
		t.Fatalf("entries = %d, want 0 (out-of-scope paths must be ignored)", len(plan.Entries))
	}
}

func TestCaseCollisionErrors(t *testing.T) {
	_, err := planner.Build(manifest.Default(), nil, nil, map[string]planner.Remote{
		"agents/A.md": {Exists: true, Hash: h("a"), Data: []byte("a")},
		"agents/a.md": {Exists: true, Hash: h("b"), Data: []byte("b")},
	}, planner.Options{})
	if err == nil {
		t.Fatal("expected case-insensitive collision error")
	}
}
