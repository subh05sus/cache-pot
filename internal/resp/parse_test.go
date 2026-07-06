package resp

import (
	"bufio"
	"strings"
	"testing"
)

func parse(t *testing.T, wire string) Value {
	t.Helper()
	v, err := ReadReply(bufio.NewReader(strings.NewReader(wire)))
	if err != nil {
		t.Fatalf("%q: %v", wire, err)
	}
	return v
}

func TestReadReply(t *testing.T) {
	if v := parse(t, "+OK\r\n"); v.Kind != '+' || v.Str != "OK" {
		t.Fatalf("simple = %+v", v)
	}
	if v := parse(t, "-ERR boom\r\n"); v.Kind != '-' || v.Str != "ERR boom" {
		t.Fatalf("error = %+v", v)
	}
	if v := parse(t, ":42\r\n"); v.Kind != ':' || v.Int != 42 {
		t.Fatalf("int = %+v", v)
	}
	if v := parse(t, "$5\r\nhello\r\n"); v.Kind != '$' || v.Str != "hello" || v.Null {
		t.Fatalf("bulk = %+v", v)
	}
	if v := parse(t, "$-1\r\n"); v.Kind != '$' || !v.Null {
		t.Fatalf("null bulk = %+v", v)
	}
	if v := parse(t, "*-1\r\n"); v.Kind != '*' || !v.Null {
		t.Fatalf("null array = %+v", v)
	}
	// Bulk strings may contain CRLF bytes; the length prefix governs.
	if v := parse(t, "$7\r\na\r\nb\r\nc\r\n"); v.Str != "a\r\nb\r\nc" {
		t.Fatalf("embedded crlf bulk = %+v", v)
	}
	v := parse(t, "*3\r\n$3\r\nfoo\r\n:7\r\n*1\r\n+PONG\r\n")
	if v.Kind != '*' || len(v.Array) != 3 {
		t.Fatalf("array = %+v", v)
	}
	if v.Array[0].Str != "foo" || v.Array[1].Int != 7 {
		t.Fatalf("array elems = %+v", v.Array)
	}
	if inner := v.Array[2]; inner.Kind != '*' || inner.Array[0].Kind != '+' || inner.Array[0].Str != "PONG" {
		t.Fatalf("nested = %+v", v.Array[2])
	}

	if _, err := ReadReply(bufio.NewReader(strings.NewReader("!bad\r\n"))); err == nil {
		t.Fatal("unknown prefix accepted")
	}
}
