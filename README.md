# mcp-citation-research

> **MCP research server that refuses to hallucinate.** Hard 0.90 confidence gate + four-axis source-mandate enforcement (iterations, considered, fetched, unique domains) + BM25 sentence-chunk citation injection. The session API **refuses to advance** to synthesis until the mandate is met — most public research MCP servers will happily synthesize from 3 sources; this one won't.
>
> Hybrid Go + Python by design: I/O daemon in Go (native goroutines, single binary), Python MCP frontend for verify and cite (rank_bm25, FastMCP). Synthesis stays inline in the calling MCP client's context window — your model does the writing, this server enforces the discipline.

[![CI](https://github.com/M00C1FER/mcp-citation-research/actions/workflows/ci.yml/badge.svg)](https://github.com/M00C1FER/mcp-citation-research/actions)
![Go](https://img.shields.io/badge/go-1.22+-blue)
![Python](https://img.shields.io/badge/python-3.10+-blue)
![License](https://img.shields.io/badge/license-MIT-green)

## What's actually unique here

After a survey of 70+ MCP servers in the [official registry](https://registry.modelcontextprotocol.io) and adjacent ecosystems (Smithery, Glama, awesome-mcp-servers @ 85k★), the differentiators are:

1. **Hard mandate enforcement** — the four-axis floor (10 iter / 400 considered / 100 fetched / 15 unique domains for `exhaustive` depth) is a *refusal contract*, not a suggestion. Other research MCPs let the model decide when to stop; this one decides *for* the model.
2. **BM25 sentence-chunked citations** — every paragraph of synthesis gets a `[S#]` tag verified against fetched source content. Unsupported paragraphs flagged `[UNVERIFIED]` automatically.
3. **0.90 confidence gate** — citation coverage + token groundedness blended; falls below the gate, the verifier surfaces it.
4. **Hybrid Go+Python** — most MCP research servers are pure Python (DeepResearchMCP, Perplexity proxies, etc.). This one demonstrates the right language at the right boundary.

## Why hybrid Go + Python

| Layer | Language | Why |
|---|---|---|
| Daemon (search/fetch/iterate/session) | **Go** | I/O-bound; native goroutines beat Python asyncio; single 9.7 MB binary |
| MCP frontend (verify/cite + tool surface) | **Python** | LLM-bound; Anthropic SDK ecosystem; `rank_bm25` + FastMCP |

Networking → Go (native goroutines, single static binary). LLM-orchestration → Python (Anthropic SDK ecosystem). Most MCP servers are pure Python; this one splits at the right boundary.

## What it does

- **Atomic loop tools**: `research_session_open` / `_update` / `_status` / `_close`, `research_search`, `research_fetch`, `research_decompose`, `research_verify`, `research_cite`.
- **Hard mandate gate** (default `exhaustive`): 10 iterations · 400 considered · 100 fetched · 15 unique domains. The session API refuses to advance without these axes cleared.
- **BM25 citation injection** — every paragraph gets `[S#]` if it matches a source above threshold; existing `[S#]` is verified or flagged `[UNVERIFIED]`.
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
