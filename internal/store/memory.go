package store

import (
	"container/heap"
	"sort"
	"strings"
	"time"

	"github.com/subh05sus/cache-pot/internal/vector"
)

// Memory sizes are heuristic: Go offers no per-object size introspection, so
// we charge each entry a fixed structural overhead plus the bytes we can
// count (key/field/member/value lengths, vector dims). The constants below
// approximate map-bucket and header costs on a 64-bit platform. Absolute
// numbers are ±30%-ish; relative ordering — which is what the dashboard's
// analysis view ranks by — is reliable.
const (
	entryOverhead      = 96 // entry struct + shard map bucket share + key string header
	strHeader          = 16 // string header
	hashPairOverhead   = 80 // two string headers + hash map bucket share
	setMemberOverhead  = 56 // string header + set map bucket share
	listElemOverhead   = 40 // string header + slice slot
	zsetMemberOverhead = 104
	vecItemOverhead    = 64 // Item struct + map bucket share
	containerOverhead  = 48 // map/slice header of the value itself
)

// sizeOfValue approximates the heap bytes held by a stored value.
func sizeOfValue(v value) int64 {
	switch x := v.(type) {
	case string:
		return strHeader + int64(len(x))
	case map[string]string:
		n := int64(containerOverhead)
		for f, val := range x {
			n += int64(len(f)+len(val)) + hashPairOverhead
		}
		return n
	case map[string]struct{}:
		n := int64(containerOverhead)
		for m := range x {
			n += int64(len(m)) + setMemberOverhead
		}
		return n
	case *list:
		n := int64(containerOverhead)
		for _, it := range x.items {
			n += int64(len(it)) + listElemOverhead
		}
		return n
	case *zset:
		n := int64(containerOverhead)
		for m := range x.scores {
			n += int64(len(m)) + 8 + zsetMemberOverhead
		}
		return n
	case *vector.Collection:
		n := int64(containerOverhead)
		for _, it := range x.Items() {
			n += int64(len(it.ID)+len(it.Meta)) + int64(len(it.Vec))*4 + vecItemOverhead
		}
		return n
	default:
		return 0
	}
}

// MemoryUsage returns the approximate heap bytes attributable to key,
// including per-entry overhead. ok is false when the key does not exist.
func (s *Store) MemoryUsage(key string) (bytes int64, ok bool) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return 0, false
	}
	return entryOverhead + int64(len(key)) + sizeOfValue(e.val), true
}

// KeySize is one key with its approximate size, used for top-key rankings.
type KeySize struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Bytes int64  `json:"bytes"`
}

// TypeAgg aggregates key count and bytes for one data type.
type TypeAgg struct {
	Keys  int64 `json:"keys"`
	Bytes int64 `json:"bytes"`
}

// PrefixAgg aggregates key count and bytes for one key-prefix namespace.
type PrefixAgg struct {
	Prefix string `json:"prefix"`
	Keys   int64  `json:"keys"`
	Bytes  int64  `json:"bytes"`
}

// TTLHist buckets keys by remaining time to live.
type TTLHist struct {
	NoTTL    int64 `json:"no_ttl"`
	Under1m  int64 `json:"under_1m"`
	Under10m int64 `json:"under_10m"`
	Under1h  int64 `json:"under_1h"`
	Under1d  int64 `json:"under_1d"`
	Over1d   int64 `json:"over_1d"`
}

// MemoryReport is a point-in-time analysis of the whole keyspace.
type MemoryReport struct {
	TotalKeys  int64       `json:"total_keys"`
	TotalBytes int64       `json:"total_bytes"`
	ByType     map[string]*TypeAgg `json:"by_type"`
	ByPrefix   []PrefixAgg `json:"by_prefix"`
	TTL        TTLHist     `json:"ttl"`
	TopKeys    []KeySize   `json:"top_keys"`
}

// maxDistinctPrefixes caps namespace aggregation so a keyspace of unique
// prefixes cannot balloon the report; overflow lands in "(other)".
const maxDistinctPrefixes = 1000

// MemoryReport walks the whole keyspace one shard at a time (read-locking
// only the shard being visited, so writers are never blocked globally) and
// aggregates sizes by type, by first-`delimiter`-segment namespace, and by
// TTL bucket, plus the topN largest keys.
func (s *Store) MemoryReport(topN int, delimiter string) MemoryReport {
	if topN <= 0 {
		topN = 25
	}
	if delimiter == "" {
		delimiter = ":"
	}
	rep := MemoryReport{ByType: make(map[string]*TypeAgg)}
	prefixes := make(map[string]*PrefixAgg)
	h := &keySizeHeap{}
	heap.Init(h)
	now := s.now()

	for _, sh := range s.shards {
		sh.mu.RLock()
		for k, e := range sh.m {
			if e.expired(now) {
				continue
			}
			size := entryOverhead + int64(len(k)) + sizeOfValue(e.val)
			tn := typeName(e.val)

			rep.TotalKeys++
			rep.TotalBytes += size

			ta := rep.ByType[tn]
			if ta == nil {
				ta = &TypeAgg{}
				rep.ByType[tn] = ta
			}
			ta.Keys++
			ta.Bytes += size

			prefix := "(none)"
			if i := strings.Index(k, delimiter); i > 0 {
				prefix = k[:i]
			}
			pa := prefixes[prefix]
			if pa == nil {
				if len(prefixes) >= maxDistinctPrefixes {
					prefix = "(other)"
					pa = prefixes[prefix]
				}
				if pa == nil {
					pa = &PrefixAgg{Prefix: prefix}
					prefixes[prefix] = pa
				}
			}
			pa.Keys++
			pa.Bytes += size

			switch ttl := e.expireAt; {
			case ttl.IsZero():
				rep.TTL.NoTTL++
			case ttl.Sub(now) < time.Minute:
				rep.TTL.Under1m++
			case ttl.Sub(now) < 10*time.Minute:
				rep.TTL.Under10m++
			case ttl.Sub(now) < time.Hour:
				rep.TTL.Under1h++
			case ttl.Sub(now) < 24*time.Hour:
				rep.TTL.Under1d++
			default:
				rep.TTL.Over1d++
			}

			if h.Len() < topN {
				heap.Push(h, KeySize{Key: k, Type: tn, Bytes: size})
			} else if size > (*h)[0].Bytes {
				(*h)[0] = KeySize{Key: k, Type: tn, Bytes: size}
				heap.Fix(h, 0)
			}
		}
		sh.mu.RUnlock()
	}

	rep.TopKeys = make([]KeySize, h.Len())
	for i := len(rep.TopKeys) - 1; i >= 0; i-- {
		rep.TopKeys[i] = heap.Pop(h).(KeySize)
	}
	rep.ByPrefix = make([]PrefixAgg, 0, len(prefixes))
	for _, pa := range prefixes {
		rep.ByPrefix = append(rep.ByPrefix, *pa)
	}
	sort.Slice(rep.ByPrefix, func(i, j int) bool {
		if rep.ByPrefix[i].Bytes != rep.ByPrefix[j].Bytes {
			return rep.ByPrefix[i].Bytes > rep.ByPrefix[j].Bytes
		}
		return rep.ByPrefix[i].Prefix < rep.ByPrefix[j].Prefix
	})
	return rep
}

// keySizeHeap is a min-heap on Bytes, holding the current topN candidates.
type keySizeHeap []KeySize

func (h keySizeHeap) Len() int            { return len(h) }
func (h keySizeHeap) Less(i, j int) bool  { return h[i].Bytes < h[j].Bytes }
func (h keySizeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *keySizeHeap) Push(x any)         { *h = append(*h, x.(KeySize)) }
func (h *keySizeHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
