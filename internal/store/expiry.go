package store

import (
	"context"
	"time"
)

// StartSweeper runs a background goroutine that periodically scans shards and
// deletes expired keys. Lazy expiry already removes keys on access; the sweep
// reclaims memory for keys that are never read again. It stops when ctx is
// cancelled.
func (s *Store) StartSweeper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweep()
			}
		}
	}()
}

// sweep deletes expired keys from every shard.
func (s *Store) sweep() {
	now := s.now()
	for _, sh := range s.shards {
		sh.mu.Lock()
		for k, e := range sh.m {
			if e.expired(now) {
				delete(sh.m, k)
			}
		}
		sh.mu.Unlock()
	}
}
