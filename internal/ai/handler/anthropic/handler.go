// Package anthropic implements base.RoundTripper using the anthropic-sdk-go SDK.
package anthropic

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/model"
)

type handler struct {
	sdk anthropic.Client
}

// New creates an Anthropic Messages handler.
func New(cfg conf.ProviderConfig) (base.RoundTripper, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.Key),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	for k, v := range cfg.Headers {
		opts = append(opts, option.WithHeader(k, v))
	}
	return &handler{
		sdk: anthropic.NewClient(opts...),
	}, nil
}

// Responses sends a streaming messages request.
func (h *handler) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	var san base.Sanitizer = &base.NoopSanitizer{}
	if len(req.Tools) > 0 {
		san = base.NewAnthropicSanitizer(req.Tools)
	}

	ctx, cancel := context.WithCancel(ctx)
	success := false
	defer func() {
		if !success {
			cancel()
		}
	}()

	params := buildParams(req, san)
	stream := h.sdk.Messages.NewStreaming(ctx, params)
	success = true
	return &chatStream{
		stream:     stream,
		sanitizer:  san,
		StreamBase: base.StreamBase{Cancel: cancel},
		toolAcc:    base.NewToolCallAccumulator(),
	}, nil
}

func buildParams(req *model.Request, san base.Sanitizer) anthropic.MessageNewParams {
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: maxTokens,
	}

	// Accumulate ALL system messages
	var systemParts []string
	var messages []anthropic.MessageParam
	for _, m := range req.Messages {
		if m.Role == model.RoleSystem {
			if text := m.TextContent(); text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		messages = append(messages, convertMessage(m, san))
	}
	params.Messages = messages
	if len(systemParts) > 0 {
		params.System = []anthropic.TextBlockParam{
			{Text: strings.Join(systemParts, "\n\n")},
		}
	}

	if req.Temperature != nil {
		params.Temperature = anthropic.Float(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = anthropic.Float(*req.TopP)
	}
	if len(req.Stop) > 0 {
		params.StopSequences = req.Stop
	}
	if len(req.Tools) > 0 {
		params.Tools = convertTools(req.Tools, san)
	}

	// Thinking support: normalize then dispatch by model capability
	thinking := base.NormalizeThinking(req.Thinking)
	if thinking != "" && thinking != "none" {
		if supportsAdaptiveThinking(req.Model) {
			params.OutputConfig = anthropic.OutputConfigParam{
				Effort: mapThinkingEffort(thinking),
			}
		} else {
			params.Thinking = anthropic.ThinkingConfigParamOfEnabled(thinkingBudget(thinking))
		}
	}

	return params
}

func convertMessage(m model.Message, san base.Sanitizer) anthropic.MessageParam {
	switch m.Role {
	case model.RoleAssistant:
		var blocks []anthropic.ContentBlockParamUnion
		// Process content blocks for thinking
		if tc := m.ThinkingContent(); tc != nil && tc.Text != "" {
			blocks = append(blocks, anthropic.ContentBlockParamUnion{
				OfThinking: &anthropic.ThinkingBlockParam{
					Thinking:  tc.Text,
					Signature: tc.Signature,
				},
			})
		}
		if text := m.TextContent(); text != "" {
			blocks = append(blocks, anthropic.NewTextBlock(text))
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, anthropic.ContentBlockParamUnion{
				OfToolUse: &anthropic.ToolUseBlockParam{
					ID:    tc.ID,
					Name:  san.SanitizeToolName(tc.Name),
					Input: tc.Arguments,
				},
			})
		}
		if len(blocks) == 0 {
			blocks = append(blocks, anthropic.NewTextBlock(""))
		}
		return anthropic.NewAssistantMessage(blocks...)

	case model.RoleTool:
		var contentBlocks []anthropic.ToolResultBlockParamContentUnion
		for _, c := range m.Content {
			switch c.Type {
			case model.ContentTypeText:
				if c.Text != "" {
					contentBlocks = append(contentBlocks, anthropic.ToolResultBlockParamContentUnion{
						OfText: &anthropic.TextBlockParam{Text: c.Text},
					})
				}
			case model.ContentTypeImage:
				contentBlocks = append(contentBlocks, buildToolResultImageBlock(c))
			case model.ContentTypeDocument:
				contentBlocks = append(contentBlocks, buildToolResultDocumentBlock(c))
			}
		}
		if len(contentBlocks) == 0 {
			contentBlocks = append(contentBlocks, anthropic.ToolResultBlockParamContentUnion{
				OfText: &anthropic.TextBlockParam{Text: ""},
			})
		}
		return anthropic.NewUserMessage(
			anthropic.ContentBlockParamUnion{
				OfToolResult: &anthropic.ToolResultBlockParam{
					ToolUseID: m.ToolCallID,
					Content:   contentBlocks,
					IsError:   anthropic.Bool(m.IsError),
				},
			},
		)

	default: // user
		var blocks []anthropic.ContentBlockParamUnion
		for _, c := range m.Content {
			switch c.Type {
			case model.ContentTypeText:
				if c.Text != "" {
					blocks = append(blocks, anthropic.NewTextBlock(c.Text))
				}
			case model.ContentTypeImage:
				blocks = append(blocks, buildImageBlock(c))
			case model.ContentTypeDocument:
				blocks = append(blocks, buildDocumentBlock(c))
			}
		}
		if len(blocks) == 0 {
			blocks = append(blocks, anthropic.NewTextBlock(""))
		}
		return anthropic.NewUserMessage(blocks...)
	}
}

// buildImageBlock creates an Anthropic image content block for user messages.
func buildImageBlock(c model.Content) anthropic.ContentBlockParamUnion {
	if media.IsDataURI(c.MediaURI) {
		mimeType, data, err := media.ParseDataURI(c.MediaURI)
		if err != nil {
			return anthropic.NewTextBlock("[image: decode error]")
		}
		return anthropic.NewImageBlockBase64(mimeType, base64.StdEncoding.EncodeToString(data))
	}
	if strings.HasPrefix(c.MediaURI, "http://") || strings.HasPrefix(c.MediaURI, "https://") {
		return anthropic.ContentBlockParamUnion{
			OfImage: &anthropic.ImageBlockParam{
				Source: anthropic.ImageBlockParamSourceUnion{
					OfURL: &anthropic.URLImageSourceParam{URL: c.MediaURI},
				},
			},
		}
	}
	return anthropic.NewTextBlock("[image: " + c.MIMEType + "]")
}

// buildToolResultImageBlock creates an image block for tool result content.
func buildToolResultImageBlock(c model.Content) anthropic.ToolResultBlockParamContentUnion {
	if media.IsDataURI(c.MediaURI) {
		mimeType, data, err := media.ParseDataURI(c.MediaURI)
		if err != nil {
			return anthropic.ToolResultBlockParamContentUnion{
				OfText: &anthropic.TextBlockParam{Text: "[image: decode error]"},
			}
		}
		return anthropic.ToolResultBlockParamContentUnion{
			OfImage: &anthropic.ImageBlockParam{
				Source: anthropic.ImageBlockParamSourceUnion{
					OfBase64: &anthropic.Base64ImageSourceParam{
						MediaType: anthropic.Base64ImageSourceMediaType(mimeType),
						Data:      base64.StdEncoding.EncodeToString(data),
					},
				},
			},
		}
	}
	return anthropic.ToolResultBlockParamContentUnion{
		OfText: &anthropic.TextBlockParam{Text: "[image: " + c.MIMEType + "]"},
	}
}

// buildDocumentBlock creates an Anthropic document block for user messages.
func buildDocumentBlock(c model.Content) anthropic.ContentBlockParamUnion {
	if media.IsDataURI(c.MediaURI) {
		mimeType, data, err := media.ParseDataURI(c.MediaURI)
		if err != nil {
			return anthropic.NewTextBlock("[document: decode error]")
		}
		switch {
		case mimeType == "application/pdf":
			return anthropic.ContentBlockParamUnion{
				OfDocument: &anthropic.DocumentBlockParam{
					Source: anthropic.DocumentBlockParamSourceUnion{
						OfBase64: &anthropic.Base64PDFSourceParam{
							Data: base64.StdEncoding.EncodeToString(data),
						},
					},
					Title: anthropic.String(c.Filename),
				},
			}
		case strings.HasPrefix(mimeType, "text/"):
			return anthropic.ContentBlockParamUnion{
				OfDocument: &anthropic.DocumentBlockParam{
					Source: anthropic.DocumentBlockParamSourceUnion{
						OfText: &anthropic.PlainTextSourceParam{
							Data: string(data),
						},
					},
					Title: anthropic.String(c.Filename),
				},
			}
		default:
			name := c.Filename
			if name == "" {
				name = c.MIMEType
			}
			return anthropic.NewTextBlock("[document: " + name + "]")
		}
	}
	name := c.Filename
	if name == "" {
		name = c.MIMEType
	}
	return anthropic.NewTextBlock("[document: " + name + "]")
}

// buildToolResultDocumentBlock creates a document block for tool result content.
func buildToolResultDocumentBlock(c model.Content) anthropic.ToolResultBlockParamContentUnion {
	if media.IsDataURI(c.MediaURI) {
		mimeType, data, err := media.ParseDataURI(c.MediaURI)
		if err != nil {
			return anthropic.ToolResultBlockParamContentUnion{
				OfText: &anthropic.TextBlockParam{Text: "[document: decode error]"},
			}
		}
		if mimeType == "application/pdf" {
			return anthropic.ToolResultBlockParamContentUnion{
				OfDocument: &anthropic.DocumentBlockParam{
					Source: anthropic.DocumentBlockParamSourceUnion{
						OfBase64: &anthropic.Base64PDFSourceParam{
							Data: base64.StdEncoding.EncodeToString(data),
						},
					},
					Title: anthropic.String(c.Filename),
				},
			}
		}
		if strings.HasPrefix(mimeType, "text/") {
			return anthropic.ToolResultBlockParamContentUnion{
				OfDocument: &anthropic.DocumentBlockParam{
					Source: anthropic.DocumentBlockParamSourceUnion{
						OfText: &anthropic.PlainTextSourceParam{
							Data: string(data),
						},
					},
					Title: anthropic.String(c.Filename),
				},
			}
		}
	}
	name := c.Filename
	if name == "" {
		name = c.MIMEType
	}
	return anthropic.ToolResultBlockParamContentUnion{
		OfText: &anthropic.TextBlockParam{Text: "[document: " + name + "]"},
	}
}

func convertTools(tools []model.ToolDef, san base.Sanitizer) []anthropic.ToolUnionParam {
	var result []anthropic.ToolUnionParam
	for _, t := range tools {
		param := anthropic.ToolParam{
			Name:        san.SanitizeToolName(t.Name),
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: base.SafeSchemaProperties(t.InputSchema),
			},
		}
		if req := base.SafeSchemaRequired(t.InputSchema); len(req) > 0 {
			param.InputSchema.Required = req
		}
		result = append(result, anthropic.ToolUnionParam{OfTool: &param})
	}
	return result
}

// supportsAdaptiveThinking returns true for models that support OutputConfig.Effort
// (Claude 4.6 family: opus-4-6, sonnet-4-6).
func supportsAdaptiveThinking(modelID string) bool {
	m := strings.ToLower(modelID)
	return strings.Contains(m, "opus-4-6") || strings.Contains(m, "sonnet-4-6")
}

// mapThinkingEffort maps a normalized thinking level to Anthropic OutputConfigEffort.
func mapThinkingEffort(level string) anthropic.OutputConfigEffort {
	switch level {
	case "minimal", "low":
		return anthropic.OutputConfigEffortLow
	case "medium", "adaptive":
		return anthropic.OutputConfigEffortMedium
	case "high":
		return anthropic.OutputConfigEffortHigh
	case "xhigh":
		return anthropic.OutputConfigEffortMax
	default:
		return anthropic.OutputConfigEffortMedium
	}
}

// thinkingBudget maps a normalized thinking level to a token budget for older models.
func thinkingBudget(level string) int64 {
	switch level {
	case "minimal":
		return 1024
	case "low":
		return 2048
	case "medium", "adaptive":
		return 8192
	case "high", "xhigh":
		return 16384
	default:
		return 8192
	}
}

// chatStream adapts the Anthropic SDK stream to model.ResponseStream.
type chatStream struct {
	base.StreamBase
	stream    *ssestream.Stream[anthropic.MessageStreamEventUnion]
	sanitizer base.Sanitizer
	toolAcc   *base.ToolCallAccumulator

	// thinking accumulation
	thinkingBuf strings.Builder
	lastToolIdx int // current content_block index for tool_use
}

func (s *chatStream) Next(ctx context.Context) (model.StreamEvent, error) {
	if ev, err, done := s.CheckDone(ctx); done {
		return ev, err
	}
	for s.stream.Next() {
		event := s.stream.Current()
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				idx := int(event.Index)
				s.lastToolIdx = idx
				s.toolAcc.StartCall(idx, event.ContentBlock.ID,
					s.sanitizer.RestoreToolName(event.ContentBlock.Name))
				return model.StreamEvent{
					Type: model.StreamEventToolCallDelta,
					ToolCall: &model.ToolCall{
						ID:   event.ContentBlock.ID,
						Name: s.sanitizer.RestoreToolName(event.ContentBlock.Name),
					},
				}, nil
			}
		case "content_block_delta":
			delta := event.Delta
			switch delta.Type {
			case "text_delta":
				s.Resp.Content += delta.Text
				return model.StreamEvent{Type: model.StreamEventTextDelta, Delta: delta.Text}, nil
			case "thinking_delta":
				s.thinkingBuf.WriteString(delta.Thinking)
				return model.StreamEvent{Type: model.StreamEventThinkingDelta, Delta: delta.Thinking}, nil
			case "signature_delta":
				// Accumulate signature for thinking block
			case "input_json_delta":
				idx := s.lastToolIdx
				s.toolAcc.AppendArgs(idx, delta.PartialJSON)
				return model.StreamEvent{
					Type:     model.StreamEventToolCallDelta,
					ToolCall: &model.ToolCall{RawJSON: delta.PartialJSON},
				}, nil
			}
		case "content_block_stop":
			// Tool call finalization happens in Finalize()
		case "message_delta":
			if string(event.Delta.StopReason) != "" {
				s.Resp.StopReason = base.MapStopReason(string(event.Delta.StopReason))
			}
			s.Resp.Usage.Output = int(event.Usage.OutputTokens)
			s.Resp.Usage.Total = s.Resp.Usage.Input + s.Resp.Usage.Output + s.Resp.Usage.CacheRead + s.Resp.Usage.CacheWrite
		case "message_start":
			if event.Message.Usage.InputTokens > 0 {
				s.Resp.Usage.Input = int(event.Message.Usage.InputTokens)
			}
			if event.Message.Usage.CacheReadInputTokens > 0 {
				s.Resp.Usage.CacheRead = int(event.Message.Usage.CacheReadInputTokens)
			}
			if event.Message.Usage.CacheCreationInputTokens > 0 {
				s.Resp.Usage.CacheWrite = int(event.Message.Usage.CacheCreationInputTokens)
			}
		}
	}

	if err := s.stream.Err(); err != nil {
		return model.StreamEvent{}, classifySDKError(err)
	}

	// Finalize accumulated state
	if s.thinkingBuf.Len() > 0 {
		s.Resp.Thinking = &model.Thinking{Content: s.thinkingBuf.String()}
	}
	s.Resp.ToolCalls = s.toolAcc.Finalize()

	s.Done = true
	return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
}

func (s *chatStream) Close() error {
	return s.CloseStream(s.stream.Close)
}

func (s *chatStream) Response() *model.Response {
	return s.StreamBase.Response()
}

// classifySDKError maps SDK errors to model.AIError.
func classifySDKError(err error) *model.AIError {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return model.ClassifyError(
			fmt.Errorf("anthropic: %s", apiErr.Error()),
			apiErr.StatusCode,
			[]byte(apiErr.RawJSON()),
		)
	}
	return model.ClassifyError(err, 0, nil)
}
