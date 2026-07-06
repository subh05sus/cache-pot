package mcp

import (
	"encoding/json"
	"strconv"
)

// toolDefs is the static tool catalogue returned by tools/list. Schemas use a
// minimal JSON Schema subset that MCP clients understand.
var toolDefs = []map[string]any{
	{
		"name":        "get",
		"description": "Get the string value stored at a key in Cache-Pot. Returns (nil) if the key does not exist.",
		"inputSchema": obj(props{
			"key": strProp("The key to read."),
		}, "key"),
	},
	{
		"name":        "set",
		"description": "Set a string value at a key in Cache-Pot, with an optional TTL in seconds.",
		"inputSchema": obj(props{
			"key":         strProp("The key to write."),
			"value":       strProp("The value to store."),
			"ttl_seconds": numProp("Optional expiry in seconds."),
		}, "key", "value"),
	},
	{
		"name":        "search",
		"description": "Semantic-cache lookup: given a natural-language prompt, return a previously cached response whose prompt was semantically similar (requires the server to have an embeddings provider configured). Returns (nil) on a miss.",
		"inputSchema": obj(props{
			"prompt":    strProp("The natural-language prompt to look up."),
			"threshold": numProp("Optional minimum cosine similarity 0..1 (default 0.9)."),
		}, "prompt"),
	},
	{
		"name":        "remember",
		"description": "Store a fact in a session-scoped agent memory namespace.",
		"inputSchema": obj(props{
			"session": strProp("The session/namespace id."),
			"key":     strProp("The fact's field name."),
			"value":   strProp("The value to remember."),
		}, "session", "key", "value"),
	},
	{
		"name":        "recall",
		"description": "Recall a fact (or all facts) from a session-scoped agent memory namespace.",
		"inputSchema": obj(props{
			"session": strProp("The session/namespace id."),
			"key":     strProp("Optional field name; omit to recall the whole session."),
		}, "session"),
	},
}

func (s *Server) handleToolCall(id json.RawMessage, params json.RawMessage) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		s.reply(id, toolError("invalid tool-call params"))
		return
	}
	text, err := s.runTool(p.Name, p.Arguments)
	if err != nil {
		s.reply(id, toolError(err.Error()))
		return
	}
	s.reply(id, toolText(text))
}

func (s *Server) runTool(name string, a map[string]any) (string, error) {
	switch name {
	case "get":
		r, err := s.do("GET", argStr(a, "key"))
		return replyOrErr(r, err)
	case "set":
		cmd := []string{"SET", argStr(a, "key"), argStr(a, "value")}
		if ttl := argStr(a, "ttl_seconds"); ttl != "" {
			cmd = append(cmd, "EX", ttl)
		}
		r, err := s.do(cmd...)
		return replyOrErr(r, err)
	case "search":
		cmd := []string{"SCACHE.GET", argStr(a, "prompt")}
		if th := argStr(a, "threshold"); th != "" {
			cmd = append(cmd, "THRESHOLD", th)
		}
		r, err := s.do(cmd...)
		return replyOrErr(r, err)
	case "remember":
		r, err := s.do("REMEMBER", argStr(a, "session"), argStr(a, "key"), argStr(a, "value"))
		return replyOrErr(r, err)
	case "recall":
		cmd := []string{"RECALL", argStr(a, "session")}
		if k := argStr(a, "key"); k != "" {
			cmd = append(cmd, k)
		}
		r, err := s.do(cmd...)
		return replyOrErr(r, err)
	default:
		return "", errUnknownTool(name)
	}
}

func replyOrErr(r any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return replyToText(r), nil
}

// --- small helpers for building schemas and replies -----------------------

type props = map[string]any

func obj(p props, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": p}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func numProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}

// argStr coerces an argument to a string ("" if absent), tolerating numbers
// that JSON decoded as float64.
func argStr(a map[string]any, key string) string {
	v, ok := a[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

func toolText(text string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}

func toolError(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

type errUnknownTool string

func (e errUnknownTool) Error() string { return "unknown tool: " + string(e) }
