// Package state persists the sync baseline: the repository, branch, commit
// and per-path hashes/absence that agent-sync last applied, plus any pending
// transaction journal used for recovery.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the state file name inside the state directory.
const FileName = "state.json"

// CurrentVersion is the state schema version understood by this binary.
const CurrentVersion = 1

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
	Version int                  `json:"version"`
	Repo    string               `json:"repo"`
	Branch  string               `json:"branch"`
	Commit  string               `json:"commit"`
	Files   map[string]FileState `json:"files"`
	Journal *Journal             `json:"journal,omitempty"`
}

// New returns an empty state for repo/branch.
func New(repo, branch string) *State {
	return &State{
		Version: CurrentVersion,
		Repo:    repo,
		Branch:  branch,
		Files:   map[string]FileState{},
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
	if s.Version != CurrentVersion {
		return nil, false, fmt.Errorf("state file %s has unsupported version %d", Path(dir), s.Version)
	}
	if s.Files == nil {
		s.Files = map[string]FileState{}
	}
	return &s, true, nil
}

// Save writes the state atomically into dir.
func (s *State) Save(dir string) error {
	s.Version = CurrentVersion
	if s.Files == nil {
		s.Files = map[string]FileState{}
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

// Hash returns the lowercase hex SHA-256 of data.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
