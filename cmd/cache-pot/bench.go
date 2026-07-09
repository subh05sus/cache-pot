// The "bench" subcommand is a load generator for a running Cache-Pot server,
// modeled on redis-benchmark. It fires a fixed number of requests across a pool
// of connections and reports throughput and latency percentiles per command.
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/subh05sus/cache-pot/internal/client"
)

// benchTest is one named command pattern the benchmark can drive. build returns
// the argv for iteration i; keys are spread across the keyspace so runs touch
// many keys rather than hammering one.
type benchTest struct {
	name  string
	build func(i int, keyspace int, val string) []string
}

func benchTests(all map[string]benchTest, names []string) ([]benchTest, error) {
	var out []benchTest
	for _, n := range names {
		t, ok := all[strings.ToUpper(strings.TrimSpace(n))]
		if !ok {
			return nil, fmt.Errorf("unknown test %q", n)
		}
		out = append(out, t)
	}
	return out, nil
}

func runBench(argv []string) error {
	fs := flag.NewFlagSet("cache-pot bench", flag.ExitOnError)
	addr := fs.String("addr", env("CACHEPOT_ADDR", "localhost:6379"), "server address (host:port)")
	password := fs.String("auth", env("CACHEPOT_AUTH", ""), "AUTH password sent on connect")
	useTLS := fs.Bool("tls", false, "connect over TLS")
	caCert := fs.String("tls-cacert", "", "CA certificate to verify the server (PEM)")
	insecure := fs.Bool("tls-insecure", false, "skip TLS certificate verification")
	n := fs.Int("n", 100000, "total requests per test")
	conc := fs.Int("c", 50, "parallel connections")
	dataSize := fs.Int("d", 3, "value size in bytes for SET-style tests")
	keyspace := fs.Int("keyspace", 10000, "number of distinct keys to spread load across")
	testList := fs.String("t", "PING,SET,GET,INCR", "comma-separated tests to run")
	quiet := fs.Bool("q", false, "quiet: one summary line per test")
	fs.Parse(argv)

	if *n <= 0 || *conc <= 0 {
		return fmt.Errorf("-n and -c must be positive")
	}
	if *conc > *n {
		*conc = *n
	}

	all := map[string]benchTest{
		"PING":  {"PING", func(int, int, string) []string { return []string{"PING"} }},
		"SET":   {"SET", func(i, ks int, v string) []string { return []string{"SET", key("k", i, ks), v} }},
		"GET":   {"GET", func(i, ks int, _ string) []string { return []string{"GET", key("k", i, ks)} }},
		"INCR":  {"INCR", func(i, ks int, _ string) []string { return []string{"INCR", key("n", i, ks)} }},
		"LPUSH": {"LPUSH", func(i, ks int, v string) []string { return []string{"LPUSH", key("l", i, ks), v} }},
		"RPUSH": {"RPUSH", func(i, ks int, v string) []string { return []string{"RPUSH", key("l", i, ks), v} }},
		"HSET":  {"HSET", func(i, ks int, v string) []string { return []string{"HSET", key("h", i, ks), "f", v} }},
		"SADD":  {"SADD", func(i, ks int, v string) []string { return []string{"SADD", key("s", i, ks), v} }},
	}

	tests, err := benchTests(all, strings.Split(*testList, ","))
	if err != nil {
		return err
	}

	val := strings.Repeat("x", *dataSize)

	fmt.Printf("cache-pot bench — %s · %d requests · %d connections · %d-byte values\n\n",
		*addr, *n, *conc, *dataSize)

	// One shared pool of connections is dialed up front and reused across every
	// test so we measure the server, not connection setup.
	conns := make([]*client.Client, *conc)
	for i := range conns {
		c, err := dial(*addr, *useTLS, *caCert, *insecure)
		if err != nil {
			return fmt.Errorf("dial connection %d: %w", i, err)
		}
		if *password != "" {
			if _, err := c.Do("AUTH", *password); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}
		defer c.Close()
		conns[i] = c
	}

	for _, t := range tests {
		res, err := runOne(t, conns, *n, *keyspace, val)
		if err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
		res.report(t.name, *quiet)
	}
	return nil
}

// runOne drives a single test to completion across the connection pool.
func runOne(t benchTest, conns []*client.Client, total, keyspace int, val string) (*benchResult, error) {
	var issued int64
	lat := make([][]time.Duration, len(conns))
	var firstErr atomic.Value // error
	var wg sync.WaitGroup

	start := time.Now()
	for w := range conns {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c := conns[w]
			rng := rand.New(rand.NewSource(int64(w) + 1))
			local := make([]time.Duration, 0, total/len(conns)+1)
			for {
				i := int(atomic.AddInt64(&issued, 1)) - 1
				if i >= total {
					break
				}
				args := t.build(rng.Intn(keyspace), keyspace, val)
				t0 := time.Now()
				_, err := c.Do(args...)
				local = append(local, time.Since(t0))
				if err != nil {
					firstErr.Store(err)
					return
				}
			}
			lat[w] = local
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)

	if e := firstErr.Load(); e != nil {
		return nil, e.(error)
	}

	var all []time.Duration
	for _, l := range lat {
		all = append(all, l...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	return &benchResult{count: len(all), elapsed: elapsed, lat: all}, nil
}

type benchResult struct {
	count   int
	elapsed time.Duration
	lat     []time.Duration // sorted ascending
}

func (r *benchResult) pct(p float64) time.Duration {
	if len(r.lat) == 0 {
		return 0
	}
	idx := int(p / 100 * float64(len(r.lat)-1))
	return r.lat[idx]
}

func (r *benchResult) report(name string, quiet bool) {
	rps := float64(r.count) / r.elapsed.Seconds()
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	if quiet {
		fmt.Printf("%-6s %10.0f req/s  p50=%.3fms p99=%.3fms\n", name, rps, ms(r.pct(50)), ms(r.pct(99)))
		return
	}
	fmt.Printf("== %s ==\n", name)
	fmt.Printf("  %d requests in %.3fs\n", r.count, r.elapsed.Seconds())
	fmt.Printf("  throughput : %.0f req/s\n", rps)
	fmt.Printf("  latency    : p50 %.3fms · p95 %.3fms · p99 %.3fms · max %.3fms\n",
		ms(r.pct(50)), ms(r.pct(95)), ms(r.pct(99)), ms(r.pct(100)))
	fmt.Println()
}

// key returns a keyspace-spread key like "k:4217".
func key(prefix string, i, keyspace int) string {
	if keyspace <= 1 {
		return prefix + ":0"
	}
	return prefix + ":" + strconv.Itoa(i%keyspace)
}
