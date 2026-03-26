package middleware

import (
	"context"

	"github.com/rs/zerolog/log"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/model"
)

// MediaResolver is the narrow interface for resolving media URIs.
// Implemented by media.LocalService.
type MediaResolver interface {
	Resolve(ctx context.Context, uri string) (*media.Resolved, error)
}

// Media wraps a RoundTripper, resolving media:// URIs in request messages
// into data URIs before passing to the underlying handler.
type Media struct {
	next     base.RoundTripper
	resolver MediaResolver
}

// NewMedia creates a Media middleware.
func NewMedia(next base.RoundTripper, resolver MediaResolver) *Media {
	return &Media{next: next, resolver: resolver}
}

func (m *Media) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	resolved := m.resolveMediaInMessages(ctx, req.Messages)
	clone := *req
	clone.Messages = resolved
	return m.next.Responses(ctx, &clone)
}

// resolveMediaInMessages walks all messages and resolves media:// URIs
// to data URIs so downstream handlers can construct proper image/document blocks.
func (m *Media) resolveMediaInMessages(ctx context.Context, messages []model.Message) []model.Message {
	result := make([]model.Message, len(messages))
	for i, msg := range messages {
		result[i] = msg
		if !msg.HasMediaContent() {
			continue
		}
		// Deep copy content slice only if we have media
		content := make([]model.Content, len(msg.Content))
		copy(content, msg.Content)
		for j := range content {
			c := &content[j]
			if c.Type != model.ContentTypeImage && c.Type != model.ContentTypeDocument {
				continue
			}
			if !media.IsMediaURI(c.MediaURI) {
				continue
			}
			resolved, err := m.resolver.Resolve(ctx, c.MediaURI)
			if err != nil {
				log.Warn().Err(err).Str("uri", c.MediaURI).Msg("failed to resolve media URI, keeping reference")
				continue
			}
			c.MediaURI = media.DataURI(resolved.MIMEType, resolved.Data)
			c.MIMEType = resolved.MIMEType
		}
		result[i].Content = content
	}
	return result
}
