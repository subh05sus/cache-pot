package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/subh05sus/cache-pot/internal/client"
	"github.com/subh05sus/cache-pot/internal/persist"
	"github.com/subh05sus/cache-pot/internal/store"
)

// startAOFServer boots a server logging to the AOF at path.
func startAOFServer(t *testing.T, st *store.Store, path string) (*client.Client, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	aof, err := persist.OpenAOF(path, persist.SyncNo)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, Config{Addr: addr, AOF: aof})
	if _, err := srv.LoadAOF(); err != nil {
		t.Fatalf("LoadAOF: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.ListenAndServe(ctx)

	var cli *client.Client
	for i := 0; i < 50; i++ {
		if cli, err = client.Dial(addr); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	return cli, func() { cli.Close(); cancel(); aof.Close() }
}

// replayInto replays the AOF at path into a fresh server/store and returns
// the store.
func replayInto(t *testing.T, path string) *store.Store {
	t.Helper()
	aof, err := persist.OpenAOF(path, persist.SyncNo)
	if err != nil {
		t.Fatal(err)
	}
	defer aof.Close()
	st := store.New()
	srv := New(st, Config{AOF: aof})
	if _, err := srv.LoadAOF(); err != nil {
		t.Fatalf("replay LoadAOF: %v", err)
	}
	return st
}

func TestAOFRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	cli, cleanup := startAOFServer(t, store.New(), path)

	mustDo(t, cli, "SET", "s", "hello")
	mustDo(t, cli, "INCR", "n")
	mustDo(t, cli, "INCRBY", "n", "41")
	mustDo(t, cli, "HSET", "h", "f", "v")
	mustDo(t, cli, "RPUSH", "l", "a", "b", "c")
	mustDo(t, cli, "LPOP", "l")
	mustDo(t, cli, "SADD", "st", "m1", "m2")
	mustDo(t, cli, "ZADD", "z", "1.5", "one")
	mustDo(t, cli, "VSET", "vec", "id1", "0.1", "0.2", "0.3", "META", "m")
	mustDo(t, cli, "SET", "gone", "x")
	mustDo(t, cli, "DEL", "gone")
	cleanup()

	st := replayInto(t, path)

	if v, ok, _ := st.Get("s"); !ok || v != "hello" {
		t.Fatalf("s = %q, %v", v, ok)
	}
	if v, ok, _ := st.Get("n"); !ok || v != "42" {
		t.Fatalf("n = %q, %v", v, ok)
	}
	if v, ok, _ := st.HGet("h", "f"); !ok || v != "v" {
		t.Fatalf("h.f = %q, %v", v, ok)
	}
	if got := st.Type("l"); got != "list" {
		t.Fatalf("type l = %q", got)
	}
	if got := st.Type("st"); got != "set" {
		t.Fatalf("type st = %q", got)
	}
	if got := st.Type("z"); got != "zset" {
		t.Fatalf("type z = %q", got)
	}
	if got := st.Type("vec"); got != "vector" {
		t.Fatalf("type vec = %q", got)
	}
	if n := st.Exists("gone"); n != 0 {
		t.Fatalf("deleted key survived replay")
	}
}

func TestAOFExpiryIsAbsolute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	cli, cleanup := startAOFServer(t, store.New(), path)

	// A TTL that will have passed by the time we replay must not resurrect
	// the key, and a long TTL must survive with a deadline, not a fresh timer.
	mustDo(t, cli, "SET", "short", "x", "PX", "50")
	mustDo(t, cli, "SET", "long", "y", "EX", "3600")
	mustDo(t, cli, "SET", "manual", "z")
	mustDo(t, cli, "EXPIRE", "manual", "3600")
	cleanup()

	time.Sleep(80 * time.Millisecond)
	st := replayInto(t, path)

	if n := st.Exists("short"); n != 0 {
		t.Fatal("expired key resurrected by AOF replay")
	}
	if d, hasTTL, ok := st.TTL("long"); !ok || !hasTTL || d > time.Hour || d < 55*time.Minute {
		t.Fatalf("long TTL after replay = %v (hasTTL=%v ok=%v)", d, hasTTL, ok)
	}
	if _, hasTTL, ok := st.TTL("manual"); !ok || !hasTTL {
		t.Fatal("EXPIRE was not preserved as an absolute deadline")
	}
}

func TestAOFFailedNXNotLogged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	cli, cleanup := startAOFServer(t, store.New(), path)

	mustDo(t, cli, "SET", "k", "first")
	// NX with EX fails: must log nothing, or replay would set a TTL on k.
	if r, err := cli.Do("SET", "k", "second", "NX", "EX", "3600"); err != nil || r != nil {
		t.Fatalf("SET NX on existing key = %v, %v", r, err)
	}
	cleanup()

	st := replayInto(t, path)
	if v, ok, _ := st.Get("k"); !ok || v != "first" {
		t.Fatalf("k = %q, %v", v, ok)
	}
	if _, hasTTL, _ := st.TTL("k"); hasTTL {
		t.Fatal("failed SET NX EX leaked a TTL into the AOF")
	}
}

func TestAOFCorruptTailRecovered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	cli, cleanup := startAOFServer(t, store.New(), path)
	mustDo(t, cli, "SET", "a", "1")
	mustDo(t, cli, "SET", "b", "2")
	cleanup()

	// Simulate a crash mid-append: a dangling partial record at the tail.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("*3\r\n$3\r\nSET\r\n$1\r\nc")
	f.Close()

	st := replayInto(t, path)
	if v, ok, _ := st.Get("a"); !ok || v != "1" {
		t.Fatalf("a = %q, %v", v, ok)
	}
	if v, ok, _ := st.Get("b"); !ok || v != "2" {
		t.Fatalf("b = %q, %v", v, ok)
	}
	if n := st.Exists("c"); n != 0 {
		t.Fatal("partial record was applied")
	}
	// The corrupt tail must have been compacted away: a second replay of the
	// repaired file sees the same two keys.
	st2 := replayInto(t, path)
	if st2.DBSize() != 2 {
		t.Fatalf("repaired AOF replays %d keys, want 2", st2.DBSize())
	}
}

func TestAOFLogsTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	cli, cleanup := startAOFServer(t, store.New(), path)

	mustDo(t, cli, "MULTI")
	mustDo(t, cli, "SET", "a", "1")
	mustDo(t, cli, "INCR", "a")
	mustDo(t, cli, "SET", "b", "2")
	mustDo(t, cli, "EXEC")
	cleanup()

	st := replayInto(t, path)
	if v, ok, _ := st.Get("a"); !ok || v != "2" {
		t.Fatalf("a = %q, %v (EXEC writes not logged?)", v, ok)
	}
	if v, ok, _ := st.Get("b"); !ok || v != "2" {
		t.Fatalf("b = %q, %v", v, ok)
	}
}

func TestAOFRewriteCompacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	st := store.New()
	cli, cleanup := startAOFServer(t, st, path)

	for i := 0; i < 100; i++ {
		mustDo(t, cli, "SET", "k", "v") // 100 writes, 1 live key
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if r := mustDo(t, cli, "BGREWRITEAOF"); r != "OK" {
		t.Fatalf("BGREWRITEAOF = %v", r)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("rewrite did not shrink the log: %d -> %d bytes", before.Size(), after.Size())
	}

	// Appends after a rewrite must land in the new file.
	mustDo(t, cli, "SET", "post", "rewrite")
	cleanup()

	st2 := replayInto(t, path)
	if v, ok, _ := st2.Get("k"); !ok || v != "v" {
		t.Fatalf("k = %q, %v", v, ok)
	}
	if v, ok, _ := st2.Get("post"); !ok || v != "rewrite" {
		t.Fatalf("post = %q, %v", v, ok)
	}
}
