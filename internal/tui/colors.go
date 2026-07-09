package tui

import "os"

// A small, fixed palette — echoes the dashboard's copper-on-graphite
// identity (internal/dashboard/static/css/tokens.css) so the terminal and
// web surfaces read as the same product.
const (
	cReset      = "\033[0m"
	cAccent     = "\033[38;5;208m" // copper — the wordmark
	cAccentBold = "\033[1;38;5;214m"
	cOK         = "\033[38;5;114m" // green — feature on / healthy
	cErr        = "\033[38;5;203m" // red — feature off
	cWarn       = "\033[38;5;221m"
	cDim        = "\033[38;5;102m" // gray — labels, rules
	cText       = "\033[97m"       // near-white — values
)

// colorEnabled respects the NO_COLOR convention (https://no-color.org) in
// addition to whatever TTY check gated the TUI in the first place.
var colorEnabled = os.Getenv("NO_COLOR") == ""

func tint(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + cReset
}
