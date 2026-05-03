package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type ServerConfig struct {
	Listen string `toml:"listen"`
	Store  string `toml:"store"`
}

type TTLRule struct {
	Tool    string `toml:"tool"`
	Seconds int    `toml:"seconds"`
}

type CacheConfig struct {
	DefaultTTLSeconds int       `toml:"default_ttl_seconds"`
	TTL               []TTLRule `toml:"ttl"`
}

type SemanticToolConfig struct {
	Tool    string   `toml:"tool"`
	ArgKeys []string `toml:"arg_keys"`
}

type SemanticConfig struct {
	Threshold float64              `toml:"threshold"`
	Enabled   bool                 `toml:"enabled"`
	Tools     []SemanticToolConfig `toml:"tools"`
}

type EvictionConfig struct {
	SweepIntervalMinutes int `toml:"sweep_interval_minutes"`
}

type Config struct {
	Server   ServerConfig   `toml:"server"`
	Cache    CacheConfig    `toml:"cache"`
	Semantic SemanticConfig `toml:"semantic"`
	Eviction EvictionConfig `toml:"eviction"`
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Listen: "127.0.0.1:7425",
			Store:  "~/.config/psst/psst.db",
		},
		Cache: CacheConfig{
			DefaultTTLSeconds: 21600,
			TTL: []TTLRule{
				{Tool: "*_get_issue", Seconds: 86400},
				{Tool: "*_get_ticket", Seconds: 86400},
				{Tool: "*_search", Seconds: 14400},
				{Tool: "*_query", Seconds: 14400},
				{Tool: "*_get_page", Seconds: 43200},
				{Tool: "*_get_document", Seconds: 43200},
				{Tool: "*_get_sprint", Seconds: 3600},
				{Tool: "*_get_board", Seconds: 3600},
			},
		},
		Semantic: SemanticConfig{
			Threshold: 0.82,
			Enabled:   true,
		},
		Eviction: EvictionConfig{
			SweepIntervalMinutes: 10,
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := defaults()
	if path == "" {
		return cfg, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "psst", "config.toml")
}

func ResolveTTL(tool string, explicitTTL int, cfg *Config) time.Duration {
	if explicitTTL > 0 {
		return time.Duration(explicitTTL) * time.Second
	}
	for _, rule := range cfg.Cache.TTL {
		if matched, _ := filepath.Match(rule.Tool, tool); matched {
			return time.Duration(rule.Seconds) * time.Second
		}
	}
	return time.Duration(cfg.Cache.DefaultTTLSeconds) * time.Second
}
