package worker

import (
	"testing"
)

func TestToInt(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int
		ok   bool
	}{
		{"int", 42, 42, true},
		{"int64", int64(99), 99, true},
		{"float64_whole", float64(10), 10, true},
		{"float64_fractional", 1.9, 0, false},
		{"string", "42", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toInt(tt.in)
			if ok != tt.ok {
				t.Errorf("toInt(%v) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("toInt(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want float64
		ok   bool
	}{
		{"float64", 3.14, 3.14, true},
		{"int", 7, 7.0, true},
		{"int64", int64(99), 99.0, true},
		{"string", "3.14", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toFloat64(tt.in)
			if ok != tt.ok {
				t.Errorf("toFloat64(%v) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("toFloat64(%v) = %f, want %f", tt.in, got, tt.want)
			}
		})
	}
}

func TestToStringSlice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []string
		ok   bool
	}{
		{"[]string", []string{"a", "b"}, []string{"a", "b"}, true},
		{"[]any_strings", []any{"a", "b"}, []string{"a", "b"}, true},
		{"[]any_mixed", []any{"a", 1}, nil, false},
		{"int", 42, nil, false},
		{"nil", nil, nil, false},
		{"empty_[]string", []string{}, []string{}, true},
		{"empty_[]any", []any{}, []string{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toStringSlice(tt.in)
			if ok != tt.ok {
				t.Errorf("toStringSlice(%v) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if ok && len(got) != len(tt.want) {
				t.Errorf("toStringSlice(%v) len = %d, want %d", tt.in, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("toStringSlice(%v)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestToMapStringAny(t *testing.T) {
	tests := []struct {
		name string
		in   any
		ok   bool
	}{
		{"map", map[string]any{"k": "v"}, true},
		{"nil", nil, false},
		{"string", "not a map", false},
		{"int", 42, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toMapStringAny(tt.in)
			if ok != tt.ok {
				t.Errorf("toMapStringAny(%v) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if ok && got == nil {
				t.Error("expected non-nil map")
			}
		})
	}
}

func TestNormalizeClaudeToolName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"mcp__srv__tool", "mcp/srv/tool"},
		{"simple_tool", "simple_tool"},
		{"no_double", "no_double"},
		{"a__b__c__d", "a/b/c/d"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := normalizeClaudeToolName(tt.in)
			if got != tt.want {
				t.Errorf("normalizeClaudeToolName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
