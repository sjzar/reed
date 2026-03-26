package token

import "testing"

func TestEstimate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"single ASCII", "a", 1},
		{"hello", "hello", 2},
		{"Hello, World!", "Hello, World!", 4},
		{"CJK pair", "你好", 3},
		{"CJK four", "你好世界", 5},
		{"mixed ASCII+CJK", "Hi你好", 3},
		{"single emoji", "😀", 3},
		{"four emoji", "😀😁😂🤣", 9},
		{"spaces only", "     ", 2},
		{"newlines", "\n\n\n", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Estimate(tt.input)
			if got != tt.want {
				t.Errorf("Estimate(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestEstimate_CJKHigherThanASCII(t *testing.T) {
	ascii := Estimate("abcde")
	cjk := Estimate("你好世界吧")
	if cjk <= ascii {
		t.Errorf("5 CJK chars (%d) should estimate higher than 5 ASCII chars (%d)", cjk, ascii)
	}
}

func TestEstimate_EmojiHigherThanASCII(t *testing.T) {
	ascii := Estimate("abcd")
	emoji := Estimate("😀😁😂🤣")
	if emoji <= ascii {
		t.Errorf("4 emoji (%d) should estimate higher than 4 ASCII chars (%d)", emoji, ascii)
	}
}
