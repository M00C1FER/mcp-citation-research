# Copilot Coding Agent — Instructions

## Project context

`mcp-citation-research` is a hard-mandate research MCP server. It refuses to synthesize until a 4-axis source floor is met (10 iter / 400 considered / 100 fetched / 15 unique domains for `exhaustive` depth). BM25 sentence-chunked citation injection, 0.90 confidence gate. Hybrid Go I/O daemon + Python MCP frontend.

The genuine differentiation vs other research MCPs (paper-qa, deep-research-mcp, gptr-mcp, etc.) is the refusal-contract pattern: most servers happily synthesize from 3 sources; this one refuses until the floor clears.

## Coding rules

- Go 1.23+ for daemon; Python 3.10+ for MCP frontend.
- Daemon is single static binary; do NOT add CGO dependencies that break cross-compilation.
- Python frontend uses `FastMCP` + `rank_bm25`; do not introduce LangChain/LlamaIndex.
- Type hints on every public function; Python `mypy --strict` clean ideally.
- Imports sorted: stdlib, third-party, local.
- Buildvcs is enabled; CI on Debian-bookworm + Alpine + Ubuntu must all stay green (recent fix in PR #19 — see `.github/workflows/ci.yml`).

## Tests

- Every new public function: unit test.
- Daemon: `go test -race ./...` from `daemon/` directory.
- Frontend: `pytest` from `python/` directory.
- New tools added to MCP frontend: smoke test via `tools/list` + a roundtrip call.

## Don't touch

- `.github/workflows/ci.yml` Debian safe-directory step (recent fix; do not regress).
- The 4-axis floor constants (`10 / 400 / 100 / 15`) — these are doctrine.
- BM25 algorithm choice — keep `rank_bm25`; do not switch to embeddings without an issue specifically requesting it.

## Acceptance signal

A PR is ready for review when:
1. All CI matrix cells green (Go: bookworm + alpine + ubuntu × 22/24/latest; Python: 3.10-3.13 × ubuntu 22/24).
2. New tools have working roundtrip test.
3. README accurately reflects the tool list.
