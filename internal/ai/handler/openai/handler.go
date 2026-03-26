// Package openai implements base.RoundTripper using the openai-go/v3 SDK.
package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/shared"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/model"
)

type handler struct {
	sdk openai.Client
}

// New creates an OpenAI Chat Completions handler.
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
		sdk: openai.NewClient(opts...),
	}, nil
}

// Responses sends a streaming chat completion request.
func (h *handler) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	var san base.Sanitizer = &base.NoopSanitizer{}
	if len(req.Tools) > 0 {
		san = base.NewOpenAISanitizer(req.Tools)
	}

	ctx, cancel := context.WithCancel(ctx)
	success := false
	defer func() {
		if !success {
			cancel()
		}
	}()

	params := buildParams(req, san)
	stream := h.sdk.Chat.Completions.NewStreaming(ctx, params)
	success = true
	return &chatStream{
		stream:     stream,
		sanitizer:  san,
		StreamBase: base.StreamBase{Cancel: cancel},
		toolAcc:    base.NewToolCallAccumulator(),
	}, nil
}

func buildParams(req *model.Request, san base.Sanitizer) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(req.Model),
		Messages: convertMessages(req.Messages, san),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = openai.Float(*req.TopP)
	}
	if len(req.Stop) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfStringArray: req.Stop,
		}
	}
	if len(req.Tools) > 0 {
		params.Tools = convertTools(req.Tools, san)
	}

	// Thinking → ReasoningEffort mapping
	thinking := base.NormalizeThinking(req.Thinking)
	if thinking != "" && thinking != "none" {
		// OpenAI doesn't support "adaptive" or "xhigh"; degrade gracefully
		effort := thinking
		switch effort {
		case "adaptive":
			effort = "high"
		case "xhigh":
			effort = "high"
		case "minimal":
			effort = "low"
		}
		params.ReasoningEffort = shared.ReasoningEffort(effort)
	}

	// ExtraParams: provider-specific parameters
	if tier, ok := base.ExtractString(req.ExtraParams, base.ExtraKeyServiceTier); ok {
		params.ServiceTier = openai.ChatCompletionNewParamsServiceTier(tier)
	}
	if req.Schema != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "response",
					Schema: req.Schema,
					Strict: openai.Bool(true),
				},
			},
		}
	}
	return params
}

func convertMessages(msgs []model.Message, san base.Sanitizer) []openai.ChatCompletionMessageParamUnion {
	var result []openai.ChatCompletionMessageParamUnion
	for _, m := range msgs {
		switch m.Role {
		case model.RoleSystem:
			result = append(result, openai.SystemMessage(m.TextContent()))
		case model.RoleUser:
			if m.HasMediaContent() {
				result = append(result, buildMultipartUserMessage(m))
			} else {
				result = append(result, openai.UserMessage(m.TextContent()))
			}
		case model.RoleAssistant:
			msg := buildAssistantMessage(m, san)
			result = append(result, msg)
		case model.RoleTool:
			// OpenAI tool messages only support text; images become text fallback
			result = append(result, openai.ToolMessage(m.ContentSummary(), m.ToolCallID))
		}
	}
	return result
}

// buildMultipartUserMessage creates a user message with text, image_url, and file parts.
func buildMultipartUserMessage(m model.Message) openai.ChatCompletionMessageParamUnion {
	var parts []openai.ChatCompletionContentPartUnionParam
	for _, c := range m.Content {
		switch c.Type {
		case model.ContentTypeText:
			if c.Text != "" {
				parts = append(parts, openai.TextContentPart(c.Text))
			}
		case model.ContentTypeImage:
			if c.MediaURI != "" {
				parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
					URL: c.MediaURI,
				}))
			}
		case model.ContentTypeDocument:
			if c.MediaURI != "" {
				fileData := extractBase64FromDataURI(c.MediaURI)
				parts = append(parts, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
					FileData: openai.String(fileData),
					Filename: openai.String(c.Filename),
				}))
			}
		}
	}
	if len(parts) == 0 {
		parts = append(parts, openai.TextContentPart(""))
	}
	msg := openai.ChatCompletionUserMessageParam{
		Content: openai.ChatCompletionUserMessageParamContentUnion{
			OfArrayOfContentParts: parts,
		},
	}
	return openai.ChatCompletionMessageParamUnion{OfUser: &msg}
}

// extractBase64FromDataURI extracts the base64 data from a data URI, or returns the URI as-is.
func extractBase64FromDataURI(uri string) string {
	if idx := strings.Index(uri, ";base64,"); idx >= 0 {
		return uri[idx+len(";base64,"):]
	}
	return uri
}

func buildAssistantMessage(m model.Message, san base.Sanitizer) openai.ChatCompletionMessageParamUnion {
	asst := openai.ChatCompletionAssistantMessageParam{}
	if text := m.TextContent(); text != "" {
		asst.Content.OfString = openai.String(text)
	}
	for _, tc := range m.ToolCalls {
		args := base.MarshalToolCallArguments(tc.Arguments)
		asst.ToolCalls = append(asst.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: tc.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      san.SanitizeToolName(tc.Name),
					Arguments: args,
				},
			},
		})
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &asst}
}

func convertTools(tools []model.ToolDef, san base.Sanitizer) []openai.ChatCompletionToolUnionParam {
	var result []openai.ChatCompletionToolUnionParam
	for _, t := range tools {
		schema := shared.FunctionParameters(t.InputSchema)
		if t.InputSchema == nil {
			schema = shared.FunctionParameters(map[string]any{"type": "object", "properties": map[string]any{}})
		}
		result = append(result, openai.ChatCompletionToolUnionParam{
			OfFunction: &openai.ChatCompletionFunctionToolParam{
				Function: shared.FunctionDefinitionParam{
					Name:        san.SanitizeToolName(t.Name),
					Description: openai.String(t.Description),
					Parameters:  schema,
				},
			},
		})
	}
	return result
}

// chatStream adapts the OpenAI SDK stream to model.ResponseStream.
type chatStream struct {
	base.StreamBase
	stream    *ssestream.Stream[openai.ChatCompletionChunk]
	sanitizer base.Sanitizer
	toolAcc   *base.ToolCallAccumulator
}

func (s *chatStream) Next(ctx context.Context) (model.StreamEvent, error) {
	if ev, err, done := s.CheckDone(ctx); done {
		return ev, err
	}
	for s.stream.Next() {
		chunk := s.stream.Current()

		// Parse usage from any chunk that carries it — some providers send usage
		// on the final choices chunk (alongside finish_reason), others send it
		// on a separate empty-choices chunk after all content.
		if chunk.JSON.Usage.Valid() {
			cached := int(chunk.Usage.PromptTokensDetails.CachedTokens)
			// OpenAI includes cached tokens in PromptTokens; subtract to get non-cached input.
			// Clamp to zero in case a non-compliant backend reports cached > prompt.
			input := max(int(chunk.Usage.PromptTokens)-cached, 0)
			// OpenAI already includes reasoning tokens in CompletionTokens; do not add them again.
			output := int(chunk.Usage.CompletionTokens)
			s.Resp.Usage = model.Usage{
				Input:      input,
				Output:     output,
				CacheRead:  cached,
				CacheWrite: 0,
				Total:      input + output + cached,
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		finish := chunk.Choices[0].FinishReason

		if delta.Content != "" {
			s.Resp.Content += delta.Content
			return model.StreamEvent{Type: model.StreamEventTextDelta, Delta: delta.Content}, nil
		}

		for _, tc := range delta.ToolCalls {
			idx := int(tc.Index)
			if tc.ID != "" || tc.Function.Name != "" {
				s.toolAcc.StartCall(idx,
					s.sanitizer.NormalizeToolCallID(tc.ID),
					s.sanitizer.RestoreToolName(tc.Function.Name))
			}
			if tc.Function.Arguments != "" {
				s.toolAcc.AppendArgs(idx, tc.Function.Arguments)
			}
			if tc.Function.Name != "" || tc.Function.Arguments != "" {
				return model.StreamEvent{
					Type: model.StreamEventToolCallDelta,
					ToolCall: &model.ToolCall{
						ID:      tc.ID,
						Name:    s.sanitizer.RestoreToolName(tc.Function.Name),
						RawJSON: tc.Function.Arguments,
					},
				}, nil
			}
		}

		if finish != "" {
			s.Resp.StopReason = base.MapStopReason(finish)
		}
	}

	if err := s.stream.Err(); err != nil {
		return model.StreamEvent{}, classifySDKError(err)
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
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return model.ClassifyError(
			fmt.Errorf("openai: %s", apiErr.Error()),
			apiErr.StatusCode,
			[]byte(apiErr.RawJSON()),
		)
	}
	return model.ClassifyError(err, 0, nil)
}
