"""Tests for verify_synthesis edge cases and confidence-gate bypass analysis.

These tests address the code-review scope from the issue:
  - empty sources_json → verify returns low confidence
  - confidence gate cannot be falsely passed by stop-word-heavy text
  - empty synthesis returns the documented "empty synthesis" message
  - verify with no overlap between synthesis and sources scores low
"""
from __future__ import annotations

import pytest

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
            "content": "RAG grounds LLM outputs in retrieved documents, improving factual accuracy.",
        },
    ]


def test_verify_empty_synthesis():
    """Empty synthesis must return confidence=0 and gate_passed=False."""
    result = verify_synthesis("", [], gate=0.90)
    assert result["confidence"] == 0.0
    assert result["confidence_gate_passed"] is False
    assert "empty synthesis" in result["confidence_gate_message"]


def test_verify_empty_sources_no_citations():
    """Synthesis with no sources and no [S#] tags must fail the gate."""
    synthesis = "The quick brown fox jumps over the lazy dog."
    result = verify_synthesis(synthesis, [], gate=0.90)
    assert result["confidence_gate_passed"] is False
    assert result["groundedness"] == 0.0
    assert result["citation_coverage"] == 0.0


def test_verify_confidence_gate_threshold_in_result(sources):
    """The gate threshold must be echoed in every result."""
    for gate in (0.5, 0.75, 0.90, 0.95):
        result = verify_synthesis("Some text [S1].", sources, gate=gate)
        assert result["confidence_gate_threshold"] == gate


def test_verify_only_stopwords_cannot_pass_gate():
    """A synthesis composed entirely of stop words scores near-zero groundedness.

    Stop words are excluded from the token comparison so even a source that
    contains the same stop words cannot inflate the overlap score above the
    gate. This guards against a naive bypass attempt.
    """
    stop_word_synthesis = "the and or but in on at to for of with by from as is"
    sources_with_stopwords = [
        {"url": "https://s.com/1", "title": "the and",
         "content": "the and or but in on at to for of with by from as is was are"}
    ]
    result = verify_synthesis(stop_word_synthesis, sources_with_stopwords, gate=0.90)
    # After stop-word filtering synth_tokens should be empty → groundedness=0
    # and citation_coverage=0, so confidence must be 0.
    assert result["confidence_gate_passed"] is False
    assert result["confidence"] == 0.0


def test_verify_fabricated_citation_alone_cannot_pass():
    """Tagging every paragraph [S1] without real content overlap must not pass.

    citation_coverage = 1.0 contributes 0.5 to confidence.
    groundedness ~ 0 contributes ~0.0.
    Combined confidence ~ 0.5, well below the 0.90 gate.
    """
    synthesis = "[S1]\n\n[S1]\n\n[S1]"
    sources = [{"url": "https://s.com/1", "title": "irrelevant",
                "content": "completely different vocabulary xyz quantum flux"}]
    result = verify_synthesis(synthesis, sources, gate=0.90)
    assert result["citation_coverage"] == 1.0
    assert result["confidence_gate_passed"] is False
    assert result["confidence"] < 0.90


def test_verify_whitespace_only_paragraphs_ignored():
    """Paragraphs that are pure whitespace should not be counted as uncited."""
    synthesis = "Real content paragraph. [S1]\n\n   \n\nAnother real paragraph. [S1]"
    sources = [{"url": "https://s.com/1", "title": "Title",
                "content": "real content paragraph another real paragraph"}]
    result = verify_synthesis(synthesis, sources, gate=0.90)
    # Both non-empty paragraphs have [S1]; coverage must be 1.0.
    assert result["citation_coverage"] == 1.0


def test_verify_result_keys_always_present(sources):
    """Every result dict must contain the documented keys."""
    expected_keys = {
        "confidence", "citation_coverage", "groundedness",
        "claims_verified", "claims_supported", "verdict",
        "confidence_gate_passed", "confidence_gate_threshold",
        "confidence_gate_message",
    }
    result = verify_synthesis("some text", sources, gate=0.90)
    missing = expected_keys - result.keys()
    assert not missing, f"result missing keys: {missing}"


def test_verify_verdict_levels(sources):
    """Verdict string must be one of 'high', 'medium', 'low'."""
    for synthesis in [
        "",
        "Only uncited speculation.",
        "Partial content from sources but no tags.",
        "Large language models are trained on vast corpora using transformer architectures. [S1]\n\n"
        "RAG grounds LLM outputs in retrieved documents, improving factual accuracy. [S2]",
    ]:
        result = verify_synthesis(synthesis, sources, gate=0.90)
        assert result["verdict"] in {"high", "medium", "low"}, (
            f"unexpected verdict {result['verdict']!r} for synthesis={synthesis!r}"
        )
