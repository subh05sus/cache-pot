package dashboard

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/subh05sus/cache-pot/internal/resp"
)

// handleCLI executes one workbench command line in-process and returns the
// typed reply. Streaming/stateful commands are rejected by Server.Execute
// with a pointer to the dedicated dashboard pages.
func (d *Dashboard) handleCLI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return
	}
	args, err := tokenizeCommand(body.Command)
	if err != nil {
		writeJSON(w, map[string]any{
			"reply":      map[string]any{"type": "error", "value": "ERR " + err.Error()},
			"elapsed_us": 0,
		})
		return
	}
	start := time.Now()
	v, err := d.srv.Execute(args)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"reply":      valueJSON(v),
		"elapsed_us": time.Since(start).Microseconds(),
	})
}

// valueJSON renders a typed RESP value for the workbench: the frontend keeps
// simple/bulk/int/error/null/array distinctions for faithful display.
func valueJSON(v resp.Value) any {
	switch v.Kind {
	case '+':
		return map[string]any{"type": "simple", "value": jsonStr(v.Str)}
	case '-':
		return map[string]any{"type": "error", "value": v.Str}
	case ':':
		return map[string]any{"type": "int", "value": v.Int}
	case '$':
		if v.Null {
			return map[string]any{"type": "null"}
		}
		return map[string]any{"type": "bulk", "value": jsonStr(v.Str)}
	case '*':
		if v.Null {
			return map[string]any{"type": "null"}
		}
		items := make([]any, len(v.Array))
		for i, el := range v.Array {
			items[i] = valueJSON(el)
		}
		return map[string]any{"type": "array", "value": items}
	default:
		return map[string]any{"type": "null"}
	}
}
