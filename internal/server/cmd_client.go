package server

import (
	"fmt"
	"strconv"
	"strings"
)

// cmdClient implements the CLIENT subcommands used by RedisInsight-style
// tooling: LIST, ID, SETNAME, GETNAME, and KILL ID.
func (c *conn) cmdClient(args []string) error {
	if len(args) < 2 {
		return c.wrongArgs("client")
	}
	switch strings.ToUpper(args[1]) {
	case "LIST":
		var b strings.Builder
		for _, ci := range c.s.ClientsSnapshot() {
			fmt.Fprintf(&b, "id=%d addr=%s name=%s age=%d idle=%d cmd=%s\n",
				ci.ID, ci.Addr, ci.Name, ci.AgeSecs, ci.IdleSecs, strings.ToLower(ci.LastCmd))
		}
		return c.writeBulk(b.String())
	case "ID":
		return c.writeInt(int64(c.id))
	case "SETNAME":
		if len(args) != 3 {
			return c.wrongArgs("client|setname")
		}
		if strings.ContainsAny(args[2], " \n") {
			return c.writeError("ERR Client names cannot contain spaces, newlines or special characters.")
		}
		c.metamu.Lock()
		c.name = args[2]
		c.metamu.Unlock()
		return c.writeSimple("OK")
	case "GETNAME":
		c.metamu.Lock()
		name := c.name
		c.metamu.Unlock()
		return c.writeBulk(name)
	case "KILL":
		if len(args) != 4 || !strings.EqualFold(args[2], "ID") {
			return c.writeError("ERR syntax error (supported: CLIENT KILL ID <id>)")
		}
		id, err := strconv.ParseUint(args[3], 10, 64)
		if err != nil {
			return c.writeError("ERR client-id should be greater than 0")
		}
		target, ok := c.s.clientByID(id)
		if !ok || target.nc == nil {
			return c.writeInt(0)
		}
		target.nc.Close() // unblocks its read loop; deregistration runs in serveConn's defers
		return c.writeInt(1)
	default:
		return c.writeError("ERR unknown CLIENT subcommand '" + args[1] + "'; supported: LIST, ID, SETNAME, GETNAME, KILL")
	}
}
