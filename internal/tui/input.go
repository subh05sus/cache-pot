package tui

import "os"

// key is a parsed keypress the render loop reacts to. Raw mode delivers
// bytes one at a time with no line buffering, so parsing happens here rather
// than through a Scanner.
type key int

const (
	keyOther key = iota
	keyEnter
	keyQuit  // 'q' or 'Q'
	keyEsc   // a lone ESC (0x1b) not followed by an escape sequence
	keyCtrlC // 0x03 — raw mode disables the terminal's automatic SIGINT here
)

// startInput spawns a goroutine that reads raw stdin one byte at a time and
// parses it into key events on the returned channel. The goroutine is not
// explicitly stopped: once the process is exiting (either the TUI quit or a
// shutdown signal), nothing else reads stdin, so a stray blocked Read is
// harmless. The channel is unbuffered by design — the render loop's select
// naturally applies backpressure, and keypresses are rare compared to the
// render tick.
func startInput() <-chan key {
	ch := make(chan key)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				close(ch)
				return
			}
			switch b := buf[0]; b {
			case '\r', '\n':
				ch <- keyEnter
			case 'q', 'Q':
				ch <- keyQuit
			case 0x03:
				ch <- keyCtrlC
			case 0x1b:
				// Could be a lone Esc, or the start of an escape sequence
				// (e.g. an arrow key: ESC '[' 'A'). v1 doesn't act on
				// sequences, so drain a short one if present and otherwise
				// report a plain Esc.
				ch <- readEscape()
			default:
				ch <- keyOther
			}
		}
	}()
	return ch
}

// readEscape consumes the rest of a CSI escape sequence (ESC '[' ... final
// byte in 0x40-0x7e) if one follows immediately, so it doesn't get
// misinterpreted as a series of individual keypresses. If no '[' follows,
// the ESC was standalone.
func readEscape() key {
	b1 := make([]byte, 1)
	if n, err := os.Stdin.Read(b1); err != nil || n == 0 || b1[0] != '[' {
		return keyEsc
	}
	final := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(final)
		if err != nil || n == 0 {
			return keyOther
		}
		if final[0] >= 0x40 && final[0] <= 0x7e {
			return keyOther // sequence consumed and ignored (e.g. arrow keys)
		}
	}
}
