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
from typing import Any, Dict, List

try:
    from fastmcp import FastMCP
except ImportError as e:  # pragma: no cover
    raise SystemExit("fastmcp not installed. Run: pip install citation-research[mcp]") from e

from . import DaemonClient, bm25_cite, verify_synthesis


mcp = FastMCP("citation-research")
_client = DaemonClient()


@mcp.tool()
def research_session_open(topic: str, depth: str = "exhaustive") -> str:
    """Open a research session. Returns {session_id, topic, depth, mandate}."""
    state = _client.session_open(topic, depth)
    return json.dumps(state)


@mcp.tool()
def research_session_update(session_id: str, iteration: int,
                            considered_urls: List[str], fetched_urls: List[str]) -> str:
    """Update session metrics after a search/fetch round."""
    return json.dumps(_client.session_update(session_id, iteration, considered_urls, fetched_urls))


@mcp.tool()
def research_session_status(session_id: str) -> str:
    """Return current session metrics + mandate compliance status."""
    return json.dumps(_client.session_status(session_id))


@mcp.tool()
def research_session_close(session_id: str) -> str:
    """Close a research session."""
    return json.dumps(_client.session_close(session_id))


@mcp.tool()
def research_search(queries: List[str], max_per_query: int = 50, k: int = 60) -> str:
    """Multi-engine RRF search. Returns {results: [{url, title, snippet, engine, score}]}.

    Engines configured at the daemon level (default: SearXNG). RRF k=60.
    """
    return json.dumps(_client.search(queries, max_per_query, k))


@mcp.tool()
def research_fetch(urls: List[str], max_concurrent: int = 16, timeout_s: int = 30) -> str:
    """Concurrent URL fetch with text extraction. Returns {pages: [{url, title, content, word_count, ok}]}.

    Bounded parallelism. Readability-style extraction.
    """
    return json.dumps(_client.fetch(urls, max_concurrent, timeout_s))


@mcp.tool()
def research_decompose(topic: str, breadth: int = 8) -> str:
    """Heuristic sub-question generation (no LLM). For LLM-grade decomposition,
    have the calling CLI (Claude/Gemini/Copilot) decompose itself before calling search.

    Returns {sub_questions: [str], rationale: str}.
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
def research_verify(synthesis: str, sources_json: str, gate: float = 0.90) -> str:
    """Verify synthesis against sources. Returns confidence + gate result."""
    sources: List[Dict[str, Any]] = json.loads(sources_json) if sources_json else []
    return json.dumps(verify_synthesis(synthesis, sources, gate))


@mcp.tool()
def research_cite(synthesis: str, sources_json: str,
                  insert_threshold: float = 3.5, verify_threshold: float = 1.0) -> str:
    """BM25 citation injection. Returns {cited_text, inserted, flagged, source_list, coverage_pct}."""
    sources: List[Dict[str, Any]] = json.loads(sources_json) if sources_json else []
    return json.dumps(bm25_cite(synthesis, sources, insert_threshold, verify_threshold))


@mcp.tool()
def research_health() -> str:
    """Check daemon health."""
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
        mcp.run(transport="http", host=host or "127.0.0.1", port=int(port or 8091))
    else:
        mcp.run()


if __name__ == "__main__":
    run()
