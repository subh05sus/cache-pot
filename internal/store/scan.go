package store

import "sort"

// ShardCount returns the number of shards the keyspace is split into. SCAN
// cursors encode a shard index, so callers need the bound.
func (s *Store) ShardCount() int { return shardCount }

// ShardKeys returns the live keys of shard i in sorted order. Sorting gives
// SCAN-style iteration a deterministic order to resume from; one shard holds
// roughly 1/256th of the keyspace, so the sort stays cheap.
func (s *Store) ShardKeys(i int) []string {
	if i < 0 || i >= shardCount {
		return nil
	}
	sh := s.shards[i]
	now := s.now()
	sh.mu.RLock()
	out := make([]string, 0, len(sh.m))
	for k, e := range sh.m {
		if !e.expired(now) {
			out = append(out, k)
		}
	}
	sh.mu.RUnlock()
	sort.Strings(out)
	return out
}

// ScanAfter iterates the keyspace with keyset pagination: it resumes strictly
// after afterKey in shard startShard and examines up to limit keys, returning
// those that match the glob pattern. Because the cursor is a key rather than
// an offset, concurrent inserts and deletes can never cause a surviving key to
// be skipped. limit bounds work per call (like Redis SCAN's COUNT), so a call
// may return fewer matches than limit — or none — while not yet being done.
func (s *Store) ScanAfter(startShard int, afterKey, match string, limit int) (keys []string, nextShard int, nextAfter string, done bool) {
	if limit <= 0 {
		limit = 100
	}
	if startShard < 0 {
		startShard = 0
	}
	budget := limit
	for si := startShard; si < shardCount; si++ {
		sk := s.ShardKeys(si)
		j := 0
		if si == startShard && afterKey != "" {
			j = sort.SearchStrings(sk, afterKey)
			if j < len(sk) && sk[j] == afterKey {
				j++
			}
		}
		for ; j < len(sk); j++ {
			k := sk[j]
			if match == "" || match == "*" || MatchPattern(match, k) {
				keys = append(keys, k)
			}
			budget--
			if budget == 0 {
				if j == len(sk)-1 {
					// Exactly exhausted this shard: resume at the next one.
					return keys, si + 1, "", si+1 >= shardCount
				}
				return keys, si, k, false
			}
		}
	}
	return keys, shardCount, "", true
}
