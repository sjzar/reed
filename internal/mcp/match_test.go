package mcp

import "testing"

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
	}
	for _, tt := range tests {
		got := levenshtein(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d",
				tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNormalizeIdentifier(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-server", "myserver"},
		{"My_Server_1", "myserver1"},
		{"hello world!", "helloworld"},
		{"ABC", "abc"},
	}
	for _, tt := range tests {
		got := normalizeIdentifier(tt.input)
		if got != tt.want {
			t.Errorf("normalizeIdentifier(%q) = %q, want %q",
				tt.input, got, tt.want)
		}
	}
}

func TestMatchName(t *testing.T) {
	candidates := []string{
		"my-server", "file-reader", "code-runner",
	}

	tests := []struct {
		input    string
		wantName string
		wantKind MatchKind
		wantNil  bool
	}{
		// Exact
		{"my-server", "my-server", MatchAuto, false},
		// Normalized
		{"myserver", "my-server", MatchAuto, false},
		// Case-insensitive
		{"My-Server", "my-server", MatchAuto, false},
		// Levenshtein (1 edit)
		{"my-servar", "my-server", MatchAuto, false},
		// Levenshtein (1 edit — deletion)
		{"my-servr", "my-server", MatchAuto, false},
		// No match
		{"completely-different", "", "", true},
	}

	for _, tt := range tests {
		result := matchName(tt.input, candidates)
		if tt.wantNil {
			if result != nil {
				t.Errorf("matchName(%q): got %+v, want nil",
					tt.input, result)
			}
			continue
		}
		if result == nil {
			t.Errorf("matchName(%q): got nil, want %q",
				tt.input, tt.wantName)
			continue
		}
		if result.Name != tt.wantName {
			t.Errorf("matchName(%q).Name = %q, want %q",
				tt.input, result.Name, tt.wantName)
		}
		if result.Kind != tt.wantKind {
			t.Errorf("matchName(%q).Kind = %q, want %q",
				tt.input, result.Kind, tt.wantKind)
		}
	}
}

func TestMatchName_EmptyCandidates(t *testing.T) {
	if matchName("foo", nil) != nil {
		t.Error("expected nil for empty candidates")
	}
}
