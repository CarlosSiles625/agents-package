package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// lockName is the writer lock file inside the state directory.
const lockName = "state.lock"

// ErrLocked is returned when another writer holds the state lock.
var ErrLocked = errors.New("another agent-sync operation is in progress (state lock held)")

// Lock is an acquired writer lock. Release is idempotent.
type Lock struct {
	path string
	held bool
}

// Acquire takes the per-state writer lock, serialising sync and model
// operations on the same state directory. The lock is created exclusively:
// a live operation is never timed out or stolen, because a legitimate run
// (a slow fetch, a large repository) can take arbitrarily long and the lock
// carries no heartbeat to distinguish it from a crashed one. When the lock is
// already held Acquire fails closed with ErrLocked and reports the owning pid
// and age so an operator can decide to remove it by hand.
//
// Directories are created as needed.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, lockName)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
		f.Close()
		return &Lock{path: p, held: true}, nil
	}
	if !os.IsExist(err) {
		return nil, err
	}
	return nil, fmt.Errorf("%w%s", ErrLocked, describeLock(p))
}

// describeLock renders a short diagnostic about an existing lock without ever
// removing it.
func describeLock(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	body, _ := os.ReadFile(p)
	owner := strings.TrimSpace(string(body))
	if owner == "" {
		return fmt.Sprintf(" (%s, age %s)", p, ageString(fi))
	}
	return fmt.Sprintf(" (%s, owner %q, age %s)", p, owner, ageString(fi))
}

func ageString(fi os.FileInfo) string {
	d := time.Since(fi.ModTime())
	if d < 0 {
		d = 0
	}
	return d.Truncate(1e9).String()
}

// Release removes the lock file.
func (l *Lock) Release() error {
	if l == nil || !l.held {
		return nil
	}
	l.held = false
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
