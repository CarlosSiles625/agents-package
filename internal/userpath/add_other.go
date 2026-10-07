//go:build !linux && !windows

package userpath

import "fmt"

// Add reports that PATH installation is unsupported on this platform.
func Add(dir string, opts Options) (Report, error) {
	return Report{Dir: dir}, fmt.Errorf("adding to PATH is only supported on Windows and Linux")
}
