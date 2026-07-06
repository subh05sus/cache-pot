// Package mcp implements a minimal Model Context Protocol server over stdio so
// that Claude (Desktop or Code) and other MCP-aware agents can use Cache-Pot
// directly as a tool — no custom wrapper service (PRD §7, §15). It speaks
// newline-delimited JSON-RPC 2.0 on stdin/stdout and talks to a running Cache-Pot
// instance over RESP via the client package.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"

	"github.com/subh05sus/cache-pot/internal/client"
)

// protocolVersion is the MCP revision this server implements.
const protocolVersion = "2024-11-05"

// Server bridges MCP tool calls to Cache-Pot commands.
type Server struct {
	cli    *client.Client
	mu     sync.Mutex // serialises access to the single RESP connection
	out    *bufio.Writer
	outMu  sync.Mutex
	logger *os.File
}

// Serve connects to the Cache-Pot instance at addr and serves MCP over the given
// streams (normally os.Stdin/os.Stdout) until in reaches EOF.
func Serve(addr string, in io.Reader, out io.Writer) error {
	cli, err := client.Dial(addr)
	if err != nil {
		return fmt.Errorf("connect to Cache-Pot at %s: %w", addr, err)
	}
	defer cli.Close()

	s := &Server{cli: cli, out: bufio.NewWriter(out), logger: os.Stderr}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		s.handleLine(line)
	}
	return scanner.Err()
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleLine(line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.send(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	// Notifications (no id) require no response.
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		s.reply(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "cache-pot", "version": "0.1.0"},
		})
	case "notifications/initialized":
		// no-op
	case "ping":
		s.reply(req.ID, map[string]any{})
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": toolDefs})
	case "tools/call":
		s.handleToolCall(req.ID, req.Params)
	default:
		if !isNotification {
			s.send(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}})
		}
	}
}

func (s *Server) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return
	}
	s.send(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) send(resp rpcResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(s.logger, "cache-pot-mcp: marshal error: %v\n", err)
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Write(b)
	s.out.WriteByte('\n')
	s.out.Flush()
}

// do runs a Cache-Pot command on the shared connection.
func (s *Server) do(args ...string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cli.Do(args...)
}

// replyToText renders a RESP reply as human/agent-readable text.
func replyToText(v any) string {
	switch t := v.(type) {
	case nil:
		return "(nil)"
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case error:
		return "error: " + t.Error()
	case []any:
		s := ""
		for i, e := range t {
			if i > 0 {
				s += "\n"
			}
			s += replyToText(e)
		}
		if s == "" {
			return "(empty)"
		}
		return s
	default:
		return fmt.Sprintf("%v", t)
	}
}
