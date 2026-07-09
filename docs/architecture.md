# Cache-Pot Architecture

A 5-minute orientation for contributors. Cache-Pot is a single Go binary with no
third-party dependencies; everything below lives under `internal/`.

```
cmd/cache-pot            entrypoint: flag/env config, subcommands (serve, mcp,
                     cli, bench), wiring, graceful shutdown
internal/
  resp               RESP2 reader + writer (the wire protocol)
  client             tiny RESP2 client (MCP bridge, cli/bench subcommands, tests)
  store              the in-memory keyspace: sharded map, all data types, expiry
  vector             cosine-similarity vector index: brute force small, HNSW large
  pubsub             PUBLISH/SUBSCRIBE channel registry
  embed              optional OpenAI-compatible embeddings client
  persist            snapshot save/load
  server             TCP accept loop, per-connection goroutine, command dispatch
                     and every command handler (cmd_*.go)
  mcp                JSON-RPC-over-stdio MCP server
  dashboard          embedded HTML + JSON stats/keys API
```

## Request flow

```
client --TCP--> server.serveConn (1 goroutine/conn)
                   |
                   v
             resp.Reader.ReadCommand        // parse one command (array or inline)
                   |
                   v
             server.dispatchCommand          // map[name]handler lookup, auth gate
                   |
                   v
             cmd_*.go handler                 // validates args, calls store/vector/...
                   |
                   v
             resp.Writer (guarded by conn.wmu) --TCP--> client
```

One goroutine serves each connection (Go's model maps cleanly onto
connection-per-client). Writes are guarded by a per-connection mutex because
pub/sub messages are delivered from a separate goroutine concurrently with
command replies.

## Storage engine (`internal/store`)

- The keyspace is split into **256 shards**, each a `map[string]*entry` behind
  its own `sync.RWMutex`. A key's shard is chosen by `fnv32a(key) % 256`, so
  unrelated keys rarely contend for the same lock. This is the PRD's "sharded
  in-process map"; revisit only if benchmarks show real contention.
- Each `entry` holds a value (`any`, one of the six supported types) and an
  optional `expireAt`.
- **Expiry is two-pronged:** lazy (a key found expired on access is deleted
  immediately) and a background **sweeper** goroutine that periodically reclaims
  keys never read again.
- The clock is injectable (`store.now`) so tests can fast-forward expiry.

Data types and their Go representations:

| Redis type | Go type |
|---|---|
| string | `string` |
| hash | `map[string]string` |
| list | `*list` (slice-backed) |
| set | `map[string]struct{}` |
| sorted set | `*zset` (map + sort-on-read) |
| vector collection | `*vector.Collection` |

## Vector index (`internal/vector`)

A **flat** index: all vectors in a collection share a dimension (fixed by the
first insert), and search is brute-force cosine similarity over every vector,
sorted by score. Simple and dependency-free for V1. The `Collection` API is
designed so an HNSW implementation can replace it later without changing
callers (PRD V2/V3).

## Semantic cache

The semantic cache is sugar over the vector index plus the embeddings client.
`SCACHE.SET` embeds the prompt and stores the vector in an internal collection
(`__scache__`) with the response and expiry in its metadata. `SCACHE.GET`
embeds the query, takes the top-1 nearest neighbour, and returns the cached
response when the cosine score clears the threshold. Because it's a normal
collection, it is snapshotted and visible like any other key.

## Persistence (`internal/persist`)

Two mechanisms, both optional and independently configurable:

**Snapshots** — the whole keyspace is exported to a serialisable
`[]store.Record`, gob-encoded, and written atomically (temp file + rename). It
runs on an interval and once more on graceful shutdown (SIGINT/SIGTERM), and
is loaded on startup.

**Append-only file** (`--aof-path`) — every write command is appended to a log
as a RESP array and replayed through the normal dispatch table on startup, so
a crash loses at most one fsync interval (`--aof-fsync always|everysec|no`).
Relative TTLs (`EXPIRE`, `SET ... EX`) are logged as absolute `PEXPIREAT`
deadlines so replay does not depend on the wall clock, and `SCACHE.SET` is
logged as its resulting `VSET` so replay never re-calls the embeddings
endpoint. A truncated tail from a mid-write crash is detected and compacted
away. When both mechanisms are on and the AOF is non-empty, the AOF is the
authoritative dataset at startup. `BGREWRITEAOF` compacts the log to the
minimal command set for the live keyspace; a compaction also runs on graceful
shutdown.

## What's intentionally absent

Clustering, replication, Lua scripting, multi-database, and RESP3 — all
deferred per the PRD's V1 non-goals. Keeping the surface small is what makes the
agent-native layer (vectors, semantic cache, MCP) shippable solo.
