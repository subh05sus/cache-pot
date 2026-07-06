package dashboard

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Limits protecting the browser and the JSON encoder from huge payloads.
const (
	maxKeysPerPage     = 1000
	defaultKeysPerPage = 200
	maxElemsPerPage    = 500
	defaultElemsPage   = 100
	defaultPreview     = 16 * 1024
	maxPreview         = 4 * 1024 * 1024
)

// keysCursor is the opaque pagination token for /api/keys: keyset pagination
// (shard + last-seen key) is immune to skips under concurrent mutation.
type keysCursor struct {
	Shard int    `json:"s"`
	After string `json:"a"`
}

func encodeCursor(c keysCursor) string {
	b, _ := json.Marshal(c)
	return base64.URLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (keysCursor, error) {
	var c keysCursor
	if s == "" {
		return c, nil
	}
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// handleKeys lists keys with cursor pagination, glob matching, and an
// optional type filter. The response's cursor field is "" when iteration is
// complete.
func (d *Dashboard) handleKeys(w http.ResponseWriter, r *http.Request) {
	cur, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad cursor")
		return
	}
	match := r.URL.Query().Get("match")
	if match == "" {
		match = "*"
	}
	typeFilter := r.URL.Query().Get("type")
	count := clampQueryInt(r, "count", defaultKeysPerPage, 1, maxKeysPerPage)

	st := d.srv.Store()
	keys, nextShard, nextAfter, done := st.ScanAfter(cur.Shard, cur.After, match, count)

	type keyInfo struct {
		Key   any    `json:"key"`
		Type  string `json:"type"`
		TTLMs int64  `json:"ttl_ms"`
	}
	infos := make([]keyInfo, 0, len(keys))
	for _, k := range keys {
		t := st.Type(k)
		if typeFilter != "" && t != typeFilter {
			continue
		}
		infos = append(infos, keyInfo{Key: jsonStr(k), Type: t, TTLMs: ttlMs(d, k)})
	}
	next := ""
	if !done {
		next = encodeCursor(keysCursor{Shard: nextShard, After: nextAfter})
	}
	writeJSON(w, map[string]any{"keys": infos, "cursor": next, "done": done})
}

// ttlMs returns the remaining TTL in milliseconds, -1 for persistent keys,
// -2 for missing ones (Redis PTTL semantics).
func ttlMs(d *Dashboard, key string) int64 {
	dur, hasTTL, ok := d.srv.Store().TTL(key)
	if !ok {
		return -2
	}
	if !hasTTL {
		return -1
	}
	return dur.Milliseconds()
}

// handleKey is the per-key CRUD endpoint: GET reads a windowed view, POST
// creates/overwrites, PATCH applies one mutation op, DELETE removes.
func (d *Dashboard) handleKey(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		d.handleKeyGet(w, r)
	case http.MethodPost:
		d.handleKeyPost(w, r)
	case http.MethodPatch:
		d.handleKeyPatch(w, r)
	case http.MethodDelete:
		d.handleKeyDelete(w, r)
	default:
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleKeyGet returns metadata plus a windowed view of the value: element
// ranges for collections, a byte-capped preview for strings. It never
// serializes an entire large value.
func (d *Dashboard) handleKeyGet(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, http.StatusBadRequest, "missing key")
		return
	}
	st := d.srv.Store()
	typ := st.Type(key)
	if typ == "none" {
		httpError(w, http.StatusNotFound, "no such key")
		return
	}
	start := clampQueryInt(r, "start", 0, 0, 1<<30)
	count := clampQueryInt(r, "count", defaultElemsPage, 1, maxElemsPerPage)
	preview := clampQueryInt(r, "preview", defaultPreview, 1, maxPreview)

	var value any
	switch typ {
	case "string":
		v, _, err := st.Get(key)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		data := v
		truncated := false
		if len(data) > preview {
			data = data[:preview]
			truncated = true
		}
		value = map[string]any{"data": jsonStr(data), "total_len": len(v), "truncated": truncated}
	case "hash":
		flat, err := st.HGetAll(key)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		type pair struct{ f, v string }
		pairs := make([]pair, 0, len(flat)/2)
		for i := 0; i+1 < len(flat); i += 2 {
			pairs = append(pairs, pair{flat[i], flat[i+1]})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].f < pairs[j].f })
		window := windowSlice(len(pairs), start, count)
		fields := make([][2]any, 0, len(window))
		for _, i := range window {
			fields = append(fields, [2]any{jsonStr(pairs[i].f), jsonStr(pairs[i].v)})
		}
		value = map[string]any{"fields": fields, "total": len(pairs)}
	case "list":
		total, err := st.LLen(key)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items, err := st.LRange(key, start, start+count-1)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]any, len(items))
		for i, it := range items {
			out[i] = jsonStr(it)
		}
		value = map[string]any{"items": out, "total": total}
	case "set":
		members, err := st.SMembers(key)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sort.Strings(members)
		window := windowSlice(len(members), start, count)
		out := make([]any, 0, len(window))
		for _, i := range window {
			out = append(out, jsonStr(members[i]))
		}
		value = map[string]any{"members": out, "total": len(members)}
	case "zset":
		total, err := st.ZCard(key)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		members, err := st.ZRange(key, start, start+count-1)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([][2]any, len(members))
		for i, m := range members {
			out[i] = [2]any{jsonStr(m.Member), m.Score}
		}
		value = map[string]any{"members": out, "total": total}
	case "vector":
		countV, _ := st.VCard(key)
		dim, _ := st.VDim(key)
		value = map[string]any{"count": countV, "dim": dim}
	}

	size, _ := st.MemoryUsage(key)
	writeJSON(w, map[string]any{
		"key":        jsonStr(key),
		"type":       typ,
		"ttl_ms":     ttlMs(d, key),
		"size_bytes": size,
		"value":      value,
	})
}

// keyPostBody is the create/overwrite payload.
type keyPostBody struct {
	Key   any             `json:"key"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
	TTLMs int64           `json:"ttl_ms"`
}

// handleKeyPost creates or overwrites a key by issuing the equivalent write
// commands through Server.Execute, so AOF, stats, slowlog, and the profiler
// all observe the mutation exactly as if a TCP client sent it.
func (d *Dashboard) handleKeyPost(w http.ResponseWriter, r *http.Request) {
	var body keyPostBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return
	}
	key, err := decodeStr(anyFromRaw(body.Key))
	if err != nil || key == "" {
		httpError(w, http.StatusBadRequest, "bad key")
		return
	}

	var cmds [][]string
	switch body.Type {
	case "string":
		var v any
		if err := json.Unmarshal(body.Value, &v); err != nil {
			httpError(w, http.StatusBadRequest, "bad value")
			return
		}
		s, err := decodeStr(v)
		if err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		cmds = append(cmds, []string{"SET", key, s})
	case "hash":
		pairs, err := decodePairs(body.Value)
		if err != nil || len(pairs) == 0 {
			httpError(w, http.StatusBadRequest, "hash value must be a non-empty [[field, value], ...]")
			return
		}
		cmd := []string{"HSET", key}
		for _, p := range pairs {
			cmd = append(cmd, p[0], p[1])
		}
		cmds = append(cmds, cmd)
	case "list", "set":
		items, err := decodeStrList(body.Value)
		if err != nil || len(items) == 0 {
			httpError(w, http.StatusBadRequest, "value must be a non-empty array of strings")
			return
		}
		verb := "RPUSH"
		if body.Type == "set" {
			verb = "SADD"
		}
		cmds = append(cmds, append([]string{verb, key}, items...))
	case "zset":
		members, err := decodeScored(body.Value)
		if err != nil || len(members) == 0 {
			httpError(w, http.StatusBadRequest, "zset value must be a non-empty [[member, score], ...]")
			return
		}
		cmd := []string{"ZADD", key}
		for _, m := range members {
			cmd = append(cmd, m.score, m.member)
		}
		cmds = append(cmds, cmd)
	default:
		httpError(w, http.StatusBadRequest, "unsupported type "+body.Type)
		return
	}

	// Creating over an existing key of another type would fail with
	// WRONGTYPE mid-way; delete first for clean overwrite semantics.
	if d.srv.Store().Type(key) != "none" {
		if _, err := d.srv.Execute([]string{"DEL", key}); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	for _, cmd := range cmds {
		if !d.execOK(w, cmd) {
			return
		}
	}
	if body.TTLMs > 0 {
		if !d.execOK(w, []string{"PEXPIRE", key, strconv.FormatInt(body.TTLMs, 10)}) {
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

// keyPatchBody is one mutation op against an existing key.
type keyPatchBody struct {
	Key    any     `json:"key"`
	Op     string  `json:"op"`
	TTLMs  int64   `json:"ttl_ms"`
	NewKey any     `json:"newkey"`
	Field  any     `json:"field"`
	Value  any     `json:"value"`
	Member any     `json:"member"`
	Score  float64 `json:"score"`
	Index  int     `json:"index"`
}

func (d *Dashboard) handleKeyPatch(w http.ResponseWriter, r *http.Request) {
	var body keyPatchBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return
	}
	key, err := decodeStr(body.Key)
	if err != nil || key == "" {
		httpError(w, http.StatusBadRequest, "bad key")
		return
	}
	getStr := func(v any, what string) (string, bool) {
		s, err := decodeStr(v)
		if err != nil {
			httpError(w, http.StatusBadRequest, "bad "+what+": "+err.Error())
			return "", false
		}
		return s, true
	}

	var cmd []string
	switch body.Op {
	case "set_ttl":
		if body.TTLMs <= 0 {
			httpError(w, http.StatusBadRequest, "ttl_ms must be positive")
			return
		}
		cmd = []string{"PEXPIRE", key, strconv.FormatInt(body.TTLMs, 10)}
	case "persist":
		cmd = []string{"PERSIST", key}
	case "rename":
		nk, ok := getStr(body.NewKey, "newkey")
		if !ok {
			return
		}
		cmd = []string{"RENAME", key, nk}
	case "set_string":
		v, ok := getStr(body.Value, "value")
		if !ok {
			return
		}
		// SET clears any TTL; capture and re-apply it so an edit in the
		// browser does not silently make the key persistent.
		prevTTL := ttlMs(d, key)
		if !d.execOK(w, []string{"SET", key, v}) {
			return
		}
		if prevTTL > 0 {
			if !d.execOK(w, []string{"PEXPIRE", key, strconv.FormatInt(prevTTL, 10)}) {
				return
			}
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	case "hset":
		f, ok := getStr(body.Field, "field")
		if !ok {
			return
		}
		v, ok := getStr(body.Value, "value")
		if !ok {
			return
		}
		cmd = []string{"HSET", key, f, v}
	case "hdel":
		f, ok := getStr(body.Field, "field")
		if !ok {
			return
		}
		cmd = []string{"HDEL", key, f}
	case "lpush", "rpush":
		v, ok := getStr(body.Value, "value")
		if !ok {
			return
		}
		cmd = []string{strings.ToUpper(body.Op), key, v}
	case "lpop", "rpop":
		cmd = []string{strings.ToUpper(body.Op), key}
	case "sadd", "srem":
		m, ok := getStr(body.Member, "member")
		if !ok {
			return
		}
		cmd = []string{strings.ToUpper(body.Op), key, m}
	case "zadd":
		m, ok := getStr(body.Member, "member")
		if !ok {
			return
		}
		cmd = []string{"ZADD", key, strconv.FormatFloat(body.Score, 'g', -1, 64), m}
	case "zrem":
		m, ok := getStr(body.Member, "member")
		if !ok {
			return
		}
		cmd = []string{"ZREM", key, m}
	default:
		httpError(w, http.StatusBadRequest, "unknown op "+body.Op)
		return
	}
	if !d.execOK(w, cmd) {
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (d *Dashboard) handleKeyDelete(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, http.StatusBadRequest, "missing key")
		return
	}
	v, err := d.srv.Execute([]string{"DEL", key})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"deleted": v.Int})
}

// execOK runs one command through Execute and translates a RESP error into
// an HTTP 400. It reports whether the command succeeded.
func (d *Dashboard) execOK(w http.ResponseWriter, cmd []string) bool {
	v, err := d.srv.Execute(cmd)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if v.Kind == '-' {
		httpError(w, http.StatusBadRequest, v.Str)
		return false
	}
	return true
}

// --- small decoding helpers -------------------------------------------------

// anyFromRaw passes through values already decoded as any (the Key field is
// declared any so it accepts both plain strings and $b64 envelopes).
func anyFromRaw(v any) any { return v }

func decodeStrList(raw json.RawMessage) ([]string, error) {
	var vals []any
	if err := json.Unmarshal(raw, &vals); err != nil {
		return nil, err
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		s, err := decodeStr(v)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

func decodePairs(raw json.RawMessage) ([][2]string, error) {
	var vals [][]any
	if err := json.Unmarshal(raw, &vals); err != nil {
		return nil, err
	}
	out := make([][2]string, len(vals))
	for i, p := range vals {
		if len(p) != 2 {
			return nil, fmt.Errorf("pair %d has %d elements", i, len(p))
		}
		f, err := decodeStr(p[0])
		if err != nil {
			return nil, err
		}
		v, err := decodeStr(p[1])
		if err != nil {
			return nil, err
		}
		out[i] = [2]string{f, v}
	}
	return out, nil
}

type scoredMember struct{ member, score string }

func decodeScored(raw json.RawMessage) ([]scoredMember, error) {
	var vals [][]any
	if err := json.Unmarshal(raw, &vals); err != nil {
		return nil, err
	}
	out := make([]scoredMember, len(vals))
	for i, p := range vals {
		if len(p) != 2 {
			return nil, fmt.Errorf("member %d has %d elements", i, len(p))
		}
		m, err := decodeStr(p[0])
		if err != nil {
			return nil, err
		}
		score, ok := p[1].(float64)
		if !ok {
			return nil, fmt.Errorf("member %d score is not a number", i)
		}
		out[i] = scoredMember{member: m, score: strconv.FormatFloat(score, 'g', -1, 64)}
	}
	return out, nil
}

// windowSlice returns the indices [start, start+count) clamped to n.
func windowSlice(n, start, count int) []int {
	if start >= n {
		return nil
	}
	end := start + count
	if end > n {
		end = n
	}
	out := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, i)
	}
	return out
}

func clampQueryInt(r *http.Request, name string, def, min, max int) int {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
