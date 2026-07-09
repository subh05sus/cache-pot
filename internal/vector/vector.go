// Package vector implements a vector index with cosine similarity.
//
// It is a hybrid: small collections are searched by brute force, which is exact
// and has no bookkeeping. Once a collection grows past bruteForceThreshold it
// builds and maintains an HNSW graph (Hierarchical Navigable Small World), which
// trades exactness for sub-linear query time on large sets. Callers see one
// Collection type; the switch is internal, so VSET/VSEARCH semantics and the
// snapshot format are unchanged.
package vector

import (
	"container/heap"
	"errors"
	"math"
	"math/rand"
	"sort"
)

// ErrDimMismatch is returned when a vector's length does not match the
// collection's established dimension.
var ErrDimMismatch = errors.New("vector dimension mismatch")

// Tuning constants. bruteForceThreshold is the collection size at which the
// HNSW graph is built; below it, brute force is faster and exact.
const (
	bruteForceThreshold = 256
	hnswM               = 16  // neighbors per node per layer
	hnswMmax0           = 32  // neighbors at layer 0 (2*M)
	hnswEfConstruction  = 200 // candidate list size while inserting
	hnswEfSearch        = 64  // minimum candidate list size while querying
)

// Item is one stored vector, its id, and optional metadata (used by the
// semantic cache to stash the cached response and its expiry).
type Item struct {
	ID   string
	Vec  []float32
	Meta string
}

// Collection is an index of vectors that all share one dimension.
type Collection struct {
	Dim   int
	items map[string]*Item // id -> item (authoritative; used for persistence)
	g     *hnsw            // nil until the collection passes bruteForceThreshold
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

	if existing := c.items[id]; existing != nil {
		if equalVec(existing.Vec, vec) {
			// Meta-only update: mutate in place so any graph node keeps its
			// pointer and position.
			existing.Meta = meta
			return nil
		}
		// The vector changed: update in place, then fix the graph position.
		cp := make([]float32, len(vec))
		copy(cp, vec)
		existing.Vec = cp
		existing.Meta = meta
		if c.g != nil {
			c.g.remove(id)
			c.g.insert(existing)
			c.g.maybeRebuild(c.items)
		}
		return nil
	}

	cp := make([]float32, len(vec))
	copy(cp, vec)
	it := &Item{ID: id, Vec: cp, Meta: meta}
	c.items[id] = it

	if c.g != nil {
		c.g.insert(it)
		return nil
	}
	if len(c.items) > bruteForceThreshold {
		c.g = buildHNSW(c.items)
	}
	return nil
}

// Del removes id; it reports whether the id existed.
func (c *Collection) Del(id string) bool {
	if _, ok := c.items[id]; !ok {
		return false
	}
	delete(c.items, id)
	if c.g != nil {
		c.g.remove(id)
		c.g.maybeRebuild(c.items)
	}
	return true
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
	if c.g != nil {
		return c.g.search(query, k), nil
	}
	return c.bruteForce(query, k), nil
}

// bruteForce scores every item exactly. Used for small collections.
func (c *Collection) bruteForce(query []float32, k int) []Result {
	results := make([]Result, 0, len(c.items))
	for _, it := range c.items {
		results = append(results, Result{Item: it, Score: cosine(query, it.Vec)})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if k > 0 && k < len(results) {
		results = results[:k]
	}
	return results
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

// ---- HNSW graph ----------------------------------------------------------

type node struct {
	item      *Item
	norm      float64   // precomputed L2 norm of item.Vec
	neighbors [][]*node // neighbors per layer; len == level+1
	deleted   bool
}

type hnsw struct {
	nodes    map[string]*node
	entry    *node
	maxLevel int
	mL       float64
	rng      *rand.Rand
	dirty    int // tombstones + stale re-inserts since the last rebuild
}

func newHNSW() *hnsw {
	return &hnsw{
		nodes: map[string]*node{},
		mL:    1 / math.Log(hnswM),
		rng:   rand.New(rand.NewSource(1)),
	}
}

// buildHNSW constructs a fresh graph from a set of items.
func buildHNSW(items map[string]*Item) *hnsw {
	g := newHNSW()
	for _, it := range items {
		g.insert(it)
	}
	return g
}

func (g *hnsw) randomLevel() int {
	return int(-math.Log(g.rng.Float64()) * g.mL)
}

func (g *hnsw) insert(it *Item) {
	n := &node{item: it, norm: l2norm(it.Vec)}
	level := g.randomLevel()
	n.neighbors = make([][]*node, level+1)

	if g.entry == nil {
		g.entry = n
		g.maxLevel = level
		g.nodes[it.ID] = n
		return
	}

	dist := g.distFn(it.Vec)
	curr := g.entry

	// Descend through layers above the new node's top, moving greedily closer.
	for lc := g.maxLevel; lc > level; lc-- {
		curr = greedyClosest(curr, dist, lc)
	}

	// From the new node's top layer down to 0, connect to nearby neighbors.
	for lc := min(level, g.maxLevel); lc >= 0; lc-- {
		w := g.searchLayer(dist, curr, hnswEfConstruction, lc)
		mMax := hnswM
		if lc == 0 {
			mMax = hnswMmax0
		}
		neighbors := selectNeighbors(w, mMax)
		for _, nb := range neighbors {
			link(n, nb, lc)
			link(nb, n, lc)
			pruneNeighbors(nb, lc, mMax)
		}
		if len(w) > 0 {
			curr = w[0].n // closest becomes entry for the next layer down
		}
	}

	g.nodes[it.ID] = n
	if level > g.maxLevel {
		g.maxLevel = level
		g.entry = n
	}
}

// remove tombstones a node so it stops appearing in results; graph links are
// left intact for connectivity and compacted on the next rebuild.
func (g *hnsw) remove(id string) {
	n, ok := g.nodes[id]
	if !ok {
		return
	}
	n.deleted = true
	delete(g.nodes, id)
	g.dirty++
	if g.entry == n {
		g.entry = g.anyLive()
	}
}

func (g *hnsw) anyLive() *node {
	var best *node
	for _, n := range g.nodes {
		if best == nil || len(n.neighbors) > len(best.neighbors) {
			best = n
		}
	}
	return best
}

// maybeRebuild rebuilds the graph once tombstones and stale inserts dominate,
// which keeps query quality from decaying under churn.
func (g *hnsw) maybeRebuild(items map[string]*Item) {
	if g.dirty*2 >= len(items) && len(items) >= bruteForceThreshold {
		fresh := buildHNSW(items)
		*g = *fresh
	}
}

func (g *hnsw) search(query []float32, k int) []Result {
	if g.entry == nil {
		return nil
	}
	dist := g.distFn(query)
	curr := g.entry
	for lc := g.maxLevel; lc > 0; lc-- {
		curr = greedyClosest(curr, dist, lc)
	}
	ef := hnswEfSearch
	if k > ef {
		ef = k
	}
	w := g.searchLayer(dist, curr, ef, 0)

	results := make([]Result, 0, len(w))
	for _, c := range w {
		if c.n.deleted {
			continue
		}
		// c.d is cosine distance (1 - similarity); convert back to similarity.
		results = append(results, Result{Item: c.n.item, Score: 1 - c.d})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if k > 0 && k < len(results) {
		results = results[:k]
	}
	return results
}

// distFn returns a closure computing cosine distance from query to any node,
// reusing the query's precomputed norm.
func (g *hnsw) distFn(query []float32) func(*node) float64 {
	qNorm := l2norm(query)
	return func(n *node) float64 {
		if qNorm == 0 || n.norm == 0 {
			return 1 // orthogonal-ish; maximally far in similarity terms
		}
		return 1 - dot(query, n.item.Vec)/(qNorm*n.norm)
	}
}

// greedyClosest hill-climbs to the locally closest node at layer lc.
func greedyClosest(start *node, dist func(*node) float64, lc int) *node {
	curr := start
	currDist := dist(curr)
	for {
		improved := false
		if lc < len(curr.neighbors) {
			for _, nb := range curr.neighbors[lc] {
				if d := dist(nb); d < currDist {
					curr, currDist = nb, d
					improved = true
				}
			}
		}
		if !improved {
			return curr
		}
	}
}

// searchLayer returns up to ef nearest nodes to the query at layer lc, sorted
// closest-first. It may include tombstoned nodes (kept for connectivity); the
// caller filters them.
func (g *hnsw) searchLayer(dist func(*node) float64, entry *node, ef, lc int) []distItem {
	visited := map[*node]bool{entry: true}
	ed := dist(entry)
	candidates := &distHeap{min: true, items: []distItem{{entry, ed}}}
	result := &distHeap{min: false, items: []distItem{{entry, ed}}}
	heap.Init(candidates)
	heap.Init(result)

	for candidates.Len() > 0 {
		c := heap.Pop(candidates).(distItem)
		if result.Len() >= ef && c.d > result.items[0].d {
			break
		}
		if lc >= len(c.n.neighbors) {
			continue
		}
		for _, e := range c.n.neighbors[lc] {
			if visited[e] {
				continue
			}
			visited[e] = true
			d := dist(e)
			if result.Len() < ef || d < result.items[0].d {
				heap.Push(candidates, distItem{e, d})
				heap.Push(result, distItem{e, d})
				if result.Len() > ef {
					heap.Pop(result)
				}
			}
		}
	}

	out := make([]distItem, result.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(result).(distItem) // pops farthest first → fill from end
	}
	return out
}

// selectNeighbors keeps the m closest candidates (candidates arrive sorted
// closest-first).
func selectNeighbors(candidates []distItem, m int) []*node {
	if m > len(candidates) {
		m = len(candidates)
	}
	out := make([]*node, 0, m)
	for i := 0; i < m; i++ {
		out = append(out, candidates[i].n)
	}
	return out
}

func link(from, to *node, lc int) {
	for len(from.neighbors) <= lc {
		from.neighbors = append(from.neighbors, nil)
	}
	from.neighbors[lc] = append(from.neighbors[lc], to)
}

// pruneNeighbors trims a node's neighbor list at layer lc back to mMax, keeping
// the closest by distance to that node.
func pruneNeighbors(n *node, lc, mMax int) {
	if lc >= len(n.neighbors) || len(n.neighbors[lc]) <= mMax {
		return
	}
	self := n.item.Vec
	selfNorm := n.norm
	nbrs := n.neighbors[lc]
	sort.Slice(nbrs, func(i, j int) bool {
		return nbrDist(self, selfNorm, nbrs[i]) < nbrDist(self, selfNorm, nbrs[j])
	})
	n.neighbors[lc] = nbrs[:mMax]
}

func nbrDist(vec []float32, norm float64, n *node) float64 {
	if norm == 0 || n.norm == 0 {
		return 1
	}
	return 1 - dot(vec, n.item.Vec)/(norm*n.norm)
}

// ---- distance heap --------------------------------------------------------

type distItem struct {
	n *node
	d float64
}

// distHeap is a binary heap over distItems. min=true → root is the closest;
// min=false → root is the farthest (used to cap the result set at ef).
type distHeap struct {
	items []distItem
	min   bool
}

func (h distHeap) Len() int { return len(h.items) }
func (h distHeap) Less(i, j int) bool {
	if h.min {
		return h.items[i].d < h.items[j].d
	}
	return h.items[i].d > h.items[j].d
}
func (h distHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *distHeap) Push(x any)   { h.items = append(h.items, x.(distItem)) }
func (h *distHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	return it
}

// ---- math helpers ---------------------------------------------------------

// cosine computes the cosine similarity of two equal-length vectors. A
// zero-magnitude vector yields 0 (treated as maximally dissimilar).
func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	na, nb := l2norm(a), l2norm(b)
	if na == 0 || nb == 0 {
		return 0
	}
	return dot(a, b) / (na * nb)
}

func dot(a, b []float32) float64 {
	var d float64
	for i := range a {
		d += float64(a[i]) * float64(b[i])
	}
	return d
}

func l2norm(a []float32) float64 {
	var s float64
	for _, v := range a {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s)
}

func equalVec(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
