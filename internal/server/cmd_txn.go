package server

import "fmt"

// Transactions (MULTI/EXEC/DISCARD/WATCH/UNWATCH). Between MULTI and EXEC,
// commands are not executed but queued and acknowledged with +QUEUED; EXEC
// then runs them in order and returns an array of their replies. WATCH gives
// optimistic concurrency: if any watched key is modified before EXEC, EXEC
// aborts and returns a null array.
//
// Isolation caveat: unlike single-threaded Redis, EXEC does not hold a global
// lock, so a command on another connection can interleave between the queued
// commands. Each queued command is individually atomic (it takes the same
// shard locks it always would), and WATCH still detects a concurrent change to
// a watched key, but a whole EXEC is not one isolated unit. This matches the
// store's existing best-effort multi-key semantics.

// queuedCmd is one command captured between MULTI and EXEC.
type queuedCmd struct {
	name string
	args []string
}

func (c *conn) cmdMulti(args []string) error {
	if len(args) != 1 {
		return c.wrongArgs("multi")
	}
	if c.inMulti {
		return c.writeError("ERR MULTI calls can not be nested")
	}
	c.inMulti = true
	c.multiErr = false
	c.queued = nil
	return c.writeSimple("OK")
}

// queueCommand records a command for later execution by EXEC. Unknown or
// transaction-forbidden commands dirty the transaction so EXEC aborts.
func (c *conn) queueCommand(name string, args []string) error {
	switch name {
	case "SUBSCRIBE", "UNSUBSCRIBE", "PSUBSCRIBE", "PUNSUBSCRIBE", "MONITOR":
		c.multiErr = true
		return c.writeError("ERR " + name + " is not allowed in transactions")
	}
	if _, ok := c.s.dispatch[name]; !ok {
		c.multiErr = true
		return c.writeError(fmt.Sprintf("ERR unknown command '%s'", args[0]))
	}
	c.queued = append(c.queued, queuedCmd{name: name, args: args})
	return c.writeSimple("QUEUED")
}

func (c *conn) cmdDiscard(args []string) error {
	if len(args) != 1 {
		return c.wrongArgs("discard")
	}
	if !c.inMulti {
		return c.writeError("ERR DISCARD without MULTI")
	}
	c.clearMulti()
	return c.writeSimple("OK")
}

func (c *conn) cmdExec(args []string) error {
	if len(args) != 1 {
		return c.wrongArgs("exec")
	}
	if !c.inMulti {
		return c.writeError("ERR EXEC without MULTI")
	}
	if c.multiErr {
		c.clearMulti()
		return c.writeError("EXECABORT Transaction discarded because of previous errors.")
	}
	// Optimistic lock: if any watched key changed since it was WATCHed, abort
	// with a null array and run nothing.
	if c.watchChanged() {
		c.clearMulti()
		return c.writeNullArray()
	}

	queued := c.queued
	// Exit MULTI state before running so queued handlers behave normally and a
	// nested control command can't recurse into queuing.
	c.inMulti = false
	c.queued = nil
	c.unwatchAll()

	if err := c.writeArrayHeaderRaw(len(queued)); err != nil {
		return err
	}
	for _, q := range queued {
		if err := c.s.runHandler(c, q.name, q.args, c.s.dispatch[q.name]); err != nil {
			return err
		}
	}
	return nil
}

func (c *conn) cmdWatch(args []string) error {
	if len(args) < 2 {
		return c.wrongArgs("watch")
	}
	if c.inMulti {
		return c.writeError("ERR WATCH inside MULTI is not allowed")
	}
	if c.watched == nil {
		c.watched = make(map[string]uint64)
	}
	for _, k := range args[1:] {
		if _, ok := c.watched[k]; ok {
			continue // already watching; keep the original version
		}
		c.watched[k] = c.s.store.Watch(k)
	}
	return c.writeSimple("OK")
}

func (c *conn) cmdUnwatch(args []string) error {
	if len(args) != 1 {
		return c.wrongArgs("unwatch")
	}
	c.unwatchAll()
	return c.writeSimple("OK")
}

// watchChanged reports whether any watched key's version advanced since WATCH.
func (c *conn) watchChanged() bool {
	for k, v := range c.watched {
		if c.s.store.WatchVersion(k) != v {
			return true
		}
	}
	return false
}

// clearMulti discards any queued transaction and releases watches.
func (c *conn) clearMulti() {
	c.inMulti = false
	c.multiErr = false
	c.queued = nil
	c.unwatchAll()
}

// unwatchAll releases every WATCH this connection holds. Safe to call when
// nothing is watched (e.g. on disconnect).
func (c *conn) unwatchAll() {
	for k := range c.watched {
		c.s.store.Unwatch(k)
	}
	c.watched = nil
}

// writeArrayHeaderRaw emits a bare array header under the write lock. EXEC uses
// it before letting each queued handler append its own reply, so the whole
// EXEC response is one RESP array.
func (c *conn) writeArrayHeaderRaw(n int) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.w.WriteArrayHeader(n)
}
