// Package source reads a git repository into a private, bare cache directory
// that lives outside the destination, then reads files from an immutable
// commit rather than from a mutable working tree.
//
// All git invocations pass arguments directly to the git binary (never through
// a shell) and credential material embedded in the repository URL is redacted
// from every error string. agent-sync only fetches: it never pushes.
package source

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// Source is a configured remote repository and branch.
type Source struct {
	Repo   string
	Branch string
	Dir    string // bare cache directory
}

// New builds a Source. dir is the bare cache directory.
func New(repo, branch, dir string) *Source {
	return &Source{Repo: repo, Branch: branch, Dir: dir}
}

// Redact removes userinfo (credentials) from a repository URL.
func Redact(repo string) string {
	if repo == "" {
		return repo
	}
	if u, err := url.Parse(repo); err == nil && u.User != nil {
		u.User = nil
		return u.String()
	}
	return repo
}

// ValidateRepoBranch rejects repository/branch values that could be
// interpreted as command-line options or that are not valid git refs, so they
// can be passed to git safely.
func ValidateRepoBranch(repo, branch string) error {
	if repo == "" {
		return fmt.Errorf("repository is empty")
	}
	if strings.HasPrefix(repo, "-") {
		return fmt.Errorf("repository must not start with %q", "-")
	}
	if err := checkText(repo); err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	if branch == "" {
		return fmt.Errorf("branch is empty")
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("branch must not start with %q", "-")
	}
	if err := checkText(branch); err != nil {
		return fmt.Errorf("branch: %w", err)
	}
	if strings.Contains(branch, "..") {
		return fmt.Errorf("branch must not contain %q", "..")
	}
	if strings.ContainsAny(branch, "~^:?*[\\ ") {
		return fmt.Errorf("branch contains an invalid character")
	}
	if strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") {
		return fmt.Errorf("branch has an invalid leading/trailing character")
	}
	if strings.Contains(branch, "@{") {
		return fmt.Errorf("branch must not contain %q", "@{")
	}
	return nil
}

func checkText(s string) error {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("value contains a control character")
		}
	}
	return nil
}

func (s *Source) scrub(msg string) string {
	if s.Repo == "" {
		return msg
	}
	return strings.ReplaceAll(msg, s.Repo, Redact(s.Repo))
}

func (s *Source) scrubArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = s.scrub(a)
	}
	return strings.Join(out, " ")
}

func (s *Source) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("git %s: %w: %s", s.scrubArgs(args), err, s.scrub(strings.TrimSpace(stderr.String())))
	}
	return stdout.Bytes(), nil
}

func (s *Source) init(ctx context.Context) error {
	if _, err := os.Stat(s.Dir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(s.Dir, 0o755); err != nil {
			return err
		}
		if _, err := s.git(ctx, "", "init", "--bare", s.Dir); err != nil {
			return err
		}
	}
	cur, err := s.git(ctx, s.Dir, "remote", "get-url", "origin")
	if err != nil {
		if _, err := s.git(ctx, s.Dir, "remote", "add", "origin", s.Repo); err != nil {
			return err
		}
		return nil
	}
	if strings.TrimSpace(string(cur)) != s.Repo {
		if _, err := s.git(ctx, s.Dir, "remote", "set-url", "origin", s.Repo); err != nil {
			return err
		}
	}
	return nil
}

func (s *Source) ref() string { return "refs/remotes/origin/" + s.Branch }

// Fetch updates the cache and returns the resolved commit for the branch.
func (s *Source) Fetch(ctx context.Context) (string, error) {
	if err := s.init(ctx); err != nil {
		return "", err
	}
	refspec := "+refs/heads/" + s.Branch + ":" + s.ref()
	if _, err := s.git(ctx, s.Dir, "fetch", "--no-tags", "origin", refspec); err != nil {
		return "", err
	}
	return s.LastCommit(ctx)
}

// LastCommit returns the last fetched commit for the branch without touching
// the network. It errors when no commit is known yet.
func (s *Source) LastCommit(ctx context.Context) (string, error) {
	out, err := s.git(ctx, s.Dir, "rev-parse", "--verify", "--quiet", s.ref())
	if err != nil {
		return "", fmt.Errorf("no known commit for branch %q: %w", s.Branch, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ListFiles lists all files in the commit tree.
func (s *Source) ListFiles(ctx context.Context, commit string) ([]string, error) {
	out, err := s.git(ctx, s.Dir, "ls-tree", "-r", "--name-only", commit)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// ReadFile returns the content of rel at commit. ok is false when the path is
// absent in that commit.
func (s *Source) ReadFile(ctx context.Context, commit, rel string) (data []byte, ok bool, err error) {
	out, err := s.git(ctx, s.Dir, "cat-file", "-p", commit+":"+rel)
	if err != nil {
		return nil, false, nil
	}
	return out, true, nil
}
