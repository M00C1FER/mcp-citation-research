"""Tests for MCP server helper functions that do not require FastMCP.

Exercises _parse_sources, which is the gating function for all
verify/cite tool calls. Its documented contract is:
  - None or empty string → []
  - Invalid JSON → []
  - Valid JSON but not a list → []
  - Valid JSON list → the list (including empty list)
"""
from __future__ import annotations

import json

import pytest

from citation_research.mcp_server import _parse_sources


# ─────────────────────────────────────────────────────────────────────────────
# _parse_sources — all edges
# ─────────────────────────────────────────────────────────────────────────────

def test_parse_sources_none():
    assert _parse_sources(None) == []


def test_parse_sources_empty_string():
    assert _parse_sources("") == []


def test_parse_sources_whitespace_only():
    assert _parse_sources("   ") == []


def test_parse_sources_invalid_json():
    assert _parse_sources("{not valid json") == []


def test_parse_sources_json_object_not_list():
    """A JSON object (dict) must be rejected; only arrays are valid sources."""
    assert _parse_sources('{"url":"https://example.com"}') == []


def test_parse_sources_json_string_not_list():
    assert _parse_sources('"just a string"') == []


def test_parse_sources_json_number_not_list():
    assert _parse_sources("42") == []


def test_parse_sources_empty_list():
    assert _parse_sources("[]") == []


def test_parse_sources_valid_list():
    sources = [{"url": "https://example.com/1", "title": "T1", "content": "c1"}]
    encoded = json.dumps(sources)
    result = _parse_sources(encoded)
    assert result == sources


def test_parse_sources_list_with_mixed_types():
    """A JSON list with non-dict elements must be returned as-is; callers
    (bm25_cite, verify_synthesis) handle per-element type checks via .get()."""
    raw = '[{"url": "https://a.com"}, null, 42]'
    result = _parse_sources(raw)
    assert len(result) == 3
