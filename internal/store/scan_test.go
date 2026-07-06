package store

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// walkScanAfter drives ScanAfter to completion and returns everything seen.
func walkScanAfter(s *Store, match string, limit int) []string {
	var out []string
	shard, after := 0, ""
	for {
		keys, ns, na, done := s.ScanAfter(shard, after, match, limit)
		out = append(out, keys...)
		if done {
			return out
		}
		shard, after = ns, na
	}
}

func TestScanAfterFullIteration(t *testing.T) {
	s := New()
	want := make(map[string]bool, 5000)
	for i := 0; i < 5000; i++ {
		k := fmt.Sprintf("key:%d", i)
		s.Set(k, "v", SetOptions{})
		want[k] = true
	}
	got := walkScanAfter(s, "*", 100)
	if len(got) != len(want) {
		t.Fatalf("scan returned %d keys, want %d", len(got), len(want))
	}
	seen := make(map[string]bool, len(got))
	for _, k := range got {
		if seen[k] {
			t.Fatalf("duplicate key %q", k)
		}
		seen[k] = true
		if !want[k] {
			t.Fatalf("unexpected key %q", k)
		}
	}
}

func TestScanAfterMatch(t *testing.T) {
	s := New()
	for i := 0; i < 100; i++ {
		s.Set(fmt.Sprintf("user:%d", i), "v", SetOptions{})
		s.Set(fmt.Sprintf("sess:%d", i), "v", SetOptions{})
	}
	got := walkScanAfter(s, "user:*", 10)
	if len(got) != 100 {
		t.Fatalf("match user:* returned %d keys, want 100", len(got))
	}
	for _, k := range got {
		if !MatchPattern("user:*", k) {
			t.Fatalf("non-matching key %q", k)
		}
	}
}

// Deleting keys during iteration must never hide a surviving key.
func TestScanAfterDeleteDuringIteration(t *testing.T) {
	s := New()
	const n = 2000
	for i := 0; i < n; i++ {
		s.Set(fmt.Sprintf("k:%04d", i), "v", SetOptions{})
	}
	deleted := make(map[string]bool)
	var got []string
	shard, after := 0, ""
	step := 0
	for {
		keys, ns, na, done := s.ScanAfter(shard, after, "*", 50)
		got = append(got, keys...)
		// Delete a few not-yet-guaranteed-seen keys mid-iteration.
		for j := 0; j < 3; j++ {
			k := fmt.Sprintf("k:%04d", (step*37+j*13)%n)
			if s.Del(k) > 0 {
				deleted[k] = true
			}
		}
		step++
		if done {
			break
		}
		shard, after = ns, na
	}
	seen := make(map[string]bool, len(got))
	for _, k := range got {
		seen[k] = true
	}
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k:%04d", i)
		if !deleted[k] && !seen[k] {
			t.Fatalf("surviving key %q missing from scan", k)
		}
	}
}

func TestScanAfterSkipsExpired(t *testing.T) {
	s := New()
	base := time.Now()
	s.now = func() time.Time { return base }
	s.Set("live", "v", SetOptions{})
	s.Set("dead", "v", SetOptions{})
	s.Expire("dead", time.Second)
	s.now = func() time.Time { return base.Add(2 * time.Second) }
	got := walkScanAfter(s, "*", 10)
	if len(got) != 1 || got[0] != "live" {
		t.Fatalf("scan = %v, want [live]", got)
	}
}

func TestRenamePreservesTTL(t *testing.T) {
	s := New()
	s.Set("a", "v", SetOptions{})
	s.Expire("a", time.Hour)
	if err := s.Rename("a", "b"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get("a"); ok {
		t.Fatal("src key still present after rename")
	}
	v, ok, _ := s.Get("b")
	if !ok || v != "v" {
		t.Fatalf("dst = %q, %v", v, ok)
	}
	if _, hasTTL, ok := s.TTL("b"); !ok || !hasTTL {
		t.Fatal("TTL lost in rename")
	}
	if err := s.Rename("missing", "x"); err != ErrNoSuchKey {
		t.Fatalf("rename missing = %v, want ErrNoSuchKey", err)
	}
}

func TestRenameNX(t *testing.T) {
	s := New()
	s.Set("a", "1", SetOptions{})
	s.Set("b", "2", SetOptions{})
	if ok, _ := s.RenameNX("a", "b"); ok {
		t.Fatal("RenameNX overwrote existing dst")
	}
	if ok, _ := s.RenameNX("a", "c"); !ok {
		t.Fatal("RenameNX to fresh dst failed")
	}
}

// Two goroutines renaming a<->b concurrently must not deadlock (lock-order
// inversion would hang this test).
func TestRenameNoDeadlock(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			s.Set("a", "v", SetOptions{})
			s.Rename("a", "b")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			s.Set("b", "v", SetOptions{})
			s.Rename("b", "a")
		}
	}()
	donech := make(chan struct{})
	go func() { wg.Wait(); close(donech) }()
	select {
	case <-donech:
	case <-time.After(30 * time.Second):
		t.Fatal("rename goroutines deadlocked")
	}
}

func TestMemoryUsageMonotonic(t *testing.T) {
	s := New()
	s.Set("small", "x", SetOptions{})
	s.Set("big", string(make([]byte, 10_000)), SetOptions{})
	small, ok := s.MemoryUsage("small")
	if !ok {
		t.Fatal("small missing")
	}
	big, ok := s.MemoryUsage("big")
	if !ok {
		t.Fatal("big missing")
	}
	if big <= small {
		t.Fatalf("big (%d) <= small (%d)", big, small)
	}
	if _, ok := s.MemoryUsage("absent"); ok {
		t.Fatal("MemoryUsage reported ok for absent key")
	}
}

func TestMemoryReport(t *testing.T) {
	s := New()
	for i := 0; i < 10; i++ {
		s.Set(fmt.Sprintf("user:%d", i), "value", SetOptions{})
	}
	s.HSet("cfg:h", map[string]string{"a": "1", "b": "2"})
	s.Set("plain", "v", SetOptions{})
	s.Expire("plain", time.Hour)

	rep := s.MemoryReport(5, ":")
	if rep.TotalKeys != 12 {
		t.Fatalf("TotalKeys = %d, want 12", rep.TotalKeys)
	}
	if rep.ByType["string"].Keys != 11 || rep.ByType["hash"].Keys != 1 {
		t.Fatalf("ByType = %+v", rep.ByType)
	}
	var sum int64
	for _, ta := range rep.ByType {
		sum += ta.Bytes
	}
	if sum != rep.TotalBytes {
		t.Fatalf("type bytes sum %d != total %d", sum, rep.TotalBytes)
	}
	var prefixKeys int64
	for _, pa := range rep.ByPrefix {
		prefixKeys += pa.Keys
	}
	if prefixKeys != rep.TotalKeys {
		t.Fatalf("prefix keys sum %d != total %d", prefixKeys, rep.TotalKeys)
	}
	if len(rep.TopKeys) != 5 {
		t.Fatalf("TopKeys len = %d, want 5", len(rep.TopKeys))
	}
	for i := 1; i < len(rep.TopKeys); i++ {
		if rep.TopKeys[i].Bytes > rep.TopKeys[i-1].Bytes {
			t.Fatal("TopKeys not sorted descending")
		}
	}
	if rep.TTL.Under1d != 1 || rep.TTL.NoTTL != 11 {
		t.Fatalf("TTL histogram = %+v", rep.TTL)
	}
}
