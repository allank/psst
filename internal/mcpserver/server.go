// TODO: migrate to a maintained MCP SDK (e.g. mark3labs/mcp-go) in a future version.
// The same migration is needed in github.com/allank/riffle/internal/mcpserver.

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/allank/psst/internal/cache"
	"github.com/allank/psst/internal/store"
)

// Cacher is the interface the MCP server uses to serve requests.
// *cache.Cache satisfies this interface.
type Cacher interface {
	Lookup(ctx context.Context, tool string, args json.RawMessage, threshold float64) (*cache.Result, error)
	Store(ctx context.Context, tool string, args json.RawMessage, result json.RawMessage, ttl int) (*store.Entry, error)
	Invalidate(tool, key string) (int, error)
}

type Server struct {
	addr      string
	c         Cacher
	entryFunc func() int
	storePath string
	startTime time.Time
	httpSrv   *http.Server
	listener  net.Listener
}

func New(addr string, c Cacher, entryFunc func() int, storePath string) *Server {
	s := &Server{addr: addr, c: c, entryFunc: entryFunc, storePath: storePath, startTime: time.Now()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /health", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("POST /mcp", s.handleMCP)
	s.httpSrv = &http.Server{Handler: mux}
	return s
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.addr, err)
	}
	s.listener = ln
	go s.httpSrv.Serve(ln)
	return nil
}

func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.addr
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"entries": s.entryFunc(),
		"store":   s.storePath,
		"uptime":  time.Since(s.startTime).Round(time.Second).String(),
	})
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Method == "notifications/initialized" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var result any
	var rpcErr *jsonRPCError

	switch req.Method {
	case "initialize":
		result = s.handleInitialize()
	case "tools/list":
		result = s.handleToolsList()
	case "tools/call":
		result, rpcErr = s.handleToolsCall(r.Context(), req.Params)
	default:
		rpcErr = &jsonRPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr})
}

func (s *Server) handleInitialize() any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "psst", "version": "2.0"},
	}
}

func (s *Server) handleToolsList() any {
	return map[string]any{"tools": []any{
		map[string]any{
			"name":        "psst_lookup",
			"description": "Check the cache before making an upstream tool call.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool":      map[string]any{"type": "string", "description": "Name of the upstream MCP tool"},
					"args":      map[string]any{"type": "object", "description": "Arguments as they would be passed to the upstream tool"},
					"threshold": map[string]any{"type": "number", "default": 0.82, "description": "Minimum cosine similarity for a semantic hit (0.0–1.0)"},
				},
				"required": []string{"tool", "args"},
			},
		},
		map[string]any{
			"name":        "psst_store",
			"description": "Populate the cache after a successful upstream call.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool":   map[string]any{"type": "string"},
					"args":   map[string]any{"type": "object"},
					"result": map[string]any{"type": "object", "description": "The upstream tool's response"},
					"ttl":    map[string]any{"type": "integer", "description": "TTL in seconds; 0 = use configured default"},
				},
				"required": []string{"tool", "args", "result"},
			},
		},
		map[string]any{
			"name":        "psst_invalidate",
			"description": "Evict entries from the cache.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool": map[string]any{"type": "string", "description": "Tool name to invalidate, or \"*\" for all"},
					"key":  map[string]any{"type": "string", "description": "Specific cache key (optional)"},
				},
				"required": []string{"tool"},
			},
		},
	}}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *jsonRPCError) {
	var p toolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params"}
	}
	switch p.Name {
	case "psst_lookup":
		return s.callLookup(ctx, p.Arguments)
	case "psst_store":
		return s.callStore(ctx, p.Arguments)
	case "psst_invalidate":
		return s.callInvalidate(p.Arguments)
	default:
		return nil, &jsonRPCError{Code: -32602, Message: fmt.Sprintf("unknown tool: %s", p.Name)}
	}
}

func textContent(v any) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}}
}

func (s *Server) callLookup(ctx context.Context, rawArgs json.RawMessage) (any, *jsonRPCError) {
	var args struct {
		Tool      string          `json:"tool"`
		Args      json.RawMessage `json:"args"`
		Threshold float64         `json:"threshold"`
	}
	args.Threshold = 0.82
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	if args.Tool == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "tool is required"}
	}

	r, err := s.c.Lookup(ctx, args.Tool, args.Args, args.Threshold)
	if err != nil {
		return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
	}

	if !r.Hit {
		return textContent(map[string]any{"hit": false}), nil
	}

	var scoreVal any
	if r.Match == "semantic" {
		scoreVal = r.Score
	}
	return textContent(map[string]any{
		"hit":     true,
		"match":   r.Match,
		"score":   scoreVal,
		"cached":  r.Entry.CachedAt.UTC().Format(time.RFC3339),
		"expires": r.Entry.ExpiresAt.UTC().Format(time.RFC3339),
		"result":  json.RawMessage(r.Entry.Result),
	}), nil
}

func (s *Server) callStore(ctx context.Context, rawArgs json.RawMessage) (any, *jsonRPCError) {
	var args struct {
		Tool   string          `json:"tool"`
		Args   json.RawMessage `json:"args"`
		Result json.RawMessage `json:"result"`
		TTL    int             `json:"ttl"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	e, err := s.c.Store(ctx, args.Tool, args.Args, args.Result, args.TTL)
	if err != nil {
		return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
	}
	return textContent(map[string]any{
		"stored":  true,
		"key":     e.Key,
		"expires": e.ExpiresAt.UTC().Format(time.RFC3339),
	}), nil
}

func (s *Server) callInvalidate(rawArgs json.RawMessage) (any, *jsonRPCError) {
	var args struct {
		Tool string `json:"tool"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	n, err := s.c.Invalidate(args.Tool, args.Key)
	if err != nil {
		return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
	}
	return textContent(map[string]any{"evicted": n}), nil
}
