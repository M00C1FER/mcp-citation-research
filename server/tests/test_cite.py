"""Tests for verify_synthesis and bm25_cite."""
from __future__ import annotations

import pytest

from citation_research._cite import bm25_cite
from citation_research._verify import verify_synthesis


@pytest.fixture()
def sources():
    return [
        {
            "url": "https://example.com/1",
            "title": "Transformer Models",
            "content": "Large language models are trained on vast corpora using transformer architectures.",
        },
        {
            "url": "https://example.com/2",
            "title": "RAG Survey",
            "content": "RAG grounds LLM outputs in retrieved documents, improving factual accuracy. BM25 is competitive.",
        },
        {
            "url": "https://example.com/3",
            "title": "MCP Protocol",
            "content": "MCP is an open standard for connecting AI agents to external tools.",
        },
    ]


def test_bm25_cite_inserts_known_citation(sources):
    synthesis = "Large language models are trained on vast corpora."
    result = bm25_cite(synthesis, sources)
    assert "cited_text" in result
    assert "coverage_pct" in result


def test_bm25_cite_empty_sources():
    result = bm25_cite("Some text.", [])
    assert result["coverage_pct"] == 0.0


def test_verify_pass(sources):
    """Legacy test retained — uses gate=0.5 to assert the existing
    well-cited synthesis always meets a minimal threshold. See also
    test_verify_pass_at_gate_90 for the production gate value.
    """
    synthesis = (
        "Large language models are trained on vast corpora. [S1]\n\n"
        "RAG grounds outputs in retrieved documents. [S2]\n\n"
        "BM25 is competitive for retrieval. [S2]\n\n"
        "MCP is an open standard. [S3]"
    )
    result = verify_synthesis(synthesis, sources, gate=0.5)
    assert result["citation_coverage"] == 1.0
    assert result["confidence_gate_passed"] is True


def test_verify_pass_at_gate_90(sources):
    """Verify that a well-cited synthesis passes the production 0.90 gate."""
    synthesis = (
        "Large language models are trained on vast corpora using transformer architectures. [S1]\n\n"
        "RAG grounds LLM outputs in retrieved documents, improving factual accuracy. [S2]\n\n"
        "BM25 is competitive for retrieval and frequently cited in survey literature. [S2]\n\n"
        "MCP is an open standard for connecting AI agents to external tools. [S3]"
    )
    result = verify_synthesis(synthesis, sources, gate=0.90)
    assert result["citation_coverage"] == 1.0
    assert result["confidence_gate_passed"] is True
    assert result["confidence_gate_threshold"] == 0.90


def test_verify_fail_at_gate_90(sources):
    """Verify that an uncited synthesis fails the production 0.90 gate."""
    synthesis = "Some speculative claim.\n\nAnother assertion without any source."
    result = verify_synthesis(synthesis, sources, gate=0.90)
    assert result["citation_coverage"] == 0.0
    assert result["confidence_gate_passed"] is False
    assert result["confidence_gate_threshold"] == 0.90
    assert "DEGRADED" in result["confidence_gate_message"]
