package model

import (
	"encoding/json"
	"testing"
)

func TestCompactionEntryJSON(t *testing.T) {
	entry := NewCompactionSessionEntry("msg_42", "User asked about Go patterns", 8000, 0)

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SessionEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != SessionEntryCompaction {
		t.Errorf("type: got %q", got.Type)
	}
	if got.Cursor != "msg_42" {
		t.Errorf("cursor: got %q", got.Cursor)
	}
	if got.TokensBefore != 8000 {
		t.Errorf("tokensBefore: got %d", got.TokensBefore)
	}
	if got.Summary != "User asked about Go patterns" {
		t.Errorf("summary: got %q", got.Summary)
	}
}

func TestCustomSessionEntryJSON(t *testing.T) {
	entry := NewCustomSessionEntry("inbox", map[string]any{
		"sessionID": "sess_1",
		"jobID":     "job_1",
		"payload":   `{"found": true}`,
	})

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SessionEntry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != SessionEntryCustom {
		t.Errorf("type: got %q", got.Type)
	}
	if got.CustomType != "inbox" {
		t.Errorf("customType: got %q", got.CustomType)
	}
	if got.Data["sessionID"] != "sess_1" {
		t.Errorf("sessionID: got %v", got.Data["sessionID"])
	}
	if got.Data["jobID"] != "job_1" {
		t.Errorf("jobID: got %v", got.Data["jobID"])
	}
}
