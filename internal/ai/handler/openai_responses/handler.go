// Package openai_responses implements base.RoundTripper using the OpenAI Responses API.
package openai_responses

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/model"
)

type handler struct {
	sdk openai.Client
}

// New creates an OpenAI Responses API handler.
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

// Responses sends a streaming Responses API request.
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
	stream := h.sdk.Responses.NewStreaming(ctx, params)
	success = true
	return &responsesStream{
		stream:     stream,
		sanitizer:  san,
		StreamBase: base.StreamBase{Cancel: cancel},
		toolAcc:    base.NewToolCallAccumulator(),
	}, nil
}

func buildParams(req *model.Request, san base.Sanitizer) responses.ResponseNewParams {
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(req.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: convertMessages(req.Messages, san),
		},
		Store: param.NewOpt(false),
	}
	if req.MaxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(int64(req.MaxTokens))
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = param.NewOpt(*req.TopP)
	}
	if len(req.Tools) > 0 {
		params.Tools = convertTools(req.Tools, san)
	}
	// req.Stop silently ignored — Responses API has no stop parameter

	// Thinking → Reasoning mapping
	thinking := base.NormalizeThinking(req.Thinking)
	if thinking != "" && thinking != "none" {
		effort := thinking
		if effort == "adaptive" {
			effort = string(shared.ReasoningEffortHigh)
		}
		params.Reasoning = shared.ReasoningParam{
			Effort:  shared.ReasoningEffort(effort),
			Summary: shared.ReasoningSummaryAuto,
		}
	}
	// TODO: send Effort="none" explicitly once model capability detection is available.
	// Pre-gpt-5.1 models don't support "none" and would fail.
	// ExtraParams
	if tier, ok := base.ExtractString(req.ExtraParams, base.ExtraKeyServiceTier); ok {
		params.ServiceTier = responses.ResponseNewParamsServiceTier(tier)
	}
	if req.Schema != nil {
		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:   "response",
					Schema: req.Schema,
					Strict: param.NewOpt(true),
				},
			},
		}
	}
	return params
}

func convertMessages(msgs []model.Message, san base.Sanitizer) responses.ResponseInputParam {
	var result responses.ResponseInputParam
	for _, m := range msgs {
		switch m.Role {
		case model.RoleSystem:
			result = append(result, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role:    "system",
					Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(m.TextContent())},
				},
			})
		case model.RoleUser:
			if m.HasMediaContent() {
				result = append(result, buildMultipartUserItem(m))
			} else {
				result = append(result, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role:    "user",
						Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(m.TextContent())},
					},
				})
			}
		case model.RoleAssistant:
			result = appendAssistantItems(result, m, san)
		case model.RoleTool:
			result = append(result, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: m.ToolCallID,
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: param.NewOpt(m.ContentSummary()),
					},
				},
			})
		}
	}
	return result
}

// buildMultipartUserItem creates a user message with text, image, and file parts.
func buildMultipartUserItem(m model.Message) responses.ResponseInputItemUnionParam {
	var parts responses.ResponseInputMessageContentListParam
	for _, c := range m.Content {
		switch c.Type {
		case model.ContentTypeText:
			if c.Text != "" {
				parts = append(parts, responses.ResponseInputContentParamOfInputText(c.Text))
			}
		case model.ContentTypeImage:
			if c.MediaURI != "" {
				parts = append(parts, responses.ResponseInputContentUnionParam{
					OfInputImage: &responses.ResponseInputImageParam{
						ImageURL: param.NewOpt(c.MediaURI),
					},
				})
			}
		case model.ContentTypeDocument:
			if c.MediaURI != "" {
				fileData := extractBase64FromDataURI(c.MediaURI)
				parts = append(parts, responses.ResponseInputContentUnionParam{
					OfInputFile: &responses.ResponseInputFileParam{
						FileData: param.NewOpt(fileData),
						Filename: param.NewOpt(c.Filename),
					},
				})
			}
		}
	}
	if len(parts) == 0 {
		parts = append(parts, responses.ResponseInputContentParamOfInputText(""))
	}
	return responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role: "user",
			Content: responses.EasyInputMessageContentUnionParam{
				OfInputItemContentList: parts,
			},
		},
	}
}

// extractBase64FromDataURI extracts the base64 data from a data URI.
func extractBase64FromDataURI(uri string) string {
	if idx := strings.Index(uri, ";base64,"); idx >= 0 {
		return uri[idx+len(";base64,"):]
	}
	return uri
}
func appendAssistantItems(result responses.ResponseInputParam, m model.Message, san base.Sanitizer) responses.ResponseInputParam {
	text := m.TextContent()
	if text != "" {
		result = append(result, responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    "assistant",
				Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(text)},
			},
		})
	}
	for _, tc := range m.ToolCalls {
		args := base.MarshalToolCallArguments(tc.Arguments)
		result = append(result, responses.ResponseInputItemUnionParam{
			OfFunctionCall: &responses.ResponseFunctionToolCallParam{
				CallID:    tc.ID,
				Name:      san.SanitizeToolName(tc.Name),
				Arguments: args,
			},
		})
	}
	return result
}

func convertTools(tools []model.ToolDef, san base.Sanitizer) []responses.ToolUnionParam {
	var result []responses.ToolUnionParam
	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		result = append(result, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        san.SanitizeToolName(t.Name),
				Description: param.NewOpt(t.Description),
				Parameters:  schema,
				Strict:      param.NewOpt(true),
			},
		})
	}
	return result
}

// ---------------------------------------------------------------------------
// Stream adapter
// ---------------------------------------------------------------------------

type responsesStream struct {
	base.StreamBase
	stream      *ssestream.Stream[responses.ResponseStreamEventUnion]
	sanitizer   base.Sanitizer
	toolAcc     *base.ToolCallAccumulator
	thinkingBuf strings.Builder
}

func (s *responsesStream) Next(ctx context.Context) (model.StreamEvent, error) {
	if ev, err, done := s.CheckDone(ctx); done {
		return ev, err
	}
	for s.stream.Next() {
		ev := s.stream.Current()
		switch variant := ev.AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			s.Resp.Content += variant.Delta
			return model.StreamEvent{Type: model.StreamEventTextDelta, Delta: variant.Delta}, nil

		case responses.ResponseRefusalDeltaEvent:
			s.Resp.Content += variant.Delta
			return model.StreamEvent{Type: model.StreamEventTextDelta, Delta: variant.Delta}, nil

		case responses.ResponseReasoningSummaryTextDeltaEvent:
			s.thinkingBuf.WriteString(variant.Delta)
			return model.StreamEvent{Type: model.StreamEventThinkingDelta, Delta: variant.Delta}, nil

		case responses.ResponseOutputItemAddedEvent:
			if variant.Item.Type == "function_call" {
				idx := int(variant.OutputIndex)
				id := s.sanitizer.NormalizeToolCallID(variant.Item.CallID)
				name := s.sanitizer.RestoreToolName(variant.Item.Name)
				s.toolAcc.StartCall(idx, id, name)
				// Seed initial arguments if present
				if initArgs := variant.Item.Arguments.OfString; initArgs != "" {
					s.toolAcc.AppendArgs(idx, initArgs)
				}
				return model.StreamEvent{
					Type:     model.StreamEventToolCallDelta,
					ToolCall: &model.ToolCall{ID: id, Name: name},
				}, nil
			}

		case responses.ResponseFunctionCallArgumentsDeltaEvent:
			idx := int(variant.OutputIndex)
			s.toolAcc.AppendArgs(idx, variant.Delta)
			return model.StreamEvent{
				Type: model.StreamEventToolCallDelta,
				ToolCall: &model.ToolCall{
					RawJSON: variant.Delta,
				},
			}, nil

		case responses.ResponseCompletedEvent:
			s.finalizeFromResponse(&variant.Response)
			s.Done = true
			return model.StreamEvent{Type: model.StreamEventDone}, io.EOF

		case responses.ResponseIncompleteEvent:
			s.finalizeFromResponse(&variant.Response)
			// Override stop reason based on incomplete details
			reason := variant.Response.IncompleteDetails.Reason
			if reason == "max_output_tokens" {
				s.Resp.StopReason = model.StopReasonMaxTokens
			}
			// content_filter: keep StopReasonEnd (no dedicated enum yet)
			s.Done = true
			return model.StreamEvent{Type: model.StreamEventDone}, io.EOF

		case responses.ResponseFailedEvent:
			respErr := variant.Response.Error
			return model.StreamEvent{}, model.ClassifyError(
				fmt.Errorf("openai responses: %s (code: %s)", respErr.Message, respErr.Code),
				mapErrorCodeToStatus(string(respErr.Code)), nil,
			)

		case responses.ResponseErrorEvent:
			return model.StreamEvent{}, model.ClassifyError(
				fmt.Errorf("openai responses stream error: %s (code: %s)", variant.Message, variant.Code),
				mapErrorCodeToStatus(variant.Code), nil,
			)
		}
	}

	if err := s.stream.Err(); err != nil {
		return model.StreamEvent{}, classifySDKError(err)
	}

	// Stream ended without terminal event — finalize what we have
	s.Resp.ToolCalls = s.toolAcc.Finalize()
	if s.thinkingBuf.Len() > 0 {
		s.Resp.Thinking = &model.Thinking{Content: s.thinkingBuf.String()}
	}
	if len(s.Resp.ToolCalls) > 0 {
		s.Resp.StopReason = model.StopReasonToolUse
	} else {
		s.Resp.StopReason = model.StopReasonEnd
	}
	s.Done = true
	return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
}

func (s *responsesStream) finalizeFromResponse(resp *responses.Response) {
	s.extractUsage(resp)

	var textBuf strings.Builder
	var toolCalls []model.ToolCall
	var thinkingBuf strings.Builder
	hasToolCall := false

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				switch c.Type {
				case "output_text":
					textBuf.WriteString(c.Text)
				case "refusal":
					textBuf.WriteString(c.Refusal)
				}
			}
		case "function_call":
			hasToolCall = true
			fc := item.AsFunctionCall()
			toolCalls = append(toolCalls, model.ToolCall{
				ID:        s.sanitizer.NormalizeToolCallID(fc.CallID),
				Name:      s.sanitizer.RestoreToolName(fc.Name),
				RawJSON:   fc.Arguments,
				Arguments: base.ParseToolCallArguments(fc.Arguments),
			})
		case "reasoning":
			ri := item.AsReasoning()
			for _, summary := range ri.Summary {
				thinkingBuf.WriteString(summary.Text)
			}
		}
	}

	if textBuf.Len() > 0 {
		s.Resp.Content = textBuf.String()
	}
	if len(toolCalls) > 0 {
		s.Resp.ToolCalls = toolCalls
	} else {
		s.Resp.ToolCalls = s.toolAcc.Finalize()
	}
	// Prefer authoritative reasoning from Output; fallback to delta accumulation
	if thinkingBuf.Len() > 0 {
		s.Resp.Thinking = &model.Thinking{Content: thinkingBuf.String()}
	} else if s.thinkingBuf.Len() > 0 {
		s.Resp.Thinking = &model.Thinking{Content: s.thinkingBuf.String()}
	}

	if hasToolCall {
		s.Resp.StopReason = model.StopReasonToolUse
	} else {
		s.Resp.StopReason = model.StopReasonEnd
	}
}

func (s *responsesStream) extractUsage(resp *responses.Response) {
	cached := int(resp.Usage.InputTokensDetails.CachedTokens)
	// OpenAI includes cached tokens in InputTokens; subtract to get non-cached input.
	// Clamp to zero in case a non-compliant backend reports cached > input.
	input := max(int(resp.Usage.InputTokens)-cached, 0)
	output := int(resp.Usage.OutputTokens)
	s.Resp.Usage = model.Usage{
		Input:      input,
		Output:     output,
		CacheRead:  cached,
		CacheWrite: 0,
		Total:      input + output + cached,
	}
}

func (s *responsesStream) Close() error {
	return s.CloseStream(s.stream.Close)
}

func (s *responsesStream) Response() *model.Response {
	return s.StreamBase.Response()
}

// mapErrorCodeToStatus maps OpenAI Responses API error codes to HTTP status codes.
func mapErrorCodeToStatus(code string) int {
	switch code {
	case string(responses.ResponseErrorCodeRateLimitExceeded):
		return 429
	case string(responses.ResponseErrorCodeServerError):
		return 500
	case "invalid_api_key":
		return 401
	case string(responses.ResponseErrorCodeInvalidPrompt),
		string(responses.ResponseErrorCodeInvalidImage),
		string(responses.ResponseErrorCodeInvalidImageFormat),
		string(responses.ResponseErrorCodeInvalidBase64Image),
		string(responses.ResponseErrorCodeInvalidImageURL):
		return 400
	default:
		return 0
	}
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
