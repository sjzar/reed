package worker

import (
	"fmt"
	"io"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/engine"
)

// stepEmitter publishes step-level status and output events to the event bus.
// It is a thin helper for shell and HTTP workers. If the bus or stepRunID are
// absent, all methods are safe no-ops.
type stepEmitter struct {
	bus   *bus.Bus
	topic string
}

func newStepEmitter(p engine.StepPayload) *stepEmitter {
	if p.Bus == nil || p.StepRunID == "" {
		return &stepEmitter{}
	}
	return &stepEmitter{
		bus:   p.Bus,
		topic: bus.StepOutputTopic(p.StepRunID),
	}
}

// PublishStatus sends a status event (e.g. "shell_start cmd=/bin/bash").
func (e *stepEmitter) PublishStatus(msg string) {
	if e.bus == nil {
		return
	}
	e.bus.Publish(e.topic, bus.Message{
		Type:    "status",
		Payload: bus.StatusPayload{Message: msg},
	})
}

// PublishStatusf is a convenience wrapper around PublishStatus with fmt.Sprintf.
func (e *stepEmitter) PublishStatusf(format string, args ...any) {
	if e.bus == nil {
		return
	}
	e.PublishStatus(fmt.Sprintf(format, args...))
}

// OutputWriter wraps w with a busWriter so that each Write also publishes to
// the event bus. If the emitter has no bus, it returns w unchanged.
func (e *stepEmitter) OutputWriter(w io.Writer) io.Writer {
	if e.bus == nil {
		return w
	}
	return newBusWriter(w, e.bus, e.topic)
}

// Active reports whether the emitter is connected to a bus.
func (e *stepEmitter) Active() bool {
	return e.bus != nil
}
