package bus

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPublishNoSubscribers(t *testing.T) {
	b := New()
	defer b.Close()
	// must not panic or block
	b.Publish("topic", Message{Type: "test"})
}

func TestSubscribeAndReceive(t *testing.T) {
	b := New()
	defer b.Close()

	sub := b.Subscribe("topic", 1)
	defer sub.Unsubscribe()

	b.Publish("topic", Message{Type: "hello", Payload: TextPayload{Delta: "world"}})

	select {
	case msg := <-sub.Ch():
		if msg.Type != "hello" {
			t.Fatalf("got type %q, want %q", msg.Type, "hello")
		}
		p, ok := ParseText(msg)
		if !ok {
			t.Fatal("expected TextPayload")
		}
		if p.Delta != "world" {
			t.Fatalf("got delta %q, want %q", p.Delta, "world")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestMultipleSubscribersSameTopic(t *testing.T) {
	b := New()
	defer b.Close()

	sub1 := b.Subscribe("topic", 1)
	sub2 := b.Subscribe("topic", 1)
	defer sub1.Unsubscribe()
	defer sub2.Unsubscribe()

	b.Publish("topic", Message{Type: "x"})

	for _, sub := range []*Subscription{sub1, sub2} {
		select {
		case msg := <-sub.Ch():
			if msg.Type != "x" {
				t.Fatalf("got type %q, want %q", msg.Type, "x")
			}
		case <-time.After(time.Second):
			t.Fatal("timeout")
		}
	}
}

func TestTopicIsolation(t *testing.T) {
	b := New()
	defer b.Close()

	subA := b.Subscribe("a", 1)
	subB := b.Subscribe("b", 1)
	defer subA.Unsubscribe()
	defer subB.Unsubscribe()

	b.Publish("a", Message{Type: "for-a"})

	select {
	case msg := <-subA.Ch():
		if msg.Type != "for-a" {
			t.Fatalf("unexpected type %q", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout on sub A")
	}

	select {
	case msg := <-subB.Ch():
		t.Fatalf("sub B should not receive, got %v", msg)
	default:
		// expected
	}
}

func TestPublishWaitDelivery(t *testing.T) {
	b := New()
	defer b.Close()

	sub1 := b.Subscribe("t", 1)
	sub2 := b.Subscribe("t", 1)
	defer sub1.Unsubscribe()
	defer sub2.Unsubscribe()

	ctx := context.Background()
	err := b.PublishWait(ctx, "t", Message{Type: "pw"})
	if err != nil {
		t.Fatalf("PublishWait error: %v", err)
	}

	for _, sub := range []*Subscription{sub1, sub2} {
		select {
		case msg := <-sub.Ch():
			if msg.Type != "pw" {
				t.Fatalf("got %q", msg.Type)
			}
		default:
			t.Fatal("message not delivered")
		}
	}
}

func TestPublishWaitContextCancel(t *testing.T) {
	b := New()
	defer b.Close()

	// subscriber with zero buffer — send will block
	sub := b.Subscribe("t", 0)
	defer sub.Unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	err := b.PublishWait(ctx, "t", Message{Type: "x"})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := New()
	defer b.Close()

	sub := b.Subscribe("t", 1)
	sub.Unsubscribe()

	// Done() should be closed
	select {
	case <-sub.Done():
		// expected
	default:
		t.Fatal("expected Done() to be closed after Unsubscribe")
	}

	// publish after unsubscribe should not panic and should not deliver
	b.Publish("t", Message{Type: "x"})

	select {
	case <-sub.Ch():
		t.Fatal("should not receive after Unsubscribe")
	default:
		// expected — message dropped via done check in trySend
	}
}

func TestDoubleUnsubscribe(t *testing.T) {
	b := New()
	defer b.Close()

	sub := b.Subscribe("t", 1)
	sub.Unsubscribe()
	sub.Unsubscribe() // must not panic
}

func TestCloseSignalsAllSubscriptions(t *testing.T) {
	b := New()

	sub1 := b.Subscribe("a", 1)
	sub2 := b.Subscribe("b", 1)

	b.Close()

	for _, sub := range []*Subscription{sub1, sub2} {
		select {
		case <-sub.Done():
			// expected
		default:
			t.Fatal("expected Done() to be closed after Bus.Close")
		}
	}
}

func TestPublishAfterClose(t *testing.T) {
	b := New()
	b.Close()
	// must not panic
	b.Publish("t", Message{Type: "x"})
}

func TestSubscribeAfterClose(t *testing.T) {
	b := New()
	b.Close()

	sub := b.Subscribe("t", 1)
	select {
	case <-sub.Done():
		// expected — subscription immediately cancelled
	default:
		t.Fatal("expected Done() to be closed from subscribe after close")
	}
}

func TestBufferFullDrops(t *testing.T) {
	b := New()
	defer b.Close()

	sub := b.Subscribe("t", 1)
	defer sub.Unsubscribe()

	// fill the buffer
	b.Publish("t", Message{Type: "first"})
	// this should be dropped (buffer full, non-blocking)
	b.Publish("t", Message{Type: "second"})

	msg := <-sub.Ch()
	if msg.Type != "first" {
		t.Fatalf("got %q, want %q", msg.Type, "first")
	}

	select {
	case m := <-sub.Ch():
		t.Fatalf("expected no more messages, got %v", m)
	default:
		// expected
	}
}

func TestConcurrentSafety(t *testing.T) {
	b := New()
	var wg sync.WaitGroup

	// concurrent subscribers
	subs := make([]*Subscription, 10)
	for i := range subs {
		subs[i] = b.Subscribe("t", 100)
	}

	// concurrent publishers
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				b.Publish("t", Message{Type: "msg"})
			}
		}()
	}

	// concurrent unsubscribers
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			subs[i].Unsubscribe()
		}()
	}

	wg.Wait()
	b.Close()
}

func TestPublishWaitConcurrentUnsubscribe(t *testing.T) {
	// Regression: PublishWait must not race or panic when a subscriber
	// is concurrently unsubscribed during delivery.
	b := New()
	defer b.Close()

	var wg sync.WaitGroup
	for range 200 {
		sub := b.Subscribe("t", 0) // unbuffered — PublishWait will block on send
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = b.PublishWait(context.Background(), "t", Message{Type: "x"})
		}()
		go func() {
			defer wg.Done()
			sub.Unsubscribe()
		}()
	}
	wg.Wait()
}

func TestPublishWaitConcurrentClose(t *testing.T) {
	// Regression: PublishWait must not race or panic when Bus.Close()
	// is called concurrently, cancelling all subscriptions.
	for range 200 {
		b := New()
		sub := b.Subscribe("t", 0)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = b.PublishWait(context.Background(), "t", Message{Type: "x"})
		}()
		go func() {
			defer wg.Done()
			b.Close()
		}()
		wg.Wait()
		_ = sub
	}
}

func TestNegativeBufferSize(t *testing.T) {
	b := New()
	defer b.Close()
	// must not panic — negative treated as 0
	sub := b.Subscribe("t", -1)
	defer sub.Unsubscribe()
	if cap(sub.ch) != 0 {
		t.Fatalf("expected cap 0, got %d", cap(sub.ch))
	}
}

func TestRemoveSubCleansUpEmptyTopic(t *testing.T) {
	b := New()
	defer b.Close()

	sub := b.Subscribe("ephemeral", 1)
	sub.Unsubscribe()

	b.mu.RLock()
	_, exists := b.subscribers["ephemeral"]
	b.mu.RUnlock()
	if exists {
		t.Fatal("expected topic key to be deleted after last subscriber unsubscribes")
	}
}

func TestTopicHelpers(t *testing.T) {
	if got := StepOutputTopic("step_run_abc123"); got != "step.step_run_abc123.output" {
		t.Fatalf("got %q", got)
	}
	if got := StepInputTopic("step_run_abc123"); got != "step.step_run_abc123.input" {
		t.Fatalf("got %q", got)
	}
}

func TestParseHelpers(t *testing.T) {
	msg := Message{Type: "lifecycle", Payload: LifecyclePayload{RunID: "r1", Status: "RUNNING"}}
	p, ok := ParseLifecycle(msg)
	if !ok || p.RunID != "r1" || p.Status != "RUNNING" {
		t.Fatalf("ParseLifecycle failed: %v %v", p, ok)
	}

	_, ok = ParseText(msg)
	if ok {
		t.Fatal("ParseText should return false for LifecyclePayload")
	}

	msg2 := Message{Type: "status", Payload: StatusPayload{Message: "hi"}}
	sp, ok := ParseStatus(msg2)
	if !ok || sp.Message != "hi" {
		t.Fatalf("ParseStatus failed: %v %v", sp, ok)
	}
}

func TestPublishWaitAfterClose(t *testing.T) {
	b := New()
	b.Close()
	err := b.PublishWait(context.Background(), "t", Message{Type: "x"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}
