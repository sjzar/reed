package tool

import (
	"encoding/json"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestTextResult(t *testing.T) {
	r := TextResult("hello")
	if r.IsError {
		t.Fatal("expected IsError=false")
	}
	if len(r.Content) != 1 || r.Content[0].Text != "hello" {
		t.Fatalf("unexpected content: %+v", r.Content)
	}
	if r.Content[0].Type != model.ContentTypeText {
		t.Fatalf("expected type text, got %s", r.Content[0].Type)
	}
}

func TestErrorResult(t *testing.T) {
	r := ErrorResult("bad")
	if !r.IsError {
		t.Fatal("expected IsError=true")
	}
	if len(r.Content) != 1 || r.Content[0].Text != "bad" {
		t.Fatalf("unexpected content: %+v", r.Content)
	}
}

func TestMarshalArgs_RawJSON(t *testing.T) {
	tc := model.ToolCall{RawJSON: `{"a":1}`}
	b, err := MarshalArgs(tc)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"a":1}` {
		t.Fatalf("got %s", b)
	}
}

func TestMarshalArgs_Arguments(t *testing.T) {
	tc := model.ToolCall{Arguments: map[string]any{"b": 2}}
	b, err := MarshalArgs(tc)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["b"] != float64(2) {
		t.Fatalf("got %v", m)
	}
}

func TestMarshalArgs_Empty(t *testing.T) {
	tc := model.ToolCall{}
	b, err := MarshalArgs(tc)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}" {
		t.Fatalf("got %s", b)
	}
}
