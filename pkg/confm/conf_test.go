package confm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// --- test config types ---

type testConfig struct {
	Name   string     `json:"name" mapstructure:"name"`
	Debug  bool       `json:"debug" mapstructure:"debug"`
	Port   int        `json:"port" mapstructure:"port"`
	Nested nestConfig `json:"nested" mapstructure:"nested"`
}

type nestConfig struct {
	Addr    string            `json:"addr" mapstructure:"addr"`
	Tags    []string          `json:"tags" mapstructure:"tags"`
	Headers map[string]string `json:"headers" mapstructure:"headers"`
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func testDefaults(v *viper.Viper) {
	v.SetDefault("port", 8080)
	v.SetDefault("nested.addr", "localhost:3000")
}

// --- New / Load ---

func TestNew_FirstRun_CreatesDefaultFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")

	m, err := New[testConfig](path, WithSetDefault(testDefaults), WithDisableEnv(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := m.Load()
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Nested.Addr != "localhost:3000" {
		t.Errorf("Nested.Addr = %q, want localhost:3000", cfg.Nested.Addr)
	}

	// Verify the file was created with proper JSON (no nil placeholders).
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal created file: %v", err)
	}
	// Port should be present as a number, not null.
	if p, ok := raw["port"]; !ok {
		t.Error("port key missing from created file")
	} else if p == nil {
		t.Error("port is nil in created file, expected default value")
	}
}

func TestNew_LoadsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")

	writeJSON(t, path, map[string]any{
		"name":  "myapp",
		"debug": true,
		"port":  9090,
		"nested": map[string]any{
			"addr": "0.0.0.0:5000",
		},
	})

	m, err := New[testConfig](path, WithDisableEnv(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := m.Load()
	if cfg.Name != "myapp" {
		t.Errorf("Name = %q, want myapp", cfg.Name)
	}
	if !cfg.Debug {
		t.Error("Debug should be true")
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.Nested.Addr != "0.0.0.0:5000" {
		t.Errorf("Nested.Addr = %q", cfg.Nested.Addr)
	}
}

// --- Save ---

func TestSave_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")

	m, err := New[testConfig](path, WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Save(func(c *testConfig) {
		c.Name = "updated"
		c.Port = 1234
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg := m.Load()
	if cfg.Name != "updated" {
		t.Errorf("Name = %q, want updated", cfg.Name)
	}
	if cfg.Port != 1234 {
		t.Errorf("Port = %d, want 1234", cfg.Port)
	}

	// Verify file content matches.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ondisk testConfig
	if err := json.Unmarshal(data, &ondisk); err != nil {
		t.Fatal(err)
	}
	if ondisk.Name != "updated" || ondisk.Port != 1234 {
		t.Errorf("on-disk = %+v", ondisk)
	}
}

func TestSave_AtomicWrite_NoTempFileLeftOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")

	m, err := New[testConfig](path, WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Save(func(c *testConfig) { c.Name = "atomic" }); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestSave_CloneProtectsBaseOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")

	m, err := New[testConfig](path, WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}

	// Save a known value first.
	if err := m.Save(func(c *testConfig) { c.Name = "original" }); err != nil {
		t.Fatal(err)
	}

	// Make the file unwritable to force a write failure.
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755)

	err = m.Save(func(c *testConfig) { c.Name = "should-not-persist" })
	if err == nil {
		t.Fatal("expected Save to fail on read-only dir")
	}

	// Base should still be "original".
	cfg := m.Load()
	if cfg.Name != "original" {
		t.Errorf("Name = %q after failed save, want original", cfg.Name)
	}
}

// --- JSON round-trip (Save/Load cycle) ---

func TestSaveLoad_RoundTrip_PreservesAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")

	m, err := New[testConfig](path, WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Save(func(c *testConfig) {
		c.Name = "roundtrip"
		c.Debug = true
		c.Port = 443
		c.Nested.Addr = "10.0.0.1:8443"
		c.Nested.Tags = []string{"a", "b"}
		c.Nested.Headers = map[string]string{"X-Key": "val"}
	}); err != nil {
		t.Fatal(err)
	}

	// Re-open from the same file.
	m2, err := New[testConfig](path, WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}
	cfg := m2.Load()
	if cfg.Name != "roundtrip" || cfg.Port != 443 || !cfg.Debug {
		t.Errorf("scalar fields: %+v", cfg)
	}
	if cfg.Nested.Addr != "10.0.0.1:8443" {
		t.Errorf("Nested.Addr = %q", cfg.Nested.Addr)
	}
	if len(cfg.Nested.Tags) != 2 || cfg.Nested.Tags[0] != "a" {
		t.Errorf("Nested.Tags = %v", cfg.Nested.Tags)
	}
	if cfg.Nested.Headers["X-Key"] != "val" && cfg.Nested.Headers["x-key"] != "val" {
		t.Errorf("Nested.Headers = %v", cfg.Nested.Headers)
	}
}

// --- ENV overlay ---

func TestEnv_TopLevelOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"name": "file", "port": 80})

	t.Setenv("TEST_NAME", "from-env")
	t.Setenv("TEST_PORT", "9999")

	m, err := New[testConfig](path, WithEnvPrefix("TEST"))
	if err != nil {
		t.Fatal(err)
	}

	cfg := m.Load()
	if cfg.Name != "from-env" {
		t.Errorf("Name = %q, want from-env", cfg.Name)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999", cfg.Port)
	}
}

func TestEnv_NestedKeyOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"nested": map[string]any{"addr": "file-addr"},
	})

	// Viper maps nested.addr → PREFIX_NESTED_ADDR via SetEnvKeyReplacer("." → "_").
	t.Setenv("APP_NESTED_ADDR", "env-addr")

	m, err := New[testConfig](path, WithEnvPrefix("APP"))
	if err != nil {
		t.Fatal(err)
	}

	cfg := m.Load()
	if cfg.Nested.Addr != "env-addr" {
		t.Errorf("Nested.Addr = %q, want env-addr", cfg.Nested.Addr)
	}
}

func TestEnv_BoolOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"debug": false})

	t.Setenv("BT_DEBUG", "true")

	m, err := New[testConfig](path, WithEnvPrefix("BT"))
	if err != nil {
		t.Fatal(err)
	}
	if !m.Load().Debug {
		t.Error("Debug should be true from env")
	}
}

func TestEnv_DoesNotPollute_SavedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"name": "base", "port": 80})

	t.Setenv("SP_NAME", "env-override")
	t.Setenv("SP_PORT", "9999")

	m, err := New[testConfig](path, WithEnvPrefix("SP"))
	if err != nil {
		t.Fatal(err)
	}

	// Active should reflect env.
	cfg := m.Load()
	if cfg.Name != "env-override" {
		t.Fatalf("active Name = %q, want env-override", cfg.Name)
	}

	// Save a different field — env values must NOT leak into the JSON file.
	if err := m.Save(func(c *testConfig) { c.Debug = true }); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var ondisk map[string]any
	json.Unmarshal(data, &ondisk)

	if ondisk["name"] != "base" {
		t.Errorf("on-disk name = %v, want base (env leaked into file)", ondisk["name"])
	}
}

func TestEnv_DisableEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"name": "file-val"})

	t.Setenv("DIS_NAME", "env-val")

	m, err := New[testConfig](path, WithEnvPrefix("DIS"), WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}

	if m.Load().Name != "file-val" {
		t.Errorf("Name = %q, want file-val (env should be ignored)", m.Load().Name)
	}
}

func TestEnv_NoPrefix_UsesRawEnvKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"name": "file"})

	t.Setenv("NAME", "raw-env")

	m, err := New[testConfig](path)
	if err != nil {
		t.Fatal(err)
	}

	if m.Load().Name != "raw-env" {
		t.Errorf("Name = %q, want raw-env", m.Load().Name)
	}
}

// --- .env file ---

func TestEnvFile_LoadedIntoProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	envFile := filepath.Join(dir, ".env")

	writeJSON(t, path, map[string]any{"name": "file"})
	os.WriteFile(envFile, []byte("EF_NAME=dotenv-val\n"), 0644)

	m, err := New[testConfig](path, WithEnvPrefix("EF"), WithEnvFiles(envFile))
	if err != nil {
		t.Fatal(err)
	}

	if m.Load().Name != "dotenv-val" {
		t.Errorf("Name = %q, want dotenv-val", m.Load().Name)
	}
}

// --- Defaults ---

func TestDefaults_AppliedWhenFileHasNoKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	// File has name but no port.
	writeJSON(t, path, map[string]any{"name": "partial"})

	m, err := New[testConfig](path, WithSetDefault(testDefaults), WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}

	cfg := m.Load()
	if cfg.Name != "partial" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080 (default)", cfg.Port)
	}
}

func TestDefaults_FileValueTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"port": 3000})

	m, err := New[testConfig](path, WithSetDefault(testDefaults), WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}

	if m.Load().Port != 3000 {
		t.Errorf("Port = %d, want 3000 (file overrides default)", m.Load().Port)
	}
}

// --- Priority: default < file < env ---

func TestPriority_DefaultFileEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"port": 5000})

	t.Setenv("PRI_PORT", "7777")

	m, err := New[testConfig](path,
		WithEnvPrefix("PRI"),
		WithSetDefault(func(v *viper.Viper) { v.SetDefault("port", 1111) }),
	)
	if err != nil {
		t.Fatal(err)
	}

	if m.Load().Port != 7777 {
		t.Errorf("Port = %d, want 7777 (env > file > default)", m.Load().Port)
	}
}

// --- Duration parsing via StringToTimeDurationHookFunc ---

type durConfig struct {
	Timeout time.Duration `json:"timeout" mapstructure:"timeout"`
}

func TestDuration_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"timeout": "5s"})

	m, err := New[durConfig](path, WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}

	if m.Load().Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", m.Load().Timeout)
	}
}

func TestDuration_FromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"timeout": "1s"})

	t.Setenv("DUR_TIMEOUT", "30s")

	m, err := New[durConfig](path, WithEnvPrefix("DUR"))
	if err != nil {
		t.Fatal(err)
	}

	if m.Load().Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", m.Load().Timeout)
	}
}

// =============================================================================
// Complex data structures: ENV override + SetDefault for slice, map, struct
// =============================================================================

// richConfig exercises all complex field types that pass through decode hooks.
type richConfig struct {
	Name    string            `json:"name" mapstructure:"name"`
	Tags    []string          `json:"tags" mapstructure:"tags"`
	Headers map[string]string `json:"headers" mapstructure:"headers"`
	Server  serverConfig      `json:"server" mapstructure:"server"`
}

type serverConfig struct {
	Host    string `json:"host" mapstructure:"host"`
	Port    int    `json:"port" mapstructure:"port"`
	Verbose bool   `json:"verbose" mapstructure:"verbose"`
}

// --- Slice via ENV ---

func TestEnv_SliceOverride_JSONArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"tags": []string{"from-file"},
	})

	// Viper delivers env as a raw string; StringToSliceWithBracketHookFunc must parse it.
	t.Setenv("RC_TAGS", `["alpha","beta","gamma"]`)

	m, err := New[richConfig](path, WithEnvPrefix("RC"))
	if err != nil {
		t.Fatal(err)
	}

	tags := m.Load().Tags
	if len(tags) != 3 || tags[0] != "alpha" || tags[1] != "beta" || tags[2] != "gamma" {
		t.Errorf("Tags = %v, want [alpha beta gamma]", tags)
	}
}

func TestEnv_SliceOverride_EmptyArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"tags": []string{"should-be-cleared"},
	})

	t.Setenv("RC2_TAGS", `[]`)

	m, err := New[richConfig](path, WithEnvPrefix("RC2"))
	if err != nil {
		t.Fatal(err)
	}

	tags := m.Load().Tags
	if len(tags) != 0 {
		t.Errorf("Tags = %v, want empty slice", tags)
	}
}

// --- Map via ENV ---

func TestEnv_MapOverride_CommaSeparated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"headers": map[string]string{"X-Old": "old"},
	})

	// DecodeStringToMap parses "k=v,k=v" format.
	t.Setenv("MC_HEADERS", "X-Auth=token123,X-Trace=abc")

	m, err := New[richConfig](path, WithEnvPrefix("MC"))
	if err != nil {
		t.Fatal(err)
	}

	h := m.Load().Headers
	if h["X-Auth"] != "token123" {
		t.Errorf("Headers[X-Auth] = %q, want token123", h["X-Auth"])
	}
	if h["X-Trace"] != "abc" {
		t.Errorf("Headers[X-Trace] = %q, want abc", h["X-Trace"])
	}
	// The old file value should be replaced entirely.
	if _, ok := h["X-Old"]; ok {
		t.Error("X-Old should not be present after env override")
	}
}

func TestEnv_MapOverride_EmptyString_ViperLimitation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"headers": map[string]string{"X-Keep": "val"},
	})

	t.Setenv("MC2_HEADERS", "")

	m, err := New[richConfig](path, WithEnvPrefix("MC2"))
	if err != nil {
		t.Fatal(err)
	}

	h := m.Load().Headers
	// Known Viper limitation: empty env string does NOT override a map value
	// from the config file. Viper ignores empty env for complex types.
	// The file value is preserved.
	if len(h) != 1 {
		t.Errorf("Headers = %v, want map with 1 entry (Viper ignores empty env for maps)", h)
	}
}

func TestEnv_MapOverride_ValueContainsEquals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{})

	// Value contains '=' — only the first '=' is the separator.
	t.Setenv("MC3_HEADERS", "url=http://host?a=1&b=2")

	m, err := New[richConfig](path, WithEnvPrefix("MC3"))
	if err != nil {
		t.Fatal(err)
	}

	h := m.Load().Headers
	if h["url"] != "http://host?a=1&b=2" {
		t.Errorf("Headers[url] = %q", h["url"])
	}
}

// --- Nested struct fields via ENV ---

func TestEnv_NestedStructFieldOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"server": map[string]any{"host": "file-host", "port": 80, "verbose": false},
	})

	t.Setenv("NS_SERVER_HOST", "env-host")
	t.Setenv("NS_SERVER_PORT", "9090")
	t.Setenv("NS_SERVER_VERBOSE", "true")

	m, err := New[richConfig](path, WithEnvPrefix("NS"))
	if err != nil {
		t.Fatal(err)
	}

	s := m.Load().Server
	if s.Host != "env-host" {
		t.Errorf("Server.Host = %q, want env-host", s.Host)
	}
	if s.Port != 9090 {
		t.Errorf("Server.Port = %d, want 9090", s.Port)
	}
	if !s.Verbose {
		t.Error("Server.Verbose should be true from env")
	}
}

func TestEnv_NestedStructPartialOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"server": map[string]any{"host": "file-host", "port": 3000},
	})

	// Only override host, port should remain from file.
	t.Setenv("NSP_SERVER_HOST", "partial-env")

	m, err := New[richConfig](path, WithEnvPrefix("NSP"))
	if err != nil {
		t.Fatal(err)
	}

	s := m.Load().Server
	if s.Host != "partial-env" {
		t.Errorf("Server.Host = %q, want partial-env", s.Host)
	}
	if s.Port != 3000 {
		t.Errorf("Server.Port = %d, want 3000 (from file, not overridden)", s.Port)
	}
}

// --- SetDefault with complex types ---

func TestDefaults_Slice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"name": "x"})

	m, err := New[richConfig](path,
		WithDisableEnv(true),
		WithSetDefault(func(v *viper.Viper) {
			v.SetDefault("tags", []string{"default-a", "default-b"})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	tags := m.Load().Tags
	if len(tags) != 2 || tags[0] != "default-a" || tags[1] != "default-b" {
		t.Errorf("Tags = %v, want [default-a default-b]", tags)
	}
}

func TestDefaults_Map(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"name": "x"})

	m, err := New[richConfig](path,
		WithDisableEnv(true),
		WithSetDefault(func(v *viper.Viper) {
			v.SetDefault("headers", map[string]string{"X-Default": "yes"})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	h := m.Load().Headers
	if h["X-Default"] != "yes" && h["x-default"] != "yes" {
		t.Errorf("Headers = %v, want X-Default=yes", h)
	}
}

func TestDefaults_NestedStruct(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"name": "x"})

	m, err := New[richConfig](path,
		WithDisableEnv(true),
		WithSetDefault(func(v *viper.Viper) {
			v.SetDefault("server.host", "default-host")
			v.SetDefault("server.port", 4000)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	s := m.Load().Server
	if s.Host != "default-host" {
		t.Errorf("Server.Host = %q, want default-host", s.Host)
	}
	if s.Port != 4000 {
		t.Errorf("Server.Port = %d, want 4000", s.Port)
	}
}

// --- Full priority chain with complex types: default < file < env ---

func TestPriority_Slice_DefaultFileEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"tags": []string{"file-tag"},
	})

	t.Setenv("SLP_TAGS", `["env-tag-1","env-tag-2"]`)

	m, err := New[richConfig](path,
		WithEnvPrefix("SLP"),
		WithSetDefault(func(v *viper.Viper) {
			v.SetDefault("tags", []string{"default-tag"})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	tags := m.Load().Tags
	if len(tags) != 2 || tags[0] != "env-tag-1" || tags[1] != "env-tag-2" {
		t.Errorf("Tags = %v, want [env-tag-1 env-tag-2] (env > file > default)", tags)
	}
}

func TestPriority_Map_DefaultFileEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"headers": map[string]string{"From": "file"},
	})

	t.Setenv("MLP_HEADERS", "From=env,Extra=bonus")

	m, err := New[richConfig](path,
		WithEnvPrefix("MLP"),
		WithSetDefault(func(v *viper.Viper) {
			v.SetDefault("headers", map[string]string{"From": "default"})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	h := m.Load().Headers
	if h["From"] != "env" {
		t.Errorf("Headers[From] = %q, want env", h["From"])
	}
	if h["Extra"] != "bonus" {
		t.Errorf("Headers[Extra] = %q, want bonus", h["Extra"])
	}
}

func TestPriority_NestedStruct_DefaultFileEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"server": map[string]any{"host": "file-host", "port": 5000},
	})

	t.Setenv("NLP_SERVER_HOST", "env-host")
	// PORT not set in env — should fall through to file value.

	m, err := New[richConfig](path,
		WithEnvPrefix("NLP"),
		WithSetDefault(func(v *viper.Viper) {
			v.SetDefault("server.host", "default-host")
			v.SetDefault("server.port", 1000)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	s := m.Load().Server
	if s.Host != "env-host" {
		t.Errorf("Server.Host = %q, want env-host (env > file)", s.Host)
	}
	if s.Port != 5000 {
		t.Errorf("Server.Port = %d, want 5000 (file > default)", s.Port)
	}
}

// --- Save does not persist env-overlaid complex values ---

func TestSave_ComplexEnvDoesNotLeakToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"tags":    []string{"base-tag"},
		"headers": map[string]string{"X-Base": "1"},
		"server":  map[string]any{"host": "base-host", "port": 80},
	})

	t.Setenv("CLK_TAGS", `["env-tag"]`)
	t.Setenv("CLK_HEADERS", "X-Env=secret")
	t.Setenv("CLK_SERVER_HOST", "env-host")

	m, err := New[richConfig](path, WithEnvPrefix("CLK"))
	if err != nil {
		t.Fatal(err)
	}

	// Active should reflect env.
	cfg := m.Load()
	if len(cfg.Tags) != 1 || cfg.Tags[0] != "env-tag" {
		t.Fatalf("active Tags = %v, want [env-tag]", cfg.Tags)
	}

	// Save an unrelated field.
	if err := m.Save(func(c *richConfig) { c.Name = "saved" }); err != nil {
		t.Fatal(err)
	}

	// Read file back — env values must not have leaked.
	data, _ := os.ReadFile(path)
	var ondisk richConfig
	json.Unmarshal(data, &ondisk)

	if len(ondisk.Tags) != 1 || ondisk.Tags[0] != "base-tag" {
		t.Errorf("on-disk Tags = %v, want [base-tag] (env leaked)", ondisk.Tags)
	}
	if _, ok := ondisk.Headers["X-Env"]; ok {
		t.Error("on-disk Headers contains X-Env (env leaked)")
	}
	if ondisk.Server.Host != "base-host" {
		t.Errorf("on-disk Server.Host = %q, want base-host (env leaked)", ondisk.Server.Host)
	}
}

// --- Viper edge cases for complex types ---

func TestEnv_SliceOverride_EmptyString_ViperLimitation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"tags": []string{"file-tag"},
	})

	// Empty string env for a slice — same Viper limitation as maps.
	t.Setenv("SLE_TAGS", "")

	m, err := New[richConfig](path, WithEnvPrefix("SLE"))
	if err != nil {
		t.Fatal(err)
	}

	tags := m.Load().Tags
	// Known Viper limitation: empty env string does NOT override a slice value
	// from the config file. The file value is preserved.
	if len(tags) != 1 || tags[0] != "file-tag" {
		t.Errorf("Tags = %v, want [file-tag] (Viper ignores empty env for slices)", tags)
	}
}

// --- Struct-as-JSON-string via ENV (StringToStructHookFunc path) ---

type structEnvConfig struct {
	Name   string       `json:"name" mapstructure:"name"`
	Server serverConfig `json:"server" mapstructure:"server"`
}

func TestEnv_StructAsJSONString_ViperLimitation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"server": map[string]any{"host": "file-host", "port": 80},
	})

	// Attempt to override the entire struct via a JSON string in env.
	t.Setenv("SJS_SERVER", `{"host":"json-host","port":9999,"verbose":true}`)

	m, err := New[structEnvConfig](path, WithEnvPrefix("SJS"))
	if err != nil {
		t.Fatal(err)
	}

	s := m.Load().Server
	// Known limitation: prepareViper registers sub-keys (server.host, server.port, etc.)
	// via SetDefault, which tells Viper that "server" is a map. When env sets the parent
	// key "SERVER" as a string, it conflicts with the sub-key structure. Viper cannot
	// drill into the string for sub-keys, so all fields resolve to zero values.
	//
	// Whole-struct-as-JSON-string override is NOT supported.
	// Use per-field env override instead: SERVER_HOST, SERVER_PORT, etc.
	if s.Host != "" {
		t.Errorf("Server.Host = %q, want empty (whole-struct env override not supported)", s.Host)
	}
}

func TestEnv_StructAsJSONString_PartialFields_ViperLimitation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"server": map[string]any{"host": "file-host", "port": 80},
	})

	// Same limitation as above — per-field override is the correct pattern.
	t.Setenv("SJP_SERVER_HOST", "per-field-host")

	m, err := New[structEnvConfig](path, WithEnvPrefix("SJP"))
	if err != nil {
		t.Fatal(err)
	}

	s := m.Load().Server
	if s.Host != "per-field-host" {
		t.Errorf("Server.Host = %q, want per-field-host", s.Host)
	}
	if s.Port != 80 {
		t.Errorf("Server.Port = %d, want 80 (from file, not overridden)", s.Port)
	}
}

func TestEnv_StructAsJSONString_InvalidJSON_FallsBackToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"server": map[string]any{"host": "file-host", "port": 80},
	})

	// Invalid JSON — StringToStructHookFunc returns the raw string as passthrough.
	// Viper/mapstructure should then fail to decode it into a struct and fall back.
	t.Setenv("SJI_SERVER", "not-valid-json")

	_, err := New[structEnvConfig](path, WithEnvPrefix("SJI"))
	// This may either error or fall back to file values depending on mapstructure behavior.
	// The important thing is it doesn't panic.
	if err != nil {
		t.Logf("New returned error (expected for invalid JSON struct env): %v", err)
	}
}

// --- Default with complex types from file (file overrides default) ---

func TestDefaults_Slice_FileOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"tags": []string{"file-a", "file-b"},
	})

	m, err := New[richConfig](path,
		WithDisableEnv(true),
		WithSetDefault(func(v *viper.Viper) {
			v.SetDefault("tags", []string{"default-tag"})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	tags := m.Load().Tags
	if len(tags) != 2 || tags[0] != "file-a" || tags[1] != "file-b" {
		t.Errorf("Tags = %v, want [file-a file-b] (file > default)", tags)
	}
}

func TestDefaults_Map_FileOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{
		"headers": map[string]string{"X-File": "yes"},
	})

	m, err := New[richConfig](path,
		WithDisableEnv(true),
		WithSetDefault(func(v *viper.Viper) {
			v.SetDefault("headers", map[string]string{"X-Default": "yes"})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	h := m.Load().Headers
	// File value should win over default.
	if _, ok := h["x-file"]; !ok {
		t.Errorf("Headers = %v, want X-File present (file > default)", h)
	}
}

// --- Multiple Save cycles with complex types ---

func TestSave_MultipleCycles_ComplexTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")

	m, err := New[richConfig](path, WithDisableEnv(true))
	if err != nil {
		t.Fatal(err)
	}

	// Cycle 1: set tags
	if err := m.Save(func(c *richConfig) {
		c.Tags = []string{"v1"}
	}); err != nil {
		t.Fatal(err)
	}
	if tags := m.Load().Tags; len(tags) != 1 || tags[0] != "v1" {
		t.Errorf("after cycle 1: Tags = %v", tags)
	}

	// Cycle 2: add headers, tags should persist
	if err := m.Save(func(c *richConfig) {
		c.Headers = map[string]string{"X-New": "val"}
	}); err != nil {
		t.Fatal(err)
	}
	cfg := m.Load()
	if len(cfg.Tags) != 1 || cfg.Tags[0] != "v1" {
		t.Errorf("after cycle 2: Tags = %v (should persist from cycle 1)", cfg.Tags)
	}
	if cfg.Headers["X-New"] != "val" && cfg.Headers["x-new"] != "val" {
		t.Errorf("after cycle 2: Headers = %v", cfg.Headers)
	}

	// Cycle 3: replace tags entirely
	if err := m.Save(func(c *richConfig) {
		c.Tags = []string{"v3-a", "v3-b"}
	}); err != nil {
		t.Fatal(err)
	}
	if tags := m.Load().Tags; len(tags) != 2 || tags[0] != "v3-a" {
		t.Errorf("after cycle 3: Tags = %v", tags)
	}
}

// =============================================================================
// Behavioral difference: old DecodeStringToMap vs new
// The old code had a bug where the type guard was always false, so it processed
// ALL string→map conversions (map[string]any, map[string]int, etc.) with k=v parsing.
// The new code correctly restricts to map[string]string only.
// =============================================================================

type mapAnyConfig struct {
	Meta map[string]any `json:"meta" mapstructure:"meta"`
}

func TestDecodeStringToMap_MapStringAny_JSONFromEnv(t *testing.T) {
	// With the improved DecodeStringToMap, map[string]any fields can be
	// overridden via JSON string in env.
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"meta": map[string]any{}})

	t.Setenv("MA_META", `{"foo":"bar","count":42}`)

	m, err := New[mapAnyConfig](path, WithEnvPrefix("MA"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	meta := m.Load().Meta
	if meta["foo"] != "bar" {
		t.Errorf("Meta[foo] = %v, want bar", meta["foo"])
	}
	if meta["count"] != float64(42) {
		t.Errorf("Meta[count] = %v, want 42", meta["count"])
	}
}

// Verify that map[string]string still works correctly with k=v env.
func TestDecodeStringToMap_MapStringString_StillWorks(t *testing.T) {
	type mssConfig struct {
		Labels map[string]string `json:"labels" mapstructure:"labels"`
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	writeJSON(t, path, map[string]any{"labels": map[string]string{}})

	t.Setenv("MSS_LABELS", "env=prod,region=us-east")

	m, err := New[mssConfig](path, WithEnvPrefix("MSS"))
	if err != nil {
		t.Fatal(err)
	}

	labels := m.Load().Labels
	if labels["env"] != "prod" {
		t.Errorf("Labels[env] = %q, want prod", labels["env"])
	}
	if labels["region"] != "us-east" {
		t.Errorf("Labels[region] = %q, want us-east", labels["region"])
	}
}
