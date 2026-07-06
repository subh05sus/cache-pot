# Cache-Pot Command Reference

This documents every command implemented today. Syntax follows Redis
conventions; arguments in `<>` are required, `[]` optional. Cache-Pot is single
-database (`SELECT 0` only) in this release.

## Connection & server

| Command | Description |
|---|---|
| `PING [msg]` | Returns `PONG`, or echoes `msg`. |
| `ECHO <msg>` | Returns `msg`. |
| `AUTH <password>` | Authenticate when `--auth` is set. |
| `SELECT <index>` | Only index `0` is accepted. |
| `QUIT` | Close the connection. |
| `COMMAND [COUNT]` | Minimal stub for client handshakes. |
| `INFO` | Server, client, keyspace, and cache stats. |
| `DBSIZE` | Number of live keys. |
| `FLUSHDB` / `FLUSHALL` | Remove all keys. |
| `SAVE` / `BGSAVE` | Write a snapshot to disk now. |
| `BGREWRITEAOF` | Compact the append-only file (requires `--aof-path`). |

## Keys

| Command | Description |
|---|---|
| `DEL <key> [key ...]` | Delete keys; returns count removed. |
| `EXISTS <key> [key ...]` | Count of keys that exist. |
| `EXPIRE <key> <seconds>` | Set a TTL in seconds. |
| `PEXPIRE <key> <ms>` | Set a TTL in milliseconds. |
| `EXPIREAT <key> <unix-seconds>` | Expire at an absolute Unix time. |
| `PEXPIREAT <key> <unix-ms>` | Expire at an absolute Unix time in ms. |
| `TTL <key>` | Seconds left (`-1` no TTL, `-2` missing). |
| `PTTL <key>` | Milliseconds left. |
| `PERSIST <key>` | Remove the TTL. |
| `KEYS <pattern>` | Glob-match keys (`*`, `?`, `[...]`). Prefer `SCAN` on big keyspaces. |
| `TYPE <key>` | `string`/`hash`/`list`/`set`/`zset`/`vector`/`none`. |
| `RENAME <key> <newkey>` | Rename a key (TTL survives); error if `key` is missing. |
| `RENAMENX <key> <newkey>` | Rename only if `newkey` does not exist; returns `0`/`1`. |
| `SCAN <cursor> [MATCH pat] [COUNT n] [TYPE t]` | Incremental keyspace iteration; cursor `0` starts and ends a scan. |
| `HSCAN <key> <cursor> [MATCH pat] [COUNT n]` | Iterate a hash's fields and values. |
| `SSCAN <key> <cursor> [MATCH pat] [COUNT n]` | Iterate a set's members. |
| `ZSCAN <key> <cursor> [MATCH pat] [COUNT n]` | Iterate a sorted set's members and scores. |
| `MEMORY USAGE <key> [SAMPLES n]` | Approximate bytes held by the key (heuristic; `SAMPLES` accepted and ignored). |

## Observability

| Command | Description |
|---|---|
| `MONITOR` | Stream every dispatched command to this connection (`QUIT`/`RESET` to exit). AUTH arguments are redacted. |
| `SLOWLOG GET [n]` / `SLOWLOG RESET` / `SLOWLOG LEN` | Log of commands slower than the threshold. |
| `CONFIG GET/SET slowlog-log-slower-than` | Threshold in microseconds; `0` logs everything, `-1` disables timing. |
| `CONFIG GET/SET slowlog-max-len` | How many slow entries to retain (default 128). |
| `CLIENT LIST` | One line per connection: id, addr, name, age, idle, last command. |
| `CLIENT ID` / `CLIENT SETNAME` / `CLIENT GETNAME` | Connection identity. |
| `CLIENT KILL ID <id>` | Close a connection by id; returns the number killed. |
| `RESET` | Leave MONITOR mode and drop this connection's subscriptions. |

## Strings

| Command | Description |
|---|---|
| `SET <key> <value> [EX s\|PX ms] [NX\|XX]` | Set a string with options. |
| `GET <key>` | Get a string. |
| `GETSET <key> <value>` | Set and return the old value. |
| `APPEND <key> <value>` | Append; returns new length. |
| `STRLEN <key>` | Length of the string in bytes; `0` if missing. |
| `GETRANGE <key> <start> <end>` | Substring by inclusive offsets; negative counts from the end. |
| `SETRANGE <key> <offset> <value>` | Overwrite from `offset`, zero-padding past the end; returns new length. |
| `INCR <key>` / `DECR <key>` | ±1 on an integer string. |
| `INCRBY <key> <n>` / `DECRBY <key> <n>` | ±n. |
| `MGET <key> [key ...]` | Multiple gets. |
| `MSET <key> <val> [key val ...]` | Multiple sets. |

## Hashes

`HSET`, `HGET`, `HDEL`, `HGETALL`, `HKEYS`, `HVALS`, `HLEN`, `HEXISTS`, `HMGET`.

```
HSET user:1 name Subh plan pro
HGET user:1 name        # "Subh"
HGETALL user:1
```

## Lists

`LPUSH`, `RPUSH`, `LPOP`, `RPOP`, `LLEN`, `LINDEX`, `LRANGE`.

```
RPUSH q a b c
LRANGE q 0 -1          # a b c
LPOP q                 # a
```

## Sets

`SADD`, `SREM`, `SMEMBERS`, `SISMEMBER`, `SCARD`.

## Sorted sets

`ZADD`, `ZREM`, `ZSCORE`, `ZCARD`, `ZRANGE [WITHSCORES]`, `ZRANGEBYSCORE [WITHSCORES]`.

```
ZADD board 100 alice 250 bob
ZRANGE board 0 -1 WITHSCORES        # alice 100 bob 250
ZRANGEBYSCORE board 150 +inf        # bob
```

## Pub/Sub

`SUBSCRIBE <channel ...>`, `UNSUBSCRIBE [channel ...]`, `PSUBSCRIBE <pattern ...>`,
`PUNSUBSCRIBE [pattern ...]`, `PUBLISH <channel> <message>`.

Patterns use the same glob syntax as `KEYS` (`news:*`). Pattern deliveries
arrive as 4-element `pmessage` replies. A slow subscriber that fills its
buffer drops messages rather than blocking publishers.

## Transactions

| Command | Description |
|---|---|
| `MULTI` | Start a transaction; following commands reply `QUEUED`. |
| `EXEC` | Run the queued commands, returning an array of their replies. |
| `DISCARD` | Drop the queued commands and leave `MULTI`. |
| `WATCH <key ...>` | Watch keys; `EXEC` aborts (returns nil) if any changed. |
| `UNWATCH` | Forget all watched keys. |

A command that can't be queued (unknown, or `SUBSCRIBE`/`MONITOR`) dirties the
transaction, and the following `EXEC` fails with `EXECABORT` and runs nothing.

Isolation is best-effort: each queued command is individually atomic and
`WATCH` detects a concurrent change to a watched key, but `EXEC` does not hold
a global lock, so commands from other connections can interleave between the
queued commands. This matches Cache-Pot's existing multi-key semantics and is
enough for the common watch-and-set (optimistic locking) pattern.

## Vector store

| Command | Description |
|---|---|
| `VSET <coll> <id> <f1 ... fn> [META <text>]` | Insert/replace a vector with optional metadata. |
| `VSEARCH <coll> <f1 ... fn> [TOPK k] [WITHSCORES]` | Cosine top-k search. Returns `id, meta` per hit (plus score with `WITHSCORES`). |
| `VDEL <coll> <id>` | Remove a vector. |
| `VCARD <coll>` | Number of vectors. |
| `VDIM <coll>` | Vector dimension of the collection. |

All vectors in a collection share the dimension fixed by the first `VSET`.

```
VSET docs d1 0.1 0.2 0.9 META "intro"
VSET docs d2 0.9 0.1 0.0 META "pricing"
VSEARCH docs 0.1 0.2 0.85 TOPK 1 WITHSCORES
```

## Semantic cache

Requires an embeddings provider (`CACHEPOT_EMBED_URL`, see the README). Stores and
retrieves responses keyed by prompt *meaning*.

| Command | Description |
|---|---|
| `SCACHE.SET <prompt> <response> [TTL <seconds>]` | Cache a response for a prompt. |
| `SCACHE.GET <prompt> [THRESHOLD <0..1>]` | Return the response if a stored prompt is similar enough (default threshold `0.9`); otherwise nil. |

Hits and misses are counted and surfaced via `INFO` and the dashboard.

### Dollar-savings demo

```
# Suppose each LLM completion costs $0.01.
SCACHE.SET "summarize the BSD-3 license" "<summary text>" TTL 3600
SCACHE.GET "give me a summary of the BSD 3-clause license"   # HIT -> $0.01 saved
```

Track the running hit ratio on the dashboard (`http://localhost:8080`) — every
hit is a model call you didn't pay for.

## Agent memory

| Command | Description |
|---|---|
| `REMEMBER <session> <field> <value>` | Store a fact under a session namespace. |
| `RECALL <session> [field]` | Recall one field, or the whole session. |

Backed by a per-session hash named `mem:<session>`; also exposed as MCP tools.
