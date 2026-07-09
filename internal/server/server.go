// Package server is Cache-Pot's RESP2 TCP server: it accepts connections, parses
// commands, dispatches them against the store, and writes replies. One
// goroutine serves each connection, which maps cleanly onto Go's concurrency
// model (PRD §9).
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/subh05sus/cache-pot/internal/embed"
	"github.com/subh05sus/cache-pot/internal/persist"
	"github.com/subh05sus/cache-pot/internal/pubsub"
	"github.com/subh05sus/cache-pot/internal/resp"
	"github.com/subh05sus/cache-pot/internal/store"
)

// Config configures a Server.
type Config struct {
	Addr        string // listen address, e.g. ":6379"
	Password    string // optional AUTH password ("" disables auth)
	TLSCert     string // PEM certificate path; enables TLS when set with TLSKey
	TLSKey      string // PEM private-key path
	Snapshotter *persist.Snapshotter
	AOF         *persist.AOF // optional append-only file (nil disables it)
	Embed       *embed.Client
}

// Server holds shared state for all connections.
type Server struct {
	cfg     Config
	store   *store.Store
	broker  *pubsub.Broker
	stats   *Stats
	started time.Time

	clientReg *clientRegistry
	slowlog   *slowlog
	monitor   *monitorHub

	ln       net.Listener
	wg       sync.WaitGroup
	dispatch map[string]handler
}

// handler executes one command. It returns a non-nil error only on a fatal I/O
// problem (e.g. the connection died); command-level failures are written to
// the client as RESP errors.
type handler func(c *conn, args []string) error

// New builds a Server over the given store.
func New(s *store.Store, cfg Config) *Server {
	srv := &Server{
		cfg:       cfg,
		store:     s,
		broker:    pubsub.NewBroker(store.MatchPattern),
		stats:     &Stats{},
		started:   time.Now(),
		clientReg: newClientRegistry(),
		slowlog:   newSlowlog(),
		monitor:   newMonitorHub(),
	}
	srv.registerCommands()
	return srv
}

// Store exposes the underlying keyspace (used by the dashboard).
func (s *Server) Store() *store.Store { return s.store }

// Stats exposes server counters (used by the dashboard).
func (s *Server) Stats() *Stats { return s.stats }

// Uptime returns how long the server has been running.
func (s *Server) Uptime() time.Duration { return time.Since(s.started) }

// ListenAndServe binds the configured address and serves connections until ctx
// is cancelled. When TLSCert and TLSKey are set, the listener speaks TLS.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.Addr, err)
	}
	proto := "plaintext"
	if s.cfg.TLSCert != "" || s.cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(s.cfg.TLSCert, s.cfg.TLSKey)
		if err != nil {
			ln.Close()
			return fmt.Errorf("load tls keypair: %w", err)
		}
		ln = tls.NewListener(ln, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
		proto = "TLS"
	}
	s.ln = ln
	fmt.Printf("cache-pot: listening on %s (%s)\n", ln.Addr(), proto)

	go func() {
		<-ctx.Done()
		ln.Close() // unblocks Accept
	}()

	for {
		nc, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.wg.Wait()
				return nil // clean shutdown
			default:
				fmt.Fprintf(os.Stderr, "cache-pot: accept error: %v\n", err)
				continue
			}
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveConn(ctx, nc)
		}()
	}
}

// dispatchCommand looks up and runs a command by name, with three cheap
// observation hooks: client metadata (CLIENT LIST), the monitor fan-out (one
// atomic load when idle), and the slowlog timer (skipped entirely when the
// threshold is -1).
func (s *Server) dispatchCommand(c *conn, args []string) error {
	if len(args) == 0 {
		return nil
	}
	s.stats.Commands.Add(1)
	name := strings.ToUpper(args[0])
	c.noteCommand(name)

	// A monitoring connection accepts nothing but QUIT and RESET.
	if c.monitoring.Load() && name != "QUIT" && name != "RESET" {
		return c.writeError("ERR only QUIT and RESET are allowed while MONITOR is active")
	}

	// When not authenticated, only AUTH (and QUIT) are permitted.
	if s.cfg.Password != "" && !c.authed && name != "AUTH" && name != "QUIT" {
		return c.writeError("NOAUTH Authentication required.")
	}

	// Fan out to MONITOR consumers before execution; unknown commands appear
	// too, matching Redis. Monitoring connections' own commands are not echoed.
	if s.monitor.active.Load() && !c.monitoring.Load() {
		s.publishMonitor(c, name, args)
	}

	// Transaction control commands are handled inline; they are never queued.
	switch name {
	case "MULTI":
		return c.cmdMulti(args)
	case "EXEC":
		return c.cmdExec(args)
	case "DISCARD":
		return c.cmdDiscard(args)
	case "WATCH":
		return c.cmdWatch(args)
	case "UNWATCH":
		return c.cmdUnwatch(args)
	}

	h, ok := s.dispatch[name]
	if !ok {
		if c.inMulti {
			c.multiErr = true // dirty the transaction so EXEC aborts
		}
		return c.writeError(fmt.Sprintf("ERR unknown command '%s'", args[0]))
	}

	// Inside MULTI, queue the command instead of running it. RESET and QUIT
	// still take effect immediately, matching Redis.
	if c.inMulti && name != "RESET" && name != "QUIT" {
		return c.queueCommand(name, args)
	}

	return s.runHandler(c, name, args, h)
}

// runHandler executes one command handler with the slowlog timer, watch
// notification, and AOF propagation. It is shared by normal dispatch and by
// EXEC replaying queued commands.
func (s *Server) runHandler(c *conn, name string, args []string, h handler) error {
	c.aofCmds = nil

	thresholdUs := s.slowlog.thresholdUs.Load()
	var start time.Time
	if thresholdUs >= 0 {
		start = time.Now()
	}
	err := h(c, args)
	if thresholdUs >= 0 {
		if elapsed := time.Since(start); elapsed.Microseconds() >= thresholdUs {
			c.metamu.Lock()
			clientName := c.name
			c.metamu.Unlock()
			s.slowlog.record(args, elapsed, c.addr(), clientName)
		}
	}
	if err == nil {
		s.notifyWatch(name, args)
		s.propagateAOF(c, name, args)
	}
	return err
}

// notifyWatch bumps the modification version of every key a write command
// touched, so any transaction watching one of them aborts at EXEC. It is a
// single atomic load when no connection is watching anything.
func (s *Server) notifyWatch(name string, args []string) {
	if !writeCommands[name] {
		return
	}
	switch name {
	case "FLUSHDB", "FLUSHALL":
		// store.Flush already bumps every watched key; nothing to do here.
	case "MSET":
		for i := 1; i+1 < len(args); i += 2 {
			s.store.Modified(args[i])
		}
	case "DEL":
		for _, k := range args[1:] {
			s.store.Modified(k)
		}
	case "RENAME", "RENAMENX":
		if len(args) == 3 {
			s.store.Modified(args[1])
			s.store.Modified(args[2])
		}
	case "REMEMBER":
		if len(args) >= 2 {
			s.store.Modified("mem:" + args[1])
		}
	default:
		if len(args) >= 2 {
			s.store.Modified(args[1])
		}
	}
}

// writeCommands lists the commands that mutate the keyspace and replay
// deterministically as sent, so they are appended to the AOF verbatim.
// Absent by design: SCACHE.SET (would re-call the embeddings endpoint on
// replay; its handler propagates the resulting VSET instead) and SAVE/BGSAVE
// (no keyspace effect).
var writeCommands = map[string]bool{
	"SET": true, "GETSET": true, "APPEND": true, "SETRANGE": true,
	"INCR": true, "DECR": true, "INCRBY": true, "DECRBY": true, "MSET": true,
	"DEL": true, "PERSIST": true, "FLUSHDB": true, "FLUSHALL": true,
	"RENAME": true, "RENAMENX": true,
	"EXPIRE": true, "PEXPIRE": true, "EXPIREAT": true, "PEXPIREAT": true,
	"HSET": true, "HDEL": true,
	"LPUSH": true, "RPUSH": true, "LPOP": true, "RPOP": true,
	"SADD": true, "SREM": true,
	"ZADD": true, "ZREM": true,
	"VSET": true, "VDEL": true,
	"REMEMBER": true,
}

// propagateAOF appends the just-executed command to the AOF. A handler can
// override what gets logged by setting c.aofCmds (an empty slice suppresses
// logging entirely); otherwise write commands are logged as received, with
// relative-TTL commands translated to absolute PEXPIREAT so replay does not
// depend on the wall clock.
func (s *Server) propagateAOF(c *conn, name string, args []string) {
	a := s.cfg.AOF
	if a == nil || !a.Active() {
		return
	}
	cmds := c.aofCmds
	if cmds == nil {
		if !writeCommands[name] {
			return
		}
		switch name {
		case "EXPIRE", "PEXPIRE":
			if len(args) != 3 {
				return
			}
			n, err := strconv.ParseInt(args[2], 10, 64)
			if err != nil {
				return // the handler already rejected it
			}
			unit := time.Second
			if name == "PEXPIRE" {
				unit = time.Millisecond
			}
			at := time.Now().Add(time.Duration(n) * unit).UnixMilli()
			cmds = [][]string{{"PEXPIREAT", args[1], strconv.FormatInt(at, 10)}}
		default:
			cmds = [][]string{args}
		}
	}
	for _, cmd := range cmds {
		if err := a.Append(cmd); err != nil {
			fmt.Fprintf(os.Stderr, "cache-pot: aof append failed: %v\n", err)
			return
		}
	}
}

// LoadAOF replays the append-only file through the normal dispatch table and
// then activates logging. If the store was pre-loaded from a snapshot and the
// AOF is empty, the file is seeded from the current keyspace instead. A
// corrupt tail (crash mid-write) is repaired by compacting the file after
// replaying the intact prefix. It returns the number of commands replayed.
func (s *Server) LoadAOF() (int, error) {
	a := s.cfg.AOF
	if a == nil {
		return 0, nil
	}
	c := &conn{
		s:       s,
		w:       resp.NewWriter(io.Discard),
		subs:    make(map[string]*pubsub.Subscription),
		psubs:   make(map[string]*pubsub.Subscription),
		authed:  true,
		kind:    "aof-replay",
		created: time.Now(),
	}
	n, corrupt, err := a.Replay(func(args []string) {
		if h, ok := s.dispatch[strings.ToUpper(args[0])]; ok {
			h(c, args)
			c.flush()
		}
	})
	if err != nil {
		return n, err
	}
	if corrupt {
		fmt.Fprintln(os.Stderr, "cache-pot: aof has a truncated tail (crash mid-write?); compacting")
		if err := a.Rewrite(s.store.Export()); err != nil {
			return n, fmt.Errorf("compact aof: %w", err)
		}
	}
	if n == 0 && s.store.DBSize() > 0 {
		// First run with AOF on top of an existing snapshot: seed the log.
		if err := a.Rewrite(s.store.Export()); err != nil {
			return n, fmt.Errorf("seed aof: %w", err)
		}
	}
	a.SetActive(true)
	return n, nil
}
