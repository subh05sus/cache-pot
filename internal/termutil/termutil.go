// Package termutil holds small terminal-detection helpers shared by the CLI
// shell and the TUI, so there is exactly one definition of "is this a real
// terminal" across the binary.
package termutil

import "os"

// IsTerminal reports whether f is a character device (a TTY) rather than a
// pipe or file. This check alone doesn't require any terminal dependency;
// raw-mode control (used by the TUI) is a separate, heavier operation.
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
