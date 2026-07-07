<p align="center">
  <img src="https://res.cloudinary.com/dmwytfweq/image/upload/v1783326877/ChatGPT_Image_Jul_6_2026_02_04_19_PM_lptjgg.png" alt="Cache-Pot" width="100%">
</p>

<h1 align="center">Cache-Pot</h1>

<p align="center"><b>In-memory, Redis-compatible, and built for AI. Runs from a single binary.</b></p>

<p align="center">
  <img src="https://img.shields.io/badge/status-V1%20%2B%20V2%20preview-orange" alt="status">
  <img src="https://img.shields.io/badge/go-1.25-00ADD8" alt="go">
  <img src="https://img.shields.io/badge/license-BSD--3--Clause-blue" alt="license">
</p>

<p align="center">
  <a href="https://github.com/subh05sus/cache-pot/stargazers"><img src="https://img.shields.io/github/stars/subh05sus/cache-pot?style=social" alt="stars"></a>
  <a href="https://github.com/subh05sus/cache-pot/labels/good%20first%20issue"><img src="https://img.shields.io/github/issues/subh05sus/cache-pot/good%20first%20issue?color=7057ff&label=good%20first%20issues" alt="good first issues"></a>
  <a href="https://github.com/subh05sus/cache-pot/issues"><img src="https://img.shields.io/github/issues/subh05sus/cache-pot?color=008672" alt="open issues"></a>
  <a href="https://github.com/subh05sus/cache-pot/graphs/contributors"><img src="https://img.shields.io/github/contributors/subh05sus/cache-pot?color=orange" alt="contributors"></a>
  <a href="CONTRIBUTING.md"><img src="https://img.shields.io/badge/PRs-welcome-brightgreen" alt="PRs welcome"></a>
</p>

Cache-Pot is an in-memory data store in the Redis mould, reworked around the way AI apps and agents actually use a cache.

Under the hood it wears three hats:

- **A Redis-compatible cache.** It talks RESP2 on the wire, so the Redis client you already have — and the code around it — keeps working untouched.
- **A vector and semantic layer.** Store vectors and search them by nearest neighbour, or cache model answers by meaning: ask something close to a question you asked before and Cache-Pot hands back the earlier answer instead of paying for another model call.
- **A native MCP endpoint.** Agents such as Claude can read, write, search, and remember through Cache-Pot as a first-class tool — no adapter layer in between.

> Redis grew up serving app servers. Cache-Pot is aimed squarely at AI agents.

## Should you reach for this instead of Redis?

For a wide slice of everyday work, yes. Anywhere you lean on Redis (or Valkey) as a cache or a plain key/value store, you can repoint the app at Cache-Pot and carry on — the RESP2 protocol is the same one your client already speaks.

What sets it apart:

- Vector search and a semantic cache ship in the box; on Redis those mean bolting on a module or writing extra glue.
- An MCP server is built in, so agents pick it up as a tool with zero setup.
- The whole thing is one self-contained binary — nothing else to install, quick to grab and run.

And the honest limits, so there are no surprises:

- No clustering, replication, or failover.
- Not hand-tuned to win a raw-throughput race against Redis or Valkey.

Read that as: a strong Redis-style cache and AI data layer for a single machine — not a stand-in for a large production Redis cluster.

## Getting started

Grab [Go](https://go.dev/dl/) 1.25 or newer, then pick whichever of the three below suits you.

### Option 1: install with Go

```bash
go install github.com/subh05sus/cache-pot/cmd/cache-pot@latest
cache-pot
```

### Option 2: run with Docker

```bash
docker run -p 6379:6379 -p 8080:8080 ghcr.io/subh05sus/cache-pot:latest
```

### Option 3: build from source

```bash
git clone https://github.com/subh05sus/cache-pot
cd Cache-Pot
go run ./cmd/cache-pot
```

On startup the log prints:

```
cache-pot: listening on [::]:6379
cache-pot: dashboard on http://localhost:8080
```

Done — the store answers on port 6379 and a live dashboard sits at http://localhost:8080.

## Working with it

### Speak Redis to it

Have `redis-cli` around? Point it at port 6379 and go:

```bash
redis-cli -p 6379
```

```
> SET hello world
OK
> GET hello
"world"
> EXPIRE hello 60
(integer) 1
```

Already running an app on Redis? Aim it here — typically a single line changes:

```bash
export REDIS_URL=redis://localhost:6379
```

### Everyday commands

Same shapes you know from Redis:

```
SET user:1 "Subh"          store a value
GET user:1                  read it back
INCR visits                 count something
HSET person name Subh      store fields under one key
RPUSH queue job1 job2       a list
SADD tags go ai             a set
ZADD board 100 alice        a ranked list
SUBSCRIBE news              listen for messages
PUBLISH news "hello"        send a message
```

Complete reference with examples: [docs/commands.md](docs/commands.md).

### Vector search

Save vectors, then pull back the nearest matches. Your app supplies the numbers directly — no API key in the loop.

```
VSET docs d1 0.1 0.2 0.9 META "intro page"
VSET docs d2 0.9 0.1 0.0 META "pricing page"
VSEARCH docs 0.1 0.2 0.85 TOPK 1 WITHSCORES
```

### Semantic cache (trim your model bill)

Store a model's answer once. When a close-enough question shows up later, Cache-Pot returns the stored answer rather than billing you for a fresh call.

```
SCACHE.SET "What is the capital of France?" "Paris"
SCACHE.GET "whats the capital of france" THRESHOLD 0.9
> "Paris"
```

You'll need an embeddings provider for this — a free local [Ollama](https://ollama.com) works, and so does OpenAI. See [Configuration](#configuration).

### Agent memory

Give an agent a place to keep things between turns:

```
REMEMBER session7 user_name Subh
RECALL session7 user_name
> "Subh"
```

### Wire it into Claude (MCP)

Launch Cache-Pot, drop this into your Claude config, and Claude gains it as a tool:

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

Full walkthrough: [docs/mcp.md](docs/mcp.md).

### The console (dashboard)

With Cache-Pot running, browse to http://localhost:8080 for a complete management console — no build step, no external assets, the whole thing baked into the binary:

- **Overview** — live stat tiles and five-minute charts (commands/sec, memory, keys, clients).
- **Browser** — search and page through keys (flat or namespace tree), inspect and edit every type, set TTLs, rename, delete, create.
- **Workbench** — a CLI in the browser with history and inline command help.
- **Profiler** — a live MONITOR-style stream of every command the server runs.
- **SlowLog** — commands slower than a configurable threshold.
- **Pub/Sub** — subscribe to channels or patterns and publish, live.
- **Analysis** — memory by type and namespace, TTL distribution, largest keys.
- **Clients** — every connection, with a kill switch.

Keys and values that aren't safe to print land as hex instead of getting mangled. Shut the console off with `--dashboard-addr ""`.

## Cache-Pot vs Redis vs Valkey

| | Cache-Pot | Redis | Valkey |
|---|---|---|---|
| Redis protocol (RESP2) | Yes | Yes | Yes |
| Works with existing Redis clients | Yes (common commands) | Yes | Yes |
| One single binary, no setup | Yes | No | No |
| Vector search built in | Yes | Needs a module | Needs a module |
| Semantic cache command | Yes | No | No |
| MCP server for AI agents | Yes | No | No |
| Clustering and replication | Not yet | Yes | Yes |
| Best raw speed on one node | Good | Best | Best |
| License | BSD-3-Clause | AGPL / RSAL (since 2024) | BSD-3-Clause |

## Configuration

Each flag mirrors a `CACHEPOT_*` environment variable.

| Flag | Env var | Default | What it does |
|---|---|---|---|
| `--addr` | `CACHEPOT_ADDR` | `:6379` | Port to listen on |
| `--auth` | `CACHEPOT_AUTH` | empty | Require a password (empty means no password) |
| `--snapshot-path` | `CACHEPOT_SNAPSHOT_PATH` | `cache-pot.snapshot` | Where to save data (empty turns saving off) |
| `--snapshot-interval` | `CACHEPOT_SNAPSHOT_INTERVAL` | `60s` | How often to save to disk |
| `--aof-path` | `CACHEPOT_AOF_PATH` | empty | Append-only file: log every write and replay on restart (empty turns it off) |
| `--aof-fsync` | `CACHEPOT_AOF_FSYNC` | `everysec` | How often to fsync the AOF: `always`, `everysec` or `no` |
| `--dashboard-addr` | `CACHEPOT_DASHBOARD_ADDR` | `:8080` | Dashboard port (empty turns it off) |
| `CACHEPOT_EMBED_URL` | `CACHEPOT_EMBED_URL` | empty | Embeddings endpoint for the semantic cache |
| `CACHEPOT_EMBED_MODEL` | `CACHEPOT_EMBED_MODEL` | `text-embedding-3-small` | Which embedding model to use |
| `CACHEPOT_EMBED_KEY` | `CACHEPOT_EMBED_KEY` | empty | API key for the embeddings endpoint |

Spin up the semantic cache for free against a local Ollama:

```bash
ollama pull nomic-embed-text
export CACHEPOT_EMBED_URL=http://localhost:11434/v1/embeddings
export CACHEPOT_EMBED_MODEL=nomic-embed-text
cache-pot
```

Or point it at OpenAI:

```bash
export CACHEPOT_EMBED_URL=https://api.openai.com/v1/embeddings
export CACHEPOT_EMBED_MODEL=text-embedding-3-small
export CACHEPOT_EMBED_KEY=sk-your-key
cache-pot
```

## Roadmap

- Shipped — core Redis commands: strings, hashes, lists, sets, sorted sets, expiry, pub/sub, snapshot saving.
- Shipped — vector store, semantic cache, agent memory, MCP server, dashboard.
- Shipped — append-only-file durability (`--aof-path`, crash-safe writes with `BGREWRITEAOF` compaction).
- Shipped — transactions (`MULTI`/`EXEC`/`DISCARD`/`WATCH`) and incremental iteration (`SCAN`/`HSCAN`/`SSCAN`/`ZSCAN`).
- On deck — replication, clustering, a faster vector index (HNSW).

## Support the project

Cache-Pot is free and open source, and that isn't going to change. If it's saved you time, or you'd like to help fund the work, you can back it directly — it makes a real difference to how fast the project moves.

<p align="center">
  <a href="https://wise.com/pay/me/subhadips25">
    <img src="https://img.shields.io/badge/Support%20Cache-Pot%20via%20Wise-9FE870?style=for-the-badge&logo=wise&logoColor=163300&labelColor=9FE870" alt="Support Cache-Pot via Wise">
  </a>
</p>

Can't chip in right now? A star, a share, or a solid bug report counts for just as much. Thank you.

## Contributing

The project is open source and still young, so this is a rare moment where a single contribution really moves the needle. Newcomers are welcome — no Go wizardry required.

Easy ways to pitch in:

- Run it, then report bugs or anything that felt confusing.
- Sharpen the docs or add examples.
- Fill in a missing Redis command.
- Try Cache-Pot with your Redis client of choice and let us know how it went.

### Good first issues

Just arrived? These are small, self-contained, and spelled out. Grab one, comment to claim it, and you're rolling.

- [**Good first issues**](https://github.com/subh05sus/cache-pot/labels/good%20first%20issue) beginner-friendly, clearly scoped tasks.
- [**Help wanted**](https://github.com/subh05sus/cache-pot/labels/help%20wanted) things we would love a hand with.
- [**All open issues**](https://github.com/subh05sus/cache-pot/issues) the full list.

Most open issues are missing Redis commands, each with the exact files and acceptance criteria already laid out — copy an existing handler as your template and you can open a PR the same day.

From there, the [Contributing Guide](CONTRIBUTING.md) walks you through the rest. Unsure where to start? Open an issue and say hi. Stars and shares go a long way too.

## License

[BSD-3-Clause](LICENSE) — the same permissive family Redis shipped under before 2024, and the license Valkey runs on today. Simple, permissive, no fine print.
