package model

import (
	"fmt"
	"strings"
	"time"
)

// ErrorKind classifies LLM API errors for recovery strategy selection.
type ErrorKind int

const (
	ErrOther           ErrorKind = iota
	ErrRateLimit                 // 429 — retryable (exponential backoff)
	ErrAuth                      // 401/403 — not retryable
	ErrContextOverflow           // context_length_exceeded — degradable
	ErrServerError               // 500/502/503 — retryable
	ErrNetwork                   // connection timeout / DNS failure — retryable
)

// AIError wraps an LLM API error with classification metadata.
type AIError struct {
	Kind       ErrorKind
	StatusCode int // HTTP status code (if available)
	Message    string
	Retryable  bool
	RetryAfter time.Duration // from Retry-After header
	Err        error         // original error
}

func (e *AIError) Error() string { return e.Message }
func (e *AIError) Unwrap() error { return e.Err }

// ClassifyError categorizes a provider error into an AIError.
func ClassifyError(err error, statusCode int, body []byte) *AIError {
	msg := fmt.Sprintf("llm api error (status %d): %s", statusCode, err)
	if err != nil {
		msg = err.Error()
	}

	switch {
	case statusCode == 429:
		return &AIError{Kind: ErrRateLimit, StatusCode: statusCode, Message: msg, Retryable: true, Err: err}
	case statusCode == 401 || statusCode == 403:
		return &AIError{Kind: ErrAuth, StatusCode: statusCode, Message: msg, Retryable: false, Err: err}
	case statusCode >= 500:
		return &AIError{Kind: ErrServerError, StatusCode: statusCode, Message: msg, Retryable: true, Err: err}
	case containsContextOverflow(body):
		return &AIError{Kind: ErrContextOverflow, StatusCode: statusCode, Message: msg, Retryable: false, Err: err}
	default:
		return &AIError{Kind: ErrOther, StatusCode: statusCode, Message: msg, Retryable: false, Err: err}
	}
}

// containsContextOverflow checks if the response body indicates a context length exceeded error.
func containsContextOverflow(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	s := string(body)
	return strings.Contains(s, "context_length_exceeded") ||
		strings.Contains(s, "maximum context length") ||
		strings.Contains(s, "max_tokens") && strings.Contains(s, "too many")
}
