package source_test

import (
	"context"
	"path/filepath"
	"testing"

	"agent-sync/internal/source"
	"agent-sync/internal/testsupport"
)

func newRemote(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote")
	commit := testsupport.InitRepo(t, dir, map[string]string{
		"AGENTS.md":   "# root\n",
		"agents/a.md": "a\n",
	})
	return dir, commit
}

func TestFetchReadList(t *testing.T) {
	remote, commit := newRemote(t)
	cache := filepath.Join(t.TempDir(), "cache")
	src := source.New(remote, "main", cache)
	ctx := context.Background()

	got, err := src.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != commit {
		t.Fatalf("commit = %s, want %s", got, commit)
	}
	if last, err := src.LastCommit(ctx); err != nil || last != commit {
		t.Fatalf("LastCommit = %q, %v; want %q", last, err, commit)
	}

	files, err := src.ListFiles(ctx, got)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if !contains(files, "AGENTS.md") || !contains(files, "agents/a.md") {
		t.Fatalf("ListFiles = %v", files)
	}

	data, ok, err := src.ReadFile(ctx, got, "AGENTS.md")
	if err != nil || !ok || string(data) != "# root\n" {
		t.Fatalf("ReadFile = %q, %v, %v", data, ok, err)
	}
	if _, ok, _ := src.ReadFile(ctx, got, "agents/missing.md"); ok {
		t.Fatal("expected missing path to be absent")
	}
}

func TestLastCommitWithoutFetch(t *testing.T) {
	remote, _ := newRemote(t)
	cache := filepath.Join(t.TempDir(), "cache")
	src := source.New(remote, "main", cache)
	if _, err := src.LastCommit(context.Background()); err == nil {
		t.Fatal("expected error when no commit is known")
	}
}

func TestNoPush(t *testing.T) {
	remote, commit := newRemote(t)
	cache := filepath.Join(t.TempDir(), "cache")
	src := source.New(remote, "main", cache)
	ctx := context.Background()

	if _, err := src.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := src.ListFiles(ctx, commit); err != nil {
		t.Fatal(err)
	}
	if _, _, err := src.ReadFile(ctx, commit, "AGENTS.md"); err != nil {
		t.Fatal(err)
	}

	after := testsupport.Git(t, remote, "rev-parse", "HEAD")
	if after != commit {
		t.Fatalf("remote HEAD changed from %s to %s: a push occurred", commit, after)
	}
}

func TestValidateRepoBranch(t *testing.T) {
	if err := source.ValidateRepoBranch("https://host/x.git", "main"); err != nil {
		t.Errorf("valid pair rejected: %v", err)
	}
	for _, tc := range []struct{ repo, branch string }{
		{"-oops", "main"},
		{"https://host/x.git", ""},
		{"https://host/x.git", "-branch"},
		{"https://host/x.git", "a..b"},
		{"https://host/x.git", "a b"},
		{"https://host/x.git", "a~b"},
	} {
		if err := source.ValidateRepoBranch(tc.repo, tc.branch); err == nil {
			t.Errorf("ValidateRepoBranch(%q,%q) = nil, want error", tc.repo, tc.branch)
		}
	}
}

func TestRedact(t *testing.T) {
	got := source.Redact("https://user:secret@host/x.git")
	if got != "https://host/x.git" {
		t.Fatalf("Redact = %q", got)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
