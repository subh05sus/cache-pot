package vector

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestSetSearch(t *testing.T) {
	c := NewCollection()
	if err := c.Set("a", []float32{1, 0, 0}, "alpha"); err != nil {
		t.Fatal(err)
	}
	c.Set("b", []float32{0, 1, 0}, "beta")
	c.Set("c", []float32{0.9, 0.1, 0}, "gamma")

	res, err := c.Search([]float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 results, got %d", len(res))
	}
	if res[0].Item.ID != "a" {
		t.Fatalf("nearest should be a, got %s", res[0].Item.ID)
	}
	if res[0].Score < 0.99 {
		t.Fatalf("identical vector should score ~1, got %f", res[0].Score)
	}
}

func TestDimMismatch(t *testing.T) {
	c := NewCollection()
	c.Set("a", []float32{1, 2, 3}, "")
	if err := c.Set("b", []float32{1, 2}, ""); err != ErrDimMismatch {
		t.Fatalf("want ErrDimMismatch, got %v", err)
	}
}

// randVec returns a deterministic random unit-ish vector.
func randVec(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(rng.NormFloat64())
	}
	return v
}

// bruteTopK computes exact top-k ids for a query, the ground truth HNSW is
// measured against.
func bruteTopK(c *Collection, query []float32, k int) []string {
	res := c.bruteForce(query, k)
	ids := make([]string, len(res))
	for i, r := range res {
		ids[i] = r.Item.ID
	}
	return ids
}

func TestHNSWEngages(t *testing.T) {
	c := NewCollection()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i <= bruteForceThreshold; i++ {
		c.Set(fmt.Sprintf("v%d", i), randVec(rng, 8), "")
	}
	if c.g == nil {
		t.Fatalf("HNSW graph should be built past %d items", bruteForceThreshold)
	}
	if c.Len() != bruteForceThreshold+1 {
		t.Fatalf("want %d items, got %d", bruteForceThreshold+1, c.Len())
	}
}

func TestHNSWRecall(t *testing.T) {
	const (
		n   = 2000
		dim = 16
		k   = 10
	)
	c := NewCollection()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < n; i++ {
		c.Set(fmt.Sprintf("v%d", i), randVec(rng, dim), "")
	}
	if c.g == nil {
		t.Fatal("expected HNSW graph")
	}

	// Compare HNSW top-k against exact brute force over many queries.
	const queries = 100
	var hits, total int
	for q := 0; q < queries; q++ {
		query := randVec(rng, dim)
		truth := bruteTopKExact(c, query, k)
		res, err := c.Search(query, k)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, r := range res {
			got[r.Item.ID] = true
		}
		for _, id := range truth {
			if got[id] {
				hits++
			}
			total++
		}
	}
	recall := float64(hits) / float64(total)
	t.Logf("HNSW recall@%d over %d queries: %.3f", k, queries, recall)
	if recall < 0.85 {
		t.Fatalf("recall %.3f below 0.85 — HNSW quality regressed", recall)
	}
}

// bruteTopKExact scans every item directly (not via the collection's engine) so
// it is a true ground truth even when the collection uses HNSW.
func bruteTopKExact(c *Collection, query []float32, k int) []string {
	type sc struct {
		id string
		s  float64
	}
	all := make([]sc, 0, len(c.items))
	for id, it := range c.items {
		all = append(all, sc{id, cosine(query, it.Vec)})
	}
	// simple partial selection
	for i := 0; i < k && i < len(all); i++ {
		best := i
		for j := i + 1; j < len(all); j++ {
			if all[j].s > all[best].s {
				best = j
			}
		}
		all[i], all[best] = all[best], all[i]
	}
	out := []string{}
	for i := 0; i < k && i < len(all); i++ {
		out = append(out, all[i].id)
	}
	return out
}

func TestHNSWDelete(t *testing.T) {
	c := NewCollection()
	rng := rand.New(rand.NewSource(3))
	ids := make([]string, 0)
	for i := 0; i < 800; i++ {
		id := fmt.Sprintf("v%d", i)
		c.Set(id, randVec(rng, 8), "")
		ids = append(ids, id)
	}
	// Delete a quarter of them.
	deleted := map[string]bool{}
	for i := 0; i < 200; i++ {
		if !c.Del(ids[i]) {
			t.Fatalf("Del reported missing id %s", ids[i])
		}
		deleted[ids[i]] = true
	}
	if c.Len() != 600 {
		t.Fatalf("want 600 after deletes, got %d", c.Len())
	}
	// A query near a surviving vector should return only live ids.
	survivor := ids[500]
	query := c.items[survivor].Vec
	res, err := c.Search(query, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("expected results")
	}
	for _, r := range res {
		if deleted[r.Item.ID] {
			t.Fatalf("deleted id %s came back in results", r.Item.ID)
		}
	}
	if res[0].Item.ID != survivor {
		t.Fatalf("nearest to a stored vector should be itself (%s), got %s", survivor, res[0].Item.ID)
	}
}

func TestOverwriteMetaAndVec(t *testing.T) {
	c := NewCollection()
	c.Set("x", []float32{1, 0, 0}, "first")

	// Meta-only update keeps the vector.
	c.Set("x", []float32{1, 0, 0}, "second")
	if c.Len() != 1 {
		t.Fatalf("overwrite should not add an item, len=%d", c.Len())
	}
	res, _ := c.Search([]float32{1, 0, 0}, 1)
	if res[0].Item.Meta != "second" {
		t.Fatalf("meta not updated, got %q", res[0].Item.Meta)
	}

	// Vector change is reflected in search.
	c.Set("x", []float32{0, 1, 0}, "third")
	res, _ = c.Search([]float32{0, 1, 0}, 1)
	if res[0].Item.ID != "x" || res[0].Score < 0.99 {
		t.Fatalf("vector change not reflected: %+v", res[0])
	}
}

func TestKGreaterThanLen(t *testing.T) {
	c := NewCollection()
	c.Set("a", []float32{1, 0}, "")
	c.Set("b", []float32{0, 1}, "")
	res, err := c.Search([]float32{1, 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 results when k>len, got %d", len(res))
	}
}

func benchCollection(n, dim int) (*Collection, [][]float32) {
	c := NewCollection()
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < n; i++ {
		c.Set(fmt.Sprintf("v%d", i), randVec(rng, dim), "")
	}
	queries := make([][]float32, 256)
	for i := range queries {
		queries[i] = randVec(rng, dim)
	}
	return c, queries
}

// BenchmarkSearchHNSW times queries through the collection's engine (HNSW).
func BenchmarkSearchHNSW(b *testing.B) {
	c, queries := benchCollection(5000, 32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Search(queries[i%len(queries)], 10)
	}
}

// BenchmarkSearchBrute times the exact brute-force scan over the same data, so
// the ratio to BenchmarkSearchHNSW is the speedup.
func BenchmarkSearchBrute(b *testing.B) {
	c, queries := benchCollection(5000, 32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.bruteForce(queries[i%len(queries)], 10)
	}
}

// TestReloadEquivalent simulates a snapshot reload: a fresh collection rebuilt
// from Items() must answer queries the same way (both cross the HNSW threshold).
func TestReloadEquivalent(t *testing.T) {
	src := NewCollection()
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 700; i++ {
		src.Set(fmt.Sprintf("v%d", i), randVec(rng, 12), fmt.Sprintf("m%d", i))
	}
	dst := NewCollection()
	for _, it := range src.Items() {
		if err := dst.Set(it.ID, it.Vec, it.Meta); err != nil {
			t.Fatal(err)
		}
	}
	if dst.Len() != src.Len() {
		t.Fatalf("len mismatch after reload: %d vs %d", dst.Len(), src.Len())
	}
	// Nearest-to-self must be self in both.
	for _, i := range []int{0, 100, 500, 699} {
		id := fmt.Sprintf("v%d", i)
		q := src.items[id].Vec
		res, _ := dst.Search(q, 1)
		if len(res) == 0 || res[0].Item.ID != id {
			t.Fatalf("reloaded collection lost nearest-to-self for %s", id)
		}
	}
}
