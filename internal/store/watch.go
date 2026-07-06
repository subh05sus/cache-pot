package store

// watchEntry tracks the modification version of a watched key and how many
// connections are watching it, so entries are dropped once no one cares.
type watchEntry struct {
	version uint64
	refs    int
}

// Watch registers interest in key and returns its current modification
// version. Every Watch must be paired with an Unwatch (e.g. at EXEC, UNWATCH,
// or disconnect). The returned version is compared at EXEC time to detect
// whether the key changed in the meantime.
func (s *Store) Watch(key string) uint64 {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	e := s.watches[key]
	if e == nil {
		e = &watchEntry{}
		s.watches[key] = e
		s.watchActive.Add(1)
	}
	e.refs++
	return e.version
}

// Unwatch drops one watcher of key.
func (s *Store) Unwatch(key string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	e := s.watches[key]
	if e == nil {
		return
	}
	e.refs--
	if e.refs <= 0 {
		delete(s.watches, key)
		s.watchActive.Add(-1)
	}
}

// WatchVersion returns the current modification version of key. A key the
// caller is actively watching always has an entry; an unwatched key returns 0.
func (s *Store) WatchVersion(key string) uint64 {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if e := s.watches[key]; e != nil {
		return e.version
	}
	return 0
}

// Modified bumps key's version if it is being watched. It is a cheap atomic
// load in the overwhelmingly common case of no active watchers.
func (s *Store) Modified(key string) {
	if s.watchActive.Load() == 0 {
		return
	}
	s.watchMu.Lock()
	if e := s.watches[key]; e != nil {
		e.version++
	}
	s.watchMu.Unlock()
}

// ModifiedAll bumps every watched key's version, aborting all in-flight
// transactions. Used by FLUSHDB/FLUSHALL.
func (s *Store) ModifiedAll() {
	if s.watchActive.Load() == 0 {
		return
	}
	s.watchMu.Lock()
	for _, e := range s.watches {
		e.version++
	}
	s.watchMu.Unlock()
}
