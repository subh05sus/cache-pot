// Package client is a tiny RESP2 client. It exists so the built-in MCP server
// (and tests) can talk to a running Cache-Pot instance over the same wire protocol
// any Redis client uses, rather than reaching into the store directly.
package client

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/subh05sus/cache-pot/internal/resp"
)

// Client is a single synchronous connection to a Cache-Pot/Redis server. It is not
// safe for concurrent use; guard it with a mutex if shared.
type Client struct {
	conn net.Conn
	r    *bufio.Reader
}

// Dial connects to addr (host:port).
func Dial(addr string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, r: bufio.NewReader(conn)}, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// RemoteAddr returns the server address this client is connected to.
func (c *Client) RemoteAddr() string { return c.conn.RemoteAddr().String() }

// Do sends a command as a RESP array of bulk strings and returns the decoded
// reply. The reply is one of: string, int64, nil, []any, or an error value
// representing a RESP error.
func (c *Client) Do(args ...string) (any, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := c.conn.Write([]byte(b.String())); err != nil {
		return nil, err
	}
	return c.readReply()
}

// ReadReply reads one server-pushed reply without sending a command — used
// for MONITOR lines and pub/sub messages.
func (c *Client) ReadReply() (any, error) { return c.readReply() }

// SetReadDeadline bounds how long reads (Do, ReadReply) may block.
func (c *Client) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

func (c *Client) readReply() (any, error) {
	v, err := resp.ReadReply(c.r)
	if err != nil {
		return nil, err
	}
	return valueToAny(v), nil
}

// valueToAny collapses a typed resp.Value into the loose shape this client
// has always returned: string, int64, nil, []any, or error for RESP errors.
func valueToAny(v resp.Value) any {
	switch v.Kind {
	case '+':
		return v.Str
	case '-':
		return fmt.Errorf("%s", v.Str)
	case ':':
		return v.Int
	case '$':
		if v.Null {
			return nil
		}
		return v.Str
	case '*':
		if v.Null {
			return nil
		}
		arr := make([]any, len(v.Array))
		for i, el := range v.Array {
			arr[i] = valueToAny(el)
		}
		return arr
	default:
		return nil
	}
}
