package bus

import (
	"context"
	"sync"
)

// Message is the unit of communication on the bus.
type Message struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// Bus is an in-memory pub/sub event bus.
// Publish is non-blocking (drops on full buffer); PublishWait is blocking.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string][]*Subscription
	closed      bool
}

// New creates a new Bus.
func New() *Bus {
	return &Bus{
		subscribers: make(map[string][]*Subscription),
	}
}

// Publish sends a message to all subscribers of the given topic.
// Non-blocking: if a subscriber's buffer is full, the message is dropped for that subscriber.
// No-op if the bus is closed or there are no subscribers.
func (b *Bus) Publish(topic string, msg Message) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	for _, sub := range b.subscribers[topic] {
		sub.trySend(msg)
	}
}

// PublishWait sends a message to all subscribers of the given topic, blocking until
// every subscriber has received the message or the context is cancelled.
func (b *Bus) PublishWait(ctx context.Context, topic string, msg Message) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil
	}
	// snapshot subscribers under lock
	subs := make([]*Subscription, len(b.subscribers[topic]))
	copy(subs, b.subscribers[topic])
	b.mu.RUnlock()

	for _, sub := range subs {
		if err := sub.trySendWait(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

// Subscribe creates a new subscription for the given topic with the specified buffer size.
// If bufSize < 0, it is treated as 0 (unbuffered).
// If the bus is already closed, the returned subscription's Done() channel will be closed immediately.
func (b *Bus) Subscribe(topic string, bufSize int) *Subscription {
	if bufSize < 0 {
		bufSize = 0
	}

	sub := &Subscription{
		topic: topic,
		ch:    make(chan Message, bufSize),
		done:  make(chan struct{}),
		bus:   b,
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		close(sub.done)
		return sub
	}

	b.subscribers[topic] = append(b.subscribers[topic], sub)
	return sub
}

// Close signals all subscriptions via Done() and marks the bus as closed.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true

	for _, subs := range b.subscribers {
		for _, sub := range subs {
			sub.once.Do(func() {
				close(sub.done)
			})
		}
	}
	b.subscribers = nil
}

// removeSub removes a subscription from the bus (called by Subscription.Unsubscribe).
func (b *Bus) removeSub(sub *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	subs := b.subscribers[sub.topic]
	for i, s := range subs {
		if s == sub {
			subs = append(subs[:i], subs[i+1:]...)
			if len(subs) == 0 {
				delete(b.subscribers, sub.topic)
			} else {
				b.subscribers[sub.topic] = subs
			}
			break
		}
	}
}
