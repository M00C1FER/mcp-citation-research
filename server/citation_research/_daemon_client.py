"""Thin HTTP client for the citation-researchd Go daemon."""
from __future__ import annotations

import os
from typing import Any, Dict, List
from urllib.parse import urlencode

import requests


class DaemonClient:
    """Client for citation-researchd HTTP API.

    The daemon owns search/fetch/session-state. The Python frontend owns
    synthesis (LLM-bound) and verify/cite (BM25). Networking → Go,
    LLM-orchestration → Python.
    """

    def __init__(self, base_url: str | None = None, timeout: int = 60,
                 token: str | None = None):
        self.base_url = (base_url or os.environ.get("CITATION_RESEARCHD_URL", "http://127.0.0.1:8090")).rstrip("/")
        self.timeout = timeout
        self._session = requests.Session()
        # Pre-shared key auth — daemon enforces if it has CITATION_RESEARCHD_TOKEN set.
        # Client sends the header unconditionally when the token is available;
        # daemon ignores it in unauthenticated mode (no env), so no harm done.
        token = token or os.environ.get("CITATION_RESEARCHD_TOKEN", "")
        if token:
            self._session.headers["Authorization"] = f"Bearer {token}"

    def healthz(self) -> Dict[str, Any]:
        return self._get("/healthz")

    def search(self, queries: List[str], max_per_query: int = 50, k: int = 60) -> Dict[str, Any]:
        return self._post("/search", {"queries": queries, "max": max_per_query, "k": k})

    # Per-call upper bound on timeout_s — defends against a misbehaving
    # caller passing 1e9 and stalling the connection forever.
    _MAX_FETCH_TIMEOUT_S = 600  # 10 min

    def fetch(self, urls: List[str], max_concurrent: int = 16, timeout_s: int = 30) -> Dict[str, Any]:
        # Override the client's default transport timeout when the per-call
        # work is expected to exceed it: the daemon may take ~timeout_s plus
        # network/processing overhead. Otherwise large fetch batches raise
        # ReadTimeout in the client while the daemon is still working.
        if timeout_s <= 0:
            raise ValueError(f"timeout_s must be positive, got {timeout_s}")
        if timeout_s > self._MAX_FETCH_TIMEOUT_S:
            raise ValueError(
                f"timeout_s={timeout_s}s exceeds client cap "
                f"{self._MAX_FETCH_TIMEOUT_S}s"
            )
        return self._post(
            "/fetch",
            {"urls": urls, "max_concurrent": max_concurrent, "timeout_s": timeout_s},
            request_timeout=max(self.timeout, timeout_s + 10),
        )

    def session_open(self, topic: str, depth: str = "exhaustive") -> Dict[str, Any]:
        return self._post("/session/open", {"topic": topic, "depth": depth})

    def session_update(self, session_id: str, iteration: int,
                       considered: List[str], fetched: List[str]) -> Dict[str, Any]:
        return self._post("/session/update", {
            "session_id": session_id,
            "iteration": iteration,
            "considered": considered,
            "fetched": fetched,
        })

    def session_status(self, session_id: str) -> Dict[str, Any]:
        return self._get(f"/session/status?{urlencode({'session_id': session_id})}")

    def session_close(self, session_id: str) -> Dict[str, Any]:
        return self._post("/session/close", {"session_id": session_id})

    def _get(self, path: str) -> Dict[str, Any]:
        r = self._session.get(self.base_url + path, timeout=self.timeout)
        r.raise_for_status()
        return r.json()

    def _post(self, path: str, body: Dict[str, Any],
              request_timeout: int | None = None) -> Dict[str, Any]:
        r = self._session.post(
            self.base_url + path, json=body,
            timeout=request_timeout or self.timeout,
        )
        r.raise_for_status()
        return r.json()
