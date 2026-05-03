package store_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/allank/psst/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"), 2*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGetMiss(t *testing.T) {
	s := openTestStore(t)
	e, err := s.Get("sha256:missing")
	require.NoError(t, err)
	assert.Nil(t, e)
}

func TestPutAndGet(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	e := &store.Entry{
		Tool:      "jira_get_issue",
		Args:      json.RawMessage(`{"issue_key":"PROJ-1"}`),
		Result:    json.RawMessage(`{"summary":"fix bug"}`),
		CachedAt:  now,
		ExpiresAt: now.Add(time.Hour),
		Key:       "sha256:abc123",
	}
	require.NoError(t, s.Put(e))

	got, err := s.Get("sha256:abc123")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "jira_get_issue", got.Tool)
	assert.Equal(t, "sha256:abc123", got.Key)
}

func TestDelete(t *testing.T) {
	s := openTestStore(t)
	e := &store.Entry{Key: "sha256:del", Tool: "t", Args: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), CachedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, s.Put(e))
	require.NoError(t, s.Delete("sha256:del"))
	got, err := s.Get("sha256:del")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestDeleteTool(t *testing.T) {
	s := openTestStore(t)
	for _, key := range []string{"sha256:a", "sha256:b"} {
		e := &store.Entry{Key: key, Tool: "tool_a", Args: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), CachedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
		require.NoError(t, s.Put(e))
	}
	other := &store.Entry{Key: "sha256:c", Tool: "tool_b", Args: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), CachedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, s.Put(other))

	n, err := s.DeleteTool("tool_a")
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	got, _ := s.Get("sha256:c")
	assert.NotNil(t, got)
}

func TestDeleteAll(t *testing.T) {
	s := openTestStore(t)
	for _, key := range []string{"sha256:x", "sha256:y", "sha256:z"} {
		e := &store.Entry{Key: key, Tool: "t", Args: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), CachedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
		require.NoError(t, s.Put(e))
	}
	n, err := s.DeleteAll()
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	got, _ := s.Get("sha256:x")
	assert.Nil(t, got)
}

func TestScan(t *testing.T) {
	s := openTestStore(t)
	for _, key := range []string{"sha256:1", "sha256:2"} {
		e := &store.Entry{Key: key, Tool: "t", Args: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), CachedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
		require.NoError(t, s.Put(e))
	}
	var count int
	require.NoError(t, s.Scan(func(e *store.Entry) error { count++; return nil }))
	assert.Equal(t, 2, count)
}

func TestVectorPutGetDelete(t *testing.T) {
	s := openTestStore(t)
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = float32(i) / 384
	}
	require.NoError(t, s.PutVector("sha256:v1", vec))

	got, err := s.GetVector("sha256:v1")
	require.NoError(t, err)
	require.Equal(t, 384, len(got))
	assert.InDelta(t, vec[0], got[0], 1e-6)
	assert.InDelta(t, vec[383], got[383], 1e-6)

	require.NoError(t, s.DeleteVector("sha256:v1"))
	got2, err := s.GetVector("sha256:v1")
	require.NoError(t, err)
	assert.Nil(t, got2)
}

func TestPath(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"), time.Second)
	require.NoError(t, err)
	defer s.Close()
	assert.Contains(t, s.Path(), "test.db")
}

func TestScanVectors(t *testing.T) {
	s := openTestStore(t)

	vec1 := make([]float32, 384)
	vec2 := make([]float32, 384)
	for i := range vec1 {
		vec1[i] = float32(i) / 384
		vec2[i] = float32(384-i) / 384
	}
	otherVec := make([]float32, 384)

	entries := []*store.Entry{
		{Key: "sha256:sv1", Tool: "tool_x", Args: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), CachedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		{Key: "sha256:sv2", Tool: "tool_x", Args: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), CachedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		{Key: "sha256:sv3", Tool: "tool_y", Args: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), CachedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
	}
	require.NoError(t, s.Put(entries[0]))
	require.NoError(t, s.PutVector("sha256:sv1", vec1))
	require.NoError(t, s.Put(entries[1]))
	require.NoError(t, s.PutVector("sha256:sv2", vec2))
	require.NoError(t, s.Put(entries[2]))
	require.NoError(t, s.PutVector("sha256:sv3", otherVec))

	seen := map[string][]float32{}
	require.NoError(t, s.ScanVectors("tool_x", func(key string, vec []float32) error {
		seen[key] = vec
		return nil
	}))

	assert.Len(t, seen, 2)
	assert.Contains(t, seen, "sha256:sv1")
	assert.Contains(t, seen, "sha256:sv2")
	assert.NotContains(t, seen, "sha256:sv3")
	assert.InDelta(t, vec1[0], seen["sha256:sv1"][0], 1e-6)
	assert.InDelta(t, vec2[0], seen["sha256:sv2"][0], 1e-6)
}
