package store

func asSet(e *entry) (map[string]struct{}, error) {
	st, ok := e.val.(map[string]struct{})
	if !ok {
		return nil, ErrWrongType
	}
	return st, nil
}

// SAdd adds members to the set at key (creating it if absent) and returns how
// many were newly added.
func (s *Store) SAdd(key string, members ...string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	var set map[string]struct{}
	if found {
		var err error
		if set, err = asSet(e); err != nil {
			return 0, err
		}
	} else {
		set = make(map[string]struct{})
		sh.m[key] = &entry{val: set}
	}
	added := 0
	for _, m := range members {
		if _, ok := set[m]; !ok {
			set[m] = struct{}{}
			added++
		}
	}
	return added, nil
}

// SRem removes members and returns how many were removed; the key is dropped
// when emptied.
func (s *Store) SRem(key string, members ...string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return 0, nil
	}
	set, err := asSet(e)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, m := range members {
		if _, ok := set[m]; ok {
			delete(set, m)
			removed++
		}
	}
	if len(set) == 0 {
		delete(sh.m, key)
	}
	return removed, nil
}

// SMembers returns all members of the set.
func (s *Store) SMembers(key string) ([]string, error) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return nil, nil
	}
	set, err := asSet(e)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	return out, nil
}

// SIsMember reports whether member is in the set.
func (s *Store) SIsMember(key, member string) (bool, error) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return false, nil
	}
	set, err := asSet(e)
	if err != nil {
		return false, err
	}
	_, ok := set[member]
	return ok, nil
}

// SCard returns the set cardinality.
func (s *Store) SCard(key string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return 0, nil
	}
	set, err := asSet(e)
	if err != nil {
		return 0, err
	}
	return len(set), nil
}
