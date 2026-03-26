package confm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Options defines optional settings for the config manager.
type Options struct {
	EnvPrefix  string
	EnvFiles   []string
	SetDefault func(*viper.Viper)
	DisableEnv bool // explicitly disable ENV overlay
}

type Option func(*Options)

func WithEnvPrefix(prefix string) Option {
	return func(o *Options) { o.EnvPrefix = prefix }
}

func WithEnvFiles(files ...string) Option {
	return func(o *Options) { o.EnvFiles = files }
}

func WithSetDefault(fn func(*viper.Viper)) Option {
	return func(o *Options) { o.SetDefault = fn }
}

func WithDisableEnv(disable bool) Option {
	return func(o *Options) { o.DisableEnv = disable }
}

// Manager is a generic configuration manager backed by a JSON file.
type Manager[T any] struct {
	path   string
	opts   Options
	base   *T
	active *T
	mu     sync.RWMutex
}

// New creates and initializes a config manager for the given JSON file path.
func New[T any](path string, opts ...Option) (*Manager[T], error) {
	options := Options{}
	for _, o := range opts {
		o(&options)
	}

	if len(options.EnvFiles) > 0 {
		_ = godotenv.Load(options.EnvFiles...)
	}

	m := &Manager[T]{
		path: path,
		opts: options,
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create config dir failed: %w", err)
	}

	if err := m.loadBase(); err != nil {
		return nil, fmt.Errorf("load base config failed: %w", err)
	}

	if err := m.reloadActive(); err != nil {
		return nil, fmt.Errorf("load active config failed: %w", err)
	}

	return m, nil
}

// prepareViper initializes a Viper instance with struct key placeholders and defaults.
func (m *Manager[T]) prepareViper() *viper.Viper {
	v := viper.New()
	v.SetConfigFile(m.path)

	// Register all struct keys so AutomaticEnv can match nested keys.
	typ := reflect.TypeOf(new(T)).Elem()
	keys := GetStructKeys(typ, "mapstructure", "squash")
	for _, key := range keys {
		v.SetDefault(key, nil)
	}

	// Apply caller-provided defaults (lowest priority).
	if m.opts.SetDefault != nil {
		m.opts.SetDefault(v)
	}

	return v
}

func (m *Manager[T]) loadBase() error {
	v := m.prepareViper()

	if err := v.ReadInConfig(); err != nil {
		var pathErr *os.PathError
		if os.IsNotExist(err) || errors.As(err, &viper.ConfigFileNotFoundError{}) || errors.As(err, &pathErr) {
			// First run: marshal zero-value T (with defaults applied) instead of Viper's WriteConfigAs
			// to avoid nil placeholders in the JSON file.
			base := new(T)
			if uerr := v.Unmarshal(base, decoderConfig()); uerr != nil {
				return uerr
			}
			data, jerr := json.MarshalIndent(base, "", "  ")
			if jerr != nil {
				return jerr
			}
			if werr := os.WriteFile(m.path, data, 0644); werr != nil {
				return werr
			}
		} else {
			return err
		}
	}

	m.base = new(T)
	return v.Unmarshal(m.base, decoderConfig())
}

func (m *Manager[T]) reloadActive() error {
	v := m.prepareViper()

	if err := v.ReadInConfig(); err != nil {
		return err
	}

	// Apply ENV overlay unless explicitly disabled.
	if !m.opts.DisableEnv {
		if m.opts.EnvPrefix != "" {
			v.SetEnvPrefix(m.opts.EnvPrefix)
		}
		v.AutomaticEnv()
		v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	}

	m.active = new(T)
	return v.Unmarshal(m.active, decoderConfig())
}

// Load returns the active (ENV-overlaid) config snapshot.
func (m *Manager[T]) Load() *T {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// Save applies fn to a clone of the base config, writes atomically, then reloads active.
// If the file write fails, the in-memory base is not modified.
func (m *Manager[T]) Save(fn func(c *T)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clone base so fn doesn't mutate the live copy on write failure.
	clone, err := cloneJSON(m.base)
	if err != nil {
		return fmt.Errorf("clone base config: %w", err)
	}

	fn(clone)

	data, err := json.MarshalIndent(clone, "", "  ")
	if err != nil {
		return err
	}

	if err := atomicWrite(m.path, data, 0644); err != nil {
		return err
	}

	m.base = clone
	return m.reloadActive()
}

// cloneJSON deep-copies a value via JSON round-trip.
func cloneJSON[T any](src *T) (*T, error) {
	data, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	dst := new(T)
	if err := json.Unmarshal(data, dst); err != nil {
		return nil, err
	}
	return dst, nil
}

// atomicWrite writes data to a temp file in the same directory, then renames.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".confm-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func decoderConfig() viper.DecoderConfigOption {
	return viper.DecodeHook(CompositeDecodeHook())
}
