package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sjzar/reed/pkg/confm"
	"github.com/spf13/viper"
)

const (
	AppName   = "reed"
	EnvPrefix = "REED"
)

// Config holds all runtime configuration for a reed process.
type Config struct {
	Home    string               `json:"-" mapstructure:"-"` // runtime only, not persisted
	Debug   bool                 `json:"debug" mapstructure:"debug"`
	HTTP    HTTPConfig           `json:"http" mapstructure:"http"`
	Models  ModelsConfig         `json:"models" mapstructure:"models"`
	Secrets []SecretSourceConfig `json:"secrets" mapstructure:"secrets"`
	Media   MediaConfig          `json:"media" mapstructure:"media"`
}

// ModelsConfig defines model providers and default model reference.
type ModelsConfig struct {
	Default   string           `json:"default" mapstructure:"default"`
	Providers []ProviderConfig `json:"providers" mapstructure:"providers"`
}

// ProviderConfig defines a single LLM provider endpoint.
type ProviderConfig struct {
	ID          string            `json:"id" mapstructure:"id"`
	Type        string            `json:"type" mapstructure:"type"`         // e.g. "openai-completions", "anthropic-messages"
	BaseURL     string            `json:"base_url" mapstructure:"base_url"` // custom API endpoint
	Key         string            `json:"key" mapstructure:"key"`           // API key (or from env)
	Disabled    bool              `json:"disabled" mapstructure:"disabled"`
	Headers     map[string]string `json:"headers" mapstructure:"headers"`
	Models      []ModelConfig     `json:"models" mapstructure:"models"`
	EnvFallback bool              `json:"-" mapstructure:"-"` // internal: auto-registered from env vars
}

// ModelConfig defines a model available through a provider.
type ModelConfig struct {
	ID            string `json:"id" mapstructure:"id"`                         // logical model ID used in workflow
	Name          string `json:"name" mapstructure:"name"`                     // display name
	ForwardID     string `json:"forward_id" mapstructure:"forward_id"`         // actual model ID sent to API
	Thinking      *bool  `json:"thinking,omitempty" mapstructure:"thinking"`   // supports extended thinking
	Vision        *bool  `json:"vision,omitempty" mapstructure:"vision"`       // supports image input
	ContextWindow int    `json:"context_window" mapstructure:"context_window"` // max context tokens
	MaxTokens     int    `json:"max_tokens" mapstructure:"max_tokens"`         // default max output tokens
	Streaming     *bool  `json:"streaming,omitempty" mapstructure:"streaming"` // supports streaming
}

// HTTPConfig holds HTTP server configuration.
type HTTPConfig struct {
	Addr string `json:"addr" mapstructure:"addr"`
}

// SecretSourceConfig defines a single secrets data source.
type SecretSourceConfig struct {
	Type string `json:"type" mapstructure:"type"` // e.g. "file"
	Path string `json:"path" mapstructure:"path"`
}

// MediaConfig holds media storage configuration.
type MediaConfig struct {
	TTL string `json:"ttl" mapstructure:"ttl"` // e.g. "7d", "24h"; default "7d"
}

// ParseTTL returns the configured media TTL or the default (7 days).
func (c *MediaConfig) ParseTTL() time.Duration {
	if c.TTL == "" {
		return 7 * 24 * time.Hour
	}
	// Try standard Go duration first (e.g. "168h")
	if d, err := time.ParseDuration(c.TTL); err == nil {
		return d
	}
	// Try "Nd" format (e.g. "7d")
	s := strings.TrimSpace(c.TTL)
	if strings.HasSuffix(s, "d") {
		if days, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	return 7 * 24 * time.Hour
}

// --- Path methods (based on Config.Home) ---

// DBDir returns the path to the database directory, creating it if needed.
func (c *Config) DBDir() string { return ensureDir(c.Home, "db") }

// LogDir returns the path to the logs directory, creating it if needed.
func (c *Config) LogDir() string { return ensureDir(c.Home, "logs") }

// SockDir returns the path to the sockets directory, creating it if needed.
func (c *Config) SockDir() string { return ensureDir(c.Home, "socks") }

// SessionDir returns the path to the sessions directory, creating it if needed.
func (c *Config) SessionDir() string { return ensureDir(c.Home, "sessions") }

// SkillDir returns the path to the skills directory, creating it if needed.
// Deprecated: Use Config.Home directly for manifest-based skill management.
func (c *Config) SkillDir() string { return ensureDir(c.Home, "skills") }

// MemoryDir returns the path to the memory directory, creating it if needed.
func (c *Config) MemoryDir() string { return ensureDir(c.Home, "memory") }

// SkillModDir returns the path to the skill modules directory, creating it if needed.
func (c *Config) SkillModDir() string { return ensureDir(c.Home, "mod", "skills") }

// MediaDir returns the path to the media cache directory, creating it if needed.
func (c *Config) MediaDir() string { return ensureDir(c.Home, "cache", "media") }

// SockPath returns the Unix socket path for a given processID.
func (c *Config) SockPath(processID string) string {
	return filepath.Join(c.SockDir(), processID+".sock")
}

// EventLogPath returns the event log path for a given processID.
func (c *Config) EventLogPath(processID string) string {
	return filepath.Join(c.LogDir(), processID+".events.jsonl")
}

// ProcessLogPath returns the application log path for a given processID.
func (c *Config) ProcessLogPath(processID string) string {
	return filepath.Join(c.LogDir(), processID+".log")
}

func ensureDir(elem ...string) string {
	dir := filepath.Join(elem...)
	os.MkdirAll(dir, 0o755)
	return dir
}

func SetDefaults(v *viper.Viper) {
	v.SetDefault("debug", false)
}

// --- Singleton lifecycle ---

var (
	manager *confm.Manager[Config]
	once    sync.Once
	loadErr error
)

// Load initializes the global config manager and returns the active config.
func Load() (*Config, error) {
	once.Do(func() {
		home := getHomeDir()
		configPath := filepath.Join(home, AppName+".json")
		envPath := filepath.Join(home, ".env")

		manager, loadErr = confm.New[Config](
			configPath,
			confm.WithEnvPrefix(EnvPrefix),
			confm.WithEnvFiles(envPath),
			confm.WithSetDefault(SetDefaults),
		)
		if loadErr != nil {
			return
		}
		// Inject runtime-only Home into the active config.
		cfg := manager.Load()
		cfg.Home = home
	})

	if loadErr != nil {
		return nil, loadErr
	}
	return manager.Load(), nil
}

// Save modifies the base config and persists to the local JSON file.
func Save(fn func(c *Config)) error {
	if manager == nil {
		return fmt.Errorf("config manager not initialized, call Load first")
	}
	return manager.Save(fn)
}

func getHomeDir() string {
	envKey := fmt.Sprintf("%s_HOME", EnvPrefix)
	if dir := os.Getenv(envKey); dir != "" {
		return dir
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "."+AppName)
	}
	return filepath.Join(home, "."+AppName)
}
