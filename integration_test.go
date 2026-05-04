//go:build integration

package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allank/psst/internal/cache"
	"github.com/allank/psst/internal/config"
	"github.com/allank/psst/internal/embedder"
	"github.com/allank/psst/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests require:
//   PSST_MODEL_PATH  — path to all-MiniLM-L6-v2 model.onnx
//   PSST_TOKENIZER_PATH — path to tokenizer.json
//   The psst binary must be built at ./psst

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("skipping integration test: %s not set", key)
	}
	return v
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "psst")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())
	return bin
}

func TestCLIExactMatchRoundtrip(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "psst.db")

	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, append(args, "--store", dbPath)...)
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	// Store
	out, code := run("store", "--tool", "jira_get_issue",
		"--args", `{"issue_key":"PROJ-1"}`,
		"--result", `{"summary":"Fix bug"}`)
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "stored")

	// Exact hit → exit 0
	out, code = run("lookup", "--tool", "jira_get_issue", "--args", `{"issue_key":"PROJ-1"}`)
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "hit match=exact")
	assert.Contains(t, out, "Fix bug")

	// Miss → exit 1
	_, code = run("lookup", "--tool", "jira_get_issue", "--args", `{"issue_key":"PROJ-999"}`)
	assert.Equal(t, 1, code)

	// Invalidate
	out, code = run("invalidate", "--tool", "jira_get_issue")
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "count=1")

	// Post-invalidate miss → exit 1
	_, code = run("lookup", "--tool", "jira_get_issue", "--args", `{"issue_key":"PROJ-1"}`)
	assert.Equal(t, 1, code)

	// Status
	out, code = run("status")
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, dbPath)

	// Clean
	out, code = run("clean")
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "removed")
	_, err := os.Stat(dbPath)
	assert.True(t, os.IsNotExist(err))
}

func TestCLISemanticMatch(t *testing.T) {
	modelPath := requireEnv(t, "PSST_MODEL_PATH")
	tokPath := requireEnv(t, "PSST_TOKENIZER_PATH")

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "psst.db")
	cfg := &config.Config{
		Cache:    config.CacheConfig{DefaultTTLSeconds: 3600},
		Semantic: config.SemanticConfig{Enabled: true, Threshold: 0.75},
	}

	s, err := store.Open(dbPath, 2*time.Second)
	require.NoError(t, err)
	defer s.Close()

	emb, err := embedder.NewONNX(modelPath, tokPath)
	require.NoError(t, err)
	defer emb.Close()

	c := cache.New(s, cfg, emb, dbPath)

	// Store "mobile OAuth login docs"
	_, err = c.Store(context.Background(), "confluence_search",
		json.RawMessage(`{"q":"mobile OAuth login docs"}`),
		json.RawMessage(`{"results":[{"title":"OAuth Mobile Guide"}]}`), 0)
	require.NoError(t, err)

	// Semantic lookup with rephrased query
	r, err := c.Lookup(context.Background(), "confluence_search",
		json.RawMessage(`{"q":"authentication flow for mobile"}`), 0.75)
	require.NoError(t, err)
	assert.True(t, r.Hit, "expected semantic hit")
	assert.Equal(t, "semantic", r.Match)
	assert.Greater(t, r.Score, float32(0.75))
}

func TestDaemonLifecycle(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "psst.db")
	listenAddr := "127.0.0.1:17425"

	// Start daemon
	daemon := exec.Command(bin, "serve", "--listen", listenAddr, "--store", dbPath)
	daemon.Stderr = os.Stderr
	require.NoError(t, daemon.Start())
	t.Cleanup(func() { daemon.Process.Kill() })

	// Poll /health
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + listenAddr + "/health")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// psst_store
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"psst_store","arguments":{"tool":"jira_get_issue","args":{"issue_key":"D-1"},"result":{"summary":"daemon test"}}}}`
	resp, err := http.Post("http://"+listenAddr+"/mcp", "application/json",
		jsonReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	// psst_lookup exact hit
	body = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"psst_lookup","arguments":{"tool":"jira_get_issue","args":{"issue_key":"D-1"}}}}`
	resp, err = http.Post("http://"+listenAddr+"/mcp", "application/json", jsonReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	content := result["result"].(map[string]any)["content"].([]any)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &out))
	assert.Equal(t, true, out["hit"])

	// SIGTERM → clean exit
	require.NoError(t, daemon.Process.Signal(os.Interrupt))
	done := make(chan error, 1)
	go func() { done <- daemon.Wait() }()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit within 5s after SIGTERM")
	}

	// psst.idx sidecar written on shutdown
	_, err = os.Stat(filepath.Join(dir, "psst.idx"))
	assert.NoError(t, err, "psst.idx sidecar should exist after clean shutdown")
}

func jsonReader(s string) *strings.Reader {
	return strings.NewReader(s)
}
