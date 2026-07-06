// Package store is Cache-Pot's in-memory keyspace. Keys are spread across a fixed
// number of shards, each guarded by its own RWMutex, so unrelated keys rarely
// contend for the same lock. Expiry is handled both lazily (on access) and by
// a background sweep (see expiry.go). The PRD calls for a "sharded in-process
// map" and to "revisit only if benchmarks show real lock contention" (§9).
package store

import (
	"hash/fnv"
	"sync"
	"time"

	"github.com/subh05sus/cache-pot/internal/vector"
)

const shardCount = 256

// value holds any of the supported data types. The concrete Go types are:
//
//	string                 -> string
//	hash                   -> map[string]string
//	list                   -> *list   (see list.go)
//	set                    -> map[string]struct{}
//	sorted set             -> *zset    (see zset.go)
//	vector collection      -> *vector.Collection
type value = any

type entry struct {
	val      value
	expireAt time.Time // zero == no expiry
}

func (e *entry) expired(now time.Time) bool {
	return !e.expireAt.IsZero() && now.After(e.expireAt)
}

type shard struct {
	mu sync.RWMutex
	m  map[string]*entry
}

// Store is the full keyspace, safe for concurrent use.
type Store struct {
	shards [shardCount]*shard
	now    func() time.Time // injectable clock for tests
}

// New returns an empty store.
func New() *Store {
	s := &Store{now: time.Now}
	for i := range s.shards {
		s.shards[i] = &shard{m: make(map[string]*entry)}
	}
	return s
}

func (s *Store) shardFor(key string) *shard {
	h := fnv.New32a()
	h.Write([]byte(key))
	return s.shards[h.Sum32()%shardCount]
}

// getLive returns the live entry for key, deleting it first if it has expired
// (lazy expiry). The caller must hold the shard write lock when del==true.
func (sh *shard) getLive(key string, now time.Time) (*entry, bool) {
	e, ok := sh.m[key]
	if !ok {
		return nil, false
	}
	if e.expired(now) {
		delete(sh.m, key)
		return nil, false
	}
	return e, true
}

// Exists reports how many of the given keys currently exist.
func (s *Store) Exists(keys ...string) int {
	now := s.now()
	count := 0
	for _, k := range keys {
		sh := s.shardFor(k)
		sh.mu.Lock()
		if _, ok := sh.getLive(k, now); ok {
			count++
		}
		sh.mu.Unlock()
	}
	return count
}

// Del removes the given keys, returning the number actually deleted.
func (s *Store) Del(keys ...string) int {
	now := s.now()
	count := 0
	for _, k := range keys {
		sh := s.shardFor(k)
		sh.mu.Lock()
		if _, ok := sh.getLive(k, now); ok {
			delete(sh.m, k)
			count++
		}
		sh.mu.Unlock()
	}
	return count
}

// Type returns the Redis type name of key ("string", "hash", "list", "set",
// "zset", "vector") or "none" if it does not exist.
func (s *Store) Type(key string) string {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.getLive(key, s.now())
	if !ok {
		return "none"
	}
	switch e.val.(type) {
	case string:
		return "string"
	case map[string]string:
		return "hash"
	case *list:
		return "list"
	case map[string]struct{}:
		return "set"
	case *zset:
		return "zset"
	case *vector.Collection:
		return "vector"
	default:
		return "none"
	}
}

// Expire sets a relative TTL in milliseconds on an existing key. It reports
// whether the key existed.
func (s *Store) Expire(key string, ttl time.Duration) bool {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.getLive(key, s.now())
	if !ok {
		return false
	}
	e.expireAt = s.now().Add(ttl)
	return true
}

// Persist removes any TTL from key, returning whether a TTL was removed.
func (s *Store) Persist(key string) bool {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.getLive(key, s.now())
	if !ok || e.expireAt.IsZero() {
		return false
	}
	e.expireAt = time.Time{}
	return true
}

// TTL returns the remaining time to live for key. ok is false when the key
// does not exist; hasTTL is false when the key exists but is persistent.
func (s *Store) TTL(key string) (d time.Duration, hasTTL, ok bool) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return 0, false, false
	}
	if e.expireAt.IsZero() {
		return 0, false, true
	}
	return time.Until(e.expireAt), true, true
}

// Keys returns every live key matching the glob-style pattern.
func (s *Store) Keys(pattern string) []string {
	now := s.now()
	var out []string
	for _, sh := range s.shards {
		sh.mu.RLock()
		for k, e := range sh.m {
			if e.expired(now) {
				continue
			}
			if matchPattern(pattern, k) {
				out = append(out, k)
			}
		}
		sh.mu.RUnlock()
	}
	return out
}

// DBSize returns the number of live keys across all shards.
func (s *Store) DBSize() int {
	now := s.now()
	n := 0
	for _, sh := range s.shards {
		sh.mu.RLock()
		for _, e := range sh.m {
			if !e.expired(now) {
				n++
			}
		}
		sh.mu.RUnlock()
	}
	return n
}

// Flush removes every key.
func (s *Store) Flush() {
	for _, sh := range s.shards {
		sh.mu.Lock()
		sh.m = make(map[string]*entry)
		sh.mu.Unlock()
	}
}
