package bus

import (
	"context"
	"sync"
	"sync/atomic"
)

// Subscription represents a topic subscription on the Bus.
type Subscription struct {
	topic   string
	ch      chan Message
	bus     *Bus
	done    chan struct{} // closed on Unsubscribe or Bus.Close; unblocks senders
	once    sync.Once     // guards close(done)
	dropped atomic.Int64  // count of messages dropped due to full buffer
}

// Ch returns a read-only channel for receiving messages.
// Consumers should select on both Ch() and Done() to detect unsubscription:
//
//	for {
//	    select {
//	    case msg := <-sub.Ch():
//	        handle(msg)
//	    case <-sub.Done():
//	        return
//	    }
//	}
func (s *Subscription) Ch() <-chan Message {
	return s.ch
}

// Done returns a channel that is closed when the subscription is cancelled
// (via Unsubscribe or Bus.Close).
func (s *Subscription) Done() <-chan struct{} {
	return s.done
}

// Topic returns the topic this subscription is for.
func (s *Subscription) Topic() string {
	return s.topic
}

// Dropped returns the number of messages dropped due to a full buffer.
func (s *Subscription) Dropped() int64 {
	return s.dropped.Load()
}

// Unsubscribe removes this subscription from the bus and signals consumers
// via Done(). Safe to call multiple times.
func (s *Subscription) Unsubscribe() {
	s.bus.removeSub(s)
	s.once.Do(func() {
		close(s.done)
	})
}

// trySend attempts a non-blocking send. If the subscription has been cancelled
// (done closed) or the buffer is full, the message is dropped.
func (s *Subscription) trySend(msg Message) {
	select {
	case <-s.done:
		return
	default:
	}
	select {
	case s.ch <- msg:
	default:
		s.dropped.Add(1)
	}
}

// trySendWait blocks until the message is delivered, the subscription is cancelled,
// or the context expires.
func (s *Subscription) trySendWait(ctx context.Context, msg Message) error {
	select {
	case s.ch <- msg:
		return nil
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
