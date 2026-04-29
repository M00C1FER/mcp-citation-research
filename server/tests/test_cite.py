"""Tests for the BM25 citation agent."""
from __future__ import annotations

import pytest

from citation_research import bm25_cite, verify_synthesis


@pytest.fixture
def sources():
    return [
        {
            "url": "https://a.com/llm",
            "title": "Large language models",
            "content": "Large language models are trained on extensive text corpora using transformer architecture and self-supervised learning. Modern LLMs include GPT, Claude, Gemini, and Llama families.",
        },
        {
            "url": "https://b.com/rag",
            "title": "Retrieval augmented generation",
            "content": "Retrieval augmented generation grounds LLM outputs in retrieved documents. BM25 is a classical sparse retrieval baseline that remains competitive for many tasks.",
        },
        {
            "url": "https://c.com/mcp",
            "title": "Model context protocol",
            "content": "The Model Context Protocol is an open standard for connecting LLMs to external tools and data. MCP servers expose tools, resources, and prompts.",
        },
    ]


def test_bm25_cite_inserts_citations(sources):
    synthesis = (
        "Large language models are trained on extensive text corpora.\n\n"
        "BM25 is a classical sparse retrieval baseline.\n\n"
        "MCP servers expose tools and resources to LLMs."
    )
    result = bm25_cite(synthesis, sources, insert_threshold=0.5)
    assert result["inserted"] >= 2
    assert "[S1]" in result["cited_text"] or "[S2]" in result["cited_text"]
    assert result["coverage_pct"] > 0
    assert len(result["source_list"]) == 3


def test_bm25_cite_no_sources():
    result = bm25_cite("hello world", [])
    assert result["inserted"] == 0
    assert result["cited_text"] == "hello world"


def test_bm25_cite_flags_unverified(sources):
    # Existing [S1] (LLM source) on a paragraph that is clearly about MCP not LLM
    synthesis = "MCP servers expose tools to LLMs via JSON-RPC. [S1]"
    result = bm25_cite(synthesis, sources, insert_threshold=999.0, verify_threshold=999.0)
    # With an absurdly high verify threshold, [S1] should be flagged
    assert "UNVERIFIED" in result["cited_text"]
    assert result["flagged"] >= 1


def test_verify_pass(sources):
    synthesis = (
        "Large language models are trained on extensive text corpora using transformer architecture. [S1]\n\n"
        "Retrieval augmented generation grounds LLM outputs in retrieved documents. [S2]\n\n"
        "BM25 is a classical sparse retrieval baseline. [S2]\n\n"
        "MCP servers expose tools and resources. [S3]"
    )
    result = verify_synthesis(synthesis, sources, gate=0.5)
    assert result["citation_coverage"] == 1.0
    assert result["confidence_gate_passed"] is True


def test_verify_fail_no_citations(sources):
    synthesis = "Speculative claim with no source."
    result = verify_synthesis(synthesis, sources, gate=0.9)
    assert result["citation_coverage"] == 0.0
    assert result["confidence_gate_passed"] is False
    assert "DEGRADED" in result["confidence_gate_message"]
