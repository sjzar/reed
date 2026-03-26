package model

import (
	"testing"
)

func TestAgentRunRequestDefaults(t *testing.T) {
	var req AgentRunRequest
	if req.MaxIterations != 0 {
		t.Errorf("MaxIterations zero value: got %d", req.MaxIterations)
	}
	if req.Temperature != nil {
		t.Error("Temperature should be nil by default")
	}
	if req.TopP != nil {
		t.Error("TopP should be nil by default")
	}
	if req.WaitForAsyncTasks {
		t.Error("WaitForAsyncTasks should be false by default")
	}
}

func TestAgentRunRequestTemperaturePointer(t *testing.T) {
	temp := 0.7
	req := AgentRunRequest{Temperature: &temp}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("Temperature: got %v", req.Temperature)
	}

	req2 := AgentRunRequest{}
	if req2.Temperature != nil {
		t.Error("nil Temperature should be distinguishable from zero")
	}
}
