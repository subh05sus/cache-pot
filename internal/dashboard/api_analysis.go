package dashboard

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// handleAnalysis runs a synchronous memory analysis of the keyspace: top
// keys by size, bytes by type and by key-prefix namespace, TTL histogram.
func (d *Dashboard) handleAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		TopN      int    `json:"top_n"`
		Delimiter string `json:"delimiter"`
	}
	// An empty body is fine; defaults apply.
	json.NewDecoder(r.Body).Decode(&body)

	start := time.Now()
	rep := d.srv.Store().MemoryReport(body.TopN, body.Delimiter)

	// Re-shape TopKeys so binary key names survive JSON.
	topKeys := make([]map[string]any, len(rep.TopKeys))
	for i, k := range rep.TopKeys {
		topKeys[i] = map[string]any{"key": jsonStr(k.Key), "type": k.Type, "bytes": k.Bytes}
	}
	writeJSON(w, map[string]any{
		"total_keys":  rep.TotalKeys,
		"total_bytes": rep.TotalBytes,
		"by_type":     rep.ByType,
		"by_prefix":   rep.ByPrefix,
		"ttl":         rep.TTL,
		"top_keys":    topKeys,
		"elapsed_ms":  time.Since(start).Milliseconds(),
		"generated":   time.Now().UnixMilli(),
	})
}

// handleSlowlog serves the slow-command log (GET) and clears it (DELETE).
func (d *Dashboard) handleSlowlog(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		n := clampQueryInt(r, "count", 128, 1, 1024)
		entries := d.srv.SlowlogEntries(n)
		out := make([]map[string]any, len(entries))
		for i, e := range entries {
			args := make([]any, len(e.Args))
			for j, a := range e.Args {
				args[j] = jsonStr(a)
			}
			out[i] = map[string]any{
				"id":     e.ID,
				"t":      e.Time.UnixMilli(),
				"dur_us": e.DurUs,
				"args":   args,
				"addr":   e.Addr,
				"client": e.Client,
			}
		}
		writeJSON(w, map[string]any{
			"entries":      out,
			"threshold_us": d.srv.SlowlogThresholdUs(),
			"max_len":      d.srv.SlowlogMaxLen(),
		})
	case http.MethodDelete:
		d.srv.SlowlogReset()
		writeJSON(w, map[string]any{"ok": true})
	default:
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleSlowlogConfig updates the slowlog threshold and retention.
func (d *Dashboard) handleSlowlogConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ThresholdUs *int64 `json:"threshold_us"`
		MaxLen      *int64 `json:"max_len"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return
	}
	if body.ThresholdUs != nil {
		d.srv.SlowlogSetThresholdUs(*body.ThresholdUs)
	}
	if body.MaxLen != nil {
		if *body.MaxLen < 0 {
			httpError(w, http.StatusBadRequest, "max_len must be >= 0")
			return
		}
		d.srv.SlowlogSetMaxLen(*body.MaxLen)
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleClients lists live connections.
func (d *Dashboard) handleClients(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"clients": d.srv.ClientsSnapshot()})
}

// handleClientKill terminates one connection by id.
func (d *Dashboard) handleClientKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ID uint64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return
	}
	v, err := d.srv.Execute([]string{"CLIENT", "KILL", "ID", strconv.FormatUint(body.ID, 10)})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"killed": v.Int})
}
