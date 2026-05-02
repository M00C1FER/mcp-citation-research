# Reference Projects

Studied during the senior-review pass on 2026-05-02. Selected for diversity of
language, maturity, and overlap with this repo's concerns (MCP tool surface,
search/fetch resilience, citation / BM25 ranking, backoff patterns).

---

## 1. modelcontextprotocol/servers (TypeScript)

**Repo**: https://github.com/modelcontextprotocol/servers  
**Stars**: ~6 k · MIT · actively maintained  
**Relevant pattern**: Each MCP tool ships a **complete JSON-Schema `inputSchema`**
alongside the handler — parameters, types, descriptions, and `required` lists are
all machine-readable. FastMCP auto-generates the schema from Python type hints, but
explicit `description` text in the parameter type annotations is still required for
the LLM to use tools correctly. The reference shows that even trivial parameters
like `timeout_seconds: int` carry a one-line description in the schema.

**Adopted here**: Expanded MCP tool docstrings so every parameter and return value
is documented. FastMCP surfaces these as the tool's help text to the calling LLM.

---

## 2. cline/cline (TypeScript)

**Repo**: https://github.com/cline/cline  
**Stars**: ~37 k · Apache-2.0 · very active  
**Relevant pattern**: Cline's web-search tool wraps HTTP calls with a
**retry-with-backoff helper** that distinguishes transient errors (429, 502, 503,
504, connection-reset) from permanent ones (400, 401, 403, 404). Transient errors
are retried up to 3 times with exponential back-off (`baseMs * 2^attempt`) plus
uniform random jitter (`± baseMs / 2`). The retry helper is also applied to its
SearXNG integration.

**Adopted here**: SearXNG.Search and fetchOne both gained a three-attempt
exponential-backoff loop for the same set of transient codes.

---

## 3. exaai/exa-py (Python)

**Repo**: https://github.com/exa-ai/exa-py  
**Stars**: ~350 · MIT · active  
**Relevant pattern**: The SDK raises a typed `ExaRateLimitError` / `ExaAPIError`
when the upstream returns 429 / 5xx, and the retry decorator inspects the
`Retry-After` response header to avoid sleeping longer than needed. The HTTP client
is also shared at session scope (`requests.Session`) rather than re-created per
call.

**Adopted here**: The Go daemon's `SearXNG.Search` now respects the `Retry-After`
header when present on a 429 response. The Python `DaemonClient` already shared a
`requests.Session`; no change needed there.

---

## 4. serpapi/google-search-results-python (Python)

**Repo**: https://github.com/serpapi/google-search-results-python  
**Stars**: ~720 · MIT · active  
**Relevant pattern**: SerpApi's client hard-caps result pages at the API limit and
converts the raw `"error"` field in a JSON 200-response to a Python exception. The
SearXNG JSON API can return `{"error": "..."}` on quota exhaustion even with HTTP
200 — this pattern prevents silently returning zero results in that case.

**Adopted here**: `SearXNG.Search` now checks for a top-level `"error"` field in
the JSON payload and returns an error when it is non-empty, so callers can
distinguish quota exhaustion from genuine "no results".

---

## 5. searxng/searxng (Python daemon reference)

**Repo**: https://github.com/searxng/searxng  
**Stars**: ~18 k · AGPL-3.0 · very active  
**Relevant pattern**: SearXNG documents that it returns HTTP **429** when the
instance is rate-limited by an upstream engine, and HTTP **502/504** during
engine-level timeouts. Both are transient and safe to retry after a short wait.
The project's own integration tests mock these status codes explicitly to verify
client resilience.

**Adopted here**: The retry loop added to `SearXNG.Search` explicitly targets
these documented transient codes.

---

## 6. golang/go (static CGO_ENABLED=0 builds)

**Repo**: https://github.com/golang/go  
**Stars**: ~125 k · BSD-3-Clause · active  
**Relevant pattern**: Setting `CGO_ENABLED=0` at build time produces a fully
static binary that links against the Go runtime only (no libc dependency). This
is the canonical way to produce binaries that run identically on glibc distros
(Debian, Ubuntu), musl distros (Alpine, Termux Bionic), and container scratch
images. The Go standard library's `net` package includes a pure-Go DNS resolver
(`netgo`) that activates automatically when CGO is disabled.

**Adopted here**: `install.sh`, `scripts/install-termux.sh`, and the CI
`go-cross-build` job all use `CGO_ENABLED=0`. The Alpine and Termux CI jobs
verify the static build path explicitly.

---

## 7. nicowillis/go-alpine (Alpine/musl CI patterns)

**Repo**: https://github.com/nicowillis/go-alpine (representative of the
`golang:*-alpine` Docker image family published by the Go team)  
**Stars**: N/A — pattern taken from the official `golang:1.x-alpine` Docker image
documentation and Go team release notes  
**Relevant pattern**: The official `golang:x.y-alpine` Docker image is the
standard way to verify Go binaries on musl libc in CI. The Go race detector
requires CGO and glibc; it must be skipped (`go test ./...` without `-race`) in
Alpine containers. Pure Alpine-compatible Go packages work without modification
as long as no cgo shims are used.

**Adopted here**: `go-daemon-alpine` CI job uses `golang:1.23-alpine` and skips
`-race` with an explanatory comment.

---

## Summary of patterns adopted

| Pattern | Source | Location in this repo |
|---|---|---|
| Retry + backoff for 429/5xx | cline/cline, exa-py | `daemon/internal/search/search.go` |
| Retry + backoff for fetch errors | cline/cline | `daemon/internal/fetch/fetch.go` |
| Respect `Retry-After` on 429 | exa-py | `daemon/internal/search/search.go` |
| Check JSON `"error"` field | serpapi | `daemon/internal/search/search.go` |
| Explicit HTTP status check | all five | `daemon/internal/search/search.go` |
| Full parameter docs in MCP tools | modelcontextprotocol/servers | `server/citation_research/mcp_server.py` |
| `CGO_ENABLED=0` static binary | golang/go | `install.sh`, `scripts/install-termux.sh`, CI |
| Alpine/musl CI pattern (skip -race) | golang Alpine image | `.github/workflows/ci.yml` |
