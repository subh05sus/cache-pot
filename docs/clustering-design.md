# Clustering & Sharding — Design Notes

**Status: proposal, not yet implemented.** This document scopes how Cache-Pot
could grow from a single node into a horizontally-sharded cluster. It exists so
the work can be reviewed and staged before any code lands. Nothing here ships in
the current binary.

## Goal

Let a dataset larger than one machine's memory — or a request rate larger than
one machine's CPU — spread across several Cache-Pot nodes, while an unmodified
Redis-cluster-aware client keeps working. Reuse Redis Cluster's wire conventions
so existing client libraries need no Cache-Pot-specific code.

Non-goals for the first cut: cross-slot transactions, automatic resharding under
load, and multi-region topologies.

## Where we start from

The server today has **no** node identity, replication, or key routing (see
`internal/server`). The only existing "sharding" is 256 in-process mutex shards
in the store for lock granularity — unrelated to cluster sharding. So this is
greenfield; the pieces below are all new.

## Slot model

Adopt Redis Cluster's fixed **16384 hash slots**. A key maps to a slot by:

```
slot = CRC16(key) mod 16384
```

with hash-tag support: if a key contains `{...}`, only the substring inside the
first `{}` pair is hashed, so `user:{42}:name` and `user:{42}:cart` land on the
same slot and can be operated on together.

Each node owns a contiguous-ish set of slot ranges. A slot map — `slot -> node`
— is replicated to every node and handed to clients via `CLUSTER SLOTS` /
`CLUSTER SHARDS`.

## Request routing

Two options, in order of effort:

1. **Redirection (Redis-compatible, preferred).** A node that receives a command
   for a slot it does not own replies `-MOVED <slot> <host:port>`. During slot
   migration it replies `-ASK` for keys already moved. Cluster-aware clients
   follow these automatically. This keeps nodes stateless about each other's
   data and matches what client libraries already expect.
2. **Proxy fan-out (fallback).** For dumb clients, any node can forward the
   command to the owner and relay the reply. Simpler for users, but adds a hop
   and makes every node a potential bottleneck. Offer later behind a flag.

Start with (1); it is the smaller, more standard change.

## Membership & the slot map

Nodes need to agree on who owns what. Two designs:

- **Gossip (Redis-style).** Each node runs a second binary port (`cluster-port`)
  speaking a compact gossip protocol: heartbeats, node join/leave, slot-ownership
  epochs. Fully decentralized, no external dependency — fits Cache-Pot's
  "single binary, no deps" ethos, but is the most code.
- **Static config / coordinator.** A `--cluster-config nodes.json` file (or a
  small control command) that an operator or orchestrator maintains. Far less
  code; good enough for Kubernetes where the orchestrator already manages
  membership. 

Recommendation: ship the **static config** path first (unblocks real multi-node
use with modest code), design the gossip protocol as a follow-up once the slot
map, routing, and migration are proven.

## Slot migration (resharding)

Moving slot `S` from node A to node B, online:

1. Mark `S` `MIGRATING` on A and `IMPORTING` on B.
2. B pulls keys in `S` from A in batches (a `CLUSTER GETKEYSINSLOT` + dump/restore
   loop).
3. While migrating, A serves reads for keys still present and `-ASK`-redirects
   keys already moved.
4. When `S` is empty on A, bump the slot-map epoch: `S` now owned by B. Propagate.

Dump/restore needs a stable per-key serialization. The snapshot encoder in
`internal/persist` already serializes every value type; factor that into a
per-key `DUMP`/`RESTORE` so migration and persistence share one format.

## Replication (prerequisite for HA, separate epic)

Clustering and replication are orthogonal but usually shipped together: each
slot range should have a primary and ≥1 replica so a node loss doesn't lose data.
Replication is its own design (leader-follower log shipping, failover election)
and is tracked separately. Clustering can land first with primaries only, at the
cost of durability on node loss.

## Command compatibility

- Cross-slot multi-key commands (`MGET a b`, `MSET`, `SINTERSTORE`, …) error with
  `-CROSSSLOT` unless all keys share a slot (hash tags let callers force this).
- `MULTI`/`EXEC` transactions are single-slot only in cluster mode.
- `SELECT` (multiple DBs) is disabled in cluster mode, as in Redis.
- `VSET`/`VSEARCH` and `SCACHE.*` route by key like any other command; a vector
  collection lives entirely on its key's owning node (no cross-node ANN in v1).

## Build sequence

1. `CRC16` + slot function + hash-tag parsing (pure, unit-testable, no I/O).
2. Slot map type + `CLUSTER SLOTS`/`SHARDS`/`KEYSLOT` read-only commands.
3. `-MOVED` redirection on the dispatch path, driven by a static slot map.
4. `--cluster-config` loading and a `cache-pot cluster` admin subcommand.
5. `DUMP`/`RESTORE` refactored out of the snapshot encoder.
6. Online slot migration (`MIGRATING`/`IMPORTING`/`-ASK`).
7. Gossip membership (replaces static config).
8. Replication + failover (separate epic).

Steps 1–3 are independently useful and low-risk; they make a single node
"cluster-aware" and testable before any real multi-node coordination exists.
