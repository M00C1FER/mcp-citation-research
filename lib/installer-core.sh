#!/usr/bin/env bash
# lib/installer-core.sh — canonical shared installer library
#
# Source of truth for all M00C1FER repos. Other repos vendor a copy of this
# file and use CI to detect drift against upstream.
#
# Usage (from per-repo install.sh):
#   REPO_NAME="my-repo"
#   ENTRY_POINT="my-entry-cmd"
#   REPO_NEEDS_GO="0"          # set "1" for Go-hybrid repos
#   source "$(dirname "$0")/lib/installer-core.sh"
#   run_install "$@"
#
# Public API:
#   run_install  — orchestrates all 6 phases
#
# Internal functions (prefixed _) are implementation details.

# ── Utility helpers ──────────────────────────────────────────────────────────

_need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

_die() {
  printf '  \033[31m✗\033[0m %s\n' "$*" >&2
  exit 1
}

_warn() {
  printf '  \033[33m!\033[0m %s\n' "$*"
}

_info() {
  printf '  \033[34mℹ\033[0m %s\n' "$*"
}

_success() {
  printf '  \033[32m✓\033[0m %s\n' "$*"
}

# ── Environment detection ────────────────────────────────────────────────────

_is_wsl() {
  grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null
}

_is_termux() {
  [ -n "${TERMUX_VERSION:-}" ] || [ -d /data/data/com.termux ]
}

# ── WSL2 guards (G1-G6) ──────────────────────────────────────────────────────

_wsl_guard() {
  _is_wsl || return 0

  # G1 — refuse WSL1 (no /mnt/wsl interop)
  [ -f /proc/sys/fs/binfmt_misc/WSLInterop ] || _die "WSL1 unsupported. Run: wsl --set-version <distro> 2"

  # G2 — strip Windows PATH (prevents node/python/npm collisions)
  export PATH
  PATH="$(printf '%s' "$PATH" | tr ':' '\n' | grep -v '^/mnt/' | tr '\n' ':' | sed 's/:$//')"

  # G3 — DrvFs perf warning (5-50x slower)
  case "$PWD" in
    /mnt/[a-z]/*) _warn "Project on Windows filesystem (/mnt/...). Move to ~/ for normal performance." ;;
  esac

  # G4 — CRLF self-heal (most-filed WSL bug across nvm/mise/desktop installers)
  if file "$0" 2>/dev/null | grep -q CRLF; then
    exec bash <(tr -d '\r' < "$0") "$@"
  fi

  # G5 — networking-mode advisory (mirrored vs NAT)
  grep -q networkingMode /etc/wsl.conf 2>/dev/null || \
    _info "WSL2 in NAT mode. 127.0.0.1 services unreachable from Windows host. See: aka.ms/wslnetworking"

  # G6 — systemd advisory (Debian/Kali WSL ship without)
  if ! systemctl is-system-running >/dev/null 2>&1; then
    _info "systemd inactive. Add '[boot]\nsystemd=true' to /etc/wsl.conf for service support."
  fi
}

# ── Termux guard (best-effort tier) ─────────────────────────────────────────

_termux_guard() {
  _is_termux || return 0
  SUDO=""
  INSTALL_PREFIX="$HOME/.local"
  SKIP_SERVICES=1
  pkg install -y python git curl gh 2>/dev/null || true
  _warn "Termux best-effort tier: services skipped, Go via prebuilt arm64 binary."
}

# ── OS detection ─────────────────────────────────────────────────────────────

_detect_os() {
  if [ -f /etc/os-release ]; then
    # shellcheck source=/dev/null
    . /etc/os-release
    OS_ID="${ID:-unknown}"
    OS_LIKE="${ID_LIKE:-}"
    OS_VER="${VERSION_ID:-rolling}"
  else
    OS_ID=$(uname -s | tr '[:upper:]' '[:lower:]')
    OS_LIKE=""
    OS_VER="unknown"
  fi
}

_is_debian_family() {
  [ "$OS_ID" = "debian" ] || [ "$OS_ID" = "ubuntu" ] || [ "$OS_ID" = "kali" ] \
    || printf '%s' "$OS_LIKE" | grep -q "debian"
}

# ── Package-manager detection (most-specific-first) ──────────────────────────

_detect_pkg_mgr() {
  if [ -n "${TERMUX_VERSION:-}" ] || [ -d /data/data/com.termux ]; then
    PKG_MGR=pkg
  elif command -v apk >/dev/null 2>&1 && [ -f /etc/alpine-release ]; then
    PKG_MGR=apk
  elif command -v pacman >/dev/null 2>&1; then
    PKG_MGR=pacman
  elif command -v dnf >/dev/null 2>&1; then
    PKG_MGR=dnf
  elif command -v apt-get >/dev/null 2>&1; then
    PKG_MGR=apt
  else
    _die "Unsupported package manager"
  fi
}

# ── Alpine bash bootstrap ────────────────────────────────────────────────────

_ensure_bash_alpine() {
  if [ -f /etc/alpine-release ] && ! command -v bash >/dev/null 2>&1; then
    $SUDO apk add --no-cache bash
  fi
}

# ── System dependency install ────────────────────────────────────────────────

_install_system_deps() {
  case "$PKG_MGR" in
    apt)
      $SUDO apt-get update -qq && \
        $SUDO apt-get install -y curl git python3 python3-venv python3-pip build-essential libffi-dev
      ;;
    dnf)
      $SUDO dnf install -y curl git python3 python3-pip python3-devel gcc libffi-devel
      ;;
    pacman)
      $SUDO pacman -Sy --noconfirm curl git python python-pip base-devel
      ;;
    apk)
      $SUDO apk add --no-cache bash curl git python3 py3-pip gcc musl-dev python3-dev libffi-dev
      ;;
    pkg)
      pkg install -y curl git python
      ;;
  esac
}

# ── Python venv readiness ────────────────────────────────────────────────────

_ensure_python_venv() {
  if ! python3 -m venv --help >/dev/null 2>&1; then
    case "$PKG_MGR" in
      apt)
        PY_VER=$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')
        $SUDO apt-get install -y "python${PY_VER}-venv" python3-venv
        ;;
      apk)
        $SUDO apk add --no-cache py3-virtualenv
        ;;
    esac
  fi
}

# ── Go install (gated by REPO_NEEDS_GO=1) ───────────────────────────────────

_install_go() {
  [ "${REPO_NEEDS_GO:-0}" = "1" ] || return 0
  if command -v go >/dev/null 2>&1; then
    cur=$(go version | awk '{print $3}' | tr -d go)
    if python3 -c "import sys; a,b=map(int,'$cur'.split('.')[:2]); sys.exit(0 if (a,b)>=(1,21) else 1)" 2>/dev/null; then
      _success "Go $cur already installed"
      return 0
    fi
  fi
  case "$PKG_MGR" in
    pacman)
      $SUDO pacman -S --noconfirm go
      ;;
    dnf)
      $SUDO dnf install -y golang
      ;;
    apk)
      $SUDO apk add --no-cache go && export CGO_ENABLED=0
      ;;
    apt|*)
      local VER="1.24.2"
      local ARCH
      ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/;s/armv7l/armv6l/')
      curl -fsSL "https://go.dev/dl/go${VER}.linux-${ARCH}.tar.gz" | $SUDO tar -C /usr/local -xz
      export PATH="$PATH:/usr/local/go/bin"
      ;;
  esac
}

# ── Isolation choice ─────────────────────────────────────────────────────────

_choose_isolation() {
  if [ -z "${ISOLATION:-}" ] && [ "${INSTALLER_UNATTENDED:-0}" != "1" ]; then
    _info "Isolation choice: (1) venv [default]  (2) pipx  (3) system  (4) docker"
    read -r -p "Choice [1]: " choice </dev/tty || choice=1
    case "${choice:-1}" in
      1) ISOLATION=venv ;;
      2) ISOLATION=pipx ;;
      3) ISOLATION=system ;;
      4) ISOLATION=docker ;;
      *) ISOLATION=venv ;;
    esac
  fi
  ISOLATION="${ISOLATION:-venv}"
}

# ── Self-install ─────────────────────────────────────────────────────────────

_install_self() {
  case "$ISOLATION" in
    venv)
      local venv_dir="$HOME/.local/share/${REPO_NAME}/.venv"
      mkdir -p "$(dirname "$venv_dir")"
      python3 -m venv "$venv_dir"
      "$venv_dir/bin/pip" install --quiet --upgrade pip
      "$venv_dir/bin/pip" install --quiet -e .
      mkdir -p "$HOME/.local/bin"
      cat > "$HOME/.local/bin/${ENTRY_POINT}" <<EOF
#!/usr/bin/env bash
exec "$venv_dir/bin/${ENTRY_POINT}" "\$@"
EOF
      chmod +x "$HOME/.local/bin/${ENTRY_POINT}"
      _success "Installed via venv → $venv_dir"
      ;;
    pipx)
      if ! _need_cmd pipx; then
        python3 -m pip install --user --quiet pipx
      fi
      pipx install --force .
      _success "Installed via pipx"
      ;;
    system)
      $SUDO python3 -m pip install --break-system-packages -e . || \
        python3 -m pip install --user -e .
      _success "Installed into system Python"
      ;;
    docker)
      _die "Docker isolation not yet implemented (v0.1 stub). Use --venv or --pipx."
      ;;
    *)
      _die "Unknown isolation mode: ${ISOLATION}"
      ;;
  esac
}

# ── Smoke verify ─────────────────────────────────────────────────────────────

_smoke_verify() {
  if _need_cmd "${ENTRY_POINT}"; then
    if "${ENTRY_POINT}" --help 2>&1 | grep -qE 'Usage:|usage:'; then
      _success "Smoke verify passed: ${ENTRY_POINT} --help"
    else
      _warn "Smoke verify: ${ENTRY_POINT} --help ran but no Usage: found"
    fi
  else
    _warn "Smoke verify: '${ENTRY_POINT}' not found in PATH (add \$HOME/.local/bin to PATH)"
  fi
}

# ── Summary ──────────────────────────────────────────────────────────────────

_summary() {
  printf '\n'
  _success "Installation complete."
  printf '\n'
  printf '  Add to PATH (if not already):\n'
  printf '    export PATH="$HOME/.local/bin:$PATH"\n'
  printf '\n'
  printf '  Or source your shell config:\n'
  printf '    source ~/.bashrc   # or ~/.zshrc\n'
  printf '\n'
  printf '  Run:\n'
  printf '    %s --help\n' "${ENTRY_POINT}"
  printf '\n'
}

# ── Public entry point ───────────────────────────────────────────────────────

run_install() {
  # Repo-side install.sh must set these BEFORE sourcing installer-core.sh:
  #   REPO_NAME      — e.g., "mcp-citation-research"
  #   ENTRY_POINT    — e.g., "citation-research-mcp"
  #   REPO_NEEDS_GO  — "1" if repo has a Go component (default "0")
  # Optional per-repo config hook:
  #   configure_<REPO_NAME_UNDERSCORED>()  — called in Phase 5

  : "${REPO_NAME:?REPO_NAME must be set by caller}"
  : "${ENTRY_POINT:?ENTRY_POINT must be set by caller}"

  # Parse flags / env
  INSTALLER_UNATTENDED="${INSTALLER_UNATTENDED:-0}"
  for arg in "$@"; do
    case "$arg" in
      --unattended|-y) INSTALLER_UNATTENDED=1 ;;
      --venv)          ISOLATION=venv ;;
      --pipx)          ISOLATION=pipx ;;
      --system)        ISOLATION=system ;;
      --docker)        ISOLATION=docker ;;
      --help|-h)
        cat <<HELP
Usage: install.sh [--unattended] [--venv|--pipx|--system|--docker]

  --unattended  Skip prompts; use defaults. Required for CI.
  --venv        Isolate via Python venv (default).
  --pipx        Isolate via pipx (recommended for end-users).
  --system      Install into system Python (PEP 668: requires --break-system-packages).
  --docker      Build local Docker image and provide run wrapper.
HELP
        exit 0
        ;;
    esac
  done

  SUDO="sudo"
  [ "$(id -u)" = "0" ] && SUDO=""

  # Early guards
  _wsl_guard "$@"
  _termux_guard
  _ensure_bash_alpine

  # Detect environment
  _detect_os
  _detect_pkg_mgr

  # ── Phase 1/6: Pre-flight ─────────────────────────────────────────────────
  printf '\n\033[1m[1/6] Pre-flight\033[0m\n'

  if ! _need_cmd python3; then
    _info "python3 not found — installing system deps"
    _install_system_deps
  fi

  # Verify python3 >= 3.10
  if ! python3 -c 'import sys; sys.exit(0 if sys.version_info >= (3,10) else 1)' 2>/dev/null; then
    _die "Python 3.10+ required (found: $(python3 --version 2>&1 || echo 'none'))"
  fi
  _success "Python $(python3 -c 'import sys; print("%d.%d" % sys.version_info[:2])')"

  if ! _need_cmd curl; then
    _info "curl not found — installing system deps"
    _install_system_deps
  fi
  _success "curl"

  if ! _need_cmd git; then
    _info "git not found — installing system deps"
    _install_system_deps
  fi
  _success "git"

  _ensure_python_venv

  # Check 500MB free disk
  local free_mb
  free_mb=$(df -m "${HOME}" 2>/dev/null | awk 'NR==2 {print $4}' || echo 9999)
  if [ "${free_mb:-0}" -lt 500 ] 2>/dev/null; then
    _warn "Less than 500MB disk space available (${free_mb}MB free)"
  fi

  # ── Phase 2/6: Existing install check ────────────────────────────────────
  printf '\n\033[1m[2/6] Existing install check\033[0m\n'
  if [ -d "$HOME/.local/share/${REPO_NAME}" ] && [ "${INSTALLER_UNATTENDED}" != "1" ]; then
    _info "Existing install detected at \$HOME/.local/share/${REPO_NAME}"
    _info "(Proceeding with reinstall. Update/abort prompts in v0.2.)"
  fi

  # ── Phase 3/6: Isolation choice ──────────────────────────────────────────
  printf '\n\033[1m[3/6] Isolation choice\033[0m\n'
  if [ "${INSTALLER_UNATTENDED}" != "1" ]; then
    _choose_isolation
  fi
  ISOLATION="${ISOLATION:-venv}"
  _info "Using isolation: ${ISOLATION}"

  # ── Phase 4/6: Dependency install ────────────────────────────────────────
  printf '\n\033[1m[4/6] Dependency install\033[0m\n'
  _install_system_deps
  _install_go

  # ── Phase 5/6: Configuration (per-repo hook) ──────────────────────────────
  printf '\n\033[1m[5/6] Configuration\033[0m\n'
  local _hook_fn
  _hook_fn="configure_$(printf '%s' "${REPO_NAME}" | tr '-' '_')"
  if declare -f "${_hook_fn}" >/dev/null 2>&1 && [ "${INSTALLER_UNATTENDED}" != "1" ]; then
    "${_hook_fn}"
  fi

  # Self-install
  _install_self

  # ── Phase 6/6: Smoke verify ──────────────────────────────────────────────
  printf '\n\033[1m[6/6] Smoke verify\033[0m\n'
  _smoke_verify

  _summary
}
