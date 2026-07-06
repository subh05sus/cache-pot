package server

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MonitorEvent is one dispatched command, fanned out to MONITOR connections
// and the dashboard's profiler stream.
type MonitorEvent struct {
	Ts       time.Time `json:"ts"`
	Addr     string    `json:"addr"`
	ClientID uint64    `json:"client_id"`
	Args     []string  `json:"args"`
}

// monitorSub is one consumer of the command stream. Its channel is bounded;
// when the consumer falls behind, events are dropped and counted rather than
// ever blocking command dispatch.
type monitorSub struct {
	ch      chan MonitorEvent
	dropped atomic.Int64
}

// C exposes the event stream for consumers outside this package.
func (m *monitorSub) C() <-chan MonitorEvent { return m.ch }

// Dropped returns how many events were discarded because the consumer was
// too slow.
func (m *monitorSub) Dropped() int64 { return m.dropped.Load() }

// MonitorSub is the exported handle used by the dashboard.
type MonitorSub = monitorSub

const monitorSubBuffer = 1024

// monitorHub fans dispatched commands out to subscribers. The hot-path cost
// with no subscribers is a single atomic load of active.
type monitorHub struct {
	active atomic.Bool
	mu     sync.RWMutex
	subs   map[*monitorSub]struct{}
}

func newMonitorHub() *monitorHub {
	return &monitorHub{subs: make(map[*monitorSub]struct{})}
}

func (h *monitorHub) subscribe() *monitorSub {
	sub := &monitorSub{ch: make(chan MonitorEvent, monitorSubBuffer)}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.active.Store(true)
	h.mu.Unlock()
	return sub
}

func (h *monitorHub) unsubscribe(sub *monitorSub) {
	h.mu.Lock()
	if _, ok := h.subs[sub]; ok {
		delete(h.subs, sub)
		close(sub.ch)
	}
	h.active.Store(len(h.subs) > 0)
	h.mu.Unlock()
}

// publish delivers ev to every subscriber without ever blocking: a full
// buffer increments the subscriber's drop counter instead.
func (h *monitorHub) publish(ev MonitorEvent) {
	h.mu.RLock()
	for sub := range h.subs {
		select {
		case sub.ch <- ev:
		default:
			sub.dropped.Add(1)
		}
	}
	h.mu.RUnlock()
}

// MonitorSubscribe attaches a consumer to the live command stream (used by
// the dashboard profiler). Callers must MonitorUnsubscribe when done.
func (s *Server) MonitorSubscribe() *MonitorSub { return s.monitor.subscribe() }

// MonitorUnsubscribe detaches a consumer and closes its channel.
func (s *Server) MonitorUnsubscribe(sub *MonitorSub) { s.monitor.unsubscribe(sub) }

// MonitorActive reports whether anything is consuming the command stream.
func (s *Server) MonitorActive() bool { return s.monitor.active.Load() }

// publishMonitor emits the command to the hub, redacting AUTH credentials.
// Called from dispatch only after the cheap active check passed.
func (s *Server) publishMonitor(c *conn, name string, args []string) {
	if name == "AUTH" {
		args = []string{args[0], "****"}
	}
	s.monitor.publish(MonitorEvent{
		Ts:       time.Now(),
		Addr:     c.addr(),
		ClientID: c.id,
		Args:     args,
	})
}

// formatMonitorLine renders an event in Redis MONITOR's text format:
// 1339518083.107412 [0 127.0.0.1:60866] "GET" "foo"
func formatMonitorLine(ev MonitorEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d.%06d [0 %s]", ev.Ts.Unix(), ev.Ts.Nanosecond()/1000, ev.Addr)
	for _, a := range ev.Args {
		b.WriteString(" \"")
		for i := 0; i < len(a); i++ {
			switch ch := a[i]; ch {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteByte(ch)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			default:
				b.WriteByte(ch)
			}
		}
		b.WriteByte('"')
	}
	return b.String()
}

// cmdMonitor puts the connection in MONITOR mode: it replies +OK, then
// streams every dispatched command as simple strings until the client
// disconnects or sends RESET. The feed goroutine shares wmu with the read
// loop, the same discipline pub/sub delivery uses.
func (c *conn) cmdMonitor(args []string) error {
	if len(args) != 1 {
		return c.wrongArgs("monitor")
	}
	if c.nc == nil {
		return c.writeError("ERR MONITOR is not available on virtual connections")
	}
	if c.monitoring.Swap(true) {
		return c.writeSimple("OK") // already monitoring
	}
	sub := c.s.monitor.subscribe()
	c.monSub = sub
	if err := c.writeSimple("OK"); err != nil {
		return err
	}
	if err := c.flush(); err != nil {
		return err
	}
	go func() {
		for ev := range sub.ch {
			c.wmu.Lock()
			c.w.WriteSimpleString(formatMonitorLine(ev))
			err := c.w.Flush()
			c.wmu.Unlock()
			if err != nil {
				return // connection died; serveConn's defers clean up
			}
		}
	}()
	return nil
}

// cmdReset implements a minimal RESET: it exits MONITOR mode (its main use
// here) and drops any pub/sub subscriptions, mirroring Redis' connection
// state reset.
func (c *conn) cmdReset(args []string) error {
	if c.monitoring.Swap(false) && c.monSub != nil {
		c.s.monitor.unsubscribe(c.monSub)
		c.monSub = nil
	}
	c.unsubscribeAll()
	c.clearMulti()
	return c.writeSimple("RESET")
}
