// Package resp implements a minimal RESP2 (REdis Serialization Protocol v2)
// reader and writer. It supports the five RESP2 types: simple strings,
// errors, integers, bulk strings and arrays. RESP3 is intentionally out of
// scope for V1 (see PRD §9).
package resp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// ErrInvalidSyntax is returned when the byte stream is not valid RESP2.
var ErrInvalidSyntax = errors.New("resp: invalid syntax")

// maxBulkLen caps a single bulk string at 512 MB, matching Redis' proto limit.
const maxBulkLen = 512 * 1024 * 1024

// Reader parses RESP2 values from a buffered stream.
type Reader struct {
	r *bufio.Reader
}

// NewReader wraps rd in a RESP2 reader.
func NewReader(rd io.Reader) *Reader {
	return &Reader{r: bufio.NewReader(rd)}
}

// ReadCommand reads a single client command. Clients send commands as RESP
// arrays of bulk strings, but the original "inline" protocol (space-separated
// words terminated by CRLF) is also accepted so that a raw `telnet`/`nc`
// session and some tooling work. The returned slice holds the command name and
// its arguments as raw strings.
func (r *Reader) ReadCommand() ([]string, error) {
	b, err := r.r.ReadByte()
	if err != nil {
		return nil, err
	}
	if b == '*' {
		return r.readArrayCommand()
	}
	// Inline command: put the byte back and read a whole line.
	if err := r.r.UnreadByte(); err != nil {
		return nil, err
	}
	return r.readInlineCommand()
}

func (r *Reader) readArrayCommand() ([]string, error) {
	n, err := r.readInteger()
	if err != nil {
		return nil, err
	}
	if n < 0 {
		return nil, nil // null array; treated as no command
	}
	args := make([]string, 0, n)
	for i := int64(0); i < n; i++ {
		typ, err := r.r.ReadByte()
		if err != nil {
			return nil, err
		}
		if typ != '$' {
			return nil, fmt.Errorf("%w: expected bulk string, got %q", ErrInvalidSyntax, typ)
		}
		s, err := r.readBulkString()
		if err != nil {
			return nil, err
		}
		args = append(args, s)
	}
	return args, nil
}

func (r *Reader) readInlineCommand() ([]string, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	return splitInline(line), nil
}

// splitInline splits an inline command on runs of ASCII whitespace.
func splitInline(line string) []string {
	var out []string
	start := -1
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == ' ' || c == '\t' {
			if start >= 0 {
				out = append(out, line[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, line[start:])
	}
	return out
}

// readBulkString reads the length line and body of a bulk string. The leading
// '$' must already have been consumed.
func (r *Reader) readBulkString() (string, error) {
	n, err := r.readInteger()
	if err != nil {
		return "", err
	}
	if n < 0 {
		return "", nil
	}
	if n > maxBulkLen {
		return "", fmt.Errorf("%w: bulk string too long", ErrInvalidSyntax)
	}
	buf := make([]byte, n+2) // include trailing CRLF
	if _, err := io.ReadFull(r.r, buf); err != nil {
		return "", err
	}
	if buf[n] != '\r' || buf[n+1] != '\n' {
		return "", fmt.Errorf("%w: bulk string missing CRLF", ErrInvalidSyntax)
	}
	return string(buf[:n]), nil
}

// readInteger reads a number followed by CRLF.
func (r *Reader) readInteger() (int64, error) {
	line, err := r.readLine()
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(line, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: bad integer %q", ErrInvalidSyntax, line)
	}
	return n, nil
}

// readLine reads up to a CRLF and returns the content without the terminator.
func (r *Reader) readLine() (string, error) {
	line, err := r.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return "", fmt.Errorf("%w: line missing CRLF", ErrInvalidSyntax)
	}
	return line[:len(line)-2], nil
}
