# psst

A single-binary Go semantic cache layer for MCP tool calls.

---

## The Hypothesis

If you use an LLM agent with a suite of MCP tools — Jira, Confluence, GitHub, Slack — it will call the same tools repeatedly with the same arguments. Every fetch of a Jira ticket, every Confluence search, every PR lookup is a round trip that takes time and burns context tokens. Across a long session, or across many sessions on the same codebase, those calls accumulate.

The underlying issue is that **many MCP tool calls are idempotent, or nearly so**. A Jira issue doesn't change between an agent reading it at 9am and re-reading it at 11am. A Confluence search for "OAuth token refresh" returns the same top results regardless of which agent session asked. The information has already been fetched and the value is already known — but nothing remembers it.

Psst's hypothesis is: **a transparent cache layer between agent and upstream tools eliminates redundant calls for the session's duration**.

### Exact Match

The simple case: the agent calls `jira_get_issue({"issue_key": "PROJ-123"})`. Psst hashes the tool name and canonicalised arguments — key order normalised, whitespace stripped — to a SHA256 key. If that key is in the store and not expired, the cached result is returned immediately. No upstream call. No latency.

This handles copy-paste arguments, argument reordering (`{"b": 1, "a": 2}` and `{"a": 2, "b": 1}` produce the same key), and the common case where an agent re-reads something it fetched five turns ago.

### Semantic Match

The more interesting case: the agent searches Confluence for "authentication flow for mobile" — but a previous call searched for "mobile OAuth login docs" and got the result it needs. The arguments differ, so exact-match won't fire.

If the tool's argument contains a recognisable query string — a key named `q`, `query`, `text`, `search`, `summary`, or `keywords`, or a tool-specific key configured by the user — Psst embeds that query with `all-MiniLM-L6-v2` and runs a flat cosine similarity scan over the in-memory vector index for that tool. If the best match exceeds the configured threshold (default 0.82), the cached result is returned as a semantic hit.

The combination of exact and semantic matching means psst is useful immediately (exact hits require no model) and becomes more valuable over time as the semantic index grows.

### The Output Design

The default output mode is terse and machine-readable — a single line per result, key=value pairs, no colour. An agent can inspect the output directly or a wrapper can parse it. Human-readable output is opt-in via `--pretty`. The tool is designed for the machine first and the human second.

---

> [!WARNING]
> ## This is a vibe coded experiment
>
> This project was designed and built almost entirely through conversational AI — prompting, reviewing, and iterating with Claude rather than writing code by hand. It is being **dog-footed**: I am running Psst against my own agent workflows to see how well the hypothesis holds in practice.
>
> **No claims are being made about the effectiveness of this approach.** Whether a semantic cache meaningfully reduces token usage or upstream API load is an open question I am trying to answer through use.
>
> **No claims are being made that a better solution doesn't already exist.** It probably does. This is an experiment of my own choosing, built to satisfy my own curiosity about the problem space and about what it feels like to vibe-code a non-trivial Go project from scratch.
>
> Use accordingly.

---

## Commands

### `psst lookup`

Checks the cache for a prior result matching the given tool and arguments. Exits 0 on hit, 1 on miss, 2 on error.

```bash
psst lookup --tool jira_get_issue --args '{"issue_key":"PROJ-123"}'
```

Exact hit:
```
hit match=exact cached=2026-05-04T09:00:00Z expires=2026-05-05T09:00:00Z
{"summary":"Fix the login bug","status":"In Progress"}
```

Semantic hit:
```
hit match=semantic score=0.91 cached=2026-05-03T14:22:00Z expires=2026-05-04T14:22:00Z
{"results":[{"title":"OAuth Mobile Guide"}]}
```

Miss:
```
miss
```

JSON output (`--format json`):
```json
{"hit":true,"match":"exact","cached":"2026-05-04T09:00:00Z","expires":"2026-05-05T09:00:00Z","result":{"summary":"Fix the login bug"}}
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--tool <name>` | (required) | Upstream tool name |
| `--args <json>` | (required) | Arguments as JSON object |
| `--threshold <f>` | `0.82` | Minimum cosine similarity for a semantic hit (0.0–1.0) |
| `--format <fmt>` | `plain` | `plain` or `json` |
| `--pretty` | false | Human-readable styled output |
| `--store <path>` | Auto-resolved | Path to `psst.db` |

---

### `psst store`

Writes a result into the cache.

```bash
psst store \
  --tool jira_get_issue \
  --args '{"issue_key":"PROJ-123"}' \
  --result '{"summary":"Fix the login bug","status":"In Progress"}'
```

```
stored key=sha256:3f2a1b… expires=2026-05-05T09:00:00Z
```

If an embedder is loaded (ONNX model available), psst detects query arguments and writes the embedding to the vector index automatically.

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--tool <name>` | (required) | Upstream tool name |
| `--args <json>` | (required) | Arguments as JSON object |
| `--result <json>` | (required) | Upstream tool response as JSON |
| `--ttl <seconds>` | `0` | TTL override; `0` uses configured default for this tool |
| `--pretty` | false | Human-readable styled output |
| `--store <path>` | Auto-resolved | Path to `psst.db` |

---

### `psst invalidate`

Evicts entries from the cache. At least one of `--tool`, `--key`, or `--all` is required.

```bash
psst invalidate --tool jira_get_issue
psst invalidate --key sha256:3f2a1b…
psst invalidate --all
```

```
evicted tool=jira_get_issue count=14
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--tool <name>` | — | Evict all entries for this tool |
| `--key <key>` | — | Evict a specific entry by key |
| `--all` | false | Flush the entire cache |
| `--pretty` | false | Human-readable styled output |
| `--store <path>` | Auto-resolved | Path to `psst.db` |

---

### `psst status`

Shows cache statistics. If the daemon is running, also shows its address and uptime.

```bash
psst status
```

```
store=/home/user/.config/psst/psst.db entries=214 expired=2 size=1.2MB
daemon=127.0.0.1:7425 uptime=3h22m
```

With `--pretty`:
```
 psst Cache  ────────────────────────────────────────────
 Store              /home/user/.config/psst/psst.db
 Entries            214   (2 expired)
 Size               1.2 MB
 Daemon             127.0.0.1:7425  uptime 3h22m
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--format <fmt>` | `plain` | `plain` or `json` |
| `--pretty` | false | Human-readable styled panel |
| `--store <path>` | Auto-resolved | Path to `psst.db` |

---

### `psst inspect`

Lists stored entries with their keys, arguments, and expiry times.

```bash
psst inspect
psst inspect --tool confluence_search
psst inspect --key sha256:3f2a1b…
psst inspect --expired   # include already-expired entries
```

```
key=sha256:3f2a1b… tool=jira_get_issue args={"issue_key":"PROJ-123"} cached=2026-05-04T09:00:00Z expires=2026-05-05T09:00:00Z
key=sha256:9c7d4e… tool=confluence_search args={"q":"OAuth token refresh"} cached=2026-05-04T10:15:00Z expires=2026-05-05T10:15:00Z
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--tool <name>` | — | Filter by tool name |
| `--key <key>` | — | Show a single entry |
| `--expired` | false | Include expired entries |
| `--format <fmt>` | `plain` | `plain` or `json` |
| `--pretty` | false | Human-readable table |
| `--store <path>` | Auto-resolved | Path to `psst.db` |

---

### `psst clean`

Removes `psst.db` and the `psst.idx` sidecar file.

```bash
psst clean
```

```
removed /home/user/.config/psst/psst.db
removed /home/user/.config/psst/psst.idx
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--store <path>` | Auto-resolved | Path to `psst.db` (sidecar inferred from same directory) |

---

### `psst serve`

Starts the MCP HTTP daemon. Exposes three MCP tools — `psst_lookup`, `psst_store`, `psst_invalidate` — and a health endpoint.

```bash
psst serve
```

```
serving store=/home/user/.config/psst/psst.db listen=127.0.0.1:7425 entries=214
```

On startup, loads the in-memory vector index from the `psst.idx` sidecar (if present and fresh) or rebuilds it from the database. A background ticker evicts expired entries every 10 minutes (configurable).

**Health endpoint** — `GET /health` for liveness checks:
```json
{ "ok": true, "entries": 214, "store": "/home/user/.config/psst/psst.db", "uptime": "3h22m" }
```

**Signals:**
- `SIGINT` / `SIGTERM` — graceful shutdown: finish in-flight requests, write `psst.idx` sidecar, close database, exit 0
- `SIGHUP` — rebuild in-memory vector index from database without restarting

**MCP tool schemas:**

`psst_lookup`:
```json
{
  "tool":      { "type": "string",  "description": "Name of the upstream MCP tool" },
  "args":      { "type": "object",  "description": "Arguments as they would be passed to the upstream tool" },
  "threshold": { "type": "number",  "default": 0.82, "description": "Minimum cosine similarity for a semantic hit (0.0–1.0)" }
}
```

`psst_store`:
```json
{
  "tool":   { "type": "string" },
  "args":   { "type": "object" },
  "result": { "type": "object", "description": "The upstream tool's response" },
  "ttl":    { "type": "integer", "description": "TTL in seconds; 0 = use configured default" }
}
```

`psst_invalidate`:
```json
{
  "tool": { "type": "string", "description": "Tool name to invalidate, or \"*\" for all" },
  "key":  { "type": "string", "description": "Specific cache key (optional)" }
}
```

**No daemonisation.** `psst serve` is a foreground process. For background operation, use your OS process supervisor. Sample unit files:

<details>
<summary>macOS launchd plist</summary>

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>         <string>com.psst.serve</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/psst</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key>     <true/>
  <key>KeepAlive</key>     <true/>
  <key>StandardOutPath</key> <string>/tmp/psst.log</string>
  <key>StandardErrorPath</key> <string>/tmp/psst.log</string>
</dict>
</plist>
```

Save to `~/Library/LaunchAgents/com.psst.serve.plist` and run `launchctl load ~/Library/LaunchAgents/com.psst.serve.plist`.
</details>

<details>
<summary>Linux systemd unit</summary>

```ini
[Unit]
Description=psst MCP cache daemon
After=network.target

[Service]
ExecStart=/usr/local/bin/psst serve
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
```

Save to `~/.config/systemd/user/psst.service` and run `systemctl --user enable --now psst`.
</details>

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--listen <addr:port>` | `127.0.0.1:7425` | MCP server bind address |
| `--store <path>` | Auto-resolved | Path to `psst.db` |
| `--pretty` | false | Human-readable log output |

---

## Store Resolution

Every command resolves the store path in the same order:

1. `--store` flag
2. `PSST_STORE` environment variable
3. `server.store` in `~/.config/psst/config.toml`
4. Default: `~/.config/psst/psst.db`

---

## Configuration

`~/.config/psst/config.toml` — all settings can be overridden per-invocation with flags:

```toml
[server]
listen = "127.0.0.1:7425"
store  = "~/.config/psst/psst.db"

[cache]
default_ttl_seconds = 21600   # 6 hours

# TTL rules are matched in order; first match wins.
# Glob patterns follow filepath.Match syntax.
[[cache.ttl]]
tool    = "*_get_issue"
seconds = 86400   # 24h

[[cache.ttl]]
tool    = "*_get_ticket"
seconds = 86400

[[cache.ttl]]
tool    = "*_search"
seconds = 14400   # 4h

[[cache.ttl]]
tool    = "*_query"
seconds = 14400

[[cache.ttl]]
tool    = "*_get_page"
seconds = 43200   # 12h

[[cache.ttl]]
tool    = "*_get_document"
seconds = 43200

[[cache.ttl]]
tool    = "*_get_sprint"
seconds = 3600    # 1h

[[cache.ttl]]
tool    = "*_get_board"
seconds = 3600

[semantic]
enabled   = true
threshold = 0.82

# Per-tool argument key overrides — any key listed here is treated as a
# query string for embedding, in addition to the well-known keys
# (q, query, text, search, summary, keywords).
[[semantic.tools]]
tool     = "my_custom_tool"
arg_keys = ["prompt", "input"]

[eviction]
sweep_interval_minutes = 10
```

Any TTL rules defined in the config file are merged with the defaults above — your rules take precedence, default rules fill in the rest.

---

## How It Works

### Canonical Keys

Before any lookup or store, psst normalises the arguments JSON by unmarshalling into `map[string]any` and re-marshalling. This sorts keys and strips whitespace. The SHA256 of `tool + ":" + canonical_json(args)` is the cache key. Two calls with `{"b": 1, "a": 2}` and `{"a": 2, "b": 1}` are the same key.

### Lookup Flow

```
Lookup(tool, args, threshold)
  1. key = SHA256(tool + ":" + canonical_json(args))
  2. store.Get(key)
     - hit, not expired → return {Hit: true, Match: "exact"}
     - hit, expired     → delete, fall through
     - miss             → fall through
  3. detectQueryString(tool, args, cfg) → query string found?
     - embed query with all-MiniLM-L6-v2
     - flat cosine scan over in-memory index for this tool
     - best score ≥ threshold and not expired → return {Hit: true, Match: "semantic", Score: s}
  4. return {Hit: false}
```

### Semantic Query Detection

A pure function checks the argument keys: if any key matches the well-known set (`q`, `query`, `text`, `search`, `summary`, `keywords`) or a tool-specific key listed in `cfg.Semantic.Tools`, its string value becomes the embedding input.

This means semantic caching is free for tools that already use conventional argument names, and configurable for everything else — no changes to the tool call or the agent prompt.

### In-Memory Vector Index

Psst holds one flat cosine index per tool name in memory, keyed by their SHA256 cache keys. The index is rebuilt on startup and updated on every `store` call. It is serialised to a `psst.idx` sidecar file alongside `psst.db` on clean shutdown and after CLI store calls when a model is loaded.

On startup, if the sidecar exists and is newer than the database, it is loaded directly — cold starts with a warm sidecar are fast. If the sidecar is stale or absent, the index is rebuilt from the vectors bucket in the database and written to sidecar.

Sending `SIGHUP` to the daemon forces an index rebuild from the database without restarting.

### Persistence

`psst.db` is a [bbolt](https://github.com/etcd-io/bbolt) embedded key-value database. Two buckets:

| Bucket | Key | Value |
|---|---|---|
| `entries` | SHA256 key | JSON-serialised entry (tool, args, result, timestamps) |
| `vectors` | SHA256 key | 384-dimension float32 embedding, little-endian |

The vector bucket is only populated for entries that had a detectable query string at store time. Exact-match lookups never touch the vector bucket.

bbolt holds an exclusive OS-level file lock. CLI commands retry with a 2-second timeout and print a clear error if the daemon holds the lock: `open store psst.db: timeout (is psst serve running?)`.

---

## Building from Source

### Prerequisites

- Go 1.22+
- A C compiler (`gcc` or `clang`) — required for the ONNX Runtime CGo bindings
- ONNX Runtime shared library:
  - macOS: `brew install onnxruntime`
  - Linux: download from the [ONNX Runtime releases](https://github.com/microsoft/onnxruntime/releases) and place `libonnxruntime.so` somewhere on `LD_LIBRARY_PATH`

### 1. Fetch the embedding model (one-time)

```bash
make fetch-model
```

Downloads `all-MiniLM-L6-v2` (~90MB ONNX model) and its tokenizer from HuggingFace into:

```
internal/embedder/model/model.onnx
internal/tokenizer/data/tokenizer.json
```

Both paths are gitignored. They must be present before any build that uses the model.

### 2. Dev build (dynamically links ONNX Runtime)

```bash
go build -o psst .
# or
make build
```

The dev binary does **not** embed the model. Point it to the files at runtime via environment variables:

```bash
export PSST_MODEL_PATH=internal/embedder/model/model.onnx
export PSST_TOKENIZER_PATH=internal/tokenizer/data/tokenizer.json
./psst serve
```

Without these variables, psst runs in exact-match-only mode — semantic lookup is disabled gracefully.

### 3. Release build (model embedded in binary)

```bash
make build-release
```

Compiles with `-tags embedmodel`, which activates `//go:embed` directives for the model and tokenizer. The result is a single self-contained binary (~110MB) that requires no environment variables and no external files at runtime.

```bash
./psst serve   # just works, semantic lookup enabled
```

### Cross-compilation

```bash
make build-darwin-arm64
make build-darwin-amd64
make build-linux-amd64
make build-linux-arm64
```

### Versioning

The version string is injected at build time from the current git tag:

```bash
./psst --version
# psst v1.0.0
```

Untagged builds report the nearest tag with a commit suffix (e.g. `v1.0.0-3-gabcdef1`) or `dev` if there are no tags. To cut a release:

```bash
make tag VERSION=v1.0.0
git push origin v1.0.0
make build-release
```

---

## Roadmap

### v1.1
- `psst evict --expired` — manual sweep without daemon
- Per-tool semantic threshold overrides in config
- Homebrew distribution tap

### v2.0
- **HNSW index** — switch from flat cosine scan to approximate nearest-neighbour for large caches (>2,000 entries per tool)
- **`psst export`** — dump cache contents to JSON for inspection or migration
- **Daemon auto-start** — CLI commands start the daemon automatically if not running, for zero-configuration use
- **Cache sharing** — optional network mode for sharing a single cache across multiple machines on the same local network

---

## Colophon

Psst was designed and built through conversational AI — specifically through an extended session with **Claude** (Anthropic), using Claude Code as the development environment. It is a companion project to [Riffle](https://github.com/allank/riffle), which follows the same pattern and shares several internal packages.

**Language & runtime**
- [Go](https://go.dev/) 1.22+ — single-binary distribution, good concurrency primitives, CGo interop for ONNX Runtime

**Persistence**
- [bbolt](https://github.com/etcd-io/bbolt) — embedded, transactional key-value store; exclusive file lock prevents concurrent writes from CLI and daemon

**Embedding**
- [all-MiniLM-L6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) (sentence-transformers) — 384-dimensional sentence embedding model; good balance of quality, size (~90MB ONNX), and inference speed
- [ONNX Runtime](https://onnxruntime.ai/) — cross-platform ML inference engine; used via [`yalue/onnxruntime_go`](https://github.com/yalue/onnxruntime_go) CGo bindings

**CLI & terminal UI**
- [Cobra](https://github.com/spf13/cobra) — command structure and flag parsing
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) — styled terminal output (pretty hit/miss indicators, status panels)
- [Charmbracelet Log](https://github.com/charmbracelet/log) — structured, styled logging in the daemon

**Configuration & testing**
- [BurntSushi/toml](https://github.com/BurntSushi/toml) — TOML config file parsing
- [testify](https://github.com/stretchr/testify) — test assertions and requirements

**Development tooling**
- [Claude Code](https://claude.ai/code) — the AI coding environment in which this project was built
- [Claude Sonnet 4.6](https://www.anthropic.com/) — the model that wrote the code
