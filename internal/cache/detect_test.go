package cache_test

import (
	"encoding/json"
	"testing"

	"github.com/allank/psst/internal/cache"
	"github.com/allank/psst/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalKeyStable(t *testing.T) {
	// Different arg orderings produce the same key
	k1, err := cache.CanonicalKey("tool", json.RawMessage(`{"b":"2","a":"1"}`))
	require.NoError(t, err)
	k2, err := cache.CanonicalKey("tool", json.RawMessage(`{"a":"1","b":"2"}`))
	require.NoError(t, err)
	assert.Equal(t, k1, k2)
}

func TestCanonicalKeyDifferentTools(t *testing.T) {
	k1, _ := cache.CanonicalKey("tool_a", json.RawMessage(`{"k":"v"}`))
	k2, _ := cache.CanonicalKey("tool_b", json.RawMessage(`{"k":"v"}`))
	assert.NotEqual(t, k1, k2)
}

func TestCanonicalKeyPrefix(t *testing.T) {
	k, _ := cache.CanonicalKey("tool", json.RawMessage(`{}`))
	assert.Contains(t, k, "sha256:")
}

func TestDetectQueryStringWellKnownKeys(t *testing.T) {
	cfg := &config.Config{Semantic: config.SemanticConfig{Enabled: true}}
	for _, key := range []string{"q", "query", "text", "search", "summary", "keywords"} {
		args, _ := json.Marshal(map[string]string{key: "find something"})
		q, ok := cache.DetectQueryString("any_tool", args, cfg)
		assert.True(t, ok, "key=%s", key)
		assert.Equal(t, "find something", q)
	}
}

func TestDetectQueryStringConfiguredTool(t *testing.T) {
	cfg := &config.Config{
		Semantic: config.SemanticConfig{
			Enabled: true,
			Tools: []config.SemanticToolConfig{
				{Tool: "jira_search", ArgKeys: []string{"jql"}},
			},
		},
	}
	args := json.RawMessage(`{"jql":"project = PROJ"}`)
	q, ok := cache.DetectQueryString("jira_search", args, cfg)
	assert.True(t, ok)
	assert.Equal(t, "project = PROJ", q)
}

func TestDetectQueryStringNoMatch(t *testing.T) {
	cfg := &config.Config{Semantic: config.SemanticConfig{Enabled: true}}
	args := json.RawMessage(`{"issue_key":"PROJ-123"}`)
	_, ok := cache.DetectQueryString("jira_get_issue", args, cfg)
	assert.False(t, ok)
}

func TestDetectQueryStringDisabled(t *testing.T) {
	cfg := &config.Config{Semantic: config.SemanticConfig{Enabled: false}}
	args := json.RawMessage(`{"q":"something"}`)
	_, ok := cache.DetectQueryString("tool", args, cfg)
	assert.False(t, ok)
}
