//go:build windows

package userpath

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
)

// StdinIsTerminal reports whether this process's actual stdin is an interactive
// console. It uses GetConsoleMode, so redirected files (e.g. /dev/null) and
// pipes are correctly reported as non-interactive.
func StdinIsTerminal() bool {
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(os.Stdin.Fd(), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}
