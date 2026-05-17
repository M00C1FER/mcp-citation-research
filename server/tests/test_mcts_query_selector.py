from citation_research.mcts_query_selector import MCTSQueryNode, MCTSQuerySelector


def test_ucb1_prefers_unvisited_nodes():
    node = MCTSQueryNode(query="child")
    assert node.ucb1(total_visits=10) == float("inf")


def test_selector_ranks_candidates_and_limits_top_k():
    selector = MCTSQuerySelector()
    root = MCTSQueryNode(query="memory mcp server", visits=1)
    candidates = [
        "official memory mcp server documentation",
        "recent memory mcp server benchmark 2026",
        "memory mcp server compare risks",
        "memory",
    ]
    selected = selector.select_next_queries(root, candidates, top_k=2)
    assert len(selected) == 2
    assert selected[0] in candidates
    assert selected[1] in candidates
    assert root.visits == 5


def test_selector_tracks_children_between_rounds():
    selector = MCTSQuerySelector()
    root = MCTSQueryNode(query="citation research", visits=1)
    selector.select_next_queries(root, ["official citation research docs"], top_k=1)
    assert "official citation research docs" in root.children
    child = root.children["official citation research docs"]
    assert child.visits == 1
    assert child.quality_sum > 0
