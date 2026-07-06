package server

import (
	"strings"
	"testing"
)

func TestExecuteVisibleToTCPClients(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()

	// Reach the server the test client is talking to.
	srv := testServerOf(t, cli)

	v, err := srv.Execute([]string{"SET", "exec-key", "exec-val"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != '+' || v.Str != "OK" {
		t.Fatalf("Execute SET reply = %+v", v)
	}
	if r := mustDo(t, cli, "GET", "exec-key"); r != "exec-val" {
		t.Fatalf("GET over TCP = %v", r)
	}

	v, err = srv.Execute([]string{"LPUSH", "exec-list", "a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != ':' || v.Int != 2 {
		t.Fatalf("Execute LPUSH reply = %+v", v)
	}
}

func TestExecuteBlocklist(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	srv := testServerOf(t, cli)

	for _, cmd := range []string{"MONITOR", "SUBSCRIBE", "PSUBSCRIBE", "QUIT", "RESET"} {
		v, err := srv.Execute([]string{cmd, "x"})
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		if v.Kind != '-' || !strings.Contains(v.Str, strings.ToLower(cmd)) {
			t.Fatalf("Execute %s = %+v, want error mentioning it", cmd, v)
		}
	}
}

func TestExecuteErrorReply(t *testing.T) {
	cli, cleanup := startTestServer(t)
	defer cleanup()
	srv := testServerOf(t, cli)

	v, err := srv.Execute([]string{"NOSUCHCMD"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != '-' || !strings.Contains(v.Str, "unknown command") {
		t.Fatalf("Execute unknown = %+v", v)
	}
}

// testServerOf digs the *Server out of the test harness by dialing its stats:
// startTestServer does not return it, so tests reconstruct via a tiny
// side-channel — the client knows the address, and every test server is
// registered here at construction.
func testServerOf(t *testing.T, cli interface{ RemoteAddr() string }) *Server {
	t.Helper()
	testServersMu.Lock()
	defer testServersMu.Unlock()
	srv, ok := testServers[cli.RemoteAddr()]
	if !ok {
		t.Fatalf("no test server registered for %s", cli.RemoteAddr())
	}
	return srv
}
