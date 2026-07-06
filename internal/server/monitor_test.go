package server

import (
	"strings"
	"testing"
	"time"

	"github.com/subh05sus/cache-pot/internal/client"
)

// clientDialSame opens a second connection to the server cli is connected to.
func clientDialSame(cli *client.Client) (*client.Client, error) {
	return client.Dial(cli.RemoteAddr())
}

func TestMonitorStreamsCommands(t *testing.T) {
	mon, cleanup := startTestServer(t)
	defer cleanup()
	worker, err := clientDialSame(mon)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	if r := mustDo(t, mon, "MONITOR"); r != "OK" {
		t.Fatalf("MONITOR = %v", r)
	}
	mustDo(t, worker, "SET", "foo", "bar")

	mon.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := mon.ReadReply()
	if err != nil {
		t.Fatalf("read monitor line: %v", err)
	}
	s, ok := line.(string)
	if !ok || !strings.Contains(s, `"SET" "foo" "bar"`) {
		t.Fatalf("monitor line = %#v", line)
	}
	if !strings.Contains(s, "[0 ") {
		t.Fatalf("monitor line missing db/addr section: %q", s)
	}
}

func TestMonitorRejectsCommands(t *testing.T) {
	mon, cleanup := startTestServer(t)
	defer cleanup()

	mustDo(t, mon, "MONITOR")
	r, err := mon.Do("GET", "x")
	if err != nil {
		t.Fatal(err)
	}
	e, ok := r.(error)
	if !ok || !strings.Contains(e.Error(), "MONITOR") {
		t.Fatalf("GET while monitoring = %#v, want error", r)
	}
	// RESET exits monitor mode; normal commands work again.
	if r := mustDo(t, mon, "RESET"); r != "RESET" {
		t.Fatalf("RESET = %v", r)
	}
	mustDo(t, mon, "SET", "x", "1")
}

func TestMonitorRedactsAuth(t *testing.T) {
	mon, cleanup := startTestServer(t)
	defer cleanup()
	worker, err := clientDialSame(mon)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	mustDo(t, mon, "MONITOR")
	worker.Do("AUTH", "hunter2") // errors (no password set) but still dispatched

	mon.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := mon.ReadReply()
	if err != nil {
		t.Fatal(err)
	}
	s := line.(string)
	if strings.Contains(s, "hunter2") {
		t.Fatalf("monitor leaked AUTH credential: %q", s)
	}
	if !strings.Contains(s, `"AUTH" "****"`) {
		t.Fatalf("monitor line = %q, want redacted AUTH", s)
	}
}

func TestSlowlogThresholdZeroLogsEverything(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	mustDo(t, cli, "CONFIG", "SET", "slowlog-log-slower-than", "0")
	mustDo(t, cli, "SET", "a", "1")
	mustDo(t, cli, "GET", "a")

	r := mustDo(t, cli, "SLOWLOG", "LEN")
	if n := r.(int64); n < 2 {
		t.Fatalf("SLOWLOG LEN = %d, want >= 2", n)
	}

	r = mustDo(t, cli, "SLOWLOG", "GET", "1")
	entries := r.([]any)
	if len(entries) != 1 {
		t.Fatalf("SLOWLOG GET 1 = %d entries", len(entries))
	}
	entry := entries[0].([]any)
	if len(entry) != 6 {
		t.Fatalf("slowlog entry has %d fields, want 6", len(entry))
	}
	if _, ok := entry[0].(int64); !ok {
		t.Fatalf("entry id = %#v", entry[0])
	}
	args := entry[3].([]any)
	if len(args) == 0 {
		t.Fatal("entry args empty")
	}

	mustDo(t, cli, "SLOWLOG", "RESET")
	// RESET itself gets logged (threshold 0), so LEN is 0 or 1.
	if n := mustDo(t, cli, "SLOWLOG", "LEN").(int64); n > 1 {
		t.Fatalf("SLOWLOG LEN after reset = %d", n)
	}

	// Restore threshold and confirm CONFIG GET round-trips.
	mustDo(t, cli, "CONFIG", "SET", "slowlog-log-slower-than", "10000")
	r = mustDo(t, cli, "CONFIG", "GET", "slowlog-*")
	arr := r.([]any)
	if len(arr) != 4 {
		t.Fatalf("CONFIG GET slowlog-* = %v", arr)
	}
}

func TestClientCommands(t *testing.T) {
	a, cleanup := startTestServer(t)
	defer cleanup()
	b, err := clientDialSame(a)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	mustDo(t, a, "CLIENT", "SETNAME", "conn-a")
	if r := mustDo(t, a, "CLIENT", "GETNAME"); r != "conn-a" {
		t.Fatalf("GETNAME = %v", r)
	}
	idA := mustDo(t, a, "CLIENT", "ID").(int64)
	idB := mustDo(t, b, "CLIENT", "ID").(int64)
	if idA == idB || idA == 0 || idB == 0 {
		t.Fatalf("client ids = %d, %d", idA, idB)
	}

	list := mustDo(t, a, "CLIENT", "LIST").(string)
	if !strings.Contains(list, "name=conn-a") {
		t.Fatalf("CLIENT LIST missing name: %q", list)
	}
	if got := strings.Count(list, "id="); got != 2 {
		t.Fatalf("CLIENT LIST shows %d clients, want 2", got)
	}

	if r := mustDo(t, a, "CLIENT", "KILL", "ID", "999999"); r != int64(0) {
		t.Fatalf("KILL missing id = %v", r)
	}
}

func TestPSubscribe(t *testing.T) {
	sub, cleanup := startTestServer(t)
	defer cleanup()
	pub, err := clientDialSame(sub)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	r := mustDo(t, sub, "PSUBSCRIBE", "news:*")
	ack := r.([]any)
	if ack[0] != "psubscribe" || ack[1] != "news:*" || ack[2] != int64(1) {
		t.Fatalf("psubscribe ack = %v", ack)
	}

	// Publish may race the subscription registration; retry until delivered.
	delivered := int64(0)
	for i := 0; i < 50 && delivered == 0; i++ {
		delivered = mustDo(t, pub, "PUBLISH", "news:tech", "hello").(int64)
		if delivered == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if delivered != 1 {
		t.Fatalf("PUBLISH delivered = %d, want 1", delivered)
	}

	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	msg, err := sub.ReadReply()
	if err != nil {
		t.Fatal(err)
	}
	m := msg.([]any)
	if len(m) != 4 || m[0] != "pmessage" || m[1] != "news:*" || m[2] != "news:tech" || m[3] != "hello" {
		t.Fatalf("pmessage = %v", m)
	}
}
