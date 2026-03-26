package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewSecretStore_Nil(t *testing.T) {
	s, err := NewSecretStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap == nil {
		t.Fatal("Snapshot must be non-nil")
	}
	if len(snap) != 0 {
		t.Errorf("expected empty, got %v", snap)
	}
}

func TestNewSecretStore_SingleFile(t *testing.T) {
	p := writeTestFile(t, t.TempDir(), "secrets.env", "API_KEY=secret123\nDB_PASS=\"hunter2\"\n")

	s, err := NewSecretStore([]SecretSource{{Type: SecretSourceTypeFile, Path: p}})
	if err != nil {
		t.Fatal(err)
	}

	if v, ok := s.Get("API_KEY"); !ok || v != "secret123" {
		t.Errorf("API_KEY = %q, ok=%v", v, ok)
	}
	if v, ok := s.Get("DB_PASS"); !ok || v != "hunter2" {
		t.Errorf("DB_PASS = %q, ok=%v", v, ok)
	}
	if _, ok := s.Get("MISSING"); ok {
		t.Error("expected MISSING to not exist")
	}
}

func TestNewSecretStore_LastWins(t *testing.T) {
	dir := t.TempDir()
	p1 := writeTestFile(t, dir, "a.env", "KEY=first\nONLY_A=a\n")
	p2 := writeTestFile(t, dir, "b.env", "KEY=second\nONLY_B=b\n")

	s, err := NewSecretStore([]SecretSource{
		{Type: SecretSourceTypeFile, Path: p1},
		{Type: SecretSourceTypeFile, Path: p2},
	})
	if err != nil {
		t.Fatal(err)
	}

	snap := s.Snapshot()
	if snap["KEY"] != "second" {
		t.Errorf("KEY = %q, want second", snap["KEY"])
	}
	if snap["ONLY_A"] != "a" {
		t.Errorf("ONLY_A = %q", snap["ONLY_A"])
	}
	if snap["ONLY_B"] != "b" {
		t.Errorf("ONLY_B = %q", snap["ONLY_B"])
	}
}

func TestNewSecretStore_MissingFile(t *testing.T) {
	_, err := NewSecretStore([]SecretSource{{Type: SecretSourceTypeFile, Path: "/nonexistent/secrets.env"}})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "/nonexistent/secrets.env") {
		t.Errorf("error should contain path, got: %v", err)
	}
}

func TestNewSecretStore_UnsupportedType(t *testing.T) {
	_, err := NewSecretStore([]SecretSource{{Type: "vault", Path: "x"}})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestNewSecretStore_EnvSyntax(t *testing.T) {
	content := "# comment\nexport EXPORTED=yes\nQUOTED='single'\nDOUBLE=\"double\"\nEMPTY=\n"
	p := writeTestFile(t, t.TempDir(), "secrets.env", content)

	s, err := NewSecretStore([]SecretSource{{Type: SecretSourceTypeFile, Path: p}})
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]string{
		"EXPORTED": "yes",
		"QUOTED":   "single",
		"DOUBLE":   "double",
		"EMPTY":    "",
	}
	for k, want := range tests {
		got, ok := s.Get(k)
		if !ok {
			t.Errorf("%s: not found", k)
		} else if got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestNewSecretStore_DollarLiteral(t *testing.T) {
	content := "PASSWORD=$SHELL\nTOKEN=abc$123\n"
	p := writeTestFile(t, t.TempDir(), "secrets.env", content)

	s, err := NewSecretStore([]SecretSource{{Type: SecretSourceTypeFile, Path: p}})
	if err != nil {
		t.Fatal(err)
	}

	// Values must be stored verbatim, no $VAR expansion.
	if v, _ := s.Get("PASSWORD"); v != "$SHELL" {
		t.Errorf("PASSWORD = %q, want $SHELL (no expansion)", v)
	}
	if v, _ := s.Get("TOKEN"); v != "abc$123" {
		t.Errorf("TOKEN = %q, want abc$123", v)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~/file.env", filepath.Join(home, "file.env")},
		{"~", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~user/path", "~user/path"}, // ~username not expanded
	}
	for _, tt := range tests {
		got, err := expandHome(tt.input)
		if err != nil {
			t.Errorf("expandHome(%q) error: %v", tt.input, err)
		} else if got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
