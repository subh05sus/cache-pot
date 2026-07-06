package server

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/subh05sus/cache-pot/internal/pubsub"
	"github.com/subh05sus/cache-pot/internal/resp"
)

// respWriter is the raw RESP writer passed to writeArray callbacks so command
// handlers can emit nested array elements without importing the resp package.
type respWriter = *resp.Writer

// conn holds per-connection state. Writes are guarded by wmu because pub/sub
// delivery runs on a separate goroutine concurrently with command replies.
type conn struct {
	s   *Server
	nc  net.Conn // nil for virtual connections (AOF replay, dashboard Execute)
	r   *resp.Reader
	w   *resp.Writer
	wmu sync.Mutex

	authed bool

	// Client-registry identity (CLIENT LIST / dashboard Clients page).
	id      uint64
	created time.Time
	kind    string // "tcp", "dashboard", "aof-replay"

	// metamu guards the mutable metadata below, which CLIENT LIST reads from
	// other goroutines while dispatch updates it.
	metamu     sync.Mutex
	name       string
	lastCmd    string
	lastActive time.Time

	// monitoring is set while this connection is in MONITOR mode; monSub is
	// its feed, cleaned up on disconnect.
	monitoring atomic.Bool
	monSub     *monitorSub

	// aofCmds, when set by a handler, replaces what dispatch would log to the
	// AOF for the current command. An empty (non-nil) slice suppresses logging.
	// Reset by dispatchCommand before every command.
	aofCmds [][]string

	submu sync.Mutex
	subs  map[string]*pubsub.Subscription
	psubs map[string]*pubsub.Subscription
}

// noteCommand records the command name and activity time for CLIENT LIST.
func (c *conn) noteCommand(name string) {
	c.metamu.Lock()
	c.lastCmd = name
	c.lastActive = time.Now()
	c.metamu.Unlock()
}

// addr returns the remote address, or the connection kind for virtual conns.
func (c *conn) addr() string {
	if c.nc != nil {
		return c.nc.RemoteAddr().String()
	}
	return c.kind
}

// serveConn runs the read/dispatch loop for a single client connection.
func (s *Server) serveConn(ctx context.Context, nc net.Conn) {
	defer nc.Close()
	s.stats.Connections.Add(1)
	s.stats.TotalConns.Add(1)
	defer s.stats.Connections.Add(-1)

	c := &conn{
		s:       s,
		nc:      nc,
		r:       resp.NewReader(nc),
		w:       resp.NewWriter(nc),
		subs:    make(map[string]*pubsub.Subscription),
		psubs:   make(map[string]*pubsub.Subscription),
		authed:  s.cfg.Password == "",
		kind:    "tcp",
		created: time.Now(),
	}
	s.registerClient(c)
	defer s.deregisterClient(c)
	defer c.unsubscribeAll()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		args, err := c.r.ReadCommand()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				// Malformed input or reset connection: nothing more to do.
			}
			return
		}
		if len(args) == 0 {
			continue
		}
		if err := s.dispatchCommand(c, args); err != nil {
			return // fatal write error
		}
		if err := c.flush(); err != nil {
			return
		}
	}
}

// --- guarded write helpers -------------------------------------------------

func (c *conn) flush() error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.w.Flush()
}

func (c *conn) writeSimple(s string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.w.WriteSimpleString(s)
}

func (c *conn) writeError(msg string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.w.WriteError(msg)
}

func (c *conn) writeInt(n int64) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.w.WriteInteger(n)
}

func (c *conn) writeBulk(s string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.w.WriteBulkString(s)
}

func (c *conn) writeNull() error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.w.WriteNull()
}

func (c *conn) writeStringArray(items []string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.w.WriteStringArray(items)
}

// writeArray runs fn while holding the write lock, after emitting an array
// header of n elements. fn writes the elements using the raw writer.
func (c *conn) writeArray(n int, fn func(w *resp.Writer) error) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.w.WriteArrayHeader(n); err != nil {
		return err
	}
	if fn == nil {
		// An empty array (n == 0) has no elements to emit.
		return nil
	}
	return fn(c.w)
}

func (c *conn) unsubscribeAll() {
	c.submu.Lock()
	defer c.submu.Unlock()
	for ch, sub := range c.subs {
		c.s.broker.Unsubscribe(sub)
		delete(c.subs, ch)
	}
	for p, sub := range c.psubs {
		c.s.broker.Unsubscribe(sub)
		delete(c.psubs, p)
	}
}
