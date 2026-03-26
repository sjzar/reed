package model

import "testing"

func TestProcessStatusValues(t *testing.T) {
	tests := []struct {
		status ProcessStatus
		want   string
	}{
		{ProcessStarting, "STARTING"},
		{ProcessRunning, "RUNNING"},
		{ProcessStopped, "STOPPED"},
		{ProcessFailed, "FAILED"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("ProcessStatus = %q, want %q", tt.status, tt.want)
		}
	}
}

func TestRunStatusValues(t *testing.T) {
	tests := []struct {
		status RunStatus
		want   string
	}{
		{RunCreated, "CREATED"},
		{RunStarting, "STARTING"},
		{RunRunning, "RUNNING"},
		{RunStopping, "STOPPING"},
		{RunSucceeded, "SUCCEEDED"},
		{RunFailed, "FAILED"},
		{RunCanceled, "CANCELED"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("RunStatus = %q, want %q", tt.status, tt.want)
		}
	}
}

func TestStepStatusValues(t *testing.T) {
	tests := []struct {
		status StepStatus
		want   string
	}{
		{StepPending, "PENDING"},
		{StepRunning, "RUNNING"},
		{StepSucceeded, "SUCCEEDED"},
		{StepFailed, "FAILED"},
		{StepCanceled, "CANCELED"},
		{StepSkipped, "SKIPPED"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("StepStatus = %q, want %q", tt.status, tt.want)
		}
	}
}

func TestProcessModeValues(t *testing.T) {
	if ProcessModeCLI != "cli" {
		t.Errorf("ProcessModeCLI = %q, want %q", ProcessModeCLI, "cli")
	}
	if ProcessModeService != "service" {
		t.Errorf("ProcessModeService = %q, want %q", ProcessModeService, "service")
	}
}
