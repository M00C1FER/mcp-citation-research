"""Tests for DaemonClient — validation logic that does not require a live daemon.

The client is tested in isolation; no real HTTP server is started. Test cases
cover input-validation paths that raise locally before any network call.
"""
from __future__ import annotations

import pytest

from citation_research._daemon_client import DaemonClient


# ─────────────────────────────────────────────────────────────────────────────
# DaemonClient.fetch — timeout_s validation
# ─────────────────────────────────────────────────────────────────────────────

def test_fetch_negative_timeout_raises():
    """A negative timeout_s must raise ValueError before any network call."""
    client = DaemonClient(base_url="http://127.0.0.1:19999")
    with pytest.raises(ValueError, match="timeout_s must be positive"):
        client.fetch(["https://example.com/"], timeout_s=-1)


def test_fetch_zero_timeout_raises():
    """timeout_s=0 must raise ValueError (a zero-duration fetch cannot succeed)."""
    client = DaemonClient(base_url="http://127.0.0.1:19999")
    with pytest.raises(ValueError, match="timeout_s must be positive"):
        client.fetch(["https://example.com/"], timeout_s=0)


def test_fetch_over_cap_raises():
    """timeout_s above the client cap must raise ValueError."""
    client = DaemonClient(base_url="http://127.0.0.1:19999")
    over_cap = DaemonClient._MAX_FETCH_TIMEOUT_S + 1
    with pytest.raises(ValueError, match="exceeds client cap"):
        client.fetch(["https://example.com/"], timeout_s=over_cap)


def test_fetch_at_cap_does_not_raise_immediately():
    """timeout_s == _MAX_FETCH_TIMEOUT_S should pass local validation.

    The call will fail with a connection error (no daemon running) but must
    NOT fail with a ValueError from the local validation guard.
    """
    client = DaemonClient(base_url="http://127.0.0.1:19999")
    with pytest.raises(Exception) as exc_info:
        client.fetch(["https://example.com/"], timeout_s=DaemonClient._MAX_FETCH_TIMEOUT_S)
    # Must not be a ValueError from our own guard.
    assert not isinstance(exc_info.value, ValueError), (
        "fetch at exactly _MAX_FETCH_TIMEOUT_S should not raise ValueError; "
        f"got {exc_info.value!r}"
    )


def test_fetch_valid_timeout_does_not_raise_value_error():
    """A valid timeout_s should not raise ValueError (connection error is ok)."""
    client = DaemonClient(base_url="http://127.0.0.1:19999")
    with pytest.raises(Exception) as exc_info:
        client.fetch(["https://example.com/"], timeout_s=30)
    assert not isinstance(exc_info.value, ValueError)


# ─────────────────────────────────────────────────────────────────────────────
# DaemonClient — constructor
# ─────────────────────────────────────────────────────────────────────────────

def test_client_default_base_url_from_env(monkeypatch):
    """When CITATION_RESEARCHD_URL is set, it must be used as base_url."""
    monkeypatch.setenv("CITATION_RESEARCHD_URL", "http://custom-host:9999")
    client = DaemonClient()
    assert client.base_url == "http://custom-host:9999"


def test_client_base_url_trailing_slash_stripped():
    """base_url with a trailing slash must be normalised."""
    client = DaemonClient(base_url="http://example.com:8090/")
    assert not client.base_url.endswith("/")


def test_client_token_sets_auth_header(monkeypatch):
    """A non-empty token must result in an Authorization header on the session."""
    client = DaemonClient(base_url="http://127.0.0.1:19999", token="my-secret")
    assert client._session.headers.get("Authorization") == "Bearer my-secret"


def test_client_no_token_no_auth_header(monkeypatch):
    """When token is empty and env var is absent, no Authorization header must be set."""
    monkeypatch.delenv("CITATION_RESEARCHD_TOKEN", raising=False)
    client = DaemonClient(base_url="http://127.0.0.1:19999", token="")
    assert "Authorization" not in client._session.headers
