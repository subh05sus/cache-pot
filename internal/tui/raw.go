package tui

import (
	"os"

	"golang.org/x/term"
)

// rawTerm holds the terminal state needed to restore stdin to its original
// (cooked) mode. It is safe to call restore multiple times or on a
// zero-value rawTerm (nothing was ever enabled).
type rawTerm struct {
	fd    int
	state *term.State
}

// enableRaw puts stdin into raw mode: no line buffering, no local echo, and
// critically no automatic SIGINT-on-Ctrl+C — the TUI reads that byte itself
// so it can route it through the same shutdown path as every other signal.
// Any failure (unsupported terminal, stdin not actually a TTY despite the
// char-device check, etc.) is returned rather than panicking; the caller
// falls back to plain logs.
func enableRaw() (*rawTerm, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return &rawTerm{fd: fd, state: state}, nil
}

// restore returns the terminal to its pre-raw state. Called via defer on the
// happy path and additionally from a recover() guard, so a panic mid-render
// can never leave the user's shell in raw mode.
func (r *rawTerm) restore() {
	if r == nil || r.state == nil {
		return
	}
	term.Restore(r.fd, r.state)
	r.state = nil // idempotent: a second call is a no-op
}

// size returns the current terminal dimensions. Called every render tick
// rather than relying on SIGWINCH, which has no Windows equivalent.
func size() (cols, rows int, err error) {
	return term.GetSize(int(os.Stdout.Fd()))
}
