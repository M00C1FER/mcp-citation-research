"""BM25 citation injection — sentence-chunked.

Each paragraph in the synthesis is matched against an index of source content.
- Existing [S#] tags scoring < verify_threshold are flagged [UNVERIFIED].
- Uncited paragraphs scoring >= insert_threshold receive a fresh [S#] tag.
"""
from __future__ import annotations

import re
import threading
from typing import Any, Dict, List, Sequence

_DEFAULT_RERANKER_MODEL = "BAAI/bge-reranker-v2-m3"
_FALLBACK_RERANKER_MODEL = "BAAI/bge-reranker-base"
_RERANK_TOP_K = 50
_RERANKER_CACHE: Dict[str, Any] = {}
_RERANKER_LOCK = threading.Lock()
_RERANKER_UNAVAILABLE: set[str] = set()


def _tokenize(text: str) -> List[str]:
    return re.findall(r"\b[a-zA-Z][a-zA-Z0-9_-]+\b", text.lower())


def _split_paragraphs(text: str) -> List[str]:
    return [p.strip() for p in re.split(r"\n\s*\n", text) if p.strip()]


def _existing_citations(paragraph: str) -> List[int]:
    """Return 1-based citation indices, including already-flagged [S# UNVERIFIED] forms."""
    return [int(m.group(1)) for m in re.finditer(r"\[S(\d+)(?:\s+UNVERIFIED)?\]", paragraph)]


def _source_value(source: Any, key: str) -> str:
    if not isinstance(source, dict):
        return ""
    return str(source.get(key, "") or "")


def _source_text(source: Any) -> str:
    title = _source_value(source, "title")
    content = _source_value(source, "content")
    return f"{title}\n{content}".strip()


def _build_reranker(model: str) -> Any | None:
    try:
        from FlagEmbedding import FlagReranker
    except ImportError:
        return None
    try:
        return FlagReranker(model, use_fp16=True)
    except Exception:
        return None


def _load_reranker(model: str) -> Any | None:
    with _RERANKER_LOCK:
        if model in _RERANKER_CACHE:
            return _RERANKER_CACHE[model]
        if model in _RERANKER_UNAVAILABLE:
            return None
    reranker = _build_reranker(model)
    if reranker is None and model != _FALLBACK_RERANKER_MODEL:
        reranker = _build_reranker(_FALLBACK_RERANKER_MODEL)
    with _RERANKER_LOCK:
        if reranker is None:
            _RERANKER_UNAVAILABLE.add(model)
            return None
        _RERANKER_CACHE[model] = reranker
        return reranker


def _score_with_reranker(
    query: str,
    sources: Sequence[Dict[str, Any]],
    candidate_indices: Sequence[int],
    model: str,
) -> Dict[int, float]:
    reranker = _load_reranker(model)
    if reranker is None or not candidate_indices:
        return {}

    pairs = [(query, _source_text(sources[idx])) for idx in candidate_indices]
    try:
        raw_scores = reranker.compute_score(pairs)
    except Exception:
        return {}

    if isinstance(raw_scores, (int, float)):
        score_values = [float(raw_scores)]
    else:
        score_values = [float(score) for score in raw_scores]
    if len(score_values) != len(candidate_indices):
        return {}
    return {idx: score_values[pos] for pos, idx in enumerate(candidate_indices)}


def bm25_cite(synthesis: str, sources: List[Dict[str, Any]],
              insert_threshold: float = 3.5,
              verify_threshold: float = 1.0,
              enable_reranker: bool = True,
              reranker_model: str = _DEFAULT_RERANKER_MODEL) -> Dict[str, Any]:
    """Inject [S#] tags into synthesis using BM25 scoring against sources.

    Args:
        synthesis: text with or without existing [S#] tags
        sources:   list of {"url", "title", "content"} (1-indexed for [S#])
        insert_threshold: BM25 score needed to auto-insert a citation
        verify_threshold: BM25 score below which existing [S#] is flagged [UNVERIFIED]
        enable_reranker: when True, rerank BM25 top candidates with a cross-encoder
        reranker_model: preferred FlagEmbedding model name; falls back to
            BAAI/bge-reranker-base if unavailable

    Returns:
        {"cited_text": str, "inserted": int, "flagged": int,
         "source_list": [{num, url, title}], "coverage_pct": float}
    """
    try:
        from rank_bm25 import BM25Okapi
    except ImportError as e:
        raise RuntimeError("rank_bm25 not installed; run: pip install rank_bm25") from e

    if not sources:
        return {
            "cited_text": synthesis,
            "inserted": 0,
            "flagged": 0,
            "source_list": [],
            "coverage_pct": 0.0,
        }

    corpus = [_tokenize(_source_text(source)) for source in sources]
    bm25 = BM25Okapi(corpus)

    paragraphs = _split_paragraphs(synthesis)
    inserted = 0
    flagged = 0
    out_paragraphs: List[str] = []

    for para in paragraphs:
        tokens = _tokenize(para)
        if not tokens:
            out_paragraphs.append(para)
            continue
        scores = bm25.get_scores(tokens)
        top_k = min(_RERANK_TOP_K, len(sources))
        candidate_indices = sorted(
            range(len(sources)),
            key=lambda idx: float(scores[idx]),
            reverse=True,
        )[:top_k]
        best_idx = int(scores.argmax())
        if enable_reranker:
            reranked_scores = _score_with_reranker(para, sources, candidate_indices, reranker_model)
            if reranked_scores:
                best_idx = max(candidate_indices, key=lambda idx: reranked_scores.get(idx, float("-inf")))
        best_score = float(scores[best_idx])

        existing = _existing_citations(para)
        if existing:
            # Verify each existing citation; flag low scorers.
            new_para = para
            for cite in existing:
                if cite < 1 or cite > len(sources):
                    continue
                cite_score = float(scores[cite - 1])
                tag = f"[S{cite}]"
                if cite_score < verify_threshold and tag in new_para:
                    # Replace [S#] with [S# UNVERIFIED]; already-flagged tags are no-ops.
                    new_para = new_para.replace(tag, f"[S{cite} UNVERIFIED]", 1)
                    flagged += 1
            out_paragraphs.append(new_para)
        elif best_score >= insert_threshold:
            out_paragraphs.append(f"{para} [S{best_idx + 1}]")
            inserted += 1
        else:
            out_paragraphs.append(para)

    cited_text = "\n\n".join(out_paragraphs)
    cited_paragraphs = sum(1 for p in out_paragraphs if "[S" in p)
    coverage_pct = (cited_paragraphs / len(out_paragraphs) * 100) if out_paragraphs else 0.0

    source_list = [
        {"num": i + 1, "url": _source_value(source, "url"), "title": _source_value(source, "title")}
        for i, source in enumerate(sources)
    ]

    return {
        "cited_text": cited_text,
        "inserted": inserted,
        "flagged": flagged,
        "source_list": source_list,
        "coverage_pct": round(coverage_pct, 1),
    }


def split_paragraphs(text: str) -> List[str]:
    """Public helper for tests."""
    return _split_paragraphs(text)


def existing_citations(paragraph: str) -> List[int]:
    """Public helper for tests."""
    return _existing_citations(paragraph)


__all__ = ["bm25_cite", "split_paragraphs", "existing_citations"]
