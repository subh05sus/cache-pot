package tui

import (
	"fmt"
	"io"
	"strings"
)

// bannerArt is rendered verbatim; it's the product's terminal wordmark.
const bannerArt = `
██████╗ █████╗  ██████╗██╗  ██╗███████╗      ██████╗  ██████╗ ████████╗
██╔════╝██╔══██╗██╔════╝██║  ██║██╔════╝      ██╔══██╗██╔═══██╗╚══██╔══╝
██║     ███████║██║     ███████║█████╗        ██████╔╝██║   ██║   ██║
██║     ██╔══██║██║     ██╔══██║██╔══╝        ██╔═══╝ ██║   ██║   ██║
╚██████╗██║  ██║╚██████╗██║  ██║███████╗      ██║     ╚██████╔╝   ██║
 ╚═════╝╚═╝  ╚═╝ ╚═════╝╚═╝  ╚═╝╚══════╝      ╚═╝      ╚═════╝    ╚═╝
`

// renderBanner draws the wordmark, the connection-info block derived from
// how the server was actually started, and the continue prompt.
func renderBanner(w io.Writer, info Info) {
	fmt.Fprint(w, tint(cAccent, bannerArt))
	fmt.Fprintf(w, "  %s\n\n", tint(cDim, "in-memory · Redis-compatible · AI-native — v"+info.Version))

	row := func(label, value string, on bool) {
		dot := tint(cErr, "●")
		if on {
			dot = tint(cOK, "●")
		}
		fmt.Fprintf(w, "  %s %-22s %s\n", dot, label, tint(cText, value))
	}

	fmt.Fprintln(w, tint(cDim, "  ── connection ──────────────────────────────────────────"))
	row("RESP (Redis clients)", info.RespAddr, true)
	if info.DashboardAddr != "" {
		row("Web dashboard", "http://localhost"+info.DashboardAddr, true)
	} else {
		row("Web dashboard", "disabled", false)
	}
	row("TLS", onOff(info.TLS), info.TLS)
	row("AUTH password", onOff(info.Auth), info.Auth)
	row("AOF persistence", pathOrOff(info.AOFPath), info.AOFPath != "")
	row("Snapshot persistence", pathOrOff(info.SnapshotPath), info.SnapshotPath != "")
	fmt.Fprintln(w, tint(cDim, "  ─────────────────────────────────────────────────────────"))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s   %s\n", tint(cAccentBold, "Press Enter"), tint(cDim, "to open the live status screen"))
	fmt.Fprintf(w, "  %s   %s\n", tint(cDim, "Press q or Ctrl+C"), tint(cDim, "to continue in plain log mode"))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func pathOrOff(path string) string {
	if path == "" {
		return "off"
	}
	return path
}

// clearScreen moves the cursor home and clears the visible screen — the same
// hand-rolled escape sequence style already used by the CLI's "clear" command.
func clearScreen(w io.Writer) { fmt.Fprint(w, "\033[H\033[2J") }

// centerPad is a tiny helper for status.go's header line; kept here since
// it's presentation, not logic.
func centerPad(s string, width int) string {
	if width <= len(s) {
		return s
	}
	left := (width - len(s)) / 2
	return strings.Repeat(" ", left) + s
}
