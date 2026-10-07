//go:build linux

package userpath

import (
	"fmt"
	"os"
)

// Add performs the PATH installation on Linux by editing the resolved shell
// profiles. dir is the directory that contains the running executable.
func Add(dir string, opts Options) (Report, error) {
	if opts.Shell != "" {
		if _, err := ParseShell(opts.Shell); err != nil {
			return Report{Dir: dir}, err
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Report{Dir: dir}, fmt.Errorf("locating home directory: %w", err)
	}
	zdotdir, zdotdirSet := os.LookupEnv("ZDOTDIR")
	return InstallPOSIX(posixConfig{
		Dir:         dir,
		Home:        home,
		Zdotdir:     zdotdir,
		ZdotdirSet:  zdotdirSet,
		ShellEnv:    os.Getenv("SHELL"),
		ShellFlag:   opts.Shell,
		DryRun:      opts.DryRun,
		AssumeYes:   opts.AssumeYes,
		Interactive: opts.Interactive,
		Stdin:       opts.Stdin,
		Stdout:      opts.Stdout,
		BackupDir:   opts.BackupDir,
	})
}
