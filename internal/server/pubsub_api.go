package server

import "github.com/subh05sus/cache-pot/internal/pubsub"

// Exported pub/sub accessors for the dashboard's live subscribe stream. The
// dashboard consumes the broker directly (rather than through Execute)
// because subscriptions are long-lived, not one-shot commands.

// PubSubSubscribe subscribes to an exact channel.
func (s *Server) PubSubSubscribe(channel string) *pubsub.Subscription {
	return s.broker.Subscribe(channel)
}

// PubSubSubscribePattern subscribes to a glob pattern.
func (s *Server) PubSubSubscribePattern(pattern string) *pubsub.Subscription {
	return s.broker.SubscribePattern(pattern)
}

// PubSubUnsubscribe releases a subscription obtained from the methods above.
func (s *Server) PubSubUnsubscribe(sub *pubsub.Subscription) {
	s.broker.Unsubscribe(sub)
}
