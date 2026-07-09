package tui

import (
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/subh05sus/cache-pot/internal/server"
)

// sample is the previous tick's cumulative counters, kept so commands/sec can
// be derived by diffing — the same approach the dashboard's history ring
// uses (internal/dashboard/history.go), just without the 5-minute buffer
// since the TUI only shows "right now".
type sample struct {
	at       time.Time
	commands int64
}

// renderStatus draws the live status screen and returns the sample to diff
// against on the next tick. Only cheap, always-available counters are read
// here — no keyspace scan (Store().MemoryReport is a full walk and is
// deliberately out of scope for this per-second render path; see the
// clustering/HNSW-style "documented future upgrade" pattern this repo
// already uses elsewhere).
func renderStatus(w io.Writer, srv *server.Server, info Info, prev *sample) *sample {
	clearScreen(w)

	stats := srv.Stats()
	commands := stats.Commands.Load()
	now := time.Now()

	var cps float64
	if prev != nil {
		dt := now.Sub(prev.at).Seconds()
		if dt > 0 {
			cps = float64(commands-prev.commands) / dt
			if cps < 0 {
				cps = 0 // counters don't wrap in practice, but never show negative
			}
		}
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	fmt.Fprint(w, tint(cAccent, "  cache·pot"), tint(cDim, " — live status "))
	fmt.Fprintf(w, tint(cDim, "(RESP %s"), info.RespAddr)
	if info.DashboardAddr != "" {
		fmt.Fprintf(w, tint(cDim, " · dashboard http://localhost%s"), info.DashboardAddr)
	}
	fmt.Fprintln(w, tint(cDim, ")"))
	fmt.Fprintln(w, tint(cDim, "  ───────────────────────────────────────────────────────────"))
	fmt.Fprintln(w)

	tile := func(label, value string) {
		fmt.Fprintf(w, "    %-22s %s\n", tint(cDim, label), tint(cAccentBold, value))
	}

	tile("Uptime", fmtDuration(srv.Uptime()))
	tile("Keys", fmtNum(int64(srv.Store().DBSize())))
	tile("Memory (process)", fmtBytes(int64(mem.Alloc)))
	tile("Connected clients", fmtNum(stats.Connections.Load()))
	tile("Total connections", fmtNum(stats.TotalConns.Load()))
	tile("Commands/sec", fmt.Sprintf("%.0f", cps))
	tile("Commands (total)", fmtNum(commands))
	tile("Semantic cache hit ratio", fmt.Sprintf("%.1f%%", stats.HitRatio()*100))

	fmt.Fprintln(w)
	fmt.Fprintln(w, tint(cDim, "  ───────────────────────────────────────────────────────────"))
	fmt.Fprintf(w, "  %s\n", tint(cDim, "q / Ctrl+C — drop to plain logs (server keeps running)"))

	return &sample{at: now, commands: commands}
}

func fmtNum(n int64) string {
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i != 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
