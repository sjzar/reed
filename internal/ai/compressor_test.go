package ai

import (
	"context"
	"io"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestIsContextExceeded(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context overflow",
			err:  &model.AIError{Kind: model.ErrContextOverflow},
			want: true,
		},
		{
			name: "rate limit",
			err:  &model.AIError{Kind: model.ErrRateLimit},
			want: false,
		},
		{
			name: "generic error",
			err:  context.DeadlineExceeded,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextExceeded(tt.err); got != tt.want {
				t.Errorf("IsContextExceeded: got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompressorCompress(t *testing.T) {
	responsesFn := func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		// Verify the compress prompt is used
		if req.Messages[0].Role != model.RoleSystem {
			t.Error("expected system message with compress prompt")
		}
		return &compressorTestStream{resp: &model.Response{Content: "Summary: user discussed Go patterns"}}, nil
	}

	comp := NewCompressor(responsesFn, "test-model")
	summary, err := comp.Compress(context.Background(), []model.Message{
		model.NewTextMessage(model.RoleUser, "Tell me about Go patterns"),
		model.NewTextMessage(model.RoleAssistant, "Go has many patterns..."),
	})
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if summary != "Summary: user discussed Go patterns" {
		t.Errorf("summary: got %q", summary)
	}
}

// compressorTestStream is a simple stream for compressor tests.
type compressorTestStream struct {
	resp *model.Response
	done bool
}

func (s *compressorTestStream) Next(_ context.Context) (model.StreamEvent, error) {
	if s.done {
		return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
	}
	s.done = true
	return model.StreamEvent{Type: model.StreamEventTextDelta, Delta: s.resp.Content}, nil
}

func (s *compressorTestStream) Close() error              { return nil }
func (s *compressorTestStream) Response() *model.Response { return s.resp }
