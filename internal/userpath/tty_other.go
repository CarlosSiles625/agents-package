//go:build !linux && !windows

package userpath

// StdinIsTerminal is false on platforms without a supported backend.
func StdinIsTerminal() bool { return false }
