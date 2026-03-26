package model

import "strings"

// Role identifies the sender of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Content represents a single content block within a message.
type Content struct {
	Type      string `json:"type,omitempty"` // "text" | "thinking" | "image" | "document"
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"` // thinking blocks: Anthropic cache signature
	MediaURI  string `json:"mediaURI,omitempty"`  // image/document: media://<id> | data:<mime>;base64,... | https://...
	MIMEType  string `json:"mimeType,omitempty"`  // image/document: e.g. "image/png", "application/pdf"
	Filename  string `json:"filename,omitempty"`  // document blocks: original filename
}

// Content type constants.
const (
	ContentTypeText     = "text"
	ContentTypeThinking = "thinking"
	ContentTypeImage    = "image"
	ContentTypeDocument = "document"
)

// TextContent creates a single text content block slice.
func TextContent(text string) []Content {
	return []Content{{Type: ContentTypeText, Text: text}}
}

// NewTextMessage creates a Message with a single text content block.
func NewTextMessage(role Role, text string) Message {
	return Message{Role: role, Content: TextContent(text)}
}

// ImageContent creates a single image content block.
func ImageContent(mediaURI, mimeType string) Content {
	return Content{Type: ContentTypeImage, MediaURI: mediaURI, MIMEType: mimeType}
}

// DocumentContent creates a single document content block.
func DocumentContent(mediaURI, mimeType, filename string) Content {
	return Content{Type: ContentTypeDocument, MediaURI: mediaURI, MIMEType: mimeType, Filename: filename}
}

// Message is a single message in a conversation.
type Message struct {
	Role       Role           `json:"role"`
	Content    []Content      `json:"content"`
	ToolCalls  []ToolCall     `json:"toolCalls,omitempty"`  // assistant: tool call requests
	ToolCallID string         `json:"toolCallID,omitempty"` // tool: corresponding call ID
	ToolName   string         `json:"toolName,omitempty"`   // tool: name of the tool
	IsError    bool           `json:"isError,omitempty"`    // tool: marks an error result
	Usage      *Usage         `json:"usage,omitempty"`      // assistant: token usage for this turn
	Meta       map[string]any `json:"meta,omitempty"`       // trace metadata (iteration, duration, model)
}

// Clone returns a deep copy of the Message.
// Slice fields (Content, ToolCalls) and pointer fields (Usage) are copied
// so that mutations to the clone do not affect the original.
func (m Message) Clone() Message {
	cp := m
	if m.Content != nil {
		cp.Content = make([]Content, len(m.Content))
		copy(cp.Content, m.Content)
	}
	if m.ToolCalls != nil {
		cp.ToolCalls = make([]ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			cp.ToolCalls[i] = tc
			if tc.Arguments != nil {
				cp.ToolCalls[i].Arguments = make(map[string]any, len(tc.Arguments))
				for k, v := range tc.Arguments {
					cp.ToolCalls[i].Arguments[k] = v
				}
			}
		}
	}
	if m.Usage != nil {
		u := *m.Usage
		cp.Usage = &u
	}
	if m.Meta != nil {
		cp.Meta = make(map[string]any, len(m.Meta))
		for k, v := range m.Meta {
			cp.Meta[k] = v
		}
	}
	return cp
}

// TextContent returns the concatenated text from all text content blocks.
func (m *Message) TextContent() string {
	var s string
	for _, c := range m.Content {
		if c.Type == ContentTypeText {
			s += c.Text
		}
	}
	return s
}

// ThinkingContent returns the first thinking content block, if any.
func (m *Message) ThinkingContent() *Content {
	for i := range m.Content {
		if m.Content[i].Type == ContentTypeThinking {
			return &m.Content[i]
		}
	}
	return nil
}

// ContentSummary returns a human-readable summary of all content blocks.
func (m *Message) ContentSummary() string {
	var parts []string
	for _, c := range m.Content {
		switch c.Type {
		case ContentTypeText:
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		case ContentTypeImage:
			parts = append(parts, "[image:"+c.MIMEType+"]")
		case ContentTypeDocument:
			name := c.Filename
			if name == "" {
				name = c.MIMEType
			}
			parts = append(parts, "[doc:"+name+"]")
		}
	}
	return strings.Join(parts, " ")
}

// HasImageContent reports whether the message contains any image content blocks.
func (m *Message) HasImageContent() bool {
	for _, c := range m.Content {
		if c.Type == ContentTypeImage {
			return true
		}
	}
	return false
}

// HasMediaContent reports whether the message contains any image or document content blocks.
func (m *Message) HasMediaContent() bool {
	for _, c := range m.Content {
		if c.Type == ContentTypeImage || c.Type == ContentTypeDocument {
			return true
		}
	}
	return false
}

// ImageContentCount returns the number of image content blocks in the message.
func (m *Message) ImageContentCount() int {
	n := 0
	for _, c := range m.Content {
		if c.Type == ContentTypeImage {
			n++
		}
	}
	return n
}

// MediaContentCount returns the number of image and document content blocks.
func (m *Message) MediaContentCount() int {
	n := 0
	for _, c := range m.Content {
		if c.Type == ContentTypeImage || c.Type == ContentTypeDocument {
			n++
		}
	}
	return n
}

// ToolCall is a single tool invocation requested by the LLM.
type ToolCall struct {
	ID        string         `json:"id"`                  // call ID (normalized)
	Name      string         `json:"name"`                // tool name (restored to original)
	Arguments map[string]any `json:"arguments,omitempty"` // parsed JSON arguments
	RawJSON   string         `json:"rawJSON,omitempty"`   // raw JSON string (for bad JSON diagnostics)
}

// Usage tracks token consumption.
type Usage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Total      int `json:"total"`
}
