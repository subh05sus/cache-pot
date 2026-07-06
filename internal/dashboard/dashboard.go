// Package dashboard serves Cache-Pot's embedded web UI: a RedisInsight-style
// management console (overview charts, key browser with CRUD, workbench,
// profiler, slowlog, pub/sub, memory analysis, client list) speaking to a
// JSON/SSE API. It deliberately has no build step and no external assets:
// the whole frontend is vanilla JS/CSS embedded in the binary.
package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/subh05sus/cache-pot/internal/server"
)

//go:embed static
var staticFS embed.FS

func init() {
	// Windows resolves content types from the registry, which frequently maps
	// .js to text/plain — browsers then refuse to run ES modules. Register
	// the correct types explicitly so the UI works everywhere.
	mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	mime.AddExtensionType(".css", "text/css; charset=utf-8")
	mime.AddExtensionType(".svg", "image/svg+xml")
	mime.AddExtensionType(".html", "text/html; charset=utf-8")
}

// Dashboard wraps an HTTP server bound to a Cache-Pot server's state.
type Dashboard struct {
	srv  *server.Server
	http *http.Server
	hist *history
}

// New builds a dashboard for srv listening on addr (e.g. ":8080").
func New(srv *server.Server, addr string) *Dashboard {
	d := &Dashboard{srv: srv, hist: &history{}}
	mux := http.NewServeMux()

	// JSON API.
	mux.HandleFunc("/api/stats", d.handleStats)
	mux.HandleFunc("/api/stats/history", d.handleStatsHistory)
	mux.HandleFunc("/api/keys", d.handleKeys)
	mux.HandleFunc("/api/key", d.handleKey)
	mux.HandleFunc("/api/cli", d.handleCLI)
	mux.HandleFunc("/api/slowlog", d.handleSlowlog)
	mux.HandleFunc("/api/config/slowlog", d.handleSlowlogConfig)
	mux.HandleFunc("/api/clients", d.handleClients)
	mux.HandleFunc("/api/clients/kill", d.handleClientKill)
	mux.HandleFunc("/api/analysis", d.handleAnalysis)

	// Live streams (Server-Sent Events).
	mux.HandleFunc("/api/stream/stats", d.handleStreamStats)
	mux.HandleFunc("/api/stream/monitor", d.handleStreamMonitor)
	mux.HandleFunc("/api/stream/pubsub", d.handleStreamPubSub)

	// Static frontend. Unknown non-API paths fall back to index.html so the
	// hash router always boots.
	sub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServerFS(sub)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})

	d.http = &http.Server{Addr: addr, Handler: mux}
	return d
}

// ListenAndServe runs the dashboard until ctx is cancelled.
func (d *Dashboard) ListenAndServe(ctx context.Context) error {
	go d.runSampler(ctx)
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		d.http.Shutdown(shutCtx)
	}()
	if err := d.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// statsPayload assembles the current gauges served by /api/stats and the
// stats SSE stream.
func (d *Dashboard) statsPayload() map[string]any {
	st := d.srv.Stats()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return map[string]any{
		"t":                 time.Now().UnixMilli(),
		"version":           server.Version,
		"keys":              d.srv.Store().DBSize(),
		"connected_clients": st.Connections.Load(),
		"total_connections": st.TotalConns.Load(),
		"commands":          st.Commands.Load(),
		"cache_hits":        st.CacheHits.Load(),
		"cache_misses":      st.CacheMisses.Load(),
		"cache_hit_ratio":   st.HitRatio(),
		"memory_bytes":      mem.Alloc,
		"uptime_seconds":    int(d.srv.Uptime().Seconds()),
		"slowlog_len":       d.srv.SlowlogLen(),
		"monitor_active":    d.srv.MonitorActive(),
	}
}

func (d *Dashboard) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, d.statsPayload())
}

func (d *Dashboard) handleStatsHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"interval_ms": 1000,
		"samples":     d.hist.snapshot(),
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"error": msg})
}
