package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allank/psst/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.toml")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:7425", cfg.Server.Listen)
	assert.Equal(t, 0.82, cfg.Semantic.Threshold)
	assert.True(t, cfg.Semantic.Enabled)
	assert.Equal(t, 21600, cfg.Cache.DefaultTTLSeconds)
	assert.Equal(t, 10, cfg.Eviction.SweepIntervalMinutes)
}

func TestLoadFromFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.toml")
	require.NoError(t, err)
	_, err = f.WriteString(`
[semantic]
threshold = 0.9
enabled = false
`)
	require.NoError(t, err)
	f.Close()

	cfg, err := config.Load(f.Name())
	require.NoError(t, err)
	assert.Equal(t, 0.9, cfg.Semantic.Threshold)
	assert.False(t, cfg.Semantic.Enabled)
	// Defaults still apply for unset fields
	assert.Equal(t, "127.0.0.1:7425", cfg.Server.Listen)
}

func TestResolveTTLExplicit(t *testing.T) {
	cfg, _ := config.Load("")
	d := config.ResolveTTL("any_tool", 300, cfg)
	assert.Equal(t, 300*time.Second, d)
}

func TestResolveTTLGlobMatch(t *testing.T) {
	cfg, _ := config.Load("")
	// *_get_issue → 86400s
	d := config.ResolveTTL("jira_get_issue", 0, cfg)
	assert.Equal(t, 86400*time.Second, d)
	// *_search → 14400s
	d = config.ResolveTTL("confluence_search", 0, cfg)
	assert.Equal(t, 14400*time.Second, d)
	// *_get_sprint → 3600s
	d = config.ResolveTTL("jira_get_sprint", 0, cfg)
	assert.Equal(t, 3600*time.Second, d)
}

func TestResolveTTLDefault(t *testing.T) {
	cfg, _ := config.Load("")
	d := config.ResolveTTL("unknown_tool_xyz", 0, cfg)
	assert.Equal(t, 21600*time.Second, d)
}

func TestResolveTTLFirstMatchWins(t *testing.T) {
	cfg := &config.Config{
		Cache: config.CacheConfig{
			DefaultTTLSeconds: 100,
			TTL: []config.TTLRule{
				{Tool: "*_get_issue", Seconds: 500},
				{Tool: "jira_get_issue", Seconds: 999},
			},
		},
	}
	d := config.ResolveTTL("jira_get_issue", 0, cfg)
	assert.Equal(t, 500*time.Second, d) // first match wins
}

func TestDefaultPath(t *testing.T) {
	p := config.DefaultPath()
	assert.True(t, filepath.IsAbs(p))
	assert.Contains(t, p, "psst")
}

func TestResolveTTLNilConfig(t *testing.T) {
	d := config.ResolveTTL("any_tool", 0, nil)
	assert.Equal(t, 21600*time.Second, d)
}

func TestLoadStorePathTildeExpanded(t *testing.T) {
	cfg, err := config.Load("")
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(cfg.Server.Store, "~"), "Store path should not contain tilde")
	assert.True(t, filepath.IsAbs(cfg.Server.Store))
}

func TestLoadUserTTLRulesDoNotReplaceDefaults(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.toml")
	require.NoError(t, err)
	_, err = f.WriteString("[[cache.ttl]]\ntool = \"my_custom_tool\"\nseconds = 999\n")
	require.NoError(t, err)
	f.Close()

	cfg, err := config.Load(f.Name())
	require.NoError(t, err)
	// User rule should be present
	d := config.ResolveTTL("my_custom_tool", 0, cfg)
	assert.Equal(t, 999*time.Second, d)
	// Default rules should still be present as fallback
	d = config.ResolveTTL("jira_get_issue", 0, cfg)
	assert.Equal(t, 86400*time.Second, d)
}
