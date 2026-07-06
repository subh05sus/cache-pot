package dashboard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/subh05sus/cache-pot/internal/server"
	"github.com/subh05sus/cache-pot/internal/store"
)

func newTestDashboard(t *testing.T) *Dashboard {
	t.Helper()
	// The RESP listener is not started; Execute and the store work without it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	srv := server.New(store.New(), server.Config{Addr: addr})
	return New(srv, ":0")
}

func doJSON(t *testing.T, d *Dashboard, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	d.http.Handler.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s: bad JSON response %q: %v", method, path, rec.Body.String(), err)
	}
	return rec, out
}

func TestKeysPagination(t *testing.T) {
	d := newTestDashboard(t)
	for i := 0; i < 250; i++ {
		d.srv.Store().Set(fmt.Sprintf("user:%03d", i), "v", store.SetOptions{})
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 50 {
			t.Fatal("cursor never terminated")
		}
		_, out := doJSON(t, d, "GET", "/api/keys?count=100&cursor="+cursor, nil)
		for _, k := range out["keys"].([]any) {
			key := k.(map[string]any)["key"].(string)
			if seen[key] {
				t.Fatalf("duplicate key %q across pages", key)
			}
			seen[key] = true
		}
		if out["done"].(bool) {
			break
		}
		cursor = out["cursor"].(string)
	}
	if len(seen) != 250 {
		t.Fatalf("paginated %d keys, want 250", len(seen))
	}
}

func TestBinaryKeyEnvelope(t *testing.T) {
	d := newTestDashboard(t)
	binKey := "bin\x00\xff"
	d.srv.Store().Set(binKey, "val\x01\x02", store.SetOptions{})

	_, out := doJSON(t, d, "GET", "/api/keys", nil)
	keys := out["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("keys = %v", keys)
	}
	env, ok := keys[0].(map[string]any)["key"].(map[string]any)
	if !ok {
		t.Fatalf("binary key not enveloped: %#v", keys[0])
	}
	if env["$b64"] == nil || env["len"].(float64) != float64(len(binKey)) {
		t.Fatalf("envelope = %#v", env)
	}

	// The response must not contain a U+FFFD-mangled copy of the key.
	if strings.Contains(fmt.Sprint(out), "�") {
		t.Fatal("response leaked replacement characters")
	}
}

func TestKeyCRUD(t *testing.T) {
	d := newTestDashboard(t)

	// Create a hash with a TTL.
	rec, _ := doJSON(t, d, "POST", "/api/key", map[string]any{
		"key":    "crud:h",
		"type":   "hash",
		"value":  [][]string{{"f1", "v1"}, {"f2", "v2"}},
		"ttl_ms": 60_000,
	})
	if rec.Code != 200 {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body.String())
	}

	_, out := doJSON(t, d, "GET", "/api/key?key=crud:h", nil)
	if out["type"] != "hash" {
		t.Fatalf("type = %v", out["type"])
	}
	if ttl := out["ttl_ms"].(float64); ttl <= 0 || ttl > 60_000 {
		t.Fatalf("ttl_ms = %v", ttl)
	}
	fields := out["value"].(map[string]any)["fields"].([]any)
	if len(fields) != 2 {
		t.Fatalf("fields = %v", fields)
	}

	// PATCH: add a field, then rename the key.
	rec, _ = doJSON(t, d, "PATCH", "/api/key", map[string]any{
		"key": "crud:h", "op": "hset", "field": "f3", "value": "v3",
	})
	if rec.Code != 200 {
		t.Fatalf("PATCH hset = %d", rec.Code)
	}
	rec, _ = doJSON(t, d, "PATCH", "/api/key", map[string]any{
		"key": "crud:h", "op": "rename", "newkey": "crud:renamed",
	})
	if rec.Code != 200 {
		t.Fatalf("PATCH rename = %d", rec.Code)
	}
	_, out = doJSON(t, d, "GET", "/api/key?key=crud:renamed", nil)
	if int(out["value"].(map[string]any)["total"].(float64)) != 3 {
		t.Fatalf("total after hset = %v", out["value"])
	}

	// DELETE.
	_, out = doJSON(t, d, "DELETE", "/api/key?key=crud:renamed", nil)
	if out["deleted"].(float64) != 1 {
		t.Fatalf("deleted = %v", out["deleted"])
	}
	rec, _ = doJSON(t, d, "GET", "/api/key?key=crud:renamed", nil)
	if rec.Code != 404 {
		t.Fatalf("GET after delete = %d", rec.Code)
	}
}

func TestKeyGetWindowing(t *testing.T) {
	d := newTestDashboard(t)
	big := strings.Repeat("x", 100_000)
	d.srv.Store().Set("bigstr", big, store.SetOptions{})

	_, out := doJSON(t, d, "GET", "/api/key?key=bigstr&preview=1024", nil)
	v := out["value"].(map[string]any)
	if !v["truncated"].(bool) {
		t.Fatal("big string not truncated")
	}
	if int(v["total_len"].(float64)) != 100_000 {
		t.Fatalf("total_len = %v", v["total_len"])
	}
	if len(v["data"].(string)) != 1024 {
		t.Fatalf("preview len = %d", len(v["data"].(string)))
	}

	for i := 0; i < 1000; i++ {
		d.srv.Store().RPush("biglist", fmt.Sprintf("item-%04d", i))
	}
	_, out = doJSON(t, d, "GET", "/api/key?key=biglist&start=990&count=100", nil)
	v = out["value"].(map[string]any)
	items := v["items"].([]any)
	if len(items) != 10 || items[0] != "item-0990" {
		t.Fatalf("windowed list = %v", items)
	}
	if int(v["total"].(float64)) != 1000 {
		t.Fatalf("total = %v", v["total"])
	}
}

func TestCLIEndpoint(t *testing.T) {
	d := newTestDashboard(t)

	rec, out := doJSON(t, d, "POST", "/api/cli", map[string]any{"command": `SET greeting "hello world"`})
	if rec.Code != 200 {
		t.Fatalf("cli = %d", rec.Code)
	}
	reply := out["reply"].(map[string]any)
	if reply["type"] != "simple" || reply["value"] != "OK" {
		t.Fatalf("reply = %v", reply)
	}

	_, out = doJSON(t, d, "POST", "/api/cli", map[string]any{"command": "GET greeting"})
	reply = out["reply"].(map[string]any)
	if reply["type"] != "bulk" || reply["value"] != "hello world" {
		t.Fatalf("reply = %v", reply)
	}

	// Blocked command surfaces as a RESP error, not an HTTP failure.
	_, out = doJSON(t, d, "POST", "/api/cli", map[string]any{"command": "SUBSCRIBE news"})
	reply = out["reply"].(map[string]any)
	if reply["type"] != "error" {
		t.Fatalf("blocked reply = %v", reply)
	}
}

func TestTokenizer(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  bool
	}{
		{in: "GET foo", want: []string{"GET", "foo"}},
		{in: `SET k "a b"`, want: []string{"SET", "k", "a b"}},
		{in: `SET k 'it''s'`, err: true},
		{in: `SET k 'it\'s'`, want: []string{"SET", "k", "it's"}},
		{in: `SET k "\x41\x42"`, want: []string{"SET", "k", "AB"}},
		{in: `SET k "tab\there"`, want: []string{"SET", "k", "tab\there"}},
		{in: `SET k "unclosed`, err: true},
		{in: "   ", err: true},
		{in: `PING`, want: []string{"PING"}},
	}
	for _, c := range cases {
		got, err := tokenizeCommand(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%q: want error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%q = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q arg %d = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestStatsStreamSmoke(t *testing.T) {
	d := newTestDashboard(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req := httptest.NewRequest("GET", "/api/stream/stats", nil).WithContext(ctx)
	pr, pw := net.Pipe()
	defer pr.Close()

	rec := &streamRecorder{header: http.Header{}, pw: pw}
	go func() {
		d.http.Handler.ServeHTTP(rec, req)
		pw.Close()
	}()

	br := bufio.NewReader(pr)
	deadline := time.Now().Add(3 * time.Second)
	pr.SetReadDeadline(deadline)
	sawTick := false
	for !sawTick {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
		if strings.HasPrefix(line, "event: tick") {
			sawTick = true
		}
	}
	cancel()
}

// streamRecorder adapts an SSE handler to a net.Pipe so the test can read
// frames as they flush (httptest.ResponseRecorder cannot stream).
type streamRecorder struct {
	header http.Header
	pw     net.Conn
	wrote  bool
}

func (s *streamRecorder) Header() http.Header { return s.header }
func (s *streamRecorder) WriteHeader(int)     { s.wrote = true }
func (s *streamRecorder) Write(b []byte) (int, error) {
	return s.pw.Write(b)
}
func (s *streamRecorder) Flush() {}
