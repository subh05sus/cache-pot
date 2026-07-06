// Package vector implements a flat (brute-force) vector index with cosine
// similarity. It is deliberately simple: V1 trades query speed for zero
// dependencies and predictable behaviour. An HNSW index is a documented future
// upgrade (see PRD §7) that can replace Collection without changing callers.
package vector

import (
	"errors"
	"math"
	"sort"
)

// ErrDimMismatch is returned when a vector's length does not match the
// collection's established dimension.
var ErrDimMismatch = errors.New("vector dimension mismatch")

// Item is one stored vector, its id, and optional metadata (used by the
// semantic cache to stash the cached response and its expiry).
type Item struct {
	ID   string
	Vec  []float32
	Meta string
}

// Collection is a flat index of vectors that all share one dimension.
type Collection struct {
	Dim   int
	items map[string]*Item // id -> item
}

// NewCollection returns an empty collection. The dimension is fixed by the
// first vector added.
func NewCollection() *Collection {
	return &Collection{items: map[string]*Item{}}
}

// Set inserts or replaces the vector stored under id. The first vector added
// to an empty collection fixes its dimension; later vectors must match.
func (c *Collection) Set(id string, vec []float32, meta string) error {
	if c.Dim == 0 {
		c.Dim = len(vec)
	} else if len(vec) != c.Dim {
		return ErrDimMismatch
	}
	cp := make([]float32, len(vec))
	copy(cp, vec)
	c.items[id] = &Item{ID: id, Vec: cp, Meta: meta}
	return nil
}

// Del removes id; it reports whether the id existed.
func (c *Collection) Del(id string) bool {
	if _, ok := c.items[id]; ok {
		delete(c.items, id)
		return true
	}
	return false
}

// Len returns the number of stored vectors.
func (c *Collection) Len() int { return len(c.items) }

// Result is one search hit: an item paired with its cosine similarity score in
// the range [-1, 1] (1 == identical direction).
type Result struct {
	Item  *Item
	Score float64
}

// Search returns the top-k items by cosine similarity to query, highest first.
func (c *Collection) Search(query []float32, k int) ([]Result, error) {
	if c.Dim != 0 && len(query) != c.Dim {
		return nil, ErrDimMismatch
	}
	results := make([]Result, 0, len(c.items))
	for _, it := range c.items {
		results = append(results, Result{Item: it, Score: cosine(query, it.Vec)})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if k > 0 && k < len(results) {
		results = results[:k]
	}
	return results, nil
}

// Items returns all stored items (used for snapshotting). The slice is freshly
// allocated; callers may not mutate the underlying vectors.
func (c *Collection) Items() []*Item {
	out := make([]*Item, 0, len(c.items))
	for _, it := range c.items {
		out = append(out, it)
	}
	return out
}

// cosine computes the cosine similarity of two equal-length vectors. A
// zero-magnitude vector yields 0 (treated as maximally dissimilar).
func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
