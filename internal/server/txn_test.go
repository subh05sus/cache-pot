package server

import (
	"testing"

	"github.com/subh05sus/cache-pot/internal/client"
)

func TestMultiExec(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	if r := mustDo(t, cli, "MULTI"); r != "OK" {
		t.Fatalf("MULTI = %v", r)
	}
	if r := mustDo(t, cli, "SET", "k", "v"); r != "QUEUED" {
		t.Fatalf("queued SET = %v", r)
	}
	if r := mustDo(t, cli, "INCR", "n"); r != "QUEUED" {
		t.Fatalf("queued INCR = %v", r)
	}
	r := mustDo(t, cli, "EXEC")
	arr, ok := r.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("EXEC = %#v", r)
	}
	if arr[0] != "OK" || arr[1] != int64(1) {
		t.Fatalf("EXEC replies = %#v", arr)
	}
	// The transaction actually took effect.
	if v := mustDo(t, cli, "GET", "k"); v != "v" {
		t.Fatalf("GET k = %v", v)
	}
}

func TestExecEmpty(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	mustDo(t, cli, "MULTI")
	r := mustDo(t, cli, "EXEC")
	arr, ok := r.([]any)
	if !ok || len(arr) != 0 {
		t.Fatalf("empty EXEC = %#v", r)
	}
}

func TestDiscard(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	mustDo(t, cli, "MULTI")
	mustDo(t, cli, "SET", "k", "queued")
	if r := mustDo(t, cli, "DISCARD"); r != "OK" {
		t.Fatalf("DISCARD = %v", r)
	}
	if r, _ := cli.Do("GET", "k"); r != nil {
		t.Fatalf("key set despite DISCARD: %v", r)
	}
	// EXEC after DISCARD is an error.
	if r, _ := cli.Do("EXEC"); toErr(r) == "" {
		t.Fatalf("EXEC after DISCARD = %v, want error", r)
	}
}

func TestExecAbortOnBadCommand(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	mustDo(t, cli, "MULTI")
	// Unknown command inside MULTI: replies with an error and dirties the txn.
	if r, _ := cli.Do("NOSUCHCMD", "x"); toErr(r) == "" {
		t.Fatalf("bad queued command = %v, want error", r)
	}
	mustDo(t, cli, "SET", "k", "v")
	r, _ := cli.Do("EXEC")
	if e := toErr(r); e == "" || e[:9] != "EXECABORT" {
		t.Fatalf("EXEC = %v, want EXECABORT", r)
	}
	// Nothing ran.
	if r, _ := cli.Do("GET", "k"); r != nil {
		t.Fatalf("aborted txn still wrote: %v", r)
	}
}

func TestWatchAbort(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	other, err := client.Dial(cli.RemoteAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	mustDo(t, cli, "SET", "k", "1")
	mustDo(t, cli, "WATCH", "k")
	mustDo(t, cli, "MULTI")
	mustDo(t, cli, "SET", "k", "from-txn")
	// A second connection modifies the watched key before EXEC.
	mustDo(t, other, "SET", "k", "from-other")

	if r, _ := cli.Do("EXEC"); r != nil {
		t.Fatalf("EXEC should abort (null), got %#v", r)
	}
	// The other connection's write stands; the txn did not run.
	if v := mustDo(t, cli, "GET", "k"); v != "from-other" {
		t.Fatalf("GET k = %v, want from-other", v)
	}
}

func TestWatchNoAbort(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	mustDo(t, cli, "SET", "k", "1")
	mustDo(t, cli, "WATCH", "k")
	mustDo(t, cli, "MULTI")
	mustDo(t, cli, "SET", "k", "2")
	r := mustDo(t, cli, "EXEC")
	arr, ok := r.([]any)
	if !ok || len(arr) != 1 || arr[0] != "OK" {
		t.Fatalf("EXEC = %#v, want [OK]", r)
	}
	if v := mustDo(t, cli, "GET", "k"); v != "2" {
		t.Fatalf("GET k = %v", v)
	}
}

func TestUnwatchClearsGuard(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	other, err := client.Dial(cli.RemoteAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	mustDo(t, cli, "SET", "k", "1")
	mustDo(t, cli, "WATCH", "k")
	mustDo(t, cli, "UNWATCH")
	mustDo(t, other, "SET", "k", "changed") // no longer watched
	mustDo(t, cli, "MULTI")
	mustDo(t, cli, "SET", "k", "2")
	r := mustDo(t, cli, "EXEC")
	if arr, ok := r.([]any); !ok || len(arr) != 1 {
		t.Fatalf("EXEC after UNWATCH = %#v, want it to run", r)
	}
}

func TestExecWithoutMulti(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	if r, _ := cli.Do("EXEC"); toErr(r) == "" {
		t.Fatalf("EXEC without MULTI = %v, want error", r)
	}
	if r, _ := cli.Do("DISCARD"); toErr(r) == "" {
		t.Fatalf("DISCARD without MULTI = %v, want error", r)
	}
}

// toErr returns the message of a RESP error reply, or "" if r is not an error.
func toErr(r any) string {
	if e, ok := r.(error); ok {
		return e.Error()
	}
	return ""
}
