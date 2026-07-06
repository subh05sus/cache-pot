// Package pubsub implements Cache-Pot's PUBLISH/SUBSCRIBE channel registry. Each
// subscriber receives Messages on a buffered Go channel; a slow subscriber
// that fills its buffer drops messages rather than blocking publishers.
package pubsub

import "sync"

// Message is a single published payload on a channel.
type Message struct {
	Channel string
	Payload string
}

// Subscription is a handle held by one connection for one channel.
type Subscription struct {
	Channel string
	C       chan Message
}

// Broker routes published messages to subscribers.
type Broker struct {
	mu       sync.RWMutex
	channels map[string]map[*Subscription]struct{}
}

// NewBroker returns an empty broker.
func NewBroker() *Broker {
	return &Broker{channels: make(map[string]map[*Subscription]struct{})}
}

// Subscribe registers interest in channel and returns a subscription whose C
// receives matching messages.
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

// Unsubscribe removes a subscription and closes its channel.
func (b *Broker) Unsubscribe(sub *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if subs, ok := b.channels[sub.Channel]; ok {
		if _, present := subs[sub]; present {
			delete(subs, sub)
			close(sub.C)
			if len(subs) == 0 {
				delete(b.channels, sub.Channel)
			}
		}
	}
}

// Publish delivers payload to every subscriber of channel and returns the
// number of clients that received it. Delivery is non-blocking: a subscriber
// whose buffer is full is skipped.
func (b *Broker) Publish(channel, payload string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	subs := b.channels[channel]
	delivered := 0
	for sub := range subs {
		select {
		case sub.C <- Message{Channel: channel, Payload: payload}:
			delivered++
		default:
			// Subscriber is too slow; drop to protect the publisher.
		}
	}
	return delivered
}
