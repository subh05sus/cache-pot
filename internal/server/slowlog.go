package server

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Slowlog defaults match Redis: log commands slower than 10ms, keep 128.
const (
	defaultSlowlogThresholdUs = 10_000
	defaultSlowlogMaxLen      = 128
	slowlogMaxArgs            = 32
	slowlogMaxArgLen          = 128
)

// SlowEntry is one logged slow command, exported for the dashboard.
type SlowEntry struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	DurUs  int64     `json:"dur_us"`
	Args   []string  `json:"args"`
	Addr   string    `json:"addr"`
	Client string    `json:"client"`
}

// slowlog is a fixed-capacity log of slow commands. thresholdUs is read on
// every dispatch with a single atomic load; -1 disables timing entirely.
type slowlog struct {
	thresholdUs atomic.Int64
	maxLen      atomic.Int64

	mu      sync.Mutex
	entries []SlowEntry // oldest first
	nextID  int64
}

func newSlowlog() *slowlog {
	sl := &slowlog{}
	sl.thresholdUs.Store(defaultSlowlogThresholdUs)
	sl.maxLen.Store(defaultSlowlogMaxLen)
	return sl
}

// record appends one slow command, trimming args like Redis does and evicting
// the oldest entries beyond maxLen.
func (sl *slowlog) record(args []string, dur time.Duration, addr, clientName string) {
	kept := args
	if len(kept) > slowlogMaxArgs {
		kept = kept[:slowlogMaxArgs]
	}
	cp := make([]string, len(kept))
	for i, a := range kept {
		if len(a) > slowlogMaxArgLen {
			a = a[:slowlogMaxArgLen] + "... (" + strconv.Itoa(len(args[i])-slowlogMaxArgLen) + " more bytes)"
		}
		cp[i] = a
	}
	if len(args) > slowlogMaxArgs {
		cp = append(cp, "... ("+strconv.Itoa(len(args)-slowlogMaxArgs)+" more arguments)")
	}

	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.nextID++
	sl.entries = append(sl.entries, SlowEntry{
		ID:     sl.nextID,
		Time:   time.Now(),
		DurUs:  dur.Microseconds(),
		Args:   cp,
		Addr:   addr,
		Client: clientName,
	})
	if max := int(sl.maxLen.Load()); max >= 0 && len(sl.entries) > max {
		sl.entries = append([]SlowEntry(nil), sl.entries[len(sl.entries)-max:]...)
	}
}

// SlowlogEntries returns up to n entries, newest first (n<=0 means all).
func (s *Server) SlowlogEntries(n int) []SlowEntry {
	sl := s.slowlog
	sl.mu.Lock()
	defer sl.mu.Unlock()
	total := len(sl.entries)
	if n <= 0 || n > total {
		n = total
	}
	out := make([]SlowEntry, n)
	for i := 0; i < n; i++ {
		out[i] = sl.entries[total-1-i]
	}
	return out
}

// SlowlogLen returns the number of logged entries.
func (s *Server) SlowlogLen() int {
	s.slowlog.mu.Lock()
	defer s.slowlog.mu.Unlock()
	return len(s.slowlog.entries)
}

// SlowlogReset clears the log.
func (s *Server) SlowlogReset() {
	s.slowlog.mu.Lock()
	s.slowlog.entries = nil
	s.slowlog.mu.Unlock()
}

// SlowlogThresholdUs returns the current threshold (-1 = disabled).
func (s *Server) SlowlogThresholdUs() int64 { return s.slowlog.thresholdUs.Load() }

// SlowlogSetThresholdUs sets the threshold in microseconds (-1 disables).
func (s *Server) SlowlogSetThresholdUs(us int64) { s.slowlog.thresholdUs.Store(us) }

// SlowlogMaxLen returns the maximum number of retained entries.
func (s *Server) SlowlogMaxLen() int64 { return s.slowlog.maxLen.Load() }

// SlowlogSetMaxLen sets the maximum number of retained entries.
func (s *Server) SlowlogSetMaxLen(n int64) {
	s.slowlog.maxLen.Store(n)
	s.slowlog.mu.Lock()
	if n >= 0 && len(s.slowlog.entries) > int(n) {
		s.slowlog.entries = append([]SlowEntry(nil), s.slowlog.entries[len(s.slowlog.entries)-int(n):]...)
	}
	s.slowlog.mu.Unlock()
}

// cmdSlowlog implements SLOWLOG GET [n] / RESET / LEN. The GET reply mirrors
// Redis 4.0+: id, unix seconds, duration µs, args array, client addr, name.
func (c *conn) cmdSlowlog(args []string) error {
	if len(args) < 2 {
		return c.wrongArgs("slowlog")
	}
	switch strings.ToUpper(args[1]) {
	case "GET":
		n := 10
		if len(args) == 3 {
			v, err := strconv.Atoi(args[2])
			if err != nil {
				return c.writeError("ERR value is not an integer or out of range")
			}
			n = v // -1 means all
		}
		entries := c.s.SlowlogEntries(n)
		return c.writeArray(len(entries), func(w respWriter) error {
			for _, e := range entries {
				if err := w.WriteArrayHeader(6); err != nil {
					return err
				}
				w.WriteInteger(e.ID)
				w.WriteInteger(e.Time.Unix())
				w.WriteInteger(e.DurUs)
				if err := w.WriteStringArray(e.Args); err != nil {
					return err
				}
				w.WriteBulkString(e.Addr)
				if err := w.WriteBulkString(e.Client); err != nil {
					return err
				}
			}
			return nil
		})
	case "RESET":
		c.s.SlowlogReset()
		return c.writeSimple("OK")
	case "LEN":
		return c.writeInt(int64(c.s.SlowlogLen()))
	default:
		return c.writeError("ERR unknown SLOWLOG subcommand '" + args[1] + "'; supported: GET, RESET, LEN")
	}
}
