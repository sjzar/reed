package errors

import (
	"fmt"
	"testing"
)

func TestNew(t *testing.T) {
	err := New(CodeNotFound, "user not found")
	if err.ErrCode != CodeNotFound {
		t.Errorf("code = %q, want %q", err.ErrCode, CodeNotFound)
	}
	if err.Error() != "user not found" {
		t.Errorf("message = %q, want %q", err.Error(), "user not found")
	}
	if err.Cause != nil {
		t.Error("expected nil cause")
	}
}

func TestNewf(t *testing.T) {
	err := Newf(CodeValidation, "field %q is required", "name")
	if err.ErrCode != CodeValidation {
		t.Errorf("code = %q, want %q", err.ErrCode, CodeValidation)
	}
	want := `field "name" is required`
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestWrap(t *testing.T) {
	cause := fmt.Errorf("connection refused")
	err := Wrap(cause, CodeUnavailable, "database unreachable")
	if err.ErrCode != CodeUnavailable {
		t.Errorf("code = %q, want %q", err.ErrCode, CodeUnavailable)
	}
	if err.Unwrap() != cause {
		t.Error("Unwrap() did not return original cause")
	}
	if !Is(err, cause) {
		t.Error("Is() should match wrapped cause")
	}
}

func TestWrapNil(t *testing.T) {
	err := Wrap(nil, CodeInternal, "should be nil")
	if err != nil {
		t.Error("Wrap(nil, ...) should return nil")
	}
}

func TestGetCode(t *testing.T) {
	tests := []struct {
		err  error
		want Code
	}{
		{New(CodeNotFound, "x"), CodeNotFound},
		{New(CodeValidation, "x"), CodeValidation},
		{fmt.Errorf("plain error"), CodeInternal},
	}
	for _, tt := range tests {
		got := GetCode(tt.err)
		if got != tt.want {
			t.Errorf("GetCode(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestDomainConstructors(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) *Error
		want Code
	}{
		{"NotFound", NewNotFound, CodeNotFound},
		{"Validation", NewValidation, CodeValidation},
		{"Conflict", NewConflict, CodeConflict},
		{"Internal", NewInternal, CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn("test message")
			if err.ErrCode != tt.want {
				t.Errorf("code = %q, want %q", err.ErrCode, tt.want)
			}
		})
	}
}

func TestErrorWithCause(t *testing.T) {
	cause := fmt.Errorf("disk full")
	err := Wrap(cause, CodeInternal, "write failed")
	want := "write failed: disk full"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestWithStack(t *testing.T) {
	err := New(CodeInternal, "something broke").WithStack()
	if len(err.Stack) == 0 {
		t.Error("WithStack() should populate Stack")
	}
	for _, frame := range err.Stack {
		if frame == "" {
			t.Error("Stack contains empty frame")
		}
	}
}

func TestWrapf(t *testing.T) {
	cause := fmt.Errorf("timeout")
	err := Wrapf(cause, CodeTimeout, "step %q timed out", "build")
	if err == nil {
		t.Fatal("Wrapf returned nil for non-nil cause")
	}
	if err.ErrCode != CodeTimeout {
		t.Errorf("code = %q, want %q", err.ErrCode, CodeTimeout)
	}
	if err.Cause != cause {
		t.Error("Cause not preserved")
	}
	want := `step "build" timed out`
	if err.Message != want {
		t.Errorf("message = %q, want %q", err.Message, want)
	}
}

func TestWrapfNil(t *testing.T) {
	err := Wrapf(nil, CodeInternal, "should be nil")
	if err != nil {
		t.Error("Wrapf(nil, ...) should return nil")
	}
}

func TestRootCause(t *testing.T) {
	root := fmt.Errorf("root cause")
	mid := Wrap(root, CodeInternal, "mid")
	outer := Wrap(mid, CodeInternal, "outer")

	got := RootCause(outer)
	if got != root {
		t.Errorf("RootCause() = %v, want %v", got, root)
	}
}

func TestRootCauseNil(t *testing.T) {
	if got := RootCause(nil); got != nil {
		t.Errorf("RootCause(nil) = %v, want nil", got)
	}
}

func TestIs(t *testing.T) {
	sentinel := fmt.Errorf("sentinel")
	wrapped := Wrap(sentinel, CodeInternal, "wrapped")
	if !Is(wrapped, sentinel) {
		t.Error("Is() should find sentinel through wrap chain")
	}
	other := fmt.Errorf("other")
	if Is(wrapped, other) {
		t.Error("Is() should not match unrelated error")
	}
}

func TestAs(t *testing.T) {
	inner := New(CodeNotFound, "inner")
	outer := Wrap(inner, CodeInternal, "outer")
	var target *Error
	if !As(outer, &target) {
		t.Fatal("As() should match *Error in chain")
	}
	if target.ErrCode != CodeInternal {
		t.Errorf("As() target code = %q, want %q", target.ErrCode, CodeInternal)
	}
}
