package dashboard

import (
	"context"
	"runtime"
	"sync"
	"time"
)

// historySamples is how many one-second samples the overview charts keep
// (five minutes of data).
const historySamples = 300

// sample is one per-second snapshot of the server's headline gauges.
type sample struct {
	T        int64  `json:"t"` // unix milliseconds
	Commands int64  `json:"commands_total"`
	Mem      uint64 `json:"memory_bytes"`
	Clients  int64  `json:"clients"`
	Keys     int    `json:"keys"`
	Hits     int64  `json:"hits"`
	Misses   int64  `json:"misses"`
}

// history is a fixed ring of samples.
type history struct {
	mu   sync.Mutex
	buf  [historySamples]sample
	n    int // number of valid samples
	head int // next write position
}

func (h *history) add(s sample) {
	h.mu.Lock()
	h.buf[h.head] = s
	h.head = (h.head + 1) % historySamples
	if h.n < historySamples {
		h.n++
	}
	h.mu.Unlock()
}

// snapshot returns samples oldest-first.
func (h *history) snapshot() []sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]sample, h.n)
	start := (h.head - h.n + historySamples) % historySamples
	for i := 0; i < h.n; i++ {
		out[i] = h.buf[(start+i)%historySamples]
	}
	return out
}

// currentSample reads the server gauges right now.
func (d *Dashboard) currentSample() sample {
	st := d.srv.Stats()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return sample{
		T:        time.Now().UnixMilli(),
		Commands: st.Commands.Load(),
		Mem:      mem.Alloc,
		Clients:  st.Connections.Load(),
		Keys:     d.srv.Store().DBSize(),
		Hits:     st.CacheHits.Load(),
		Misses:   st.CacheMisses.Load(),
	}
}

// runSampler records one sample per second until ctx is cancelled.
func (d *Dashboard) runSampler(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.hist.add(d.currentSample())
		}
	}
}
