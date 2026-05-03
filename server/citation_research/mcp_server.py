"""mcp-citation-research MCP server.

Exposes the atomic research-loop tools to any MCP client:

  research_session_open / _update / _status / _close — mandate tracking
  research_search                                     — multi-engine RRF (via daemon)
  research_fetch                                      — concurrent extraction (via daemon)
  research_decompose                                  — sub-question generation (heuristic; LLM-optional)
  research_verify                                     — citation coverage + groundedness
  research_cite                                       — BM25 [S#] injection

Synthesis stays inline in the calling MCP client's context window — this server
intentionally does NOT call an LLM for synthesis. The caller (Claude Code,
Cursor, Continue.dev) reads the fetched sources and synthesizes themselves.
"""
from __future__ import annotations

import argparse
import json
import os
from typing import Any, Dict, List

try:
    from fastmcp import FastMCP
except ImportError as e:  # pragma: no cover
    raise SystemExit("fastmcp not installed. Run: pip install citation-research[mcp]") from e

from . import DaemonClient, bm25_cite, verify_synthesis


mcp = FastMCP("citation-research")
_client = DaemonClient()

# Hard confidence gate — not caller-configurable to prevent bypass.
# Override at deployment time via CITATION_RESEARCH_GATE env variable.
_HARD_GATE = float(os.environ.get("CITATION_RESEARCH_GATE", "0.90"))


def _parse_sources(sources_json: str | None) -> List[Dict[str, Any]]:
    """Safely parse a JSON sources list.

    Returns an empty list on None, empty string, non-list JSON, or invalid JSON,
    so callers never receive None or a TypeError downstream.
    """
    if not sources_json:
        return []
    try:
        result = json.loads(sources_json)
    except json.JSONDecodeError:
        return []
    if not isinstance(result, list):
        return []
    return result


@mcp.tool()
def research_session_open(topic: str, depth: str = "exhaustive") -> str:
    """Open a research session and return a session token.

    Args:
        topic: The research question or subject to investigate.
        depth: Mandate depth preset. One of:
            - "exhaustive" (default): 10 iterations · 400 considered · 100 fetched · 15 unique domains
            - "standard": lighter mandate for shorter research tasks

    Returns:
        JSON object: {session_id: str, topic: str, depth: str, mandate: {iterations, considered, fetched, unique_domains}}
    """
    state = _client.session_open(topic, depth)
    return json.dumps(state)


@mcp.tool()
def research_session_update(session_id: str, iteration: int,
                            considered_urls: List[str], fetched_urls: List[str]) -> str:
    """Record progress after a search/fetch round and check mandate compliance.

    Call this after every iteration of the research loop to track progress toward
    the four-axis mandate (iterations, sources considered, sources fetched, unique domains).

    Args:
        session_id: Token returned by research_session_open.
        iteration: Current iteration number (1-based).
        considered_urls: All URLs surfaced by search in this iteration (not just fetched ones).
        fetched_urls: URLs whose full content was fetched and extracted in this iteration.

    Returns:
        JSON object: {ok: bool, iteration: int, sources_considered: int,
                      sources_fetched: int, unique_domains: int, mandate_met: bool}
    """
    return json.dumps(_client.session_update(session_id, iteration, considered_urls, fetched_urls))


@mcp.tool()
def research_session_status(session_id: str) -> str:
    """Return current mandate metrics for an active session.

    Args:
        session_id: Token returned by research_session_open.

    Returns:
        JSON object: {session_id: str, topic: str, depth: str,
                      iteration: int, sources_considered: int, sources_fetched: int,
                      unique_domains: int, mandate_met: bool}
    """
    return json.dumps(_client.session_status(session_id))


@mcp.tool()
def research_session_close(session_id: str) -> str:
    """Close a research session and return final metrics.

    Args:
        session_id: Token returned by research_session_open.

    Returns:
        JSON object with final session metrics (same fields as research_session_status).
    """
    return json.dumps(_client.session_close(session_id))


@mcp.tool()
def research_search(queries: List[str], max_per_query: int = 50, k: int = 60) -> str:
    """Fan-out multi-engine RRF search across all configured engines.

    Issues all queries against every configured engine concurrently and fuses
    results using Reciprocal Rank Fusion (RRF). Duplicate URLs are merged; the
    fused score reflects agreement across engines and query variants.

    Args:
        queries: One or more search queries to issue in parallel. Providing
            multiple focused sub-questions (from research_decompose) yields
            better recall than a single broad query.
        max_per_query: Per-engine result cap per query (default 50, max ~100).
        k: RRF smoothing constant (default 60). Higher k gives more weight to
            top-ranked results; lower k is more aggressive about promoting
            results that appear in many engines.

    Returns:
        JSON object: {results: [{url: str, title: str, snippet: str,
                                 engine: str, score: float}], total: int}
    """
    return json.dumps(_client.search(queries, max_per_query, k))


@mcp.tool()
def research_fetch(urls: List[str], max_concurrent: int = 16, timeout_s: int = 30) -> str:
    """Fetch and extract readable text from URLs in parallel.

    Performs bounded-concurrency HTTP GET requests with readability-style HTML
    extraction. Non-HTML responses (PDF, plain text) are returned as-is up to
    512 KiB. SSRF-protected: private/loopback addresses are rejected.

    Transient server errors (429, 502, 503, 504) are retried up to 3 times
    with exponential back-off before marking a page as failed.

    Args:
        urls: List of URLs to fetch. Loopback and RFC-1918 addresses are rejected.
        max_concurrent: Maximum simultaneous in-flight requests (default 16).
        timeout_s: Per-URL fetch timeout in seconds (default 30, max 600).

    Returns:
        JSON object: {pages: [{url: str, title: str, text: str, ok: bool,
                               error: str|null, tier: int}], total: int}
        tier codes: 1=SSRF/scheme rejected, 2=network error, 3=HTTP error,
                    4=non-HTML content, 5=HTML extracted.
    """
    return json.dumps(_client.fetch(urls, max_concurrent, timeout_s))


@mcp.tool()
def research_decompose(topic: str, breadth: int = 8) -> str:
    """Generate heuristic sub-questions to guide multi-angle research.

    Produces a fixed set of aspect-covering sub-questions for the topic. This
    is a deterministic heuristic with no LLM call — for production research
    the calling CLI (Claude, Cursor, Copilot) should override with LLM-grade
    decomposition before issuing searches.

    Args:
        topic: The research topic to decompose.
        breadth: Number of sub-questions to return (1–8, default 8).

    Returns:
        JSON object: {sub_questions: [str], rationale: str}
    """
    aspects = [
        f"What is {topic}?",
        f"Why does {topic} matter? Who is affected?",
        f"Current state-of-the-art for {topic}",
        f"Best alternatives to {topic}",
        f"Common pitfalls / failure modes of {topic}",
        f"Key open questions about {topic}",
        f"Recent developments in {topic} (last 12 months)",
        f"Verifiable claims and counter-claims about {topic}",
    ]
    return json.dumps({
        "sub_questions": aspects[:breadth],
        "rationale": "Heuristic decomposition. CLI should override with LLM-grade decomposition for production work.",
    })


@mcp.tool()
def research_verify(synthesis: str, sources_json: str) -> str:
    """Verify synthesis quality against fetched sources.

    Computes two complementary scores:
    - **Citation coverage**: fraction of synthesis paragraphs that already carry
      a [S#] tag.
    - **Token groundedness**: fraction of content tokens in the synthesis that
      also appear in the source corpus (stop-words excluded).

    The overall confidence score is their arithmetic mean. It is compared against
    the hard gate threshold (default 0.90, configurable via the
    CITATION_RESEARCH_GATE environment variable at deployment time — not
    caller-configurable to prevent bypass).

    Args:
        synthesis: The synthesised text to verify. May contain [S#] citation tags.
        sources_json: JSON-encoded list of source objects, each with at minimum
            a "content" field and optionally "title" and "url".
            Example: '[{"url": "https://…", "title": "…", "content": "…"}]'

    Returns:
        JSON object: {confidence: float, citation_coverage: float,
                      groundedness: float, claims_verified: int,
                      claims_supported: int, verdict: "high"|"medium"|"low",
                      confidence_gate_passed: bool,
                      confidence_gate_threshold: float,
                      confidence_gate_message: str}
    """
    sources = _parse_sources(sources_json)
    return json.dumps(verify_synthesis(synthesis, sources, _HARD_GATE))


@mcp.tool()
def research_cite(synthesis: str, sources_json: str,
                  insert_threshold: float = 3.5, verify_threshold: float = 1.0) -> str:
    """Inject and verify BM25 [S#] citation tags in a synthesis.

    For each paragraph in the synthesis:
    - If a paragraph already contains [S#] tags, each tag is checked: tags whose
      BM25 score against the corresponding source falls below verify_threshold
      are rewritten as [S# UNVERIFIED].
    - If a paragraph has no citation and its best-matching source scores ≥
      insert_threshold, a [S#] tag is appended automatically.

    Args:
        synthesis: The synthesised text to annotate.
        sources_json: JSON-encoded list of source objects with "content", "title",
            and "url" fields (same format as research_verify).
        insert_threshold: BM25 score required to auto-insert a citation tag
            (default 3.5). Raise to be more conservative; lower to cite more.
        verify_threshold: BM25 score below which an existing [S#] tag is flagged
            as [UNVERIFIED] (default 1.0).

    Returns:
        JSON object: {cited_text: str, inserted: int, flagged: int,
                      source_list: [{num: int, url: str, title: str}],
                      coverage_pct: float}
    """
    sources = _parse_sources(sources_json)
    return json.dumps(bm25_cite(synthesis, sources, insert_threshold, verify_threshold))


@mcp.tool()
def research_health() -> str:
    """Check liveness of the MCP frontend and the Go daemon.

    Returns:
        JSON object: {daemon: "ok"|"unreachable", frontend: "ok", error?: str}
    """
    try:
        return json.dumps({"daemon": _client.healthz(), "frontend": "ok"})
    except Exception as e:
        return json.dumps({"daemon": "unreachable", "error": str(e), "frontend": "ok"})


def run() -> None:
    parser = argparse.ArgumentParser(description="mcp-citation-research MCP server")
    parser.add_argument("--http", metavar="HOST:PORT", help="HTTP transport (default: stdio)")
    args = parser.parse_args()
    if args.http:
        host, _, port = args.http.partition(":")
        mcp.run(transport="http", host=host or os.environ.get("CITATION_MCP_HOST", "127.0.0.1"), port=int(port or os.environ.get("CITATION_MCP_PORT", "8091")))
    else:
        mcp.run()


if __name__ == "__main__":
    run()
