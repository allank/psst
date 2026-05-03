# psst — Implementation Design
**Date:** 2026-05-03
**Status:** Approved
**PRD:** `docs/psst-prd.md`

---

## 1. Scope

Full v1.0 implementation as specified in the PRD. All seven CLI commands, MCP daemon, exact-match and semantic caching. No phasing — the complete feature set ships together.

---

## 2. Architecture & Package Layout

```
github.com/allank/psst
├── main.go                          # calls cmd.Execute()
├── go.mod                           # module github.com/allank/psst
├── Makefile                         # mirrors riffle: fetch-model, build, build-release, cross builds
├── cmd/
│   ├── root.go                      # cobra root, version flag
│   ├── lookup.go                    # psst lookup
│   ├── store.go                     # psst store
│   ├── invalidate.go                # psst invalidate
│   ├── status.go                    # psst status
│   ├── inspect.go                   # psst inspect
│   ├── clean.go                     # psst clean
│   ├── serve.go                     # psst serve (daemon)
│   └── shared.go                    # resolveStorePath, loadEmbedder
└── internal/
    ├── config/                      # TOML config, per-tool TTL glob matching
    ├── store/                       # raw bbolt ops — get/put/delete/scan, entry serialisation
    ├── cache/                       # lookup logic — exact-match, semantic, TTL gating, eviction
    ├── embedder/                    # copied from riffle — ONNX session, Embed interface
    ├── tokenizer/                   # copied from riffle — WordPiece, tokenizer.json
    ├── vector/                      # copied from riffle — flat cosine index, serialise/load
    ├── mcpserver/                   # mirrored from riffle — JSON-RPC 2.0, psst tools
    └── output/                      # plain / json / pretty formatters per command
```

`embedder`, `tokenizer`, and `vector` are direct copies of their Riffle counterparts — same interfaces, same file names, same build tags (`embedmodel`). The `mcpserver` is mirrored from Riffle and adapted for psst's three tools.

---

## 3. Store Layer (`internal/store/`)

bbolt database with two buckets:

```
psst.db
├── bucket "entries"    # key: SHA256(tool + canonical_json(args))
│                       # value: JSON-serialised Entry
└── bucket "vectors"    # key: same SHA256
                        # value: []float32, 384 dims, little-endian
                        # present only for entries with an embedded query
```

Two buckets keeps vector data separate — exact-match lookups never deserialise vectors.

### Entry struct

```go
type Entry struct {
    Tool      string          `json:"tool"`
    Args      json.RawMessage `json:"args"`       // canonical JSON
    Result    json.RawMessage `json:"result"`
    CachedAt  time.Time       `json:"cached_at"`
    ExpiresAt time.Time       `json:"expires_at"`
    Key       string          `json:"key"`        // "sha256:xxxx"
}
```

### Store interface

```go
func (s *Store) Get(key string) (*Entry, error)
func (s *Store) Put(e *Entry) error
func (s *Store) Delete(key string) error
func (s *Store) DeleteTool(tool string) (int, error)
func (s *Store) DeleteAll() (int, error)
func (s *Store) Scan(fn func(*Entry) error) error
func (s *Store) GetVector(key string) ([]float32, error)
func (s *Store) PutVector(key string, vec []float32) error
func (s *Store) ScanVectors(tool string, fn func(key string, vec []float32) error) error
```

`Store` wraps `*bbolt.DB` and a `sync.RWMutex`. bbolt uses an exclusive OS-level file lock on open; CLI commands retry with 2-second timeout and return a clear error if the daemon holds the lock.

---

## 4. Cache Layer (`internal/cache/`)

Coordinates `store`, `vector`, and `embedder`. Implements both lookup paths.

### Lookup flow

```
Lookup(tool, args, threshold)
  1. key = SHA256(tool + canonical_json(args))
  2. store.Get(key)
     - hit, not expired → return Result{Hit: true, Match: "exact"}
     - hit, expired     → store.Delete(key); fall through
     - miss             → fall through
  3. q, ok = detectQueryString(tool, args, cfg); ok?
     - embed q
     - flat cosine scan over in-memory index for this tool
     - best score >= threshold AND not expired → return Result{Hit: true, Match: "semantic", Score: s}
  4. return Result{Hit: false}
```

### Store flow

```
Store(tool, args, result, ttl)
  1. key = SHA256(tool + canonical_json(args))
  2. expiresAt = now + resolveTTL(tool, ttl, config)
  3. store.Put(Entry{...})
  4. detectQueryString → embed q → store.PutVector(key, vec) → update in-memory index
```

### Cache interface

```go
type Result struct {
    Hit   bool
    Match string    // "exact" | "semantic"
    Score float64   // 0 for exact; cosine similarity for semantic
    Entry *store.Entry
}

func (c *Cache) Lookup(ctx context.Context, tool string, args json.RawMessage, threshold float64) (*Result, error)
func (c *Cache) Store(ctx context.Context, tool string, args json.RawMessage, result json.RawMessage, ttl int) (*store.Entry, error)
func (c *Cache) Invalidate(tool, key string) (int, error)
func (c *Cache) Evict() (int, error)
```

### Query argument detection

Pure function, no I/O:

```go
func detectQueryString(tool string, args json.RawMessage, cfg *config.Config) (query string, ok bool)
```

Unmarshals `args`, iterates keys, returns the string value of the first key that is in `{"q", "query", "text", "search", "summary", "keywords"}` or that matches a configured `arg_key` for the tool in `cfg.Semantic.Tools`. Returns `"", false` if no query argument is found.

### Canonical JSON

`encoding/json` marshal of `map[string]any` — sorts keys, strips whitespace. Sole normalisation step for exact-match key stability.

### In-memory vector index

`cache.Cache` holds `map[string]*vector.FlatIndex` keyed by tool name.
- **Daemon:** built on startup from `psst.idx` sidecar (if fresh) or by scanning `store.ScanVectors`. Updated on each `Store` call.
- **CLI:** built lazily on first semantic `Lookup` call. Loaded from sidecar if present and not stale (sidecar mtime > newest bbolt entry mtime); otherwise rebuilt from bbolt and written to sidecar.

### Sidecar (`psst.idx`)

Serialised flat index written alongside `psst.db`. Daemon writes it on each `Store` call and on `SIGTERM`. CLI writes it after a rebuild. Keeps cold CLI semantic lookups fast.

The sidecar is named `psst.idx` (not `psst.hnsw` — reflects the actual flat implementation).

---

## 5. MCP Server (`internal/mcpserver/`)

Direct mirror of `github.com/allank/riffle/internal/mcpserver/server.go`, adapted for psst's tools. Same `Server` struct, `New`/`Start`/`Addr`/`Shutdown` API, and JSON-RPC 2.0 dispatch pattern.

**Migration TODO** at top of `server.go`:
```go
// TODO: migrate to a maintained MCP SDK (e.g. mark3labs/mcp-go) in a future version.
// The same migration is needed in github.com/allank/riffle/internal/mcpserver.
```

### Cacher interface (satisfied by `*cache.Cache`)

```go
type Cacher interface {
    Lookup(ctx context.Context, tool string, args json.RawMessage, threshold float64) (*cache.Result, error)
    Store(ctx context.Context, tool string, args json.RawMessage, result json.RawMessage, ttl int) (*store.Entry, error)
    Invalidate(tool, key string) (int, error)
}
```

### Health endpoint

`GET /health` returns:
```json
{ "ok": true, "entries": 214, "store": "~/.config/psst/psst.db", "uptime": "3h22m" }
```

### Tools

`tools/list` registers `psst_lookup`, `psst_store`, `psst_invalidate` with input schemas exactly as specified in PRD §7.

---

## 6. CLI Commands (`cmd/`)

| File | Flags | Exit codes |
|---|---|---|
| `lookup.go` | `--tool`, `--args`, `--threshold`, `--format`, `--pretty` | 0=hit, 1=miss, 2=error |
| `store.go` | `--tool`, `--args`, `--result`, `--ttl` | 0=ok, 2=error |
| `invalidate.go` | `--tool`, `--key`, `--all` | 0=ok, 2=error |
| `status.go` | `--pretty` | 0=ok, 2=error |
| `inspect.go` | `--tool`, `--key`, `--expired`, `--pretty` | 0=ok, 2=error |
| `clean.go` | — | 0=ok, 2=error |
| `serve.go` | `--listen`, `--store`, `--pretty` | 0=clean exit, 1=bind fail |

`shared.go` provides:
- `resolveStorePath()` — flag → `PSST_STORE` env → `~/.config/psst/psst.db`
- `loadEmbedder()` — mirrors Riffle; tries `PSST_ONNX_LIB` env, then platform default (`/usr/local/lib/libonnxruntime.dylib` on Darwin, `.so` on Linux)

Output formatting follows Riffle's `internal/output/` pattern — each command has `writePlain`, `writeJSON`, and `writePretty` variants.

### `serve` daemon lifecycle

1. Open `psst.db`, build in-memory vector index (from sidecar or full scan).
2. Start background eviction ticker (10-minute interval).
3. Start MCP HTTP server.
4. Log: `serving store=~/.config/psst/psst.db listen=127.0.0.1:7425 entries=214`
5. `SIGTERM`/`SIGINT` → flush writes, serialise `psst.idx`, exit 0.
6. `SIGHUP` → rebuild in-memory index from bbolt (does not evict entries).

---

## 7. Configuration (`internal/config/`)

TOML at `~/.config/psst/config.toml`, loaded once at command start. Same `Load(path) (*Config, error)` + hardcoded defaults pattern as Riffle.

```go
type Config struct {
    Server   ServerConfig
    Cache    CacheConfig
    Semantic SemanticConfig
    Eviction EvictionConfig
}
```

`resolveTTL(tool string, explicitTTL int, cfg *Config) time.Duration` — checks explicit TTL first, walks `cfg.Cache.TTL` globs in order (first match wins), falls back to `cfg.Cache.DefaultTTLSeconds`. Pure function.

Default TTL tiers match PRD §8 exactly.

---

## 8. Build & Dependencies

Mirrors Riffle's Makefile exactly:
- `make fetch-model` — downloads `model.onnx` + `tokenizer.json` from HuggingFace if absent
- `make build` — dev build, loads model from disk
- `make build-release` — `-tags embedmodel`, bundles model into binary (~110MB)
- Cross-compile targets: `build-darwin-arm64`, `build-darwin-amd64`, `build-linux-amd64`, `build-linux-arm64`

**Dependencies** (matches Riffle's go.mod, minus walker/merkle, plus bbolt):

| Dependency | Purpose |
|---|---|
| `go.etcd.io/bbolt` | Cache persistence |
| `github.com/yalue/onnxruntime_go` | ONNX model inference |
| `github.com/charmbracelet/lipgloss` | Pretty terminal output |
| `github.com/charmbracelet/log` | Structured logging |
| `github.com/spf13/cobra` | CLI command structure |
| `github.com/BurntSushi/toml` | Config parsing |
| `github.com/stretchr/testify` | Test assertions |

No USearch dependency — vector index uses flat cosine scan for all store sizes (HNSW is a future TODO, consistent with Riffle).

---

## 9. Testing

### Unit tests (alongside each package)

- `store/`: bbolt get/put/delete/scan, vector read/write, file lock timeout behaviour
- `cache/`: canonical JSON key stability, exact-match hit/miss/expiry, semantic threshold gating, query arg detection, TTL glob matching
- `config/`: `resolveTTL` glob priority, default fallback
- `output/`: plain/json/pretty output for each command

All use `t.TempDir()` for bbolt files. No mocks — tests hit real bbolt instances.

### MCP server tests (`internal/mcpserver/server_test.go`)

Mirrors Riffle's test file. Fake `Cacher` implementation, server bound to `127.0.0.1:0`. Covers all three tools, health endpoint, unknown method/tool error paths.

### Integration tests (`integration_test.go`)

Requires `PSST_MODEL_PATH` / `PSST_TOKENIZER_PATH` env vars (mirrors Riffle). Runs the PRD §12 CLI sequence:

1. `psst store` → exact hit → miss → semantic hit → TTL expiry → miss
2. `psst invalidate --tool` → subsequent lookup → miss
3. `psst status` → correct entry count
4. `psst clean` → store and sidecar removed
5. Daemon: start `psst serve` subprocess → poll `/health` → MCP store/lookup/invalidate → `SIGTERM` → verify `psst.idx` written
