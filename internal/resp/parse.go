package resp

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Value is a decoded RESP2 reply that preserves the wire type (a simple
// string stays distinguishable from a bulk string, an error from a string),
// which reply renderers like the dashboard workbench need.
type Value struct {
	Kind  byte // '+' simple, '-' error, ':' integer, '$' bulk, '*' array
	Str   string
	Int   int64
	Array []Value
	Null  bool // null bulk string ($-1) or null array (*-1)
}

// ReadReply decodes one RESP2 reply from br.
func ReadReply(br *bufio.Reader) (Value, error) {
	prefix, err := br.ReadByte()
	if err != nil {
		return Value{}, err
	}
	line, err := readLine(br)
	if err != nil {
		return Value{}, err
	}
	switch prefix {
	case '+':
		return Value{Kind: '+', Str: line}, nil
	case '-':
		return Value{Kind: '-', Str: line}, nil
	case ':':
		n, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("resp: bad integer %q", line)
		}
		return Value{Kind: ':', Int: n}, nil
	case '$':
		n, err := strconv.Atoi(line)
		if err != nil {
			return Value{}, fmt.Errorf("resp: bad bulk length %q", line)
		}
		if n < 0 {
			return Value{Kind: '$', Null: true}, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return Value{}, err
		}
		return Value{Kind: '$', Str: string(buf[:n])}, nil
	case '*':
		n, err := strconv.Atoi(line)
		if err != nil {
			return Value{}, fmt.Errorf("resp: bad array length %q", line)
		}
		if n < 0 {
			return Value{Kind: '*', Null: true}, nil
		}
		arr := make([]Value, n)
		for i := 0; i < n; i++ {
			if arr[i], err = ReadReply(br); err != nil {
				return Value{}, err
			}
		}
		return Value{Kind: '*', Array: arr}, nil
	default:
		return Value{}, fmt.Errorf("resp: unknown reply type %q", prefix)
	}
}

func readLine(br *bufio.Reader) (string, error) {
	s, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}
