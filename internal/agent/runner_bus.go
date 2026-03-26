package agent

import (
	"context"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/model"
)

// busInputHandler processes a single inbound bus message.
type busInputHandler func(ctx context.Context, msg bus.Message)

// startBusSubscriber subscribes to the step input topic and dispatches messages
// to registered handlers in a background goroutine. Returns a stop function
// that unsubscribes and waits for the goroutine to exit.
// No-op (returns a no-op stop) when eventBus or stepRunID is empty.
func startBusSubscriber(ctx context.Context, eventBus *bus.Bus, stepRunID, sessionID string, sess SessionProvider) func() {
	noop := func() {}
	if eventBus == nil || stepRunID == "" {
		return noop
	}

	topic := bus.StepInputTopic(stepRunID)
	sub := eventBus.Subscribe(topic, 16)

	handlers := map[string]busInputHandler{
		"steer": newSteerHandler(sessionID, sess),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runBusSubscriber(ctx, sub, handlers)
	}()

	return func() {
		sub.Unsubscribe()
		wg.Wait()
	}
}

// runBusSubscriber is the select loop that reads from the subscription channel
// and dispatches each message to the matching handler. Exits when the
// subscription is done or ctx is cancelled.
func runBusSubscriber(ctx context.Context, sub *bus.Subscription, handlers map[string]busInputHandler) {
	for {
		select {
		case msg := <-sub.Ch():
			h, ok := handlers[msg.Type]
			if !ok {
				log.Debug().Str("type", msg.Type).Msg("bus subscriber: unknown message type, ignoring")
				continue
			}
			h(ctx, msg)
		case <-sub.Done():
			return
		case <-ctx.Done():
			return
		}
	}
}

// newSteerHandler returns a busInputHandler that parses a SteerPayload and
// writes a RoleUser message into the session inbox so the agent loop picks it
// up via drainInboxOrComplete.
func newSteerHandler(sessionID string, sess SessionProvider) busInputHandler {
	return func(ctx context.Context, msg bus.Message) {
		payload, ok := bus.ParseSteer(msg)
		if !ok {
			log.Warn().Msg("bus subscriber: steer message has invalid payload")
			return
		}
		if payload.Message == "" {
			return
		}

		userMsg := model.NewTextMessage(model.RoleUser, payload.Message)
		entry := model.NewMessageSessionEntry(userMsg, "")
		if err := sess.AppendInbox(ctx, sessionID, entry); err != nil {
			log.Error().Err(err).Str("sessionID", sessionID).Msg("bus subscriber: failed to append steer to inbox")
		}
	}
}
