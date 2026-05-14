# mcp-citation-research

> MCP research server with **hard 0.90 confidence gate** + **BM25 sentence-chunk citations**. Hybrid architecture: Go daemon for I/O (search, fetch, session state), Python MCP frontend for verify and cite. Synthesis stays inline in the calling MCP client's context window.

[![CI](https://github.com/M00C1FER/mcp-citation-research/actions/workflows/ci.yml/badge.svg)](https://github.com/M00C1FER/mcp-citation-research/actions)

## Why hybrid Go + Python

| Layer | Language | Why |
|---|---|---|
| Daemon (search/fetch/iterate/session) | **Go** | I/O-bound; native goroutines beat Python asyncio; single 9.7 MB binary |
| MCP frontend (verify/cite + tool surface) | **Python** | LLM-bound; Anthropic SDK ecosystem; `rank_bm25` + FastMCP |

Networking → Go (native goroutines, single static binary). LLM-orchestration → Python (Anthropic SDK ecosystem). Most MCP servers are pure Python; this one splits at the right boundary.

## What it does

- **Atomic loop tools**: `research_session_open` / `_update` / `_status` / `_close`, `research_search`, `research_fetch`, `research_decompose`, `research_verify`, `research_cite`.
- **Hard mandate gate** (default `exhaustive`): 10 iterations · 400 considered · 100 fetched · 15 unique domains. The session API refuses to advance without these axes cleared.
- **BM25 + cross-encoder rerank citation injection** — BM25 retrieves candidates, then `BAAI/bge-reranker-v2-m3` reranks before final `[S#]` assignment (set `CITATION_RESEARCH_RERANK=0` to disable).
- **0.90 confidence gate** — citation coverage + token groundedness; returns `confidence_gate_passed: bool`.
- **CLI-orchestrated** by design — synthesis happens in the calling MCP client's context window (Claude Code, Cursor, Continue.dev), not in the server. This preserves the caller's full 1M-token context.

## Architecture

```mermaid
graph TB
    A[MCP Client<br/>Claude Code / Cursor] -->|tools/call| B[citation-research MCP server<br/>Python · FastMCP]
    B -->|HTTP| C[citation-researchd<br/>Go · 9.7 MB binary]
    C -->|/search| D[SearXNG · port 8080]
    C -->|/fetch| E[net/http + golang.org/x/net/html]
    C -->|session state| F[(in-memory)]
    B -->|verify/cite<br/>(no daemon)| G[rank_bm25 in Python]
```

## Quick start

### Build the daemon
```bash
cd daemon
go build -o /usr/local/bin/citation-researchd ./cmd/citation-researchd
citation-researchd -addr 127.0.0.1:8090 -searxng http://127.0.0.1:8080 &
```

### Install the MCP server
```bash
cd server
pip install -e .
```

### Wire to Claude Desktop
```json
{
  "mcpServers": {
    "citation-research": {
      "command": "citation-research-mcp",
      "env": { "CITATION_RESEARCHD_URL": "http://127.0.0.1:8090" }
    }
  }
}
```

### Reranker memory/latency tradeoff

- Default is **enabled** (`CITATION_RESEARCH_RERANK=1`).
- Disable via `CITATION_RESEARCH_RERANK=0` for BM25-only low-latency mode.
- Default model is `BAAI/bge-reranker-v2-m3`; if that model fails to load/download, the server falls back to `BAAI/bge-reranker-base`.
- Expect roughly +600 MB model footprint and higher per-query latency when reranking is enabled.

## Tool reference

| Tool | Purpose |
|---|---|
| `research_session_open` | Start a session; returns `session_id` |
| `research_session_update` | Record iteration metrics; tracks four-axis mandate |
| `research_session_status` | Current metrics + `mandate_met: bool` |
| `research_session_close` | End session; final metrics |
| `research_search` | Multi-engine RRF (k=60); SearXNG default |
| `research_fetch` | Concurrent extraction; 16-way bounded parallelism |
| `research_decompose` | Heuristic sub-question generation (CLI should override) |
| `research_verify` | Citation coverage + groundedness; gate at 0.90 |
| `research_cite` | BM25 `[S#]` injection + `[UNVERIFIED]` flagging |
| `research_health` | Frontend + daemon liveness |

## Performance

Paper-reported figures from arXiv:2505.23250 (Deep Retrieval at CheckThat! 2025), **not measured on this repo's corpus**:

| Configuration | MRR@5 | Latency (ms) | Memory (MB) |
|---|---|---|---|
| BM25 only | 0.62 | 8 | 50 |
| BM25 + bge-reranker-v2-m3 | 0.71 | 180 | 650 |

## Comparison vs alternatives

| Server | Confidence gate | BM25 citations | Mandate enforcement | I/O language |
|---|:-:|:-:|:-:|:-:|
| `mcp-perplexity-ask` | ❌ | ❌ | ❌ | proxy |
| `mcp-server-fetch` | ❌ | ❌ | ❌ | Python |
| `chroma-mcp` | ❌ | partial | ❌ | Python |
| `pinecone-mcp` | ❌ | ❌ | ❌ | Python |
| **`mcp-citation-research`** | **✅ 0.90** | **✅ sentence-chunked** | **✅ 4-axis hard gate** | **Go daemon + Python frontend** |

## Testing

```bash
# Go daemon
cd daemon && go test ./...

# Python frontend
cd server && pip install -e .[dev] && pytest
```

## License

MIT.
