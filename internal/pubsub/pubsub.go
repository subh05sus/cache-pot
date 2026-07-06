// Package pubsub implements Cache-Pot's PUBLISH/SUBSCRIBE channel registry. Each
// subscriber receives Messages on a buffered Go channel; a slow subscriber
// that fills its buffer drops messages rather than blocking publishers.
// Pattern subscriptions (PSUBSCRIBE) match channels with a glob matcher
// injected at construction, which keeps this package dependency-free.
package pubsub

import "sync"

// Message is a single published payload on a channel. Pattern is non-empty
// only when the message was delivered through a pattern subscription.
type Message struct {
	Channel string
	Pattern string
	Payload string
}

// Subscription is a handle held by one connection for one channel or pattern.
type Subscription struct {
	Channel string // exact channel, or "" for pattern subscriptions
	Pattern string // glob pattern, or "" for channel subscriptions
	C       chan Message
}

// Broker routes published messages to subscribers.
type Broker struct {
	mu       sync.RWMutex
	channels map[string]map[*Subscription]struct{}
	patterns map[string]map[*Subscription]struct{}
	match    func(pattern, s string) bool
}

// NewBroker returns an empty broker. match implements glob-style pattern
// matching for PSUBSCRIBE; nil disables pattern delivery.
func NewBroker(match func(pattern, s string) bool) *Broker {
	return &Broker{
		channels: make(map[string]map[*Subscription]struct{}),
		patterns: make(map[string]map[*Subscription]struct{}),
		match:    match,
	}
}

// Subscribe registers interest in an exact channel and returns a subscription
// whose C receives matching messages.
func (b *Broker) Subscribe(channel string) *Subscription {
	sub := &Subscription{Channel: channel, C: make(chan Message, 64)}
	b.mu.Lock()
	defer b.mu.Unlock()
	subs, ok := b.channels[channel]
	if !ok {
		subs = make(map[*Subscription]struct{})
		b.channels[channel] = subs
	}
	subs[sub] = struct{}{}
	return sub
}

// SubscribePattern registers interest in every channel matching pattern.
func (b *Broker) SubscribePattern(pattern string) *Subscription {
	sub := &Subscription{Pattern: pattern, C: make(chan Message, 64)}
	b.mu.Lock()
	defer b.mu.Unlock()
	subs, ok := b.patterns[pattern]
	if !ok {
		subs = make(map[*Subscription]struct{})
		b.patterns[pattern] = subs
	}
	subs[sub] = struct{}{}
	return sub
}

// Unsubscribe removes a subscription (channel or pattern) and closes its
// channel.
func (b *Broker) Unsubscribe(sub *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	registry, key := b.channels, sub.Channel
	if sub.Pattern != "" {
		registry, key = b.patterns, sub.Pattern
	}
	if subs, ok := registry[key]; ok {
		if _, present := subs[sub]; present {
			delete(subs, sub)
			close(sub.C)
			if len(subs) == 0 {
				delete(registry, key)
			}
		}
	}
}

// Publish delivers payload to every subscriber of channel — exact and
// pattern — and returns the number of clients that received it. Delivery is
// non-blocking: a subscriber whose buffer is full is skipped.
func (b *Broker) Publish(channel, payload string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	delivered := 0
	for sub := range b.channels[channel] {
		select {
		case sub.C <- Message{Channel: channel, Payload: payload}:
			delivered++
		default:
			// Subscriber is too slow; drop to protect the publisher.
		}
	}
	if b.match == nil {
		return delivered
	}
	for pattern, subs := range b.patterns {
		if !b.match(pattern, channel) {
			continue
		}
		for sub := range subs {
			select {
			case sub.C <- Message{Channel: channel, Pattern: pattern, Payload: payload}:
				delivered++
			default:
			}
		}
	}
	return delivered
}
