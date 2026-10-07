package userpath

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-sync/internal/pathsafe"
)

const (
	markerBegin = "# >>> agent-sync PATH >>>"
	markerEnd   = "# <<< agent-sync PATH <<<"
)

// POSIXQuote returns s as a POSIX shell single-quoted word. The result is
// literal for every character, including spaces, $, backticks, globs, quotes
// and punctuation; an apostrophe is emitted with the standard '\'' sequence.
func POSIXQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pathCasePattern returns a shell case-pattern that matches when a PATH
// component equals dir. dir is single-quoted, so glob metacharacters in it are
// matched literally rather than expanded.
func pathCasePattern(dir string) string {
	return "*:" + POSIXQuote(dir) + ":*"
}

// PathBlock returns the marker-delimited shell snippet that prepends dir to
// PATH only when it is not already a complete component.
func PathBlock(dir string) string {
	return markerBegin + "\n" +
		"case \":${PATH}:\" in\n" +
		"  " + pathCasePattern(dir) + ") ;;\n" +
		"  *) export PATH=" + POSIXQuote(dir) + ":\"${PATH}\" ;;\n" +
		"esac\n" +
		markerEnd + "\n"
}

// validatePOSIXDir rejects directories that cannot be represented safely as a
// single PATH component.
func validatePOSIXDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("executable directory is empty")
	}
	for _, r := range dir {
		switch r {
		case ':', '\n', '\r', 0:
			return fmt.Errorf("executable directory %q contains a character that cannot be used in PATH", dir)
		}
	}
	return nil
}

type fileSnapshot struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// snapshotProfile reads the current state of a profile without following
// symlinks. Missing files are reported as absent; symlinks, non-regular files
// and symlinked ancestors are refused.
func snapshotProfile(path string) (fileSnapshot, error) {
	if err := pathsafe.CheckNoSymlinkParents(path); err != nil {
		return fileSnapshot{}, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileSnapshot{}, nil
		}
		return fileSnapshot{}, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fileSnapshot{}, fmt.Errorf("refusing to edit %q: it is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return fileSnapshot{}, fmt.Errorf("refusing to edit %q: not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{exists: true, data: data, mode: fi.Mode().Perm()}, nil
}

// existingBlock returns the exact text of the current agent-sync block
// (including its trailing newline) if one is present.
func existingBlock(cur []byte) (string, bool) {
	text := string(cur)
	bi := strings.Index(text, markerBegin)
	if bi < 0 {
		return "", false
	}
	ei := strings.Index(text[bi:], markerEnd)
	if ei < 0 {
		return "", false
	}
	end := bi + ei + len(markerEnd)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return text[bi:end], true
}

// renderProfile returns the new profile bytes with the block inserted or
// replaced. Malformed or duplicated agent-sync markers fail closed; an already
// identical block is a no-op.
func renderProfile(cur []byte, block string) ([]byte, bool, error) {
	text := string(cur)
	bCount := strings.Count(text, markerBegin)
	eCount := strings.Count(text, markerEnd)
	if bCount != eCount || bCount > 1 {
		return nil, false, fmt.Errorf("malformed agent-sync PATH markers (begin=%d end=%d)", bCount, eCount)
	}
	if bCount == 0 {
		sep := ""
		if len(cur) > 0 && cur[len(cur)-1] != '\n' {
			sep = "\n"
		}
		return []byte(text + sep + block), true, nil
	}
	old, ok := existingBlock(cur)
	if !ok {
		return nil, false, fmt.Errorf("malformed agent-sync PATH markers")
	}
	if old == block {
		return cur, false, nil
	}
	bi := strings.Index(text, markerBegin)
	return []byte(text[:bi] + block + text[bi+len(old):]), true, nil
}

// checkUnchanged verifies the target still matches the snapshot taken before
// planning. Any drift aborts before a write.
func checkUnchanged(path string, snap fileSnapshot) error {
	fi, err := os.Lstat(path)
	if !snap.exists {
		if err == nil {
			return fmt.Errorf("%s appeared while planning; refusing to overwrite it", path)
		}
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s disappeared while planning; aborting", path)
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s changed type while planning; aborting", path)
	}
	if fi.Mode().Perm() != snap.mode {
		return fmt.Errorf("%s permissions changed while planning; aborting", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if hashBytes(data) != hashBytes(snap.data) {
		return fmt.Errorf("%s changed while planning; aborting", path)
	}
	return nil
}

// writeProfileAtomic writes data through a same-directory temporary file and an
// atomic rename, preserving the original mode (0644 for a new file). It
// re-checks content and mode immediately before the rename; an inherent race
// remains between that check and the rename (documented).
func writeProfileAtomic(path string, data []byte, snap fileSnapshot) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".agent-sync-path-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	mode := snap.mode
	if !snap.exists {
		mode = 0o644
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := checkUnchanged(path, snap); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func sanitizeName(s string) string {
	if s == "" {
		return "profile"
	}
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == 0 {
			return '_'
		}
		return r
	}, s)
}

// writeBackup stores the exact original bytes under dir with a unique,
// non-overwriting private (0600) file. The original profile may hold secrets,
// so the backup inherits the same private intent and is never printed.
func writeBackup(dir, original string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	base := sanitizeName(filepath.Base(original))
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("%s.%d.%d.bak", base, time.Now().UnixNano(), i)
		p := filepath.Join(dir, name)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil {
			return "", werr
		}
		if cerr != nil {
			return "", cerr
		}
		return p, nil
	}
	return "", fmt.Errorf("could not allocate a unique backup name")
}

type receipt struct {
	path    string
	existed bool
	mode    os.FileMode
	backup  string
	newHash string
}

// applyProfile applies the block to one profile. It returns a nil receipt (and
// a no-op) when the profile already holds the block.
func applyProfile(path, block string, opts Options, prev *[]Preview) (*receipt, error) {
	snap, err := snapshotProfile(path)
	if err != nil {
		return nil, err
	}

	pv := Preview{Target: path, Create: !snap.exists}
	var out []byte
	if !snap.exists {
		out = []byte(block)
		pv.Added = blockLines(block)
	} else {
		var changed bool
		out, changed, err = renderProfile(snap.data, block)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if !changed {
			pv.Already = true
			*prev = append(*prev, pv)
			return nil, nil
		}
		if old, ok := existingBlock(snap.data); ok && old != block {
			pv.Removed = blockLines(old)
		}
		pv.Added = blockLines(block)
	}
	*prev = append(*prev, pv)

	if opts.DryRun {
		// No write, no backup, no directory.
		return &receipt{path: path, existed: snap.exists, mode: snap.mode, newHash: hashBytes(out)}, nil
	}
	if opts.BackupDir == "" {
		return nil, fmt.Errorf("no backup directory configured")
	}
	var backup string
	if snap.exists {
		backup, err = writeBackup(opts.BackupDir, path, snap.data)
		if err != nil {
			return nil, fmt.Errorf("backing up %s: %w", path, err)
		}
	}
	if err := writeProfileAtomic(path, out, snap); err != nil {
		return nil, err
	}
	return &receipt{path: path, existed: snap.exists, mode: snap.mode, backup: backup, newHash: hashBytes(out)}, nil
}

func blockLines(block string) []string {
	return strings.Split(strings.TrimSuffix(block, "\n"), "\n")
}

// rollback restores the profile only when it still holds exactly what this run
// wrote; any later user edit is left untouched. A profile that did not exist is
// removed only under the same condition.
func rollback(rec *receipt) error {
	if rec == nil {
		return nil
	}
	cur, err := os.ReadFile(rec.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if hashBytes(cur) != rec.newHash {
		return nil
	}
	if rec.existed {
		data, err := os.ReadFile(rec.backup)
		if err != nil {
			return err
		}
		return os.WriteFile(rec.path, data, rec.mode)
	}
	return os.Remove(rec.path)
}

func promptLine(stdin io.Reader, stdout io.Writer, label string) (string, error) {
	fmt.Fprint(stdout, label)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func confirmPrompt(stdin io.Reader, stdout io.Writer, label string) (bool, error) {
	line, err := promptLine(stdin, stdout, label)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

type posixConfig struct {
	Dir        string
	Home       string
	Zdotdir    string
	ZdotdirSet bool
	ShellEnv   string
	ShellFlag  string
	DryRun     bool
	AssumeYes  bool
	Interactive bool
	Stdin      io.Reader
	Stdout     io.Writer
	BackupDir  string
}

func chooseShell(cfg posixConfig) (string, error) {
	if cfg.ShellFlag != "" {
		return ParseShell(cfg.ShellFlag)
	}
	if s, ok := detectShell(cfg.ShellEnv); ok {
		return s, nil
	}
	if cfg.Interactive {
		line, err := promptLine(cfg.Stdin, cfg.Stdout, "shell [bash/zsh]: ")
		if err != nil {
			return "", fmt.Errorf("reading shell choice: %w", err)
		}
		return ParseShell(strings.ToLower(line))
	}
	return "", fmt.Errorf("cannot detect a supported shell from $SHELL; pass --shell bash or --shell zsh")
}

// InstallPOSIX edits the resolved shell profiles so dir is added to PATH. It is
// the cross-platform-testable core used by the Linux backend.
func InstallPOSIX(cfg posixConfig) (Report, error) {
	rep := Report{Dir: cfg.Dir}
	if err := validatePOSIXDir(cfg.Dir); err != nil {
		return rep, err
	}
	shell, err := chooseShell(cfg)
	if err != nil {
		return rep, err
	}
	profiles, err := resolveProfiles(shell, cfg)
	if err != nil {
		return rep, err
	}
	if !cfg.DryRun && !cfg.AssumeYes {
		if !cfg.Interactive {
			rep.Skipped = true
			rep.Note = "skipped: not a terminal"
			return rep, nil
		}
		ok, perr := confirmPrompt(cfg.Stdin, cfg.Stdout, fmt.Sprintf("Add %s to PATH? [y/N] ", cfg.Dir))
		if perr != nil {
			return rep, fmt.Errorf("reading confirmation: %w", perr)
		}
		if !ok {
			rep.Skipped = true
			return rep, nil
		}
	}

	opts := Options{
		DryRun:      cfg.DryRun,
		AssumeYes:   cfg.AssumeYes,
		Interactive: cfg.Interactive,
		Stdin:       cfg.Stdin,
		Stdout:      cfg.Stdout,
		BackupDir:   cfg.BackupDir,
	}
	block := PathBlock(cfg.Dir)
	var receipts []*receipt
	for _, p := range profiles {
		rec, aerr := applyProfile(p, block, opts, &rep.Previews)
		if aerr != nil {
			// Roll back the profiles already changed in this run, in reverse.
			for i := len(receipts) - 1; i >= 0; i-- {
				_ = rollback(receipts[i])
			}
			return rep, aerr
		}
		if rec != nil {
			receipts = append(receipts, rec)
			if rec.backup != "" {
				rep.Backups = append(rep.Backups, rec.backup)
			}
		}
	}
	rep.Note = "new terminals pick up PATH; a fresh login/logout is the reliable way (the current shell keeps the old value)"
	return rep, nil
}
