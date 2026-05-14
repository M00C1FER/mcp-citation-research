from __future__ import annotations

import importlib
import json
import os

import pytest
from citation_research import _cite
from citation_research import mcp_server


@pytest.fixture(autouse=True)
def _restore_mcp_server_rerank_setting():
    original = os.environ.get("CITATION_RESEARCH_RERANK")
    yield
    if original is None:
        os.environ.pop("CITATION_RESEARCH_RERANK", None)
    else:
        os.environ["CITATION_RESEARCH_RERANK"] = original
    importlib.reload(mcp_server)


def test_bm25_cite_reranker_changes_ordering(monkeypatch):
    sources = [
        {
            "url": "https://example.com/semantic",
            "title": "Neural Retrieval",
            "content": "Dense retrieval models improve semantic recall for legal passages.",
        },
        {
            "url": "https://example.com/lexical",
            "title": "Keyword Match",
            "content": "alpha beta alpha beta alpha beta exact keyword overlap baseline.",
        },
        {
            "url": "https://example.com/other",
            "title": "Other",
            "content": "completely unrelated corpus entry to avoid BM25 two-document tie effects.",
        },
    ]
    synthesis = "alpha beta"

    baseline = _cite.bm25_cite(synthesis, sources, insert_threshold=0.0, enable_reranker=False)
    assert baseline["cited_text"].endswith("[S2]")

    monkeypatch.setattr(
        _cite,
        "_score_with_reranker",
        lambda _query, _sources, candidate_indices, _model: {
            idx: (100.0 if idx == 0 else 0.0) for idx in candidate_indices
        },
    )
    reranked = _cite.bm25_cite(synthesis, sources, insert_threshold=0.0, enable_reranker=True)
    assert reranked["cited_text"].endswith("[S1]")


def test_research_cite_rerank_default_on(monkeypatch):
    monkeypatch.delenv("CITATION_RESEARCH_RERANK", raising=False)
    module = importlib.reload(mcp_server)

    captured: dict[str, bool] = {}

    def fake_bm25_cite(
        synthesis: str,
        sources: list[dict[str, str]],
        insert_threshold: float = 3.5,
        verify_threshold: float = 1.0,
        enable_reranker: bool = True,
        reranker_model: str = "BAAI/bge-reranker-v2-m3",
    ) -> dict[str, object]:
        del insert_threshold, verify_threshold, reranker_model
        captured["enable"] = enable_reranker
        return {
            "cited_text": synthesis,
            "inserted": 0,
            "flagged": 0,
            "source_list": sources,
            "coverage_pct": 0.0,
        }

    monkeypatch.setattr(module, "bm25_cite", fake_bm25_cite)
    module.research_cite("hello", json.dumps([]))
    assert captured["enable"] is True


def test_research_cite_rerank_env_opt_out(monkeypatch):
    monkeypatch.setenv("CITATION_RESEARCH_RERANK", "0")
    module = importlib.reload(mcp_server)

    captured: dict[str, bool] = {}

    def fake_bm25_cite(
        synthesis: str,
        sources: list[dict[str, str]],
        insert_threshold: float = 3.5,
        verify_threshold: float = 1.0,
        enable_reranker: bool = True,
        reranker_model: str = "BAAI/bge-reranker-v2-m3",
    ) -> dict[str, object]:
        del insert_threshold, verify_threshold, reranker_model
        captured["enable"] = enable_reranker
        return {
            "cited_text": synthesis,
            "inserted": 0,
            "flagged": 0,
            "source_list": sources,
            "coverage_pct": 0.0,
        }

    monkeypatch.setattr(module, "bm25_cite", fake_bm25_cite)
    module.research_cite("hello", json.dumps([]))
    assert captured["enable"] is False
