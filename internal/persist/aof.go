// Append-only-file durability (V3 of the PRD's persistence roadmap): every
// write command is appended to a log as a RESP array, exactly as a client
// would have sent it, and replayed through the normal dispatch table on
// startup. Commands whose effect depends on the wall clock (EXPIRE, SET EX)
// are translated to absolute PEXPIREAT forms before logging so replay is
// deterministic.
//
// Known limitation: a command is logged after it executes, and the store's
// shard locks do not extend over the log write, so two connections racing on
// the same key can log in a different order than they executed. Single-writer
// keys — the common cache pattern — are unaffected.
package persist

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/subh05sus/cache-pot/internal/resp"
	"github.com/subh05sus/cache-pot/internal/store"
)

// SyncPolicy controls when the AOF is fsynced to disk.
type SyncPolicy int

const (
	// SyncEverySec fsyncs once per second in the background (default).
	SyncEverySec SyncPolicy = iota
	// SyncAlways fsyncs after every appended command. Safest, slowest.
	SyncAlways
	// SyncNo never fsyncs explicitly; the OS decides. Fastest, least safe.
	SyncNo
)

// ParseSyncPolicy converts a flag value ("always", "everysec", "no") to a
// SyncPolicy.
func ParseSyncPolicy(s string) (SyncPolicy, error) {
	switch s {
	case "always":
		return SyncAlways, nil
	case "everysec", "":
		return SyncEverySec, nil
	case "no":
		return SyncNo, nil
	default:
		return 0, fmt.Errorf("invalid aof-fsync policy %q (want always, everysec or no)", s)
	}
}

// AOF is an append-only command log. Append is safe for concurrent use.
type AOF struct {
	path   string
	policy SyncPolicy
	active atomic.Bool // false until replay finishes, so replay doesn't re-log

	mu sync.Mutex // guards f and w
	f  *os.File
	w  *resp.Writer
}

// OpenAOF opens (creating if needed) the append-only file at path.
func OpenAOF(path string, policy SyncPolicy) (*AOF, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &AOF{path: path, policy: policy, f: f, w: resp.NewWriter(f)}, nil
}

// Active reports whether appends are currently being recorded. It is false
// during startup replay so replayed commands are not logged twice.
func (a *AOF) Active() bool { return a.active.Load() }

// SetActive enables or disables recording.
func (a *AOF) SetActive(v bool) { a.active.Store(v) }

// HasData reports whether the file exists and is non-empty, i.e. whether it
// should be treated as the authoritative dataset over a snapshot.
func (a *AOF) HasData() bool {
	fi, err := os.Stat(a.path)
	return err == nil && fi.Size() > 0
}

// Append logs one command. The args are the full command including its name.
func (a *AOF) Append(args []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.w.WriteStringArray(args); err != nil {
		return err
	}
	if err := a.w.Flush(); err != nil {
		return err
	}
	if a.policy == SyncAlways {
		return a.f.Sync()
	}
	return nil
}

// Sync flushes the file to stable storage.
func (a *AOF) Sync() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.f.Sync()
}

// StartSync fsyncs once per second until stop is closed (SyncEverySec only).
// Run it in its own goroutine.
func (a *AOF) StartSync(stop <-chan struct{}) {
	if a.policy != SyncEverySec {
		<-stop
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if err := a.Sync(); err != nil {
				fmt.Fprintf(os.Stderr, "cache-pot: aof fsync failed: %v\n", err)
			}
		}
	}
}

// Close syncs and closes the file.
func (a *AOF) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.f.Sync()
	return a.f.Close()
}

// Replay reads the log from the start and calls apply for each command. A
// truncated or corrupt tail (e.g. from a crash mid-write) stops the replay
// and is reported via corrupt so the caller can compact the file; everything
// before it is still applied.
func (a *AOF) Replay(apply func(args []string)) (n int, corrupt bool, err error) {
	f, err := os.Open(a.path)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer f.Close()

	r := resp.NewReader(f)
	for {
		args, rerr := r.ReadCommand()
		if errors.Is(rerr, io.EOF) {
			return n, false, nil
		}
		if rerr != nil {
			// Partial or garbled record at the tail: keep what replayed.
			return n, true, nil
		}
		if len(args) == 0 {
			continue
		}
		apply(args)
		n++
	}
}

// Rewrite compacts the log: it writes the given records as the minimal set of
// commands to a temp file, then atomically swaps it in. Safe to call while
// the server is running; concurrent appends block until the swap completes.
func (a *AOF) Rewrite(recs []store.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	tmp := a.path + ".rewrite"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := resp.NewWriter(f)
	for _, rec := range recs {
		for _, cmd := range recordCommands(rec) {
			if err := w.WriteStringArray(cmd); err != nil {
				f.Close()
				os.Remove(tmp)
				return err
			}
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	// The old handle must be closed before the rename can replace the file on
	// Windows. If anything past this point fails, reopen so appends keep going
	// to whichever file is at the path.
	a.f.Close()
	if err := os.Rename(tmp, a.path); err != nil {
		os.Remove(tmp)
		if nf, err2 := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err2 == nil {
			a.f = nf
			a.w = resp.NewWriter(nf)
		}
		return err
	}
	nf, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	a.f = nf
	a.w = resp.NewWriter(nf)
	return nil
}

// recordCommands converts one exported record into replayable commands. It
// deliberately emits one element per command (one HSET per field, one SADD
// per member, ...) so it never depends on variadic command arity.
func recordCommands(rec store.Record) [][]string {
	var out [][]string
	switch rec.Type {
	case "string":
		out = append(out, []string{"SET", rec.Key, rec.Str})
	case "hash":
		for f, v := range rec.Hash {
			out = append(out, []string{"HSET", rec.Key, f, v})
		}
	case "list":
		for _, it := range rec.List {
			out = append(out, []string{"RPUSH", rec.Key, it})
		}
	case "set":
		for _, m := range rec.Set {
			out = append(out, []string{"SADD", rec.Key, m})
		}
	case "zset":
		for _, m := range rec.ZSet {
			out = append(out, []string{"ZADD", rec.Key, strconv.FormatFloat(m.Score, 'g', -1, 64), m.Member})
		}
	case "vector":
		for _, v := range rec.Vectors {
			cmd := make([]string, 0, len(v.Vec)+5)
			cmd = append(cmd, "VSET", rec.Key, v.ID)
			for _, f := range v.Vec {
				cmd = append(cmd, strconv.FormatFloat(float64(f), 'g', -1, 32))
			}
			cmd = append(cmd, "META", v.Meta)
			out = append(out, cmd)
		}
	}
	if rec.ExpireAtUnixMs != 0 {
		out = append(out, []string{"PEXPIREAT", rec.Key, strconv.FormatInt(rec.ExpireAtUnixMs, 10)})
	}
	return out
}
