package server

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"time"

	"github.com/subh05sus/cache-pot/internal/pubsub"
	"github.com/subh05sus/cache-pot/internal/resp"
)

// executeBlocked lists commands that make no sense on a one-shot virtual
// connection: they either stream indefinitely or manage connection state.
// The dashboard has dedicated pages (Profiler, Pub/Sub) for the streaming
// ones.
var executeBlocked = map[string]bool{
	"MONITOR":      true,
	"SUBSCRIBE":    true,
	"UNSUBSCRIBE":  true,
	"PSUBSCRIBE":   true,
	"PUNSUBSCRIBE": true,
	"QUIT":         true,
	"RESET":        true,
}

// Execute runs one command in-process on a virtual connection and returns
// the decoded reply. It goes through the same dispatch path as TCP clients,
// so stats, the slowlog, MONITOR fan-out, and AOF propagation all observe
// the command. The virtual connection is born authenticated: Execute callers
// (the dashboard) are trusted by configuration.
func (s *Server) Execute(args []string) (resp.Value, error) {
	if len(args) == 0 {
		return resp.Value{}, errors.New("empty command")
	}
	name := strings.ToUpper(args[0])
	if executeBlocked[name] {
		return resp.Value{
			Kind: '-',
			Str:  "ERR '" + strings.ToLower(name) + "' is not available here — use the dedicated dashboard page",
		}, nil
	}

	var buf bytes.Buffer
	c := &conn{
		s:       s,
		w:       resp.NewWriter(&buf),
		subs:    make(map[string]*pubsub.Subscription),
		psubs:   make(map[string]*pubsub.Subscription),
		authed:  true,
		kind:    "dashboard",
		created: time.Now(),
	}
	if err := s.dispatchCommand(c, args); err != nil {
		return resp.Value{}, err
	}
	if err := c.flush(); err != nil {
		return resp.Value{}, err
	}
	return resp.ReadReply(bufio.NewReader(&buf))
}
