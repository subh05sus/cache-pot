package store

import "errors"

// ErrNoSuchKey is returned by Rename/RenameNX when the source key is absent.
var ErrNoSuchKey = errors.New("ERR no such key")

// Rename moves the entry at src to dst, overwriting dst if it exists. The
// entry pointer moves wholesale, so any TTL on src survives.
func (s *Store) Rename(src, dst string) error {
	_, err := s.rename(src, dst, false)
	return err
}

// RenameNX renames only when dst does not already exist; it reports whether
// the rename happened.
func (s *Store) RenameNX(src, dst string) (bool, error) {
	return s.rename(src, dst, true)
}

func (s *Store) rename(src, dst string, nx bool) (bool, error) {
	si, di := shardIndexFor(src), shardIndexFor(dst)
	// Lock both shards in ascending index order so two concurrent renames in
	// opposite directions cannot deadlock.
	lo, hi := si, di
	if lo > hi {
		lo, hi = hi, lo
	}
	s.shards[lo].mu.Lock()
	defer s.shards[lo].mu.Unlock()
	if hi != lo {
		s.shards[hi].mu.Lock()
		defer s.shards[hi].mu.Unlock()
	}

	now := s.now()
	e, ok := s.shards[si].getLive(src, now)
	if !ok {
		return false, ErrNoSuchKey
	}
	if nx {
		if _, exists := s.shards[di].getLive(dst, now); exists {
			return false, nil
		}
	}
	delete(s.shards[si].m, src)
	s.shards[di].m[dst] = e
	return true, nil
}
