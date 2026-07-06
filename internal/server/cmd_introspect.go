package server

import (
	"sort"
	"strconv"
	"strings"

	"github.com/subh05sus/cache-pot/internal/store"
)

// This file implements keyspace introspection commands: the SCAN family,
// MEMORY, RENAME/RENAMENX, and a minimal CONFIG.

// scanOpts holds the parsed optional arguments shared by the SCAN family.
type scanOpts struct {
	match string
	count int
	typ   string
}

// parseScanOpts parses [MATCH pat] [COUNT n] [TYPE t] starting at args[from].
// allowType is true only for SCAN (Redis rejects TYPE on HSCAN/SSCAN/ZSCAN).
func parseScanOpts(args []string, from int, allowType bool) (scanOpts, string) {
	o := scanOpts{match: "*", count: 10}
	for i := from; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return o, "ERR syntax error"
		}
		switch strings.ToUpper(args[i]) {
		case "MATCH":
			o.match = args[i+1]
		case "COUNT":
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return o, store.ErrNotInteger.Error()
			}
			o.count = n
		case "TYPE":
			if !allowType {
				return o, "ERR syntax error"
			}
			o.typ = strings.ToLower(args[i+1])
		default:
			return o, "ERR syntax error"
		}
	}
	return o, ""
}

// writeScanReply writes the standard two-element SCAN reply: next cursor as a
// bulk string, then the array of items.
func (c *conn) writeScanReply(cursor uint64, items []string) error {
	return c.writeArray(2, func(w respWriter) error {
		if err := w.WriteBulkString(strconv.FormatUint(cursor, 10)); err != nil {
			return err
		}
		return w.WriteStringArray(items)
	})
}

// cmdScan implements SCAN cursor [MATCH pat] [COUNT n] [TYPE t]. The cursor
// packs a shard index in the high 32 bits and an offset into that shard's
// sorted key list in the low 32, so it round-trips through clients that treat
// cursors as integers. COUNT bounds keys examined, not keys returned.
// Approximation: deleting keys in the shard currently being scanned shifts
// the offset, which can skip or duplicate keys within that one shard;
// completed shards are unaffected (Redis SCAN itself permits duplicates).
func (c *conn) cmdScan(args []string) error {
	if len(args) < 2 {
		return c.wrongArgs("scan")
	}
	cur, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		return c.writeError("ERR invalid cursor")
	}
	o, errMsg := parseScanOpts(args, 2, true)
	if errMsg != "" {
		return c.writeError(errMsg)
	}

	st := c.s.store
	shard := int(cur >> 32)
	offset := int(uint32(cur))
	budget := o.count
	var out []string
	for shard < st.ShardCount() && budget > 0 {
		keys := st.ShardKeys(shard)
		for offset < len(keys) && budget > 0 {
			k := keys[offset]
			offset++
			budget--
			if !store.MatchPattern(o.match, k) {
				continue
			}
			if o.typ != "" && st.Type(k) != o.typ {
				continue
			}
			out = append(out, k)
		}
		if offset >= len(keys) {
			shard++
			offset = 0
		}
	}
	next := uint64(0)
	if shard < st.ShardCount() {
		next = uint64(shard)<<32 | uint64(uint32(offset))
	}
	return c.writeScanReply(next, out)
}

// scanElements applies offset-cursor pagination over a sorted element list
// shared by HSCAN/SSCAN/ZSCAN. stride is 2 for field/value or member/score
// pairs and 1 for plain members; matching is applied to elems[i] (the field
// or member), with elems[i+1] carried along when stride is 2.
func (c *conn) scanElements(cursorArg string, elems []string, stride int, o scanOpts) error {
	cur, err := strconv.ParseUint(cursorArg, 10, 64)
	if err != nil {
		return c.writeError("ERR invalid cursor")
	}
	offset := int(cur) * stride
	budget := o.count
	var out []string
	for offset < len(elems) && budget > 0 {
		if store.MatchPattern(o.match, elems[offset]) {
			out = append(out, elems[offset:offset+stride]...)
		}
		offset += stride
		budget--
	}
	next := uint64(0)
	if offset < len(elems) {
		next = uint64(offset / stride)
	}
	return c.writeScanReply(next, out)
}

// cmdHScan implements HSCAN key cursor [MATCH pat] [COUNT n] over the hash's
// fields in sorted order.
func (c *conn) cmdHScan(args []string) error {
	if len(args) < 3 {
		return c.wrongArgs("hscan")
	}
	o, errMsg := parseScanOpts(args, 3, false)
	if errMsg != "" {
		return c.writeError(errMsg)
	}
	flat, err := c.s.store.HGetAll(args[1])
	if err != nil {
		return c.storeErr(err)
	}
	// HGetAll returns [f1,v1,f2,v2,...] in map order; sort pairs by field so
	// the offset cursor resumes deterministically.
	type pair struct{ f, v string }
	pairs := make([]pair, 0, len(flat)/2)
	for i := 0; i+1 < len(flat); i += 2 {
		pairs = append(pairs, pair{flat[i], flat[i+1]})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].f < pairs[j].f })
	elems := make([]string, 0, len(pairs)*2)
	for _, p := range pairs {
		elems = append(elems, p.f, p.v)
	}
	return c.scanElements(args[2], elems, 2, o)
}

// cmdSScan implements SSCAN key cursor [MATCH pat] [COUNT n] over the set's
// members in sorted order.
func (c *conn) cmdSScan(args []string) error {
	if len(args) < 3 {
		return c.wrongArgs("sscan")
	}
	o, errMsg := parseScanOpts(args, 3, false)
	if errMsg != "" {
		return c.writeError(errMsg)
	}
	members, err := c.s.store.SMembers(args[1])
	if err != nil {
		return c.storeErr(err)
	}
	sort.Strings(members)
	return c.scanElements(args[2], members, 1, o)
}

// cmdZScan implements ZSCAN key cursor [MATCH pat] [COUNT n]; elements come
// out as member,score pairs in score order (the zset's deterministic order).
func (c *conn) cmdZScan(args []string) error {
	if len(args) < 3 {
		return c.wrongArgs("zscan")
	}
	o, errMsg := parseScanOpts(args, 3, false)
	if errMsg != "" {
		return c.writeError(errMsg)
	}
	members, err := c.s.store.ZRange(args[1], 0, -1)
	if err != nil {
		return c.storeErr(err)
	}
	elems := make([]string, 0, len(members)*2)
	for _, m := range members {
		elems = append(elems, m.Member, formatFloat(m.Score))
	}
	return c.scanElements(args[2], elems, 2, o)
}

// cmdMemory implements MEMORY USAGE key [SAMPLES n]. SAMPLES is accepted for
// redis-cli compatibility and ignored — the estimate always walks the whole
// value. The returned size is a documented heuristic (see store/memory.go).
func (c *conn) cmdMemory(args []string) error {
	if len(args) < 2 {
		return c.wrongArgs("memory")
	}
	switch strings.ToUpper(args[1]) {
	case "USAGE":
		if len(args) != 3 && len(args) != 5 {
			return c.wrongArgs("memory|usage")
		}
		if len(args) == 5 {
			if strings.ToUpper(args[3]) != "SAMPLES" {
				return c.writeError("ERR syntax error")
			}
			if _, err := strconv.Atoi(args[4]); err != nil {
				return c.writeError(store.ErrNotInteger.Error())
			}
		}
		n, ok := c.s.store.MemoryUsage(args[2])
		if !ok {
			return c.writeNull()
		}
		return c.writeInt(n)
	default:
		return c.writeError("ERR unknown MEMORY subcommand '" + args[1] + "'; supported: USAGE")
	}
}

func (c *conn) cmdRename(args []string) error {
	if len(args) != 3 {
		return c.wrongArgs("rename")
	}
	if err := c.s.store.Rename(args[1], args[2]); err != nil {
		return c.storeErr(err)
	}
	return c.writeSimple("OK")
}

func (c *conn) cmdRenameNX(args []string) error {
	if len(args) != 3 {
		return c.wrongArgs("renamenx")
	}
	ok, err := c.s.store.RenameNX(args[1], args[2])
	if err != nil {
		return c.storeErr(err)
	}
	return c.writeInt(boolToInt(ok))
}

// cmdConfig implements a minimal CONFIG GET/SET. Parameters are served from
// the configParams table; CONFIG GET on anything else returns an empty array
// (like Redis with a non-matching glob), which keeps redis-cli and client
// libraries that probe CONFIG on connect happy.
func (c *conn) cmdConfig(args []string) error {
	if len(args) < 3 {
		return c.wrongArgs("config")
	}
	switch strings.ToUpper(args[1]) {
	case "GET":
		var out []string
		for _, p := range c.configParams() {
			if store.MatchPattern(strings.ToLower(args[2]), p.name) {
				out = append(out, p.name, p.get())
			}
		}
		return c.writeStringArray(out)
	case "SET":
		if len(args) != 4 {
			return c.wrongArgs("config|set")
		}
		for _, p := range c.configParams() {
			if strings.EqualFold(args[2], p.name) {
				if err := p.set(args[3]); err != nil {
					return c.writeError("ERR CONFIG SET failed - " + err.Error())
				}
				return c.writeSimple("OK")
			}
		}
		return c.writeError("ERR Unknown option or number of arguments for CONFIG SET - '" + args[2] + "'")
	default:
		return c.writeError("ERR unknown CONFIG subcommand '" + args[1] + "'; supported: GET, SET")
	}
}

// errInvalidConfigValue rejects out-of-range CONFIG SET values.
var errInvalidConfigValue = errInvalid("argument couldn't be parsed into an integer")

type errInvalid string

func (e errInvalid) Error() string { return string(e) }

// configParam is one settable server parameter exposed through CONFIG.
type configParam struct {
	name string
	get  func() string
	set  func(string) error
}

// configParams lists the supported parameters. The slowlog parameters are
// wired in slowlog.go; keeping the table a method lets later files extend it.
func (c *conn) configParams() []configParam {
	return c.s.configParams()
}

// configParams returns the server's supported CONFIG parameters.
func (s *Server) configParams() []configParam {
	return []configParam{
		{
			name: "slowlog-log-slower-than",
			get:  func() string { return strconv.FormatInt(s.SlowlogThresholdUs(), 10) },
			set: func(v string) error {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return err
				}
				s.SlowlogSetThresholdUs(n)
				return nil
			},
		},
		{
			name: "slowlog-max-len",
			get:  func() string { return strconv.FormatInt(s.SlowlogMaxLen(), 10) },
			set: func(v string) error {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 0 {
					return errInvalidConfigValue
				}
				s.SlowlogSetMaxLen(n)
				return nil
			},
		},
	}
}
