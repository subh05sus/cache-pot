// Package tui is Cache-Pot's optional terminal front end: a branded splash
// screen followed by a live, auto-refreshing status view. It only ever runs
// when both stdin and stdout are a real terminal (checked by the caller via
// internal/termutil before Run is invoked) — anything piped, redirected, or
// running under Docker/systemd/CI never reaches this package, so those
// environments' plain-log behavior is completely unaffected.
package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/subh05sus/cache-pot/internal/server"
)

// Info is the connection/feature summary known at flag-parse time in
// cmd/cache-pot/main.go, before the server ever starts listening — the
// banner needs no new plumbing to gather it.
type Info struct {
	Version       string
	RespAddr      string
	DashboardAddr string // "" if the dashboard is disabled
	TLS           bool
	Auth          bool
	AOFPath       string // "" if AOF is disabled
	SnapshotPath  string // "" if snapshotting is disabled
}

const (
	minCols = 60
	minRows = 16
)

const tickInterval = time.Second

// Run owns the terminal for as long as the TUI is on screen: banner, then
// the live status view. cancel is called on Ctrl+C so that trigger goes
// through the exact same shutdown path as an external SIGINT/SIGTERM (raw
// mode disables the terminal driver's own SIGINT-on-Ctrl+C, so without this
// Ctrl+C would otherwise do nothing while the TUI has the terminal).
//
// Run always leaves the terminal restored to cooked mode before returning,
// including on a raw-mode failure, a panic, or ctx being cancelled
// externally — the caller can unconditionally fall through to plain log
// output afterward.
func Run(ctx context.Context, cancel context.CancelFunc, srv *server.Server, info Info) error {
	rt, err := enableRaw()
	if err != nil {
		// Unsupported terminal, or stdin stopped being a TTY between the
		// caller's check and here — never fail startup over this.
		return err
	}
	defer rt.restore()
	defer func() {
		if r := recover(); r != nil {
			rt.restore()
			fmt.Fprintf(os.Stderr, "cache-pot: tui panic recovered: %v\n", r)
		}
	}()

	keys := startInput()

	if !awaitEnter(ctx, cancel, keys, info) {
		return nil // user skipped straight to logs, or shutdown requested
	}

	runStatusLoop(ctx, cancel, srv, info, keys)
	return nil
}

// awaitEnter shows the banner and blocks until the user presses Enter
// (proceed to the status screen), q/Esc (skip straight to plain logs), the
// server shuts down externally, or Ctrl+C (skip to logs AND trigger
// shutdown). Returns true only for "proceed to the status screen".
func awaitEnter(ctx context.Context, cancel context.CancelFunc, keys <-chan key, info Info) bool {
	render := func() {
		if cols, rows, err := size(); err == nil && (cols < minCols || rows < minRows) {
			clearScreen(os.Stdout)
			fmt.Fprintln(os.Stdout, tint(cWarn, "  terminal too small — resize to see the Cache-Pot banner"))
			return
		}
		clearScreen(os.Stdout)
		renderBanner(os.Stdout, info)
	}
	render()

	// Re-render on a slow tick too, purely so a resize while sitting at the
	// banner (no keypress yet) recovers without requiring a keystroke.
	ticker := time.NewTicker(2 * tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			render()
		case k, ok := <-keys:
			if !ok {
				return false
			}
			switch k {
			case keyEnter:
				return true
			case keyQuit, keyEsc:
				return false
			case keyCtrlC:
				cancel()
				return false
			}
		}
	}
}

// runStatusLoop drives the live status screen until the user backs out or
// the server shuts down.
func runStatusLoop(ctx context.Context, cancel context.CancelFunc, srv *server.Server, info Info, keys <-chan key) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	render := func(prev *sample) *sample {
		if cols, rows, err := size(); err == nil && (cols < minCols || rows < minRows) {
			clearScreen(os.Stdout)
			fmt.Fprintln(os.Stdout, tint(cWarn, "  terminal too small — resize to see the status view"))
			return prev
		}
		return renderStatus(os.Stdout, srv, info, prev)
	}

	var prev *sample
	prev = render(prev)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prev = render(prev)
		case k, ok := <-keys:
			if !ok {
				return
			}
			switch k {
			case keyQuit, keyEsc:
				return
			case keyCtrlC:
				cancel()
				return
			}
		}
	}
}
