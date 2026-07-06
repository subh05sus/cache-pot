package server

import (
	"fmt"
	"testing"
)

// scanAll drives SCAN through a real RESP client until the cursor returns to
// 0, verifying the uint64 cursor round-trips through the wire protocol.
func scanAll(t *testing.T, cli interface {
	Do(args ...string) (any, error)
}, extra ...string) []string {
	t.Helper()
	var out []string
	cursor := "0"
	for {
		args := append([]string{"SCAN", cursor}, extra...)
		r, err := cli.Do(args...)
		if err != nil {
			t.Fatal(err)
		}
		arr, ok := r.([]any)
		if !ok || len(arr) != 2 {
			t.Fatalf("SCAN reply = %#v", r)
		}
		cursor = arr[0].(string)
		for _, k := range arr[1].([]any) {
			out = append(out, k.(string))
		}
		if cursor == "0" {
			return out
		}
	}
}

func TestScanCommand(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	for i := 0; i < 500; i++ {
		mustDo(t, cli, "SET", fmt.Sprintf("user:%d", i), "v")
	}
	mustDo(t, cli, "HSET", "h1", "f", "v")

	keys := scanAll(t, cli, "COUNT", "50")
	if len(keys) != 501 {
		t.Fatalf("SCAN walked %d keys, want 501", len(keys))
	}

	matched := scanAll(t, cli, "MATCH", "user:*", "COUNT", "50")
	if len(matched) != 500 {
		t.Fatalf("SCAN MATCH user:* = %d keys, want 500", len(matched))
	}

	hashes := scanAll(t, cli, "TYPE", "hash", "COUNT", "50")
	if len(hashes) != 1 || hashes[0] != "h1" {
		t.Fatalf("SCAN TYPE hash = %v", hashes)
	}
}

func TestElementScans(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	mustDo(t, cli, "HSET", "h", "f1", "v1")
	mustDo(t, cli, "HSET", "h", "f2", "v2")
	r := mustDo(t, cli, "HSCAN", "h", "0")
	arr := r.([]any)
	if arr[0].(string) != "0" {
		t.Fatalf("HSCAN cursor = %v", arr[0])
	}
	elems := arr[1].([]any)
	if len(elems) != 4 || elems[0] != "f1" || elems[1] != "v1" {
		t.Fatalf("HSCAN elems = %v", elems)
	}

	mustDo(t, cli, "SADD", "s", "a", "b", "c")
	r = mustDo(t, cli, "SSCAN", "s", "0", "MATCH", "a")
	elems = r.([]any)[1].([]any)
	if len(elems) != 1 || elems[0] != "a" {
		t.Fatalf("SSCAN MATCH a = %v", elems)
	}

	mustDo(t, cli, "ZADD", "z", "1", "one", "2", "two")
	r = mustDo(t, cli, "ZSCAN", "z", "0")
	elems = r.([]any)[1].([]any)
	if len(elems) != 4 || elems[0] != "one" || elems[1] != "1" {
		t.Fatalf("ZSCAN elems = %v", elems)
	}
}

func TestMemoryUsageCommand(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	mustDo(t, cli, "SET", "k", "hello")
	r := mustDo(t, cli, "MEMORY", "USAGE", "k")
	if n, ok := r.(int64); !ok || n <= 0 {
		t.Fatalf("MEMORY USAGE = %v", r)
	}
	if r, _ := cli.Do("MEMORY", "USAGE", "absent"); r != nil {
		t.Fatalf("MEMORY USAGE absent = %v, want nil", r)
	}
	// SAMPLES accepted and ignored.
	r = mustDo(t, cli, "MEMORY", "USAGE", "k", "SAMPLES", "5")
	if n, ok := r.(int64); !ok || n <= 0 {
		t.Fatalf("MEMORY USAGE SAMPLES = %v", r)
	}
}

func TestRenameCommands(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	mustDo(t, cli, "SET", "a", "1")
	if r := mustDo(t, cli, "RENAME", "a", "b"); r != "OK" {
		t.Fatalf("RENAME = %v", r)
	}
	if r := mustDo(t, cli, "GET", "b"); r != "1" {
		t.Fatalf("GET b = %v", r)
	}
	if r, _ := cli.Do("RENAME", "missing", "x"); fmt.Sprint(r) != "ERR no such key" {
		t.Fatalf("RENAME missing = %v", r)
	}
	mustDo(t, cli, "SET", "c", "2")
	if r := mustDo(t, cli, "RENAMENX", "b", "c"); r != int64(0) {
		t.Fatalf("RENAMENX onto existing = %v", r)
	}
	if r := mustDo(t, cli, "RENAMENX", "b", "d"); r != int64(1) {
		t.Fatalf("RENAMENX to fresh = %v", r)
	}
}

func TestConfigStub(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	r := mustDo(t, cli, "CONFIG", "GET", "maxmemory")
	if arr, ok := r.([]any); !ok || len(arr) != 0 {
		t.Fatalf("CONFIG GET unknown = %#v, want empty array", r)
	}
}
