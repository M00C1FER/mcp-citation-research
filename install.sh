#!/usr/bin/env bash
# mcp-citation-research — interactive install wizard.
# Installs the Go daemon (citation-researchd) + Python MCP frontend.
set -euo pipefail

if [ -t 1 ]; then C_BOLD="$(tput bold)"; C_RESET="$(tput sgr0)"; C_GREEN="$(tput setaf 2)"; C_YELLOW="$(tput setaf 3)"; C_RED="$(tput setaf 1)"; else C_BOLD=""; C_RESET=""; C_GREEN=""; C_YELLOW=""; C_RED=""; fi
say()  { printf "%s%s%s\n" "$C_BOLD" "$1" "$C_RESET"; }
info() { printf "  %s\n" "$1"; }
ok()   { printf "  %s✓%s %s\n" "$C_GREEN" "$C_RESET" "$1"; }
warn() { printf "  %s!%s %s\n" "$C_YELLOW" "$C_RESET" "$1"; }
fail() { printf "  %s✗%s %s\n" "$C_RED" "$C_RESET" "$1" >&2; exit 1; }
prompt_yn() { local q="$1" def="${2:-y}" ans; if [ "$def" = "y" ]; then read -r -p "  $q [Y/n]: " ans; ans="${ans:-y}"; else read -r -p "  $q [y/N]: " ans; ans="${ans:-n}"; fi; [[ "$ans" =~ ^[Yy] ]]; }
prompt_default() { read -r -p "  $1 [$2]: " ans; echo "${ans:-$2}"; }

detect_os() {
    OS_ID="unknown"; OS_LIKE=""; OS_VERSION=""; OS_WSL=0
    if [ -f /etc/os-release ]; then . /etc/os-release; OS_ID="${ID:-}"; OS_LIKE="${ID_LIKE:-}"; OS_VERSION="${VERSION_ID:-}"; fi
    [ "$(uname)" = "Darwin" ] && OS_ID="macos"
    grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null && OS_WSL=1 || true
}
pkg_install() {
    case "$OS_ID" in
        debian|ubuntu) sudo apt-get update -qq && sudo apt-get install -y "$@";;
        fedora|rhel|centos) sudo dnf install -y "$@";;
        arch|manjaro) sudo pacman -S --noconfirm "$@";;
        alpine) sudo apk add --no-cache "$@";;
        opensuse*|sles) sudo zypper install -y "$@";;
        macos) brew install "$@";;
        *) warn "unknown OS — install manually: $*"; return 1;;
    esac
}

ensure_go() {
    if command -v go >/dev/null 2>&1; then ok "Go: $(go version | awk '{print $3}')"; return 0; fi
    if prompt_yn "Install Go via system package manager?"; then
        case "$OS_ID" in
            debian|ubuntu|fedora|arch|manjaro|alpine|opensuse*|sles|macos) pkg_install go || pkg_install golang-go || pkg_install golang;;
            *) fail "install Go 1.22+ manually then re-run";;
        esac
    else fail "Go required"; fi
}

ensure_python() {
    if command -v python3 >/dev/null 2>&1; then
        local pyv; pyv="$(python3 -c 'import sys; print("%d.%d"%sys.version_info[:2])')"
        case "$pyv" in 3.1[0-9]|3.[2-9][0-9]) ok "Python $pyv"; return 0;; esac
        warn "Python $pyv — needs ≥ 3.10"
    fi
    if prompt_yn "Install Python 3.10+ via system package manager?"; then
        case "$OS_ID" in
            debian|ubuntu) pkg_install python3 python3-venv python3-pip;;
            fedora|rhel|centos) pkg_install python3 python3-pip;;
            arch|manjaro) pkg_install python python-pip;;
            alpine) pkg_install python3 py3-pip;;
            macos) pkg_install python@3.12;;
            *) fail "install Python 3.10+ manually then re-run";;
        esac
    else fail "Python 3.10+ required"; fi
}

main() {
    say "mcp-citation-research — install wizard (Go daemon + Python frontend)"
    detect_os
    info "OS: ${OS_ID}${OS_VERSION:+ $OS_VERSION}$([ "$OS_WSL" = 1 ] && echo ' (WSL2)')"

    say ""; say "Step 1/5: Go toolchain"; ensure_go
    say ""; say "Step 2/5: Python 3.10+"; ensure_python

    say ""; say "Step 3/5: Install location + search backend"
    local INSTALL_HOME SEARCH_BACKEND DAEMON_BIN
    INSTALL_HOME="$(prompt_default "Install root" "$HOME/.local/share/mcp-citation-research")"
    DAEMON_BIN="$(prompt_default "Daemon binary path" "$HOME/.local/bin/citation-researchd")"
    say ""
    say "  Search backends:"
    info "    [1] DuckDuckGo only       (zero infrastructure, default)"
    info "    [2] SearXNG + DuckDuckGo  (best — needs a SearXNG host)"
    SEARCH_BACKEND="$(prompt_default "Choice" "1")"
    local SEARXNG_URL=""
    if [ "$SEARCH_BACKEND" = "2" ]; then
        SEARXNG_URL="$(prompt_default "SearXNG URL" "http://127.0.0.1:8080")"
    fi

    say ""; say "Step 4/5: Fetch + build"
    mkdir -p "$INSTALL_HOME" "$(dirname "$DAEMON_BIN")"
    if [ -d "$INSTALL_HOME/.git" ]; then
        ( cd "$INSTALL_HOME" && git pull -q )
    else
        git clone -q https://github.com/M00C1FER/mcp-citation-research.git "$INSTALL_HOME"
    fi
    ( cd "$INSTALL_HOME/daemon" && go build -o "$DAEMON_BIN" ./cmd/citation-researchd )
    ok "daemon built → $DAEMON_BIN"

    ( cd "$INSTALL_HOME/server" && python3 -m venv .venv && \
        .venv/bin/pip install --quiet --upgrade pip && \
        .venv/bin/pip install --quiet -e . )
    local BIN="${HOME}/.local/bin"; mkdir -p "$BIN"
    cat > "$BIN/citation-research-mcp" <<EOF
#!/usr/bin/env bash
exec "$INSTALL_HOME/server/.venv/bin/citation-research-mcp" "\$@"
EOF
    chmod +x "$BIN/citation-research-mcp"
    ok "MCP frontend installed → $BIN/citation-research-mcp"

    # Optional: write a launcher that boots the daemon with the chosen backend.
    cat > "$BIN/citation-researchd-start" <<EOF
#!/usr/bin/env bash
# Boots citation-researchd with the install-wizard's chosen search backend.
exec "$DAEMON_BIN" -addr 127.0.0.1:8090 -searxng "${SEARXNG_URL}" "\$@"
EOF
    chmod +x "$BIN/citation-researchd-start"

    say ""; say "Step 5/5: Verify"
    if "$DAEMON_BIN" -addr "127.0.0.1:18091" -searxng "$SEARXNG_URL" >/tmp/cr.log 2>&1 & then
        local PID=$!; sleep 1
        if curl -fsS http://127.0.0.1:18091/healthz >/dev/null 2>&1; then ok "daemon healthcheck OK"; else warn "daemon healthcheck failed (see /tmp/cr.log)"; fi
        kill "$PID" 2>/dev/null || true
    fi
    say ""
    ok "Done. Start the daemon with: citation-researchd-start &"
    info "Then wire the MCP server into your client (e.g. Claude Desktop config)."
}
main "$@"
