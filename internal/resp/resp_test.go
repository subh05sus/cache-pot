package resp

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestReadCommandArray(t *testing.T) {
	in := "*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nbar\r\n"
	got, err := NewReader(strings.NewReader(in)).ReadCommand()
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	want := []string{"SET", "foo", "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestReadCommandInline(t *testing.T) {
	got, err := NewReader(strings.NewReader("PING\r\n")).ReadCommand()
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"PING"}) {
		t.Fatalf("got %v", got)
	}
}

func TestReadCommandInlineArgs(t *testing.T) {
	got, err := NewReader(strings.NewReader("SET   foo bar\r\n")).ReadCommand()
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"SET", "foo", "bar"}) {
		t.Fatalf("got %v", got)
	}
}

func TestReadEmptyBulk(t *testing.T) {
	got, err := NewReader(strings.NewReader("*1\r\n$0\r\n\r\n")).ReadCommand()
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	if !reflect.DeepEqual(got, []string{""}) {
		t.Fatalf("got %v", got)
	}
}

func TestWriterRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.WriteSimpleString("OK")
	w.WriteInteger(42)
	w.WriteBulkString("hi")
	w.WriteNull()
	w.WriteStringArray([]string{"a", "b"})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "+OK\r\n:42\r\n$2\r\nhi\r\n$-1\r\n*2\r\n$1\r\na\r\n$1\r\nb\r\n"
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}
