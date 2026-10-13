// Package state persists the sync baseline: the repository, branch, commit,
// target identity and per-path hashes/absence that agent-sync last applied,
// the per-agent model preferences and their reversible receipts, plus any
// pending transaction journal used for recovery.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"agent-sync/internal/modelpref"
)

// FileName is the state file name inside the state directory.
const FileName = "state.json"

// CurrentVersion is the state schema version written by this binary. Version 1
// files load and are upgraded in memory; the on-disk file is only rewritten
// when a write actually happens.
const CurrentVersion = 2

// FileState records the baseline content of a managed path.
type FileState struct {
	Exists bool   `json:"exists"`
	Hash   string `json:"hash,omitempty"`
}

// JournalOp describes one file operation recorded before it is performed.
type JournalOp struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // "write" or "delete"
	HadOld  bool   `json:"had_old"`
	Backup  string `json:"backup,omitempty"` // path relative to the state dir
	NewHash string `json:"new_hash,omitempty"`
}

// Journal is the pending transaction record. It is written before the target
// is modified and cleared only after the whole transaction succeeds.
type Journal struct {
	ID  string      `json:"id"`
	Ops []JournalOp `json:"ops"`
}

// State is the persisted baseline.
type State struct {
	Version    int                       `json:"version"`
	Repo       string                    `json:"repo"`
	Branch     string                    `json:"branch"`
	Commit     string                    `json:"commit"`
	Target     string                    `json:"target,omitempty"`
	Files      map[string]FileState      `json:"files"`
	ModelPrefs map[string]modelpref.Pref `json:"model_prefs,omitempty"`
	Journal    *Journal                  `json:"journal,omitempty"`
	// Empty in legacy Pi states means upstream. OpenCode never sets this field.
	PermissionParser string             `json:"pi_permission_parser,omitempty"`
}

// New returns an empty state for repo/branch.
func New(repo, branch string) *State {
	return &State{
		Version:    CurrentVersion,
		Repo:       repo,
		Branch:     branch,
		Files:      map[string]FileState{},
		ModelPrefs: map[string]modelpref.Pref{},
	}
}

// Path returns the state file path inside dir.
func Path(dir string) string { return filepath.Join(dir, FileName) }

// Load reads the state from dir. exists is false when the file is absent, in
// which case a fresh empty state is returned. A corrupt or unsupported state
// file yields an error: agent-sync never guesses ownership from bad state.
func Load(dir string) (st *State, exists bool, err error) {
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return New("", ""), false, nil
		}
		return nil, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, false, fmt.Errorf("state file %s is corrupt: %w", Path(dir), err)
	}
	if s.Version != 1 && s.Version != CurrentVersion {
		return nil, false, fmt.Errorf("state file %s has unsupported version %d", Path(dir), s.Version)
	}
	s.Version = CurrentVersion // upgrade in memory; persisted on the next Save
	if s.Files == nil {
		s.Files = map[string]FileState{}
	}
	if s.ModelPrefs == nil {
		s.ModelPrefs = map[string]modelpref.Pref{}
	}
	return &s, true, nil
}

// Save writes the state atomically into dir.
func (s *State) Save(dir string) error {
	s.Version = CurrentVersion
	if s.Files == nil {
		s.Files = map[string]FileState{}
	}
	if s.ModelPrefs == nil {
		s.ModelPrefs = map[string]modelpref.Pref{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(dir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path(dir))
}

// NormalizeTarget turns a destination path into the stable identity stored in
// state so that the same state directory cannot be reused for another target.
func NormalizeTarget(target string) string {
	if target == "" {
		return ""
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return filepath.Clean(target)
	}
	return filepath.Clean(abs)
}

// SamePath compares two normalized target identities. On Windows, where paths
// are case-insensitive, the comparison is case-folded.
func SamePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Hash returns the lowercase hex SHA-256 of data.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
