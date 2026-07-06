package store

// asHash returns the hash value of e, or ErrWrongType.
func asHash(e *entry) (map[string]string, error) {
	h, ok := e.val.(map[string]string)
	if !ok {
		return nil, ErrWrongType
	}
	return h, nil
}

// HSet sets the given field/value pairs on the hash at key (creating it if
// absent) and returns the number of newly created fields.
func (s *Store) HSet(key string, pairs map[string]string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	var h map[string]string
	if found {
		var err error
		if h, err = asHash(e); err != nil {
			return 0, err
		}
	} else {
		h = make(map[string]string)
		sh.m[key] = &entry{val: h}
	}
	added := 0
	for f, v := range pairs {
		if _, ok := h[f]; !ok {
			added++
		}
		h[f] = v
	}
	return added, nil
}

// HGet returns the value of a single field.
func (s *Store) HGet(key, field string) (string, bool, error) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return "", false, nil
	}
	h, err := asHash(e)
	if err != nil {
		return "", false, err
	}
	v, ok := h[field]
	return v, ok, nil
}

// HDel removes fields and returns how many were removed. The key is dropped
// when its last field is deleted.
func (s *Store) HDel(key string, fields ...string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return 0, nil
	}
	h, err := asHash(e)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, f := range fields {
		if _, ok := h[f]; ok {
			delete(h, f)
			removed++
		}
	}
	if len(h) == 0 {
		delete(sh.m, key)
	}
	return removed, nil
}

// HGetAll returns all fields and values as a flat [f1,v1,f2,v2,...] slice.
func (s *Store) HGetAll(key string) ([]string, error) {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return nil, nil
	}
	h, err := asHash(e)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(h)*2)
	for f, v := range h {
		out = append(out, f, v)
	}
	return out, nil
}

// hashView runs fn against the hash at key under a read lock; fn receives nil
// for a missing key.
func (s *Store) hashView(key string, fn func(map[string]string)) error {
	sh := s.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		fn(nil)
		return nil
	}
	h, err := asHash(e)
	if err != nil {
		return err
	}
	fn(h)
	return nil
}

// HKeys returns the field names of the hash.
func (s *Store) HKeys(key string) ([]string, error) {
	var out []string
	err := s.hashView(key, func(h map[string]string) {
		for f := range h {
			out = append(out, f)
		}
	})
	return out, err
}

// HVals returns the values of the hash.
func (s *Store) HVals(key string) ([]string, error) {
	var out []string
	err := s.hashView(key, func(h map[string]string) {
		for _, v := range h {
			out = append(out, v)
		}
	})
	return out, err
}

// HLen returns the number of fields in the hash.
func (s *Store) HLen(key string) (int, error) {
	n := 0
	err := s.hashView(key, func(h map[string]string) { n = len(h) })
	return n, err
}
