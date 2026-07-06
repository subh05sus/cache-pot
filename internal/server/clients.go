package server

import (
	"sync"
	"time"
)

// clientRegistry tracks live connections for CLIENT LIST / CLIENT KILL and
// the dashboard's Clients page.
type clientRegistry struct {
	mu     sync.Mutex
	nextID uint64
	conns  map[uint64]*conn
}

func newClientRegistry() *clientRegistry {
	return &clientRegistry{conns: make(map[uint64]*conn)}
}

// registerClient assigns the connection an id and adds it to the registry.
func (s *Server) registerClient(c *conn) {
	r := s.clientReg
	r.mu.Lock()
	r.nextID++
	c.id = r.nextID
	r.conns[c.id] = c
	r.mu.Unlock()
}

// deregisterClient removes the connection and releases its monitor feed.
func (s *Server) deregisterClient(c *conn) {
	r := s.clientReg
	r.mu.Lock()
	delete(r.conns, c.id)
	r.mu.Unlock()
	if c.monSub != nil {
		s.monitor.unsubscribe(c.monSub)
		c.monSub = nil
	}
}

// ClientInfo is a point-in-time view of one connection, exported for the
// dashboard.
type ClientInfo struct {
	ID       uint64 `json:"id"`
	Addr     string `json:"addr"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	AgeSecs  int64  `json:"age_secs"`
	IdleSecs int64  `json:"idle_secs"`
	LastCmd  string `json:"last_cmd"`
	Subs     int    `json:"subs"`
	Monitor  bool   `json:"monitor"`
}

// ClientsSnapshot returns the current connections ordered by id.
func (s *Server) ClientsSnapshot() []ClientInfo {
	r := s.clientReg
	r.mu.Lock()
	conns := make([]*conn, 0, len(r.conns))
	for _, c := range r.conns {
		conns = append(conns, c)
	}
	r.mu.Unlock()

	now := time.Now()
	out := make([]ClientInfo, 0, len(conns))
	for _, c := range conns {
		c.metamu.Lock()
		name, lastCmd, lastActive := c.name, c.lastCmd, c.lastActive
		c.metamu.Unlock()
		c.submu.Lock()
		subs := len(c.subs) + len(c.psubs)
		c.submu.Unlock()
		idle := int64(0)
		if !lastActive.IsZero() {
			idle = int64(now.Sub(lastActive).Seconds())
		}
		out = append(out, ClientInfo{
			ID:       c.id,
			Addr:     c.addr(),
			Name:     name,
			Kind:     c.kind,
			AgeSecs:  int64(now.Sub(c.created).Seconds()),
			IdleSecs: idle,
			LastCmd:  lastCmd,
			Subs:     subs,
			Monitor:  c.monitoring.Load(),
		})
	}
	// Registry map order is random; sort by id for stable output.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// clientByID returns the live connection with the given id.
func (s *Server) clientByID(id uint64) (*conn, bool) {
	r := s.clientReg
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.conns[id]
	return c, ok
}
