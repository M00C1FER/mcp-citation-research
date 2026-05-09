#!/usr/bin/env bash
# mcp-citation-research — install wrapper.
# Sources lib/installer-core.sh and delegates to run_install.
#
# Variables consumed by installer-core.sh:
#   REPO_NAME      — used for install path and config-hook name
#   ENTRY_POINT    — the console-script name to smoke-verify
#   REPO_NEEDS_GO  — "1" triggers _install_go (downloads Go toolchain if absent)
set -euo pipefail

REPO_NAME="mcp-citation-research"
ENTRY_POINT="citation-research-mcp"
REPO_NEEDS_GO="1"

# Locate lib/installer-core.sh relative to this script.
INSTALLER_LIB="$(cd "$(dirname "$0")" && pwd)/lib/installer-core.sh"
if [ ! -f "$INSTALLER_LIB" ]; then
  printf '  \033[31m✗\033[0m lib/installer-core.sh not found at %s\n' "$INSTALLER_LIB" >&2
  exit 1
fi
# shellcheck source=lib/installer-core.sh
. "$INSTALLER_LIB"

# Optional per-repo configuration hook (Phase 5).
# Called by run_install when not --unattended.
configure_mcp_citation_research() {
  # Ask for optional SearXNG URL (the only repo-specific config knob).
  printf '  SearXNG URL (leave blank for DuckDuckGo-only mode): '
  read -r SEARXNG_URL </dev/tty || SEARXNG_URL=""
  if [ -n "$SEARXNG_URL" ]; then
    printf '  \033[34mℹ\033[0m SearXNG URL recorded: %s\n' "$SEARXNG_URL"
    export SEARXNG_URL
  fi
}

run_install "$@"
