package cache_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/allank/psst/internal/cache"
	"github.com/allank/psst/internal/config"
	"github.com/allank/psst/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(text string) ([]float32, error) {
	vec := make([]float32, 384)
	h := 0
	for _, c := range text {
		h = (h*31 + int(c)) % 384
	}
	vec[h] = 1.0
	return vec, nil
}
func (f *fakeEmbedder) Close() error { return nil }

func openTestCache(t *testing.T, emb cache.Embedder) (*store.Store, *cache.Cache) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "psst.db")
	s, err := store.Open(dbPath, 2*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	cfg := &config.Config{
		Cache:    config.CacheConfig{DefaultTTLSeconds: 3600},
		Semantic: config.SemanticConfig{Enabled: true, Threshold: 0.82},
	}
	c := cache.New(s, cfg, emb, dbPath)
	return s, c
}

func TestExactHit(t *testing.T) {
	_, c := openTestCache(t, nil)
	args := json.RawMessage(`{"issue_key":"PROJ-1"}`)
	result := json.RawMessage(`{"summary":"fix bug"}`)

	_, err := c.Store(context.Background(), "jira_get_issue", args, result, 0)
	require.NoError(t, err)

	r, err := c.Lookup(context.Background(), "jira_get_issue", args, 0.82)
	require.NoError(t, err)
	assert.True(t, r.Hit)
	assert.Equal(t, "exact", r.Match)
}

func TestExactMiss(t *testing.T) {
	_, c := openTestCache(t, nil)
	r, err := c.Lookup(context.Background(), "jira_get_issue", json.RawMessage(`{"issue_key":"PROJ-999"}`), 0.82)
	require.NoError(t, err)
	assert.False(t, r.Hit)
}

func TestExpiredExactEntryIsMiss(t *testing.T) {
	_, c := openTestCache(t, nil)
	args := json.RawMessage(`{"issue_key":"PROJ-2"}`)

	_, err := c.Store(context.Background(), "jira_get_issue", args, json.RawMessage(`{}`), 1) // TTL=1s
	require.NoError(t, err)

	time.Sleep(1100 * time.Millisecond)

	r, err := c.Lookup(context.Background(), "jira_get_issue", args, 0.82)
	require.NoError(t, err)
	assert.False(t, r.Hit)
}

func TestSemanticHit(t *testing.T) {
	_, c := openTestCache(t, &fakeEmbedder{})
	_, err := c.Store(context.Background(), "confluence_search",
		json.RawMessage(`{"q":"mobile auth flow"}`),
		json.RawMessage(`{"results":[]}`), 0)
	require.NoError(t, err)

	r, err := c.Lookup(context.Background(), "confluence_search",
		json.RawMessage(`{"q":"mobile auth flow"}`), 0.82)
	require.NoError(t, err)
	assert.True(t, r.Hit)
}

func TestSemanticBelowThresholdIsMiss(t *testing.T) {
	_, c := openTestCache(t, &fakeEmbedder{})
	_, err := c.Store(context.Background(), "confluence_search",
		json.RawMessage(`{"q":"aaa"}`),
		json.RawMessage(`{}`), 0)
	require.NoError(t, err)

	r, err := c.Lookup(context.Background(), "confluence_search",
		json.RawMessage(`{"q":"zzz"}`), 0.82)
	require.NoError(t, err)
	assert.False(t, r.Hit)
}

func TestInvalidateTool(t *testing.T) {
	_, c := openTestCache(t, nil)
	for _, k := range []string{`{"issue_key":"A"}`, `{"issue_key":"B"}`} {
		_, err := c.Store(context.Background(), "jira_get_issue", json.RawMessage(k), json.RawMessage(`{}`), 0)
		require.NoError(t, err)
	}
	n, err := c.Invalidate("jira_get_issue", "")
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	r, _ := c.Lookup(context.Background(), "jira_get_issue", json.RawMessage(`{"issue_key":"A"}`), 0.82)
	assert.False(t, r.Hit)
}

func TestInvalidateAll(t *testing.T) {
	_, c := openTestCache(t, nil)
	_, err := c.Store(context.Background(), "tool_a", json.RawMessage(`{"k":"v"}`), json.RawMessage(`{}`), 0)
	require.NoError(t, err)
	n, err := c.Invalidate("*", "")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestEvict(t *testing.T) {
	_, c := openTestCache(t, nil)
	_, err := c.Store(context.Background(), "t", json.RawMessage(`{"k":"expired"}`), json.RawMessage(`{}`), 1)
	require.NoError(t, err)
	_, err = c.Store(context.Background(), "t", json.RawMessage(`{"k":"live"}`), json.RawMessage(`{}`), 3600)
	require.NoError(t, err)

	time.Sleep(1100 * time.Millisecond)

	n, err := c.Evict()
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}
