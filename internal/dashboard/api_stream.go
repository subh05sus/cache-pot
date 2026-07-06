package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/subh05sus/cache-pot/internal/pubsub"
	"github.com/subh05sus/cache-pot/internal/server"
)

// SSE plumbing. Every stream handler: sets the event-stream headers, sends a
// keepalive comment every 15s so proxies don't cut the connection, and exits
// when the request context is cancelled (browser navigated away).

const sseKeepalive = 15 * time.Second

// sseStart prepares an SSE response and returns a send function, or ok=false
// if the connection cannot stream.
func sseStart(w http.ResponseWriter) (send func(event string, data any) error, ok bool) {
	fl, canFlush := w.(http.Flusher)
	if !canFlush {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	return func(event string, data any) error {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return err
		}
		fl.Flush()
		return nil
	}, true
}

// handleStreamStats emits one `tick` event per second with the current
// gauges — the overview page's live feed.
func (d *Dashboard) handleStreamStats(w http.ResponseWriter, r *http.Request) {
	send, ok := sseStart(w)
	if !ok {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	keep := time.NewTicker(sseKeepalive)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keep.C:
			fmt.Fprint(w, ": ping\n\n")
			w.(http.Flusher).Flush()
		case <-ticker.C:
			if err := send("tick", d.statsPayload()); err != nil {
				return
			}
		}
	}
}

// Profiler stream limits: how hard we truncate per-event args and how often
// frames go out. A command firehose degrades to sampled batches with an
// accurate dropped count rather than ever freezing the browser or dispatch.
const (
	monBatchInterval = 100 * time.Millisecond
	monBatchMax      = 200
	monMaxArgs       = 8
	monMaxArgLen     = 256
)

// handleStreamMonitor attaches to the server's MONITOR hub and relays events
// as batched `cmd` frames.
func (d *Dashboard) handleStreamMonitor(w http.ResponseWriter, r *http.Request) {
	send, ok := sseStart(w)
	if !ok {
		return
	}
	sub := d.srv.MonitorSubscribe()
	defer d.srv.MonitorUnsubscribe(sub)

	type monEventJSON struct {
		T    int64  `json:"t"` // unix micros
		Addr string `json:"addr"`
		ID   uint64 `json:"client_id"`
		Args []any  `json:"args"`
	}
	var batch []monEventJSON
	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := send("cmd", map[string]any{"events": batch, "dropped": sub.Dropped()})
		batch = batch[:0]
		return err
	}
	toJSON := func(ev server.MonitorEvent) monEventJSON {
		args := ev.Args
		extra := 0
		if len(args) > monMaxArgs {
			extra = len(args) - monMaxArgs
			args = args[:monMaxArgs]
		}
		out := make([]any, 0, len(args)+1)
		for _, a := range args {
			if len(a) > monMaxArgLen {
				a = a[:monMaxArgLen] + "…"
			}
			out = append(out, jsonStr(a))
		}
		if extra > 0 {
			out = append(out, fmt.Sprintf("…(+%d)", extra))
		}
		return monEventJSON{T: ev.Ts.UnixMicro(), Addr: ev.Addr, ID: ev.ClientID, Args: out}
	}

	ticker := time.NewTicker(monBatchInterval)
	defer ticker.Stop()
	keep := time.NewTicker(sseKeepalive)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keep.C:
			fmt.Fprint(w, ": ping\n\n")
			w.(http.Flusher).Flush()
		case ev, open := <-sub.C():
			if !open {
				return
			}
			batch = append(batch, toJSON(ev))
			if len(batch) >= monBatchMax {
				if err := flushBatch(); err != nil {
					return
				}
			}
		case <-ticker.C:
			if err := flushBatch(); err != nil {
				return
			}
		}
	}
}

// handleStreamPubSub subscribes to the requested channels and patterns and
// relays messages as `msg` events until the client disconnects.
func (d *Dashboard) handleStreamPubSub(w http.ResponseWriter, r *http.Request) {
	var subs []*pubsub.Subscription
	for _, ch := range splitCSV(r.URL.Query().Get("channels")) {
		subs = append(subs, d.srv.PubSubSubscribe(ch))
	}
	for _, p := range splitCSV(r.URL.Query().Get("patterns")) {
		subs = append(subs, d.srv.PubSubSubscribePattern(p))
	}
	if len(subs) == 0 {
		httpError(w, http.StatusBadRequest, "no channels or patterns given")
		return
	}
	send, ok := sseStart(w)
	if !ok {
		for _, s := range subs {
			d.srv.PubSubUnsubscribe(s)
		}
		return
	}
	defer func() {
		for _, s := range subs {
			d.srv.PubSubUnsubscribe(s)
		}
	}()

	// Fan the (up to a handful of) subscriptions into one channel so the
	// select below stays simple. The pump goroutines end when Unsubscribe
	// closes their subscription channel.
	merged := make(chan pubsub.Message, 256)
	for _, s := range subs {
		go func(s *pubsub.Subscription) {
			for m := range s.C {
				select {
				case merged <- m:
				default: // merged buffer full; drop like the broker would
				}
			}
		}(s)
	}

	keep := time.NewTicker(sseKeepalive)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keep.C:
			fmt.Fprint(w, ": ping\n\n")
			w.(http.Flusher).Flush()
		case m := <-merged:
			payload := map[string]any{
				"channel": jsonStr(m.Channel),
				"payload": jsonStr(m.Payload),
				"t":       time.Now().UnixMilli(),
			}
			if m.Pattern != "" {
				payload["pattern"] = jsonStr(m.Pattern)
			}
			if err := send("msg", payload); err != nil {
				return
			}
		}
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
