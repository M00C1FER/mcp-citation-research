#!/data/data/com.termux/files/usr/bin/bash
# mcp-citation-research — Termux installer
#
# Tested on: Termux 0.118+ (Android 7+, arm64 / aarch64)
# Dependencies installed via pkg: golang python git curl openssl
#
# Usage (run inside Termux):
#   curl -fsSL https://raw.githubusercontent.com/M00C1FER/mcp-citation-research/main/scripts/install-termux.sh | bash
#
# Caveats
# -------
# * SearXNG is NOT available in Termux. All search goes through DuckDuckGo
#   scraping (the built-in zero-infrastructure fallback).
# * The daemon binds to 127.0.0.1:8090 (loopback only). Other Android apps
#   cannot reach it; MCP client must run in the same Termux session.
# * No /etc/passwd, /proc/sys/kernel/osrelease, or systemd. The daemon uses
#   none of these — pure HTTP + in-memory state.
# * Race detector (-race) requires CGO + glibc. Termux ships a Go toolchain
#   built against Bionic libc; the race detector is unavailable. The binary
#   is built with CGO_ENABLED=0 (fully static, no Bionic dependency).
set -euo pipefail

log()  { printf '\033[1m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
die()  { printf '  \033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

# Verify we are actually inside Termux.
if [ -z "${TERMUX_VERSION:-}" ] && [ ! -d "/data/data/com.termux" ]; then
    die "This script must be run inside the Termux terminal emulator."
fi

log "mcp-citation-research — Termux installer"
log ""

# ── Step 1: system packages ─────────────────────────────────────────────────
log "Step 1/5: Install system packages"
pkg update -y -q
pkg install -y golang python git curl openssl
ok "golang $(go version | awk '{print $3}'), $(python --version 2>&1)"

# ── Step 2: clone or update repository ─────────────────────────────────────
INSTALL_DIR="${HOME}/mcp-citation-research"
log ""
log "Step 2/5: Fetch source → ${INSTALL_DIR}"
if [ -d "${INSTALL_DIR}/.git" ]; then
    git -C "${INSTALL_DIR}" pull -q
    ok "Repository updated"
else
    git clone -q https://github.com/M00C1FER/mcp-citation-research.git "${INSTALL_DIR}"
    ok "Repository cloned"
fi

# ── Step 3: build Go daemon ─────────────────────────────────────────────────
BIN_DIR="${HOME}/.local/bin"
mkdir -p "${BIN_DIR}"
DAEMON_BIN="${BIN_DIR}/citation-researchd"
log ""
log "Step 3/5: Build citation-researchd (CGO_ENABLED=0)"
(
    cd "${INSTALL_DIR}/daemon"
    # CGO_ENABLED=0: fully static binary, no Bionic/musl runtime dependency.
    CGO_ENABLED=0 go build -o "${DAEMON_BIN}" ./cmd/citation-researchd
)
ok "Daemon built → ${DAEMON_BIN}"

# ── Step 4: install Python MCP frontend ─────────────────────────────────────
log ""
log "Step 4/5: Install Python MCP frontend"
(
    cd "${INSTALL_DIR}/server"
    python -m venv .venv
    .venv/bin/pip install --quiet --upgrade pip
    .venv/bin/pip install --quiet -e .
)
# Wrapper script so 'citation-research-mcp' is on PATH.
cat > "${BIN_DIR}/citation-research-mcp" << 'WRAPPER'
#!/data/data/com.termux/files/usr/bin/bash
TOKEN="${CITATION_RESEARCHD_TOKEN:-$(cat "${HOME}/.local/share/mcp-citation-research/daemon.token" 2>/dev/null || echo '')}"
export CITATION_RESEARCHD_TOKEN="${TOKEN}"
exec "${HOME}/mcp-citation-research/server/.venv/bin/citation-research-mcp" "$@"
WRAPPER
chmod +x "${BIN_DIR}/citation-research-mcp"
ok "MCP frontend installed → ${BIN_DIR}/citation-research-mcp"

# ── Step 5: generate auth token ─────────────────────────────────────────────
TOKEN_DIR="${HOME}/.local/share/mcp-citation-research"
TOKEN_FILE="${TOKEN_DIR}/daemon.token"
mkdir -p "${TOKEN_DIR}"
if [ ! -s "${TOKEN_FILE}" ]; then
    openssl rand -base64 32 | tr '+/' '-_' | tr -d '=' > "${TOKEN_FILE}"
    chmod 600 "${TOKEN_FILE}"
    ok "Auth token generated → ${TOKEN_FILE}"
else
    ok "Reusing existing auth token"
fi

# Launcher script that sources the token automatically.
cat > "${BIN_DIR}/citation-researchd-start" << LAUNCHER
#!/data/data/com.termux/files/usr/bin/bash
# Starts citation-researchd on 127.0.0.1:8090 (loopback only).
TOKEN="\${CITATION_RESEARCHD_TOKEN:-\$(cat "${TOKEN_FILE}" 2>/dev/null || echo '')}"
export CITATION_RESEARCHD_TOKEN="\${TOKEN}"
exec "${DAEMON_BIN}" -addr 127.0.0.1:8090 "\$@"
LAUNCHER
chmod +x "${BIN_DIR}/citation-researchd-start"
ok "Launcher installed → ${BIN_DIR}/citation-researchd-start"

# ── Smoke test ───────────────────────────────────────────────────────────────
log ""
log "Smoke test: verify daemon starts on 127.0.0.1:18091"
TOKEN="$(cat "${TOKEN_FILE}")"
CITATION_RESEARCHD_TOKEN="${TOKEN}" \
    "${DAEMON_BIN}" -addr 127.0.0.1:18091 >/tmp/cr-smoke.log 2>&1 &
SMOKE_PID=$!
sleep 2

SMOKE_OK=1
if curl -fsS http://127.0.0.1:18091/healthz >/dev/null 2>&1; then
    ok "/healthz → 200 OK"
else
    warn "/healthz failed (see /tmp/cr-smoke.log)"
    SMOKE_OK=0
fi
# /search must reject unauthenticated requests.
if curl -fsS -X POST http://127.0.0.1:18091/search -d '{}' >/dev/null 2>&1; then
    warn "Auth check FAILED: /search accepted unauthenticated request"
    SMOKE_OK=0
else
    ok "Auth check OK (/search rejects unauthenticated requests)"
fi
kill "${SMOKE_PID}" 2>/dev/null || true
[ "${SMOKE_OK}" = "0" ] && { warn "Smoke test had warnings — check /tmp/cr-smoke.log"; }

# ── Usage hints ──────────────────────────────────────────────────────────────
log ""
log "Installation complete."
cat << 'USAGE'

  Start the daemon (loopback-only, auto-sources token):
    citation-researchd-start &

  Run the MCP server (stdio transport — wire into Claude Desktop / Continue):
    citation-research-mcp

  Claude Desktop config (~/.config/claude/claude_desktop_config.json):
    {
      "mcpServers": {
        "citation-research": {
          "command": "citation-research-mcp"
        }
      }
    }

  Auth token:
    ~/.local/share/mcp-citation-research/daemon.token

  Notes for Termux users:
    - SearXNG is not available; search uses DuckDuckGo scraping only.
    - Daemon binds to 127.0.0.1 (loopback). Other Android apps cannot
      reach it. The MCP client must run in the same Termux session.
    - To keep the daemon running after closing the terminal, use
      Termux:Boot or start-server via a Wake Lock.
USAGE
