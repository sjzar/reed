package reed

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/engine"
)

// RunEventConsumer subscribes to lifecycle and step output events,
// rendering them to w. It blocks until ctx is cancelled or the bus is closed.
// When singleRun is true (CLI mode), it returns after the first run finalization.
// When singleRun is false (service/schedule mode), it continues consuming until
// ctx is cancelled or the bus is closed.
func RunEventConsumer(ctx context.Context, b *bus.Bus, w io.Writer, singleRun bool) {
	var mu sync.Mutex
	lifecycleSub := b.Subscribe(bus.TopicLifecycle, 256)
	defer lifecycleSub.Unsubscribe()

	stepSubs := make(map[string]*bus.Subscription) // stepRunID → sub
	var wg sync.WaitGroup
	defer func() {
		for _, sub := range stepSubs {
			sub.Unsubscribe()
		}
		wg.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-lifecycleSub.Done():
			return
		case msg := <-lifecycleSub.Ch():
			p, ok := bus.ParseLifecycle(msg)
			if !ok {
				continue
			}
			switch p.Type {
			case engine.EventStepStarted:
				mu.Lock()
				fmt.Fprintf(w, "▶ %s/%s\n", p.JobID, p.StepID)
				mu.Unlock()

				// Subscribe to step output
				topic := bus.StepOutputTopic(p.StepRunID)
				sub := b.Subscribe(topic, 256)
				stepSubs[p.StepRunID] = sub
				wg.Add(1)
				go func(sub *bus.Subscription) {
					defer wg.Done()
					drainStepOutput(ctx, sub, w, &mu)
				}(sub)

			case engine.EventStepFinished:
				if sub, ok := stepSubs[p.StepRunID]; ok {
					sub.Unsubscribe()
					delete(stepSubs, p.StepRunID)
				}
				mu.Lock()
				fmt.Fprintf(w, "\n✓ %s/%s %s\n", p.JobID, p.StepID, p.Status)
				mu.Unlock()

			case engine.EventStepFailed:
				if sub, ok := stepSubs[p.StepRunID]; ok {
					sub.Unsubscribe()
					delete(stepSubs, p.StepRunID)
				}
				mu.Lock()
				fmt.Fprintf(w, "✗ %s/%s %s", p.JobID, p.StepID, p.Status)
				if p.Error != "" {
					fmt.Fprintf(w, ": %s", p.Error)
				}
				fmt.Fprintln(w)
				mu.Unlock()

			case engine.EventRunFinalized:
				if singleRun {
					return
				}
			}
		}
	}
}

// drainStepOutput reads text messages from a step output subscription
// and writes them to w until the subscription is done or ctx is cancelled.
func drainStepOutput(ctx context.Context, sub *bus.Subscription, w io.Writer, mu *sync.Mutex) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Done():
			flushStepChannel(sub.Ch(), w, mu)
			return
		case msg := <-sub.Ch():
			if tp, ok := bus.ParseText(msg); ok {
				mu.Lock()
				fmt.Fprint(w, tp.Delta)
				mu.Unlock()
			}
		}
	}
}

// flushStepChannel drains any buffered text messages from the channel.
func flushStepChannel(ch <-chan bus.Message, w io.Writer, mu *sync.Mutex) {
	for {
		select {
		case msg := <-ch:
			if tp, ok := bus.ParseText(msg); ok {
				mu.Lock()
				fmt.Fprint(w, tp.Delta)
				mu.Unlock()
			}
		default:
			return
		}
	}
}

// WriteTextDeltas subscribes to a step output topic and writes text deltas to w.
// Returns a channel that closes when streaming is complete.
// Used by cmd_do.go for standalone agent streaming.
func WriteTextDeltas(b *bus.Bus, stepRunID string, w io.Writer) <-chan struct{} {
	done := make(chan struct{})
	topic := bus.StepOutputTopic(stepRunID)
	sub := b.Subscribe(topic, 256)

	go func() {
		defer close(done)
		for {
			select {
			case <-sub.Done():
				// Flush remaining
				for {
					select {
					case msg := <-sub.Ch():
						if tp, ok := bus.ParseText(msg); ok {
							fmt.Fprint(w, tp.Delta)
						}
					default:
						return
					}
				}
			case msg := <-sub.Ch():
				if tp, ok := bus.ParseText(msg); ok {
					fmt.Fprint(w, tp.Delta)
				}
			}
		}
	}()
	return done
}
