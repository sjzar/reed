package model

import (
	"errors"
	"testing"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		statusCode int
		body       []byte
		wantKind   ErrorKind
		wantRetry  bool
	}{
		{
			name:       "rate limit",
			err:        errors.New("rate limited"),
			statusCode: 429,
			wantKind:   ErrRateLimit,
			wantRetry:  true,
		},
		{
			name:       "auth 401",
			err:        errors.New("unauthorized"),
			statusCode: 401,
			wantKind:   ErrAuth,
			wantRetry:  false,
		},
		{
			name:       "auth 403",
			err:        errors.New("forbidden"),
			statusCode: 403,
			wantKind:   ErrAuth,
			wantRetry:  false,
		},
		{
			name:       "server error 500",
			err:        errors.New("internal server error"),
			statusCode: 500,
			wantKind:   ErrServerError,
			wantRetry:  true,
		},
		{
			name:       "server error 503",
			err:        errors.New("service unavailable"),
			statusCode: 503,
			wantKind:   ErrServerError,
			wantRetry:  true,
		},
		{
			name:       "context overflow",
			err:        errors.New("context too long"),
			statusCode: 400,
			body:       []byte(`{"error":{"type":"context_length_exceeded"}}`),
			wantKind:   ErrContextOverflow,
			wantRetry:  false,
		},
		{
			name:       "other error",
			err:        errors.New("bad request"),
			statusCode: 400,
			wantKind:   ErrOther,
			wantRetry:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aiErr := ClassifyError(tt.err, tt.statusCode, tt.body)
			if aiErr.Kind != tt.wantKind {
				t.Errorf("kind: got %v, want %v", aiErr.Kind, tt.wantKind)
			}
			if aiErr.Retryable != tt.wantRetry {
				t.Errorf("retryable: got %v, want %v", aiErr.Retryable, tt.wantRetry)
			}
			if aiErr.StatusCode != tt.statusCode {
				t.Errorf("statusCode: got %d, want %d", aiErr.StatusCode, tt.statusCode)
			}
			// Verify Unwrap
			if !errors.Is(aiErr, tt.err) {
				t.Error("Unwrap should return original error")
			}
		})
	}
}

func TestAIError_ErrorString(t *testing.T) {
	e := &AIError{Message: "test error"}
	if e.Error() != "test error" {
		t.Errorf("got %q, want %q", e.Error(), "test error")
	}
}

func TestContainsContextOverflow(t *testing.T) {
	tests := []struct {
		body []byte
		want bool
	}{
		{nil, false},
		{[]byte(""), false},
		{[]byte(`{"error":"context_length_exceeded"}`), true},
		{[]byte(`{"error":"maximum context length"}`), true},
		{[]byte(`{"error":"something else"}`), false},
	}
	for _, tt := range tests {
		got := containsContextOverflow(tt.body)
		if got != tt.want {
			t.Errorf("containsContextOverflow(%q) = %v, want %v", tt.body, got, tt.want)
		}
	}
}
