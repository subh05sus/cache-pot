package server

import "github.com/subh05sus/cache-pot/internal/pubsub"

func (c *conn) cmdSubscribe(args []string) error {
	if len(args) < 2 {
		return c.wrongArgs("subscribe")
	}
	for _, channel := range args[1:] {
		c.submu.Lock()
		if _, already := c.subs[channel]; already {
			c.submu.Unlock()
			continue
		}
		sub := c.s.broker.Subscribe(channel)
		c.subs[channel] = sub
		count := len(c.subs) + len(c.psubs)
		c.submu.Unlock()

		// Confirm the subscription: ["subscribe", channel, count].
		if err := c.writeSubReply("subscribe", channel, count); err != nil {
			return err
		}
		go c.forward(sub)
	}
	return c.flush()
}

func (c *conn) cmdPSubscribe(args []string) error {
	if len(args) < 2 {
		return c.wrongArgs("psubscribe")
	}
	for _, pattern := range args[1:] {
		c.submu.Lock()
		if _, already := c.psubs[pattern]; already {
			c.submu.Unlock()
			continue
		}
		sub := c.s.broker.SubscribePattern(pattern)
		c.psubs[pattern] = sub
		count := len(c.subs) + len(c.psubs)
		c.submu.Unlock()

		if err := c.writeSubReply("psubscribe", pattern, count); err != nil {
			return err
		}
		go c.forward(sub)
	}
	return c.flush()
}

func (c *conn) cmdPUnsubscribe(args []string) error {
	c.submu.Lock()
	var patterns []string
	if len(args) < 2 {
		for p := range c.psubs {
			patterns = append(patterns, p)
		}
	} else {
		patterns = args[1:]
	}
	c.submu.Unlock()

	if len(patterns) == 0 {
		return c.writeSubReply("punsubscribe", "", 0)
	}
	for _, pattern := range patterns {
		c.submu.Lock()
		sub, ok := c.psubs[pattern]
		if ok {
			c.s.broker.Unsubscribe(sub)
			delete(c.psubs, pattern)
		}
		count := len(c.subs) + len(c.psubs)
		c.submu.Unlock()
		if err := c.writeSubReply("punsubscribe", pattern, count); err != nil {
			return err
		}
	}
	return nil
}

func (c *conn) cmdUnsubscribe(args []string) error {
	c.submu.Lock()
	var channels []string
	if len(args) < 2 {
		for ch := range c.subs {
			channels = append(channels, ch)
		}
	} else {
		channels = args[1:]
	}
	c.submu.Unlock()

	if len(channels) == 0 {
		return c.writeSubReply("unsubscribe", "", 0)
	}
	for _, channel := range channels {
		c.submu.Lock()
		sub, ok := c.subs[channel]
		if ok {
			c.s.broker.Unsubscribe(sub) // closes sub.C, ending the forward goroutine
			delete(c.subs, channel)
		}
		count := len(c.subs) + len(c.psubs)
		c.submu.Unlock()
		if err := c.writeSubReply("unsubscribe", channel, count); err != nil {
			return err
		}
	}
	return nil
}

func (c *conn) cmdPublish(args []string) error {
	if len(args) != 3 {
		return c.wrongArgs("publish")
	}
	n := c.s.broker.Publish(args[1], args[2])
	return c.writeInt(int64(n))
}

// forward pumps messages from a subscription to the client until the
// subscription's channel is closed (on unsubscribe / disconnect). Messages
// delivered through a pattern subscription use the 4-element "pmessage"
// format; exact-channel messages use the 3-element "message" format.
func (c *conn) forward(sub *pubsub.Subscription) {
	for msg := range sub.C {
		c.wmu.Lock()
		if msg.Pattern != "" {
			c.w.WriteArrayHeader(4)
			c.w.WriteBulkString("pmessage")
			c.w.WriteBulkString(msg.Pattern)
		} else {
			c.w.WriteArrayHeader(3)
			c.w.WriteBulkString("message")
		}
		c.w.WriteBulkString(msg.Channel)
		c.w.WriteBulkString(msg.Payload)
		c.w.Flush()
		c.wmu.Unlock()
	}
}

// writeSubReply writes the 3-element (un)subscribe acknowledgement.
func (c *conn) writeSubReply(kind, channel string, count int) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.w.WriteArrayHeader(3)
	c.w.WriteBulkString(kind)
	if channel == "" {
		c.w.WriteNull()
	} else {
		c.w.WriteBulkString(channel)
	}
	return c.w.WriteInteger(int64(count))
}
