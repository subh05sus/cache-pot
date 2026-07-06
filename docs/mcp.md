# Using Cache-Pot as an MCP tool

Cache-Pot ships a built-in [Model Context Protocol](https://modelcontextprotocol.io)
server so Claude (Desktop or Code) and other MCP-aware agents can use it
directly — no custom wrapper service.

The MCP bridge is a subcommand that speaks JSON-RPC 2.0 over stdio and talks to
a running Cache-Pot instance over the normal RESP protocol:

```
cache-pot mcp --addr localhost:6379
```

## Tools exposed

| Tool | Arguments | Maps to |
|---|---|---|
| `get` | `key` | `GET` |
| `set` | `key`, `value`, `ttl_seconds?` | `SET ... [EX]` |
| `search` | `prompt`, `threshold?` | `SCACHE.GET` (semantic cache) |
| `remember` | `session`, `key`, `value` | `REMEMBER` |
| `recall` | `session`, `key?` | `RECALL` |

> `search` requires the Cache-Pot server to have an embeddings provider configured
> (`CACHEPOT_EMBED_URL`). See the README's Configuration section.

## Setup

### 1. Start Cache-Pot

```bash
cache-pot            # listens on :6379
```

### 2a. Claude Desktop

Edit your `claude_desktop_config.json` (macOS:
`~/Library/Application Support/Claude/`, Windows: `%APPDATA%\Claude\`):

```json
{
  "mcpServers": {
    "cache-pot": {
      "command": "cache-pot",
      "args": ["mcp", "--addr", "localhost:6379"]
    }
  }
}
```

Restart Claude Desktop. The `cache-pot` tools appear in the tools menu.

### 2b. Claude Code

```bash
claude mcp add cache-pot -- cache-pot mcp --addr localhost:6379
```

Then in a session:

```
> set the key "deploy:status" to "green"
> what's stored at deploy:status?
> remember in session "proj-x" that the owner is Subh
```

## Verifying the bridge manually

You can drive it without an MCP client to confirm it works:

```bash
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | cache-pot mcp --addr localhost:6379
```

You should see an `initialize` result followed by the tool catalogue.

## Notes

- If `cache-pot` isn't on your `PATH`, use an absolute path in `command`.
- The bridge holds a single connection to Cache-Pot and serialises tool calls over
  it; run multiple bridges if you need concurrency.
- stdout carries the JSON-RPC stream; diagnostics go to stderr, so don't print
  to stdout from hooks that wrap the command.
