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


# ─────────────────────────────────────────────────────────────────────────────
# BM25 UNVERIFIED tagging
# ─────────────────────────────────────────────────────────────────────────────

def test_bm25_unverified_tag_low_score():
    """A paragraph with an existing [S#] citation whose BM25 score is below
    verify_threshold must have that tag rewritten to [S# UNVERIFIED].
    """
    # Source is about cooking; synthesis paragraph is about astronomy.
    sources_cooking = [
        {"url": "https://cook.example.com/1", "title": "Recipes",
         "content": "boil pasta stir sauce add salt pepper cook dinner kitchen"},
    ]
    synthesis = "The Andromeda galaxy is two million light years away. [S1]"
    result = bm25_cite(synthesis, sources_cooking, verify_threshold=100.0)
    assert "[S1 UNVERIFIED]" in result["cited_text"], (
        "expected [S1 UNVERIFIED] when BM25 score is well below verify_threshold; "
        f"got: {result['cited_text']!r}"
    )
    assert result["flagged"] == 1


def test_bm25_unverified_tag_relevant_source(sources):
    """A paragraph that closely matches its cited source must NOT be flagged."""
    synthesis = "Large language models are trained on vast corpora using transformer architectures. [S1]"
    result = bm25_cite(synthesis, sources, verify_threshold=0.01)
    assert "[S1 UNVERIFIED]" not in result["cited_text"], (
        f"well-matched [S1] should not be flagged; got: {result['cited_text']!r}"
    )
    assert result["flagged"] == 0


def test_bm25_out_of_range_citation_ignored(sources):
    """Citation numbers outside [1, len(sources)] must be silently ignored."""
    synthesis = "Some claim backed by a nonexistent source. [S99]"
    result = bm25_cite(synthesis, sources, verify_threshold=0.1)
    # Out-of-range tag: not flagged, not UNVERIFIED, just left as-is.
    assert result["flagged"] == 0
    assert "[S99 UNVERIFIED]" not in result["cited_text"]


def test_bm25_multi_paragraph_coverage(sources):
    """Multi-paragraph synthesis: coverage_pct reflects the cited fraction."""
    synthesis = (
        "Large language models use transformer architectures.\n\n"
        "This paragraph has no source at all and is completely off-topic.\n\n"
        "MCP connects AI agents to tools."
    )
    result = bm25_cite(synthesis, sources, insert_threshold=0.1)
    # With a very low threshold all three paragraphs should receive a citation.
    assert result["coverage_pct"] > 0


def test_bm25_result_keys_always_present(sources):
    """bm25_cite must always return the documented keys."""
    expected = {"cited_text", "inserted", "flagged", "source_list", "coverage_pct"}
    result = bm25_cite("anything", sources)
    missing = expected - result.keys()
    assert not missing, f"bm25_cite result missing keys: {missing}"


def test_bm25_source_list_indexed_from_one(sources):
    """source_list entries must be 1-indexed to match [S#] tags."""
    result = bm25_cite("text", sources)
    nums = [s["num"] for s in result["source_list"]]
    assert nums == list(range(1, len(sources) + 1)), (
        f"source_list nums should be 1..{len(sources)}, got {nums}"
    )



def test_bm25_cite_idempotent():
    """Second bm25_cite pass on already-flagged synthesis must be a no-op."""
    synthesis = "Quantum computers use qubits [S1]."
    sources = [{"url": "u1", "title": "q", "content": "qubit entanglement quantum"}]
    # threshold above any realistic BM25 score — every citation gets flagged
    first = bm25_cite(synthesis, sources, verify_threshold=999.0)
    assert "[S1 UNVERIFIED]" in first["cited_text"]
    assert first["flagged"] == 1

    # second pass: [S1 UNVERIFIED] must not be re-flagged or double-counted
    second = bm25_cite(first["cited_text"], sources, verify_threshold=999.0)
    assert second["cited_text"] == first["cited_text"]
    assert second["flagged"] == 0
