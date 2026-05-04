package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allank/psst/internal/cache"
	"github.com/allank/psst/internal/mcpserver"
	"github.com/allank/psst/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCacher struct {
	lookupResult  *cache.Result
	lookupErr     error
	storeResult   *store.Entry
	storeErr      error
	invalidateErr error
	invalidated   int
}

func (f *fakeCacher) Lookup(_ context.Context, _ string, _ json.RawMessage, _ float64) (*cache.Result, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	if f.lookupResult == nil {
		return &cache.Result{Hit: false}, nil
	}
	return f.lookupResult, nil
}

func (f *fakeCacher) Store(_ context.Context, tool string, args json.RawMessage, result json.RawMessage, ttl int) (*store.Entry, error) {
	if f.storeErr != nil {
		return nil, f.storeErr
	}
	if f.storeResult == nil {
		now := time.Now()
		f.storeResult = &store.Entry{
			Key: "sha256:test", Tool: tool, Args: args, Result: result,
			CachedAt: now, ExpiresAt: now.Add(time.Hour),
		}
	}
	return f.storeResult, nil
}

func (f *fakeCacher) Invalidate(tool, key string) (int, error) {
	if f.invalidateErr != nil {
		return 0, f.invalidateErr
	}
	f.invalidated++
	return f.invalidated, nil
}

func newTestServer(t *testing.T, c mcpserver.Cacher) *mcpserver.Server {
	t.Helper()
	srv := mcpserver.New("127.0.0.1:0", c, func() int { return 0 }, "test.db")
	require.NoError(t, srv.Start())
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	return srv
}

func postMCP(t *testing.T, addr, body string) map[string]any {
	t.Helper()
	resp, err := http.Post("http://"+addr+"/mcp", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

func TestHealthEndpoint(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	resp, err := http.Get("http://" + srv.Addr() + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["ok"])
}

func TestMCPInitialize(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	assert.Equal(t, "2.0", result["jsonrpc"])
	rr := result["result"].(map[string]any)
	assert.Equal(t, "2024-11-05", rr["protocolVersion"])
	assert.Equal(t, "psst", rr["serverInfo"].(map[string]any)["name"])
}

func TestMCPNotificationsInitialized(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	resp, err := http.Post("http://"+srv.Addr()+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 204, resp.StatusCode)
}

func TestMCPToolsList(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	tools := result["result"].(map[string]any)["tools"].([]any)
	assert.Len(t, tools, 3)
	names := make([]string, 3)
	for i, tool := range tools {
		names[i] = tool.(map[string]any)["name"].(string)
	}
	assert.ElementsMatch(t, []string{"psst_lookup", "psst_store", "psst_invalidate"}, names)
}

func TestMCPLookupHit(t *testing.T) {
	now := time.Now()
	fc := &fakeCacher{lookupResult: &cache.Result{
		Hit: true, Match: "exact",
		Entry: &store.Entry{
			Key: "sha256:abc", Tool: "jira_get_issue",
			Args: json.RawMessage(`{"issue_key":"PROJ-1"}`),
			Result: json.RawMessage(`{"summary":"fix bug"}`),
			CachedAt: now, ExpiresAt: now.Add(time.Hour),
		},
	}}
	srv := newTestServer(t, fc)
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"psst_lookup","arguments":{"tool":"jira_get_issue","args":{"issue_key":"PROJ-1"}}}}`)
	assert.Nil(t, result["error"])
	content := result["result"].(map[string]any)["content"].([]any)
	item := content[0].(map[string]any)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(item["text"].(string)), &out))
	assert.Equal(t, true, out["hit"])
	assert.Equal(t, "exact", out["match"])
}

func TestMCPLookupMiss(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"psst_lookup","arguments":{"tool":"t","args":{}}}}`)
	assert.Nil(t, result["error"])
	content := result["result"].(map[string]any)["content"].([]any)
	item := content[0].(map[string]any)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(item["text"].(string)), &out))
	assert.Equal(t, false, out["hit"])
}

func TestMCPStore(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"psst_store","arguments":{"tool":"t","args":{},"result":{}}}}`)
	assert.Nil(t, result["error"])
	content := result["result"].(map[string]any)["content"].([]any)
	item := content[0].(map[string]any)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(item["text"].(string)), &out))
	assert.Equal(t, true, out["stored"])
}

func TestMCPInvalidate(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"psst_invalidate","arguments":{"tool":"t"}}}`)
	assert.Nil(t, result["error"])
}

func TestMCPUnknownMethod(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":7,"method":"no/such","params":{}}`)
	assert.NotNil(t, result["error"])
	assert.Equal(t, float64(-32601), result["error"].(map[string]any)["code"])
}

func TestMCPUnknownTool(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"no_tool","arguments":{}}}`)
	assert.NotNil(t, result["error"])
}

func TestHealthWrongMethod(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	resp, err := http.Post("http://"+srv.Addr()+"/health", "application/json", bytes.NewReader(nil))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 405, resp.StatusCode)
}

func TestMCPLookupInternalError(t *testing.T) {
	fc := &fakeCacher{lookupErr: fmt.Errorf("db unavailable")}
	srv := newTestServer(t, fc)
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"psst_lookup","arguments":{"tool":"t","args":{}}}}`)
	assert.NotNil(t, result["error"])
	assert.Equal(t, float64(-32603), result["error"].(map[string]any)["code"])
}

func TestMCPStoreInternalError(t *testing.T) {
	fc := &fakeCacher{storeErr: fmt.Errorf("db full")}
	srv := newTestServer(t, fc)
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"psst_store","arguments":{"tool":"t","args":{},"result":{}}}}`)
	assert.NotNil(t, result["error"])
	assert.Equal(t, float64(-32603), result["error"].(map[string]any)["code"])
}

func TestMCPStoreRequiredToolField(t *testing.T) {
	srv := newTestServer(t, &fakeCacher{})
	result := postMCP(t, srv.Addr(), `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"psst_store","arguments":{"args":{},"result":{}}}}`)
	assert.NotNil(t, result["error"])
	assert.Equal(t, float64(-32602), result["error"].(map[string]any)["code"])
}
