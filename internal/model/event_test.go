package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventJSONRoundTrip(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		evt  Event
	}{
		{
			name: "llm_start",
			evt: Event{
				Type: EventLLMStart, Timestamp: now,
				Data: LLMStartData{Iteration: 1, Model: "gpt-4", Messages: 5, Tools: 3},
			},
		},
		{
			name: "llm_end",
			evt: Event{
				Type: EventLLMEnd, Timestamp: now,
				Data: LLMEndData{StopReason: StopReasonEnd, ToolCalls: 2, Usage: Usage{Input: 100, Output: 50, Total: 150}},
			},
		},
		{
			name: "text_chunk",
			evt:  Event{Type: EventTextChunk, Timestamp: now, Data: TextChunkData{Delta: "hello"}},
		},
		{
			name: "tool_start",
			evt: Event{
				Type: EventToolStart, Timestamp: now,
				Data: ToolStartData{ToolCallID: "tc1", ToolName: "search", Arguments: map[string]any{"q": "test"}},
			},
		},
		{
			name: "tool_end",
			evt: Event{
				Type: EventToolEnd, Timestamp: now,
				Data: ToolEndData{ToolCallID: "tc1", ToolName: "search", Result: "found", Duration: time.Second},
			},
		},
		{
			name: "subagent_spawn",
			evt: Event{
				Type: EventSubAgentSpawn, Timestamp: now,
				Data: SubAgentSpawnData{ID: "sa1", AgentID: "helper", Prompt: "do stuff"},
			},
		},
		{
			name: "subagent_complete",
			evt: Event{
				Type: EventSubAgentComplete, Timestamp: now,
				Data: SubAgentCompleteData{ID: "sa1", Output: "done"},
			},
		},
		{
			name: "status",
			evt:  Event{Type: EventStatus, Timestamp: now, Data: StatusData{Status: "running"}},
		},
		{
			name: "error",
			evt:  Event{Type: EventError, Timestamp: now, Data: ErrorData{Error: "boom", Code: "E001"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.evt)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var got Event
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if got.Type != tt.evt.Type {
				t.Errorf("type: got %q, want %q", got.Type, tt.evt.Type)
			}
			if !got.Timestamp.Equal(tt.evt.Timestamp) {
				t.Errorf("timestamp: got %v, want %v", got.Timestamp, tt.evt.Timestamp)
			}
		})
	}
}
