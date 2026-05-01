"""Confidence verification — citation coverage + token groundedness.

This is the deterministic, LLM-free portion of verify. The full LLM-as-judge
verifier can be wired by the caller via the Anthropic SDK; this module
provides the structural-quality floor.
"""
from __future__ import annotations

import re
from typing import Any, Dict, List


_DEFAULT_GATE = 0.90

# Common English stop words excluded from the groundedness token comparison to
# prevent high-frequency function words from inflating the overlap score (fix #8).
_STOP_WORDS = frozenset({
    "an", "the", "and", "or", "but", "in", "on", "at", "to", "for",
    "of", "with", "by", "from", "as", "is", "was", "are", "were", "be",
    "been", "being", "have", "has", "had", "do", "does", "did", "will",
    "would", "could", "should", "may", "might", "shall", "can", "that",
    "this", "these", "those", "it", "its", "they", "them", "their", "we",
    "us", "our", "you", "your", "he", "him", "his", "she", "her", "not",
    "no", "so", "if", "out", "up", "about", "into", "than", "then", "now",
    "just", "also", "after", "over", "back", "what", "how", "who", "when",
    "which", "my", "one", "all", "only", "some", "any", "most", "me",
    "get", "go", "make", "time", "well", "good", "new", "work", "use",
    "way", "two", "come", "like", "give", "day", "want", "look", "know",
    "say", "see", "take", "even", "first", "because", "there", "people",
    "year", "same", "other", "more", "such", "since",
})


def _tokenize(text: str) -> List[str]:
    tokens = re.findall(r"\b[a-zA-Z][a-zA-Z0-9_-]+\b", text.lower())
    return [t for t in tokens if t not in _STOP_WORDS]


def _split_paragraphs(text: str) -> List[str]:
    return [p.strip() for p in re.split(r"\n\s*\n", text) if p.strip()]


def verify_synthesis(synthesis: str, sources: List[Dict[str, Any]],
                     gate: float = _DEFAULT_GATE) -> Dict[str, Any]:
    """Compute citation coverage + token groundedness for the synthesis.

    Returns:
        {"confidence": float, "citation_coverage": float, "groundedness": float,
         "claims_verified": int, "claims_supported": int, "verdict": str,
         "confidence_gate_passed": bool, "confidence_gate_threshold": float}
    """
    paragraphs = _split_paragraphs(synthesis)
    if not paragraphs:
        return {
            "confidence": 0.0,
            "citation_coverage": 0.0,
            "groundedness": 0.0,
            "claims_verified": 0,
            "claims_supported": 0,
            "verdict": "low",
            "confidence_gate_passed": False,
            "confidence_gate_threshold": gate,
            "confidence_gate_message": "empty synthesis",
        }

    cited = sum(1 for p in paragraphs if re.search(r"\[S\d+\]", p))
    citation_coverage = cited / len(paragraphs)

    # Token groundedness: fraction of synthesis tokens also present in source corpus
    synth_tokens = set(_tokenize(synthesis))
    source_tokens: set[str] = set()
    for s in sources:
        source_tokens.update(_tokenize(s.get("content", "")))
        source_tokens.update(_tokenize(s.get("title", "")))
    if synth_tokens:
        groundedness = len(synth_tokens & source_tokens) / len(synth_tokens)
    else:
        groundedness = 0.0

    confidence = 0.5 * citation_coverage + 0.5 * groundedness
    verdict = "high" if confidence >= gate else ("medium" if confidence >= 0.6 else "low")

    return {
        "confidence": round(confidence, 3),
        "citation_coverage": round(citation_coverage, 3),
        "groundedness": round(groundedness, 3),
        "claims_verified": len(paragraphs),
        "claims_supported": cited,
        "verdict": verdict,
        "confidence_gate_passed": confidence >= gate,
        "confidence_gate_threshold": gate,
        "confidence_gate_message": (
            f"PASS: {confidence:.2f} >= {gate}" if confidence >= gate
            else f"DEGRADED: {confidence:.2f} < {gate}"
        ),
    }


__all__ = ["verify_synthesis"]
