# Product Requirements Document
# psst — Semantic MCP Cache Layer

**Version:** 0.2  
**Status:** Draft  
**Author:** Design session output  
**Changelog:** v0.2 — Renamed from Stash to psst; full CLI parity added (all cache operations available without running the MCP server). v0.1 — Initial draft.

---

## 1. Overview

`psst` is a single-binary tool written in Go that acts as a transparent semantic cache between an LLM agent and its upstream MCP servers. When an agent needs to call a tool — `jira_get_issue`, `confluence_search`, `get_sprint_tickets` — it queries `psst` first. If a sufficiently similar call has been seen before, `psst` returns the cached result immediately without touching the upstream server.

The name is intentional: *psst* — "hey, check here first." The onomatopoeia captures what the cache does: quietly intercept before the noisy upstream call goes out.

Two complementary lookup strategies are combined:

- **Exact-match cache (memoization):** tool name + canonicalised arguments → stored result. Zero-cost lookups for identical calls; correct for deterministic tools like `get_issue(PROJ-123)`.
- **Semantic cache:** tool name + query argument embedded as a vector → ANN lookup against past calls. Catches rephrased or intent-equivalent queries: *"mobile OAuth login docs"* hits the same cached result as *"authentication flow for mobile"*.

`psst` can be used in two modes:

- **Daemon mode** (`psst serve`): runs a long-lived MCP Streamable HTTP server. Agents call it via MCP, exactly as they would any other MCP tool server.
- **CLI mode**: all cache operations — lookup, store, invalidate, inspect, status — are available as direct CLI commands against the store on disk, with no server process required. Useful in scripted environments, CI, or any context where a persistent daemon is inconvenient.

The primary motivation is a workload where an LLM agent repeatedly queries the same Jira project or Confluence space over hours or days. Cold upstream calls are expensive (latency, API rate limits, context tokens consumed by re-fetching identical data). `psst` eliminates that redundancy.

---

## 2. Goals

| Goal | Description |
|---|---|
| Transparent interposition | Agents query `psst` via MCP before calling upstream. No changes to existing MCP server implementations required. |
| Dual lookup strategy | Exact-match memoization for deterministic calls; semantic ANN lookup for natural-language query arguments. |
| Full CLI parity | Every cache operation available as a CLI command against the store directly — no running server required. |
| Per-tool TTL | Jira ticket status and Confluence search results have different staleness profiles. TTLs are configurable per tool name. |
| Offline embedding | Semantic lookup uses a local ONNX embedding model — no external API call to embed a cache key. |
| Confidence gating | Semantic cache hits below a configurable similarity threshold are not returned; the agent falls back to the upstream call. |
| Shared cache across sessions | Multiple concurrent agent sessions working on the same project share a single cache store. |
| Single binary | One file to deploy; no separate database process required. |
| LLM-first output | `psst`'s MCP responses and default CLI output are terse and token-efficient. Human-readable output is opt-in via `--pretty`. |

### Non-Goals

- Proxying or routing upstream MCP calls on the agent's behalf (the agent still makes the upstream call on a cache miss — `psst` only stores and retrieves).
- Authentication or TLS between agent and `psst` (loopback-only by default; non-loopback use logs a warning).
- Caching binary or streaming tool results (JSON responses only in v1).
- Windows support in v1 (Linux and macOS only).
- Cache result diffing or merging across calls with partially overlapping arguments.

---

## 3. User Personas

### Primary: LLM Agent (Claude, Gemini, etc.)
An automated agent running a multi-step workflow against Jira and Confluence. In daemon mode it calls `psst` via MCP before each upstream tool call. In scripted workflows it may invoke the CLI directly. Cache hits must be indistinguishable in schema from live upstream responses.

### Secondary: Developer / Workflow Author
A human building or debugging agent workflows. They use `psst status --pretty` and `psst inspect` to understand what is in the cache, why a hit or miss occurred, and whether TTLs need adjustment — without necessarily running the daemon.

### Tertiary: Long-Running Project Worker
A human running an agent repeatedly against the same Jira project over days. They benefit from `SIGHUP`-triggered invalidation when they know upstream data has changed significantly, and from per-tool TTL tuning to balance freshness against call volume.

---

## 4. Architecture

**Cache store** — an in-process key/value store backed by a single `psst.db` file (bbolt). Entries are keyed by a compound key (`tool_name` + SHA256 of canonicalised arguments). Each entry carries: the raw JSON result, the embedding vector of the query argument (if applicable), the source tool name, the creation timestamp, and the TTL.

**Vector index** — an in-memory HNSW index (USearch) over stored query embeddings, rebuilt from `psst.db` on startup (daemon mode) or on first semantic lookup (CLI mode). Persisted as a sidecar file `psst.hnsw` alongside `psst.db` for fast warm restarts.

**Embedding** — `all-MiniLM-L6-v2`, embedded in the binary via `go:embed`, running via ONNX Runtime. Used only for semantic key construction at store time and ANN lookup at query time. Both daemon and CLI modes use the same embedding path.

**Daemon mode** additionally runs an MCP HTTP server (two concurrent goroutines — store accessor + HTTP server — sharing the store via a read/write mutex).

**CLI mode** opens `psst.db` directly for the duration of the command and exits. No network, no background process. Write operations acquire a file lock on `psst.db` to prevent corruption if a daemon is also running concurrently.

---

## 5. Lookup Flow

### Via MCP (daemon mode)

```
1. Call psst_lookup(tool, args, threshold?)
2. If hit → use cached result, skip upstream call.
3. If miss → call upstream MCP server as normal.
4. Call psst_store(tool, args, result, ttl?) to populate the cache.
```

### Via CLI (no daemon)

```bash
# 1. Check before calling upstream
psst lookup --tool jira_get_issue --args '{"issue_key":"PROJ-123"}'

# 2. On miss, call upstream via whatever mechanism the script uses, then store
psst store --tool jira_get_issue --args '{"issue_key":"PROJ-123"}' --result "$(cat result.json)"
```

Both paths produce consistent output and operate on the same `psst.db` store. A script using the CLI and a daemon serving MCP requests share the same cache file transparently.

This is a **pull-through pattern** — `psst` never calls upstream itself. The caller (agent or script) remains in control of the upstream call. This keeps `psst` decoupled from upstream server credentials and authentication.

### Exact-match path

The cache key is `SHA256(tool_name + canonical_json(args))`. Canonical JSON sorts object keys and strips insignificant whitespace. A match returns the stored result immediately if the entry is not expired.

### Semantic path

Triggered when the call includes at least one string argument that looks like a natural-language query (heuristic: argument key is named `q`, `query`, `text`, `search`, or `summary`; or the tool is listed in `[semantic] tools` in config). `psst` embeds the query string and runs an ANN search over stored query vectors for the same tool name. If the nearest neighbour's cosine similarity exceeds `threshold` (default `0.82`) and the entry is not expired, the cached result is returned.

Both paths are attempted in order: exact match first, semantic match second. The first hit wins.

---

## 6. CLI Commands

All commands operate directly on the store file (`~/.config/psst/psst.db` by default, overridable via `--store` or `PSST_STORE` env var). None require the daemon to be running.

---

### 6.1 `psst lookup`

Check the cache for a prior result. Exits `0` on hit, `1` on miss, `2` on error — suitable for use in shell conditionals.

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--tool <name>` | required | Upstream tool name |
| `--args <json>` | required | Arguments as JSON object |
| `--threshold <f>` | `0.82` | Minimum cosine similarity for a semantic hit |
| `--format <fmt>` | `plain` | Output format: `plain`, `json` |
| `--pretty` | false | Human-readable output |

**LLM output — hit (plain):**
```
hit match=exact cached=2026-04-28T09:14:22Z expires=2026-04-29T09:14:22Z
{"key":"PROJ-123","summary":"Fix login bug",...}
```

**LLM output — miss:**
```
miss
```

**JSON output (`--format json`) — hit:**
```json
{
  "hit":     true,
  "match":   "semantic",
  "score":   0.91,
  "cached":  "2026-04-28T09:14:22Z",
  "expires": "2026-04-29T09:14:22Z",
  "result":  { ...upstream tool response... }
}
```

**Human output (`--pretty`) — hit:**
```
 Cache hit  match=semantic  score=0.91
 Cached:    2026-04-28 09:14    Expires: 2026-04-29 09:14

 {"key":"PROJ-123","summary":"Fix login bug",...}
```

---

### 6.2 `psst store`

Write a result into the cache.

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--tool <name>` | required | Upstream tool name |
| `--args <json>` | required | Arguments as JSON object |
| `--result <json>` | required | The upstream tool's response as a JSON object |
| `--ttl <seconds>` | `0` | TTL in seconds; `0` = use configured default for this tool |

**LLM output:**
```
stored key=sha256:a3f8... expires=2026-04-29T09:14:22Z
```

**Human output (`--pretty`):**
```
 Stored  key=sha256:a3f8…  tool=jira_get_issue  expires=2026-04-29 09:14
```

---

### 6.3 `psst invalidate`

Evict entries from the cache.

```bash
psst invalidate --tool confluence_search         # all entries for this tool
psst invalidate --tool jira_get_issue --key sha256:a3f8  # specific entry
psst invalidate --all                            # flush everything
```

**LLM output:**
```
evicted tool=confluence_search count=63
```

---

### 6.4 `psst status`

Show cache statistics.

**LLM output (default):**
```
store=~/.config/psst/psst.db entries=214 expired=12 size=8.1MB
```

**Human output (`--pretty`):**
```
 psst Cache
 ────────────────────────────────────
 Store              ~/.config/psst/psst.db
 Entries            214  (12 expired, pending eviction)
 Store size         8.1 MB
 Tools cached       jira_get_issue (84)  confluence_search (63)  jira_search (41) …
```

When the daemon is running, `psst status` contacts it via the health endpoint and appends:
```
 Daemon             listening on 127.0.0.1:7425  uptime=3h22m
```

When no daemon is running, the daemon line is omitted.

---

### 6.5 `psst inspect`

Show stored entries for a given tool, or the full record for a specific key.

```bash
psst inspect --tool jira_get_issue
psst inspect --key sha256:a3f8
```

**LLM output:**
```
key=sha256:a3f8  tool=jira_get_issue  args={"issue_key":"PROJ-123"}  cached=2026-04-28T09:14Z  expires=2026-04-29T09:14Z  match=exact
key=sha256:b7c2  tool=jira_get_issue  args={"issue_key":"PROJ-456"}  cached=2026-04-27T16:01Z  expires=2026-04-28T16:01Z  match=exact
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--tool <name>` | — | Filter by tool name |
| `--key <hash>` | — | Show single entry by key |
| `--expired` | false | Include expired entries |
| `--pretty` | false | Human-readable table output |

---

### 6.6 `psst clean`

Remove the store and sidecar files entirely.

```bash
psst clean
# removed ~/.config/psst/psst.db
# removed ~/.config/psst/psst.hnsw
```

---

### 6.7 `psst serve`

Start the MCP HTTP daemon (foreground process). Optional — all other commands work without it.

```bash
psst serve
```

**Startup sequence:**
1. Open or create `psst.db`.
2. Rebuild in-memory HNSW index from stored entries. If `psst.hnsw` sidecar is present and not stale, load it directly (fast path).
3. Start the MCP HTTP server.
4. Log one startup line:

```
serving store=~/.config/psst/psst.db listen=127.0.0.1:7425 entries=214
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--listen <addr:port>` | `127.0.0.1:7425` | MCP server bind address |
| `--store <path>` | `~/.config/psst/psst.db` | Path to the bbolt database file |
| `--pretty` | false | Human-readable log output |

**Signals:**
- `SIGINT` / `SIGTERM` — flush pending writes, persist `psst.hnsw`, exit 0.
- `SIGHUP` — flush and rebuild the HNSW index from the current store (useful after bulk invalidation).

---

## 7. MCP Server

Available only in daemon mode (`psst serve`). The CLI commands in §6 are the primary interface for non-daemon use.

### Transport

MCP Streamable HTTP. Single endpoint: `POST /mcp`, JSON-RPC 2.0.

Default bind address: `127.0.0.1:7425`. Configurable via `--listen` or `config.toml`.

When `--listen` binds to a non-loopback address:
```
warn listen=0.0.0.0:7425 auth=none network_accessible=true
```

### Tools

---

**`psst_lookup`**

Check the cache before making an upstream tool call.

Input schema:
```json
{
  "tool":      { "type": "string",  "description": "Name of the upstream MCP tool" },
  "args":      { "type": "object",  "description": "Arguments as they would be passed to the upstream tool" },
  "threshold": { "type": "number",  "default": 0.82, "description": "Minimum cosine similarity for a semantic hit (0.0–1.0)" }
}
```

Response on **hit**:
```json
{
  "hit":     true,
  "match":   "exact",
  "result":  { "...upstream tool response..." },
  "cached":  "2026-04-28T09:14:22Z",
  "expires": "2026-04-29T09:14:22Z",
  "score":   null
}
```

`match` is `"exact"` or `"semantic"`. `score` is `null` for exact matches; a float (0.0–1.0) for semantic matches.

Response on **miss**:
```json
{ "hit": false }
```

---

**`psst_store`**

Populate the cache after a successful upstream call.

Input schema:
```json
{
  "tool":   { "type": "string" },
  "args":   { "type": "object" },
  "result": { "type": "object", "description": "The upstream tool's response" },
  "ttl":    { "type": "integer", "description": "TTL in seconds; 0 = use configured default for this tool" }
}
```

Response:
```json
{
  "stored":  true,
  "key":     "sha256:a3f8...",
  "expires": "2026-04-29T09:14:22Z"
}
```

---

**`psst_invalidate`**

Evict entries from the cache.

Input schema:
```json
{
  "tool": { "type": "string", "description": "Tool name to invalidate, or \"*\" for all" },
  "key":  { "type": "string", "description": "Specific cache key (optional; if omitted, invalidates all entries for tool)" }
}
```

Response:
```json
{ "evicted": 14 }
```

### Health Endpoint

`GET /health` — plain HTTP, not MCP. Returns `200 OK`:
```json
{
  "ok":      true,
  "entries": 214,
  "store":   "/home/user/.config/psst/psst.db",
  "uptime":  "3h22m"
}
```

---

## 8. TTL & Invalidation

Cache entries expire based on per-tool TTL configuration. After expiry, entries are not served; they are lazily evicted on next read or during a background sweep (daemon mode: every 10 minutes; CLI mode: on any write command).

### Default TTL tiers

| Tool pattern | Default TTL | Rationale |
|---|---|---|
| `*_get_issue`, `*_get_ticket` | 24h | Closed/stable tickets rarely change |
| `*_search`, `*_query` | 4h | Search results shift as work progresses |
| `*_get_page`, `*_get_document` | 12h | Documents are edited but not constantly |
| `*_get_sprint`, `*_get_board` | 1h | Sprint state changes frequently |
| `*` (catch-all) | 6h | Conservative default for unconfigured tools |

All values are overridable per tool in `config.toml`. A `ttl=0` passed to `psst_store` or `psst store` uses the configured default.

### Manual invalidation

- Agent calls `psst_invalidate` (MCP) or `psst invalidate` (CLI) when it knows upstream data has changed.
- `SIGHUP` to the daemon rebuilds the vector index but does not evict entries — use `psst invalidate --all` for a full flush.
- The CLI `psst invalidate` command operates directly on the store file; it does not require the daemon and does not signal it.

---

## 9. Semantic Lookup Design

### Query argument detection

`psst` applies semantic lookup only when a string argument is identified as a natural-language query. Detection is by argument key name first (`q`, `query`, `text`, `search`, `summary`, `keywords`), with an explicit opt-in list of tool names in config for tools that use non-standard key names.

Tools without natural-language query arguments (e.g. `get_issue(issue_key: "PROJ-123")`) use exact-match only.

### Embedding

Query strings are embedded using `all-MiniLM-L6-v2` (384 dimensions, int8 quantised) via ONNX Runtime. Embedding happens at store time and is persisted in `psst.db` alongside the result. At lookup time, the query is embedded live and compared against stored vectors.

In CLI mode, the HNSW sidecar (`psst.hnsw`) is loaded if present. If absent or stale, a flat brute-force scan is performed. The sidecar is written after any `psst store` call that triggers an embedding.

### ANN search

The HNSW index is scoped per tool name. A lookup searches only within the index partition for the named tool, preventing cross-tool false positives.

For stores with fewer than 200 entries per tool, a flat brute-force cosine scan is used instead of HNSW.

### Threshold and false-positive risk

The default threshold (`0.82`) is deliberately conservative. The risk of a false positive — returning a cached result that is semantically close but factually different — is higher here than in a navigation use case, because the agent may act on the content. Operators tuning for recall can lower the threshold; high-stakes workflows should raise it or disable semantic lookup for specific tools.

---

## 10. Configuration

`~/.config/psst/config.toml`:

```toml
[server]
listen    = "127.0.0.1:7425"
store     = "~/.config/psst/psst.db"

[cache]
default_ttl_seconds = 21600   # 6h catch-all default

  # Per-tool TTL overrides. Glob patterns matched in order; first match wins.
  [[cache.ttl]]
  tool    = "*_get_issue"
  seconds = 86400   # 24h

  [[cache.ttl]]
  tool    = "*_search"
  seconds = 14400   # 4h

  [[cache.ttl]]
  tool    = "*_get_sprint"
  seconds = 3600    # 1h

[semantic]
threshold = 0.82   # minimum cosine similarity for a semantic cache hit
enabled   = true   # set false to disable semantic lookup globally

  # Tools where the natural-language query argument uses a non-standard key name
  [[semantic.tools]]
  tool     = "confluence_search"
  arg_keys = ["cql"]

  [[semantic.tools]]
  tool     = "jira_search"
  arg_keys = ["jql"]

[eviction]
sweep_interval_minutes = 10   # daemon mode: background sweep for expired entries
```

All settings can be overridden with flags. The `PSST_STORE` environment variable overrides the store path for all commands.

---

## 11. Error Handling

| Scenario | Behaviour |
|---|---|
| Store file missing on startup or first CLI use | Create a new empty store; log `info store_created=true` |
| HNSW sidecar stale or missing | Rebuild from store contents; log rebuild duration |
| Embedding fails for a store call | Store entry with exact-match key only; log warning; do not fail the store operation |
| MCP lookup arrives during HNSW rebuild (daemon) | Serve from previous in-memory index; never blocked |
| Store write fails (e.g. disk full) | Log error; return `{"stored": false}` (MCP) or exit `2` (CLI) |
| Semantic lookup returns hit but entry has since expired | Treat as miss |
| `--listen` address already in use | Exit 1 with clear error: `error bind_failed addr=127.0.0.1:7425 err=address already in use` |
| CLI write command issued while daemon holds file lock | Retry with backoff for 2 seconds; fail with clear error if lock not released |

---

## 12. Testing

### Unit tests
- Exact-match key canonicalisation (argument ordering, whitespace).
- TTL glob matching (correct tier selection per tool name pattern).
- Semantic threshold gating (hits above, at, and below threshold).
- Expiry: entries past TTL return miss.
- CLI exit codes: `0` hit, `1` miss, `2` error.

### CLI integration tests (no daemon)

Run against a temporary store file:

1. `psst store --tool jira_get_issue --args '{"issue_key":"PROJ-123"}' --result '{"summary":"Fix bug"}'`
2. `psst lookup --tool jira_get_issue --args '{"issue_key":"PROJ-123"}'` → exit `0`, `match=exact`.
3. `psst lookup --tool jira_get_issue --args '{"issue_key":"PROJ-999"}'` → exit `1`.
4. `psst lookup --tool confluence_search --args '{"q":"mobile auth flow"}'` with a prior semantic store → exit `0`, `match=semantic`.
5. Advance time past TTL → same lookup → exit `1`.
6. `psst invalidate --tool jira_get_issue` → `evicted=N`; subsequent lookup → exit `1`.
7. `psst status` → correct entry count.
8. `psst clean` → store and sidecar removed.

### Daemon integration tests

Start `psst serve` as a subprocess:

1. Poll `GET /health` until `{"ok": true}`.
2. `POST /mcp` `psst_store` → `{"stored": true}`.
3. `POST /mcp` `psst_lookup` identical args → `hit=true, match=exact`.
4. `POST /mcp` `psst_lookup` semantic equivalent → `hit=true, match=semantic`.
5. `POST /mcp` `psst_lookup` unrelated → `hit=false`.
6. `POST /mcp` `psst_invalidate` → `evicted=N`; subsequent lookup → `hit=false`.
7. Run `psst status` CLI while daemon is up → daemon uptime line present.
8. Send `SIGTERM`; assert clean exit and `psst.hnsw` written.

### Concurrent access test

Start `psst serve`; simultaneously run `psst store` CLI commands in a loop. Assert no store corruption and no deadlock.

---

## 13. Dependency Summary

| Dependency | Purpose | Binding |
|---|---|---|
| `go.etcd.io/bbolt` | Embedded key/value store (cache persistence) | Pure Go |
| `github.com/unum-cloud/usearch` | HNSW vector index | CGo |
| `github.com/yalue/onnxruntime_go` | ONNX model inference for embedding | CGo |
| `github.com/charmbracelet/lipgloss` | Styled terminal output | Pure Go |
| `github.com/charmbracelet/log` | Structured pretty logging | Pure Go |
| `github.com/spf13/cobra` | CLI command structure | Pure Go |
| `github.com/BurntSushi/toml` | Config file parsing | Pure Go |

The `all-MiniLM-L6-v2` ONNX model and tokenizer are embedded directly in the `psst` binary via `go:embed`. `psst` is a standalone repository with no shared packages or monorepo dependency on Riffle. Binary size is approximately 110MB.

---

## 14. Build & Distribution

### Build Requirements
- Go 1.22+
- C compiler (CGo — usearch + onnxruntime)
- ONNX Runtime shared library

### Build Targets
```
make build-linux-amd64
make build-linux-arm64
make build-darwin-arm64
make build-darwin-amd64
```

### Distribution
- GitHub Releases: pre-built binaries per platform.
- Homebrew tap (v1.1).
- No Docker image.

---

## 15. Scope / Phasing

### v1.0 — Core
- `lookup`, `store`, `invalidate`, `status`, `inspect`, `clean`, `serve` commands.
- Exact-match memoization cache.
- Semantic cache via all-MiniLM-L6-v2 + USearch HNSW.
- Per-tool TTL with glob-pattern matching.
- bbolt persistence; HNSW sidecar for fast restart.
- Full CLI parity: all operations available without `psst serve`.
- MCP Streamable HTTP (daemon mode): `psst_lookup`, `psst_store`, `psst_invalidate`.
- `GET /health` endpoint.
- `SIGTERM` graceful shutdown; `SIGHUP` index rebuild.
- Concurrent access: file lock for CLI writes when daemon is running.

### v1.1
- Cache hit/miss metrics endpoint (`GET /metrics`, Prometheus format).
- `psst explain <key>` — renders the stored query embedding and nearest neighbours for debugging semantic matches.
- Per-session cache namespacing (multiple concurrent agents on different projects share a store but can be isolated by namespace prefix).
- Homebrew distribution tap.

### v2.0 (future)
- Upstream change detection via webhook or polling — `psst` proactively invalidates entries when Jira/Confluence signals a resource update, rather than relying on TTL alone.
- Hybrid lookup: exact + semantic + BM25 keyword fallback for structured query arguments (JQL, CQL).
- Multi-store federation: query across caches from multiple machines (useful for team-shared caches).
- `psst diff` — show which cached entries are past TTL but not yet evicted.

---

## 16. Decisions

| # | Question | Resolution |
|---|---|---|
| 1 | Pull-through vs. push-through (does `psst` call upstream itself)? | Pull-through only. Caller makes the upstream call; `psst` stores and retrieves. Keeps `psst` decoupled from upstream credentials. |
| 2 | Persistence: SQLite vs. bbolt vs. flat files? | bbolt — embedded, no CGo, single-file, well-suited to keyed blob storage. SQLite adds query flexibility not needed in v1. |
| 3 | Should semantic lookup be opt-in per tool or opt-out? | Opt-out (enabled by default for tools with recognised query-argument key names). Explicit opt-in list in config for non-standard key names. |
| 4 | Threshold default — 0.82 vs lower? | 0.82. The false-positive cost (agent acts on wrong cached data) is higher here than in a navigation use case. Operators can lower it. |
| 5 | Should `psst` share Riffle's embedding package? | No. `psst` is a standalone binary with its own embedded `all-MiniLM-L6-v2` ONNX model and tokenizer. No monorepo, no shared packages. The duplication is intentional — each binary is self-contained and independently deployable. |
| 6 | Port number? | 7425 — adjacent to Riffle's 7424 to signal the relationship, avoid collision. |
| 7 | Should expired entries be served with a staleness warning rather than treated as misses? | No. Expired = miss. Serving stale data silently is the failure mode this tool exists to avoid. |
| 8 | HNSW index rebuild on startup — block or background? | Daemon: block until rebuild completes (fast, avoids serving from empty index). CLI: lazy — load sidecar if present, fall back to flat scan if not. |
| 9 | CLI concurrent write safety when daemon is running? | File lock on `psst.db` with 2-second retry backoff; fail with clear error if not released. |
| 10 | Does `psst status` CLI require the daemon? | No. Reads store stats directly from `psst.db`. Daemon uptime line is added only if daemon is reachable at the configured listen address. |
