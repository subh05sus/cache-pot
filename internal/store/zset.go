package store

import "sort"

// zset is a sorted set: a member->score map. V1 sorts on demand rather than
// maintaining a skiplist, which is adequate for the basic range ops the PRD
// scopes (ZADD/ZRANGE/ZRANGEBYSCORE/ZSCORE/ZCARD).
type zset struct {
	scores map[string]float64
}

func asZSet(e *entry) (*zset, error) {
	z, ok := e.val.(*zset)
	if !ok {
		return nil, ErrWrongType
	}
	return z, nil
}

// ZMember pairs a member with its score.
type ZMember struct {
	Member string
	Score  float64
}

// ZAdd sets the score for each member (creating the set if absent) and returns
// the number of newly added members.
func (s *Store) ZAdd(key string, members []ZMember) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	var z *zset
	if found {
		var err error
		if z, err = asZSet(e); err != nil {
			return 0, err
		}
	} else {
		z = &zset{scores: make(map[string]float64)}
		sh.m[key] = &entry{val: z}
	}
	added := 0
	for _, m := range members {
		if _, ok := z.scores[m.Member]; !ok {
			added++
		}
		z.scores[m.Member] = m.Score
	}
	return added, nil
}

// ZRem removes members and returns how many were removed.
func (s *Store) ZRem(key string, members ...string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return 0, nil
	}
	z, err := asZSet(e)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, m := range members {
		if _, ok := z.scores[m]; ok {
			delete(z.scores, m)
			removed++
		}
	}
	if len(z.scores) == 0 {
		delete(sh.m, key)
	}
	return removed, nil
}

// ZScore returns the score of member.
func (s *Store) ZScore(key, member string) (float64, bool, error) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return 0, false, nil
	}
	z, err := asZSet(e)
	if err != nil {
		return 0, false, err
	}
	sc, ok := z.scores[member]
	return sc, ok, nil
}

// ZCard returns the number of members.
func (s *Store) ZCard(key string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return 0, nil
	}
	z, err := asZSet(e)
	if err != nil {
		return 0, err
	}
	return len(z.scores), nil
}

// sortedMembers returns all members ordered by score ascending, ties broken
// lexicographically (Redis ordering).
func (z *zset) sortedMembers() []ZMember {
	out := make([]ZMember, 0, len(z.scores))
	for m, sc := range z.scores {
		out = append(out, ZMember{Member: m, Score: sc})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score < out[j].Score
		}
		return out[i].Member < out[j].Member
	})
	return out
}

// ZRange returns members in the inclusive rank range [start, stop] ordered by
// score (negative indices count from the end).
func (s *Store) ZRange(key string, start, stop int) ([]ZMember, error) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return nil, nil
	}
	z, err := asZSet(e)
	if err != nil {
		return nil, err
	}
	sorted := z.sortedMembers()
	start, stop = normalizeRange(start, stop, len(sorted))
	if start > stop {
		return []ZMember{}, nil
	}
	return sorted[start : stop+1], nil
}

// ZRangeByScore returns members whose score lies in [min, max] inclusive,
// ordered by score.
func (s *Store) ZRangeByScore(key string, min, max float64) ([]ZMember, error) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return nil, nil
	}
	z, err := asZSet(e)
	if err != nil {
		return nil, err
	}
	var out []ZMember
	for _, m := range z.sortedMembers() {
		if m.Score >= min && m.Score <= max {
			out = append(out, m)
		}
	}
	return out, nil
}
