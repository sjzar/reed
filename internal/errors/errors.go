package errors

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// Code represents a domain error code.
type Code string

const (
	CodeNotFound      Code = "NOT_FOUND"
	CodeValidation    Code = "VALIDATION"
	CodeConflict      Code = "CONFLICT"
	CodeInternal      Code = "INTERNAL"
	CodeInvalidArg    Code = "INVALID_ARGUMENT"
	CodeAlreadyExists Code = "ALREADY_EXISTS"
	CodePermission    Code = "PERMISSION_DENIED"
	CodeUnavailable   Code = "UNAVAILABLE"
	CodeTimeout       Code = "TIMEOUT"
	CodeCanceled      Code = "CANCELED"
)

// Error is the domain error type for reed.
type Error struct {
	Message string   `json:"message"`
	Cause   error    `json:"-"`
	ErrCode Code     `json:"code"`
	Stack   []string `json:"-"`
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) WithStack() *Error {
	const depth = 32
	var pcs [depth]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])

	stack := make([]string, 0, n)
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.File, "runtime/") {
			stack = append(stack, fmt.Sprintf("%s:%d %s", frame.File, frame.Line, frame.Function))
		}
		if !more {
			break
		}
	}
	e.Stack = stack
	return e
}

// New creates a domain error with the given code and message.
func New(code Code, message string) *Error {
	return &Error{Message: message, ErrCode: code}
}

// Newf creates a domain error with a formatted message.
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...), ErrCode: code}
}

// Wrap wraps an existing error with a domain code and message.
func Wrap(err error, code Code, message string) *Error {
	if err == nil {
		return nil
	}
	return &Error{Message: message, Cause: err, ErrCode: code}
}

// Wrapf wraps an existing error with a formatted message.
func Wrapf(err error, code Code, format string, args ...any) *Error {
	if err == nil {
		return nil
	}
	return &Error{
		Message: fmt.Sprintf(format, args...),
		Cause:   err,
		ErrCode: code,
	}
}

// Domain error constructors.

func NewNotFound(message string) *Error {
	return New(CodeNotFound, message)
}

func NewValidation(message string) *Error {
	return New(CodeValidation, message)
}

func NewConflict(message string) *Error {
	return New(CodeConflict, message)
}

func NewInternal(message string) *Error {
	return New(CodeInternal, message)
}

// GetCode extracts the domain error code from an error.
func GetCode(err error) Code {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.ErrCode
	}
	return CodeInternal
}

// RootCause unwraps to the deepest error in the chain.
func RootCause(err error) error {
	for err != nil {
		unwrapped := errors.Unwrap(err)
		if unwrapped == nil {
			return err
		}
		err = unwrapped
	}
	return err
}

// Re-export stdlib errors functions for convenience.
func Is(err, target error) bool     { return errors.Is(err, target) }
func As(err error, target any) bool { return errors.As(err, target) }
