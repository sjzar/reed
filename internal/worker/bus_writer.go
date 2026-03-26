package worker

import (
	"io"

	"github.com/sjzar/reed/internal/bus"
)

// busWriter wraps an io.Writer and publishes each Write as a TextPayload
// to the event bus. If the bus is nil, it simply delegates to the inner writer.
type busWriter struct {
	inner io.Writer
	bus   *bus.Bus
	topic string
}

func newBusWriter(inner io.Writer, b *bus.Bus, topic string) *busWriter {
	return &busWriter{inner: inner, bus: b, topic: topic}
}

func (w *busWriter) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	if n > 0 && w.bus != nil && w.topic != "" {
		w.bus.Publish(w.topic, bus.Message{
			Type:    "text",
			Payload: bus.TextPayload{Delta: string(p[:n])},
		})
	}
	return n, err
}
