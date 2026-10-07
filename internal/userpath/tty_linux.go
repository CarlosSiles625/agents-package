//go:build linux

package userpath

import (
	"os"
	"syscall"
	"unsafe"
)

// StdinIsTerminal reports whether this process's actual stdin is a terminal.
// It issues a TCGETS ioctl, so /dev/null, pipes and regular files are correctly
// reported as non-interactive.
func StdinIsTerminal() bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		os.Stdin.Fd(),
		uintptr(syscall.TCGETS),
		uintptr(unsafe.Pointer(&termios)),
		0, 0, 0,
	)
	return errno == 0
}
