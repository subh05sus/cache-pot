package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/subh05sus/cache-pot/internal/client"
	"github.com/subh05sus/cache-pot/internal/store"
)

// startTestServer boots a server on an ephemeral port and returns a connected
// client plus a cleanup function.
func startTestServer(t *testing.T) (*client.Client, func()) {
	t.Helper()
	// Grab a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := New(store.New(), Config{Addr: addr})
	ctx, cancel := context.WithCancel(context.Background())
	go srv.ListenAndServe(ctx)

	// Wait for the listener to come up.
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
	return cli, func() { cli.Close(); cancel() }
}

func mustDo(t *testing.T, cli *client.Client, args ...string) any {
	t.Helper()
	r, err := cli.Do(args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	if e, ok := r.(error); ok {
		t.Fatalf("%v -> server error: %v", args, e)
	}
	return r
}

func TestCoreCommands(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	if r := mustDo(t, cli, "PING"); r != "PONG" {
		t.Fatalf("PING = %v", r)
	}
	mustDo(t, cli, "SET", "foo", "bar")
	if r := mustDo(t, cli, "GET", "foo"); r != "bar" {
		t.Fatalf("GET = %v", r)
	}
	if r := mustDo(t, cli, "EXISTS", "foo"); r != int64(1) {
		t.Fatalf("EXISTS = %v", r)
	}
	if r := mustDo(t, cli, "INCR", "n"); r != int64(1) {
		t.Fatalf("INCR = %v", r)
	}
	if r := mustDo(t, cli, "DEL", "foo"); r != int64(1) {
		t.Fatalf("DEL = %v", r)
	}
	if r, _ := cli.Do("GET", "foo"); r != nil {
		t.Fatalf("GET after DEL = %v", r)
	}
}

func TestDataStructures(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	mustDo(t, cli, "RPUSH", "l", "a", "b", "c")
	if r := mustDo(t, cli, "LLEN", "l"); r != int64(3) {
		t.Fatalf("LLEN = %v", r)
	}
	r := mustDo(t, cli, "LRANGE", "l", "0", "-1").([]any)
	if len(r) != 3 || r[0] != "a" || r[2] != "c" {
		t.Fatalf("LRANGE = %v", r)
	}

	mustDo(t, cli, "HSET", "h", "f1", "v1", "f2", "v2")
	if r := mustDo(t, cli, "HGET", "h", "f1"); r != "v1" {
		t.Fatalf("HGET = %v", r)
	}

	mustDo(t, cli, "SADD", "s", "x", "y", "x")
	if r := mustDo(t, cli, "SCARD", "s"); r != int64(2) {
		t.Fatalf("SCARD = %v", r)
	}

	mustDo(t, cli, "ZADD", "z", "1", "one", "2", "two")
	zr := mustDo(t, cli, "ZRANGE", "z", "0", "-1").([]any)
	if len(zr) != 2 || zr[0] != "one" || zr[1] != "two" {
		t.Fatalf("ZRANGE = %v", zr)
	}
}

func TestVectorCommands(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	mustDo(t, cli, "VSET", "vc", "a", "1", "0", "0", "META", "alpha")
	mustDo(t, cli, "VSET", "vc", "b", "0", "1", "0", "META", "beta")
	if r := mustDo(t, cli, "VCARD", "vc"); r != int64(2) {
		t.Fatalf("VCARD = %v", r)
	}
	res := mustDo(t, cli, "VSEARCH", "vc", "1", "0", "0", "TOPK", "1").([]any)
	if len(res) != 2 || res[0] != "a" || res[1] != "alpha" {
		t.Fatalf("VSEARCH = %v", res)
	}
}

func TestWrongTypeOverWire(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	mustDo(t, cli, "RPUSH", "l", "a")
	r, err := cli.Do("GET", "l")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(error); !ok {
		t.Fatalf("expected WRONGTYPE error, got %v", r)
	}
}

// TestCommandDoesNotCrash guards against the nil-pointer panic where COMMAND
// and COMMAND DOCS wrote an empty array with a nil callback (issue #1). redis-cli
// issues COMMAND DOCS on connect, which previously killed the server.
func TestCommandDoesNotCrash(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	// Plain COMMAND and COMMAND DOCS must reply with an (empty) array, not panic.
	if r := mustDo(t, cli, "COMMAND"); r == nil {
		t.Fatalf("COMMAND returned nil")
	}
	if r := mustDo(t, cli, "COMMAND", "DOCS"); r == nil {
		t.Fatalf("COMMAND DOCS returned nil")
	}
	// COMMAND COUNT still returns the number of registered commands.
	if r := mustDo(t, cli, "COMMAND", "COUNT"); r == int64(0) {
		t.Fatalf("COMMAND COUNT = %v, want > 0", r)
	}
	// The server must still be alive and serving after those calls.
	if r := mustDo(t, cli, "PING"); r != "PONG" {
		t.Fatalf("PING after COMMAND = %v", r)
	}
}

func TestAgentMemory(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	mustDo(t, cli, "REMEMBER", "sess1", "name", "Subh")
	if r := mustDo(t, cli, "RECALL", "sess1", "name"); r != "Subh" {
		t.Fatalf("RECALL = %v", r)
	}
}

func TestPubSub(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	// SUBSCRIBE should acknowledge with ["subscribe", channel, count].
	r := mustDo(t, cli, "SUBSCRIBE", "ch").([]any)
	if r[0] != "subscribe" || r[1] != "ch" || r[2] != int64(1) {
		t.Fatalf("SUBSCRIBE ack = %v", r)
	}
	// A publisher on a second connection should see one subscriber.
	pub, cleanup2 := startTestServerClientOn(t, cli)
	defer cleanup2()
	if n := mustDo(t, pub, "PUBLISH", "ch", "hello"); n != int64(1) {
		t.Fatalf("PUBLISH delivered to %v subscribers, want 1", n)
	}
}

// startTestServerClientOn opens a second client to the same server the given
// client is connected to.
func startTestServerClientOn(t *testing.T, cli *client.Client) (*client.Client, func()) {
	t.Helper()
	c2, err := client.Dial(cli.RemoteAddr())
	if err != nil {
		t.Fatalf("dial second client: %v", err)
	}
	return c2, func() { c2.Close() }
}

func TestStrLen(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	if r := mustDo(t, cli, "STRLEN", "nope"); r != int64(0) {
		t.Fatalf("STRLEN missing = %v, want 0", r)
	}
	mustDo(t, cli, "SET", "s", "Hello World")
	if r := mustDo(t, cli, "STRLEN", "s"); r != int64(11) {
		t.Fatalf("STRLEN = %v, want 11", r)
	}

	mustDo(t, cli, "RPUSH", "l", "a")
	r, err := cli.Do("STRLEN", "l")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(error); !ok {
		t.Fatalf("STRLEN on a list = %v, want WRONGTYPE error", r)
	}
}

func TestGetRange(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	mustDo(t, cli, "SET", "s", "Hello World")
	if r := mustDo(t, cli, "GETRANGE", "s", "0", "4"); r != "Hello" {
		t.Fatalf("GETRANGE 0 4 = %v, want Hello", r)
	}
	if r := mustDo(t, cli, "GETRANGE", "s", "-5", "-1"); r != "World" {
		t.Fatalf("GETRANGE -5 -1 = %v, want World", r)
	}
	if r := mustDo(t, cli, "GETRANGE", "s", "0", "-1"); r != "Hello World" {
		t.Fatalf("GETRANGE 0 -1 = %v, want Hello World", r)
	}
	if r := mustDo(t, cli, "GETRANGE", "nope", "0", "10"); r != "" {
		t.Fatalf("GETRANGE missing = %v, want empty", r)
	}
	if r := mustDo(t, cli, "GETRANGE", "s", "0", "-12"); r != "H" {
		t.Fatalf("GETRANGE 0 -12 = %v, want H", r)
	}
}

func TestSetRange(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	mustDo(t, cli, "SET", "s", "Hello World")
	if r := mustDo(t, cli, "SETRANGE", "s", "6", "Redis"); r != int64(11) {
		t.Fatalf("SETRANGE = %v, want 11", r)
	}
	if r := mustDo(t, cli, "GET", "s"); r != "Hello Redis" {
		t.Fatalf("GET after SETRANGE = %v, want Hello Redis", r)
	}

	if r := mustDo(t, cli, "SETRANGE", "pad", "5", "hi"); r != int64(7) {
		t.Fatalf("SETRANGE pad = %v, want 7", r)
	}
	if r := mustDo(t, cli, "GET", "pad"); r != "\x00\x00\x00\x00\x00hi" {
		t.Fatalf("GET pad = %q, want 5 zero bytes then hi", r)
	}

	r, err := cli.Do("SETRANGE", "big", "9223372036854775807", "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(error); !ok {
		t.Fatalf("SETRANGE huge offset = %v, want error", r)
	}
	if r := mustDo(t, cli, "PING"); r != "PONG" {
		t.Fatalf("PING after huge SETRANGE = %v, server may have crashed", r)
	}
}
