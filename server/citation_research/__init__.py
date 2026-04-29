"""mcp-citation-research: MCP server with confidence-gated synthesis + BM25 citation."""
from ._daemon_client import DaemonClient
from ._cite import bm25_cite
from ._verify import verify_synthesis

__version__ = "0.1.0"
__all__ = ["DaemonClient", "bm25_cite", "verify_synthesis"]
