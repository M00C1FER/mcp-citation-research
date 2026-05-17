"""MCTS-inspired follow-up query selection for research_decompose."""
from __future__ import annotations

from dataclasses import dataclass, field
import math
import re
from typing import Dict, Iterable, List


@dataclass
class QuerySelectionConfig:
    """Configuration for query ranking."""

    use_mcts: bool = True
    exploration_constant: float = 1.414
    top_k: int = 5


@dataclass
class MCTSQueryNode:
    """Single query node tracked during follow-up selection."""

    query: str
    visits: int = 0
    quality_sum: float = 0.0
    children: Dict[str, "MCTSQueryNode"] = field(default_factory=dict)

    @property
    def exploitation(self) -> float:
        if self.visits == 0:
            return 0.0
        return self.quality_sum / self.visits

    def ucb1(self, total_visits: int, c: float = 1.414) -> float:
        if self.visits == 0:
            return float("inf")
        return self.exploitation + c * math.sqrt(math.log(max(total_visits, 1)) / self.visits)


class MCTSQuerySelector:
    """Rank candidate follow-up queries using UCB1 + CRAAP-style exploitation."""

    def __init__(self, config: QuerySelectionConfig | None = None) -> None:
        self.config = config or QuerySelectionConfig()

    def select_next_queries(
        self,
        current_node: MCTSQueryNode,
        candidates: Iterable[str],
        top_k: int | None = None,
    ) -> List[str]:
        top_k = top_k or self.config.top_k
        ranked: List[tuple[float, str]] = []
        total_visits = max(current_node.visits, 1)

        for candidate in candidates:
            node = current_node.children.get(candidate)
            if node is None:
                node = MCTSQueryNode(query=candidate)
                current_node.children[candidate] = node

            exploitation = self._craap_exploitation_score(current_node.query, candidate)
            node.quality_sum += exploitation
            node.visits += 1
            ranked.append((node.ucb1(total_visits + len(ranked) + 1, self.config.exploration_constant), candidate))

        current_node.visits += len(ranked)
        ranked.sort(key=lambda item: item[0], reverse=True)
        return [query for _, query in ranked[:top_k]]

    def _craap_exploitation_score(self, topic: str, candidate: str) -> float:
        """Stub CRAAP-style signal derived from query quality when no source score exists."""
        topic_tokens = set(_tokenize(topic))
        candidate_tokens = _tokenize(candidate)
        if not candidate_tokens:
            return 0.0

        overlap = sum(1 for token in candidate_tokens if token in topic_tokens)
        novelty = len(set(candidate_tokens) - topic_tokens)

        score = 0.2
        score += min(0.4, overlap / max(len(topic_tokens), 1))
        score += min(0.2, novelty * 0.05)

        lower = candidate.lower()
        if any(term in lower for term in ("recent", "latest", "2024", "2025", "2026")):
            score += 0.1  # Currency
        if any(term in lower for term in ("official", "spec", "docs", "documentation", "benchmark")):
            score += 0.1  # Authority / Accuracy
        if any(term in lower for term in ("compare", "risk", "pitfall", "failure")):
            score += 0.1  # Purpose / Relevance

        return max(0.0, min(1.0, score))


def _tokenize(text: str) -> List[str]:
    return re.findall(r"[a-zA-Z0-9_+-]+", text.lower())


__all__ = ["MCTSQueryNode", "MCTSQuerySelector", "QuerySelectionConfig"]
