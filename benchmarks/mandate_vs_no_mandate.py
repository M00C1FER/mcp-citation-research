#!/usr/bin/env python3
"""Benchmark harness for mandate-enforced vs single-pass research."""
from __future__ import annotations

import json
from pathlib import Path
import re
import sys
from typing import Dict, List

ROOT = Path(__file__).resolve().parents[1]
SERVER_ROOT = ROOT / "server"
if str(SERVER_ROOT) not in sys.path:
    sys.path.insert(0, str(SERVER_ROOT))

from citation_research import DaemonClient, bm25_cite  # noqa: E402
from citation_research.mcp_server import research_decompose  # noqa: E402


def _normalise_pages(pages: List[Dict]) -> List[Dict]:
    normalised = []
    for page in pages:
        if not page.get("ok"):
            continue
        normalised.append(
            {
                "url": page.get("url", ""),
                "title": page.get("title", ""),
                "content": page.get("text", ""),
            }
        )
    return normalised


def _claim_text(topic: str, pages: List[Dict], max_claims: int = 5) -> str:
    claims = []
    for page in pages[:max_claims]:
        summary = " ".join(page.get("content", "").split())[:180]
        title = page.get("title") or page.get("url") or topic
        claims.append(f"- {title}: {summary}")
    return "\n\n".join(claims) if claims else f"- No fetched sources for {topic}"


def _citation_density(text: str) -> float:
    claims = [paragraph for paragraph in text.split("\n\n") if paragraph.strip()]
    citations = len(re.findall(r"\[S\d+(?:\s+UNVERIFIED)?\]", text))
    return citations / max(len(claims), 1)


def _overlap_score(left: str, right: str) -> float:
    left_tokens = set(re.findall(r"[a-zA-Z0-9_+-]+", left.lower()))
    right_tokens = set(re.findall(r"[a-zA-Z0-9_+-]+", right.lower()))
    if not left_tokens and not right_tokens:
        return 1.0
    return len(left_tokens & right_tokens) / max(len(left_tokens | right_tokens), 1)


def _run_flow(topic: str, queries: List[str], max_results: int, max_fetch: int) -> Dict:
    client = DaemonClient()
    results = client.search(queries, max_per_query=max_results, k=60).get("results", [])
    urls = [result.get("url", "") for result in results[:max_fetch] if result.get("url")]
    pages = _normalise_pages(client.fetch(urls, max_concurrent=4, timeout_s=30).get("pages", []))
    draft = _claim_text(topic, pages)
    cited = bm25_cite(draft, pages)
    return {
        "queries": queries,
        "unique_sources": len({page["url"] for page in pages if page.get("url")}),
        "synthesis": cited["cited_text"],
        "citation_density": _citation_density(cited["cited_text"]),
    }


def main(argv: List[str]) -> int:
    if len(argv) < 2:
        print('usage: python benchmarks/mandate_vs_no_mandate.py "your query"', file=sys.stderr)
        return 1

    topic = argv[1]
    mandate_queries = json.loads(research_decompose(topic, breadth=5, use_mcts=True))["sub_questions"]
    mandate_run = _run_flow(topic, mandate_queries, max_results=10, max_fetch=10)
    single_pass_run = _run_flow(topic, [topic], max_results=5, max_fetch=3)

    result = {
        "topic": topic,
        "mandate_enforced": {
            "unique_sources": mandate_run["unique_sources"],
            "citation_density": mandate_run["citation_density"],
            "queries": mandate_run["queries"],
        },
        "single_pass": {
            "unique_sources": single_pass_run["unique_sources"],
            "citation_density": single_pass_run["citation_density"],
            "queries": single_pass_run["queries"],
        },
        "overlap_score": _overlap_score(
            mandate_run["synthesis"],
            single_pass_run["synthesis"],
        ),
    }

    results_dir = ROOT / "benchmarks" / "results"
    results_dir.mkdir(parents=True, exist_ok=True)
    slug = re.sub(r"[^a-z0-9]+", "-", topic.lower()).strip("-") or "benchmark"
    output_path = results_dir / f"{slug}.json"
    output_path.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))
    print(f"\nWrote benchmark results to {output_path}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
