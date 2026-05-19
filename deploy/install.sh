#!/bin/bash
# bestphone-pppoe — single-shot installer for Debian 12.
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/tkiet2105/bestphone-pppoe/main/deploy/install.sh | sudo bash
# Env override:
#   INSTALL_GITHUB_TOKEN=... (token cho git pull về private repo)

set -euo pipefail

REPO_URL="https://github.com/tkiet2105/bestphone-pppoe.git"
REPO_DIR="/opt/bestphone-pppoe"
GO_VERSION="1.22.6"

log() { echo -e "\033[1;36m[install]\033[0m $*"; }
err() { echo -e "\033[1;31m[install]\033[0m $*" >&2; exit 1; }

# ── 1. Pre-flight ────────────────────────────────────────────────────────────
[ "$(id -u)" = "0" ] || err "phải chạy với root"
. /etc/os-release 2>/dev/null || err "không đọc được /etc/os-release"
[ "$ID" = "debian" ] || log "WARN: hệ là $ID (script test trên Debian 12)"

# ── 2. Apt deps ──────────────────────────────────────────────────────────────
log "Step 2: apt deps"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y --no-install-recommends \
  ppp pppoe iproute2 iptables nginx sqlite3 \
  curl ca-certificates jq git rsync openssl tar >/dev/null
log "✓ deps installed"

# ── 3. Go ────────────────────────────────────────────────────────────────────
need_go=1
if command -v go >/dev/null 2>&1; then
  CUR_GO=$(go version | awk '{print $3}' | sed 's/go//')
  if [ "$(printf '%s\n%s\n' "1.22" "$CUR_GO" | sort -V | head -n1)" = "1.22" ]; then
    need_go=0
  fi
fi
if [ "$need_go" = "1" ]; then
  log "Step 3: cài Go $GO_VERSION"
  cd /tmp
  curl -fsSLO "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
  rm -f "go${GO_VERSION}.linux-amd64.tar.gz"
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
fi
log "✓ go $(go version | awk '{print $3}')"
export PATH=$PATH:/usr/local/go/bin

# ── 4. Clone repo ────────────────────────────────────────────────────────────
log "Step 4: clone/pull repo"
GIT_ARGS=()
if [ -n "${INSTALL_GITHUB_TOKEN:-}" ]; then
  AUTH="Authorization: Basic $(printf 'tkiet2105:%s' "$INSTALL_GITHUB_TOKEN" | base64 -w0)"
  GIT_ARGS=(-c "http.extraHeader=$AUTH")
fi
if [ -d "$REPO_DIR/.git" ]; then
  cd "$REPO_DIR"
  git "${GIT_ARGS[@]}" pull origin main
else
  git "${GIT_ARGS[@]}" clone "$REPO_URL" "$REPO_DIR"
  cd "$REPO_DIR"
fi
log "✓ repo at $(git rev-parse --short HEAD)"

# ── 5. Build ─────────────────────────────────────────────────────────────────
log "Step 5: build backend"
CGO_ENABLED=0 go build -o /usr/local/bin/bestphone-pppoe ./cmd/bestphone-pppoe
log "✓ binary $(ls -la /usr/local/bin/bestphone-pppoe | awk '{print $5}') bytes"

# ── 6. Bin scripts ───────────────────────────────────────────────────────────
log "Step 6: install bin scripts"
install -m 0755 deploy/bin/bestphone-pppoe-update /usr/local/bin/bestphone-pppoe-update
install -m 0755 deploy/bin/bestphone-pppoe-deploy-ui /usr/local/bin/bestphone-pppoe-deploy-ui

# ── 7. State dirs ────────────────────────────────────────────────────────────
log "Step 7: state dirs"
mkdir -p /var/lib/bestphone-pppoe /var/log/bestphone-pppoe /var/www/bestphone-pppoe /etc/ppp/peers

# ── 8. Config + admin token ──────────────────────────────────────────────────
log "Step 8: config + admin token"
if [ ! -f /etc/default/bestphone-pppoe ]; then
  TOKEN=$(openssl rand -hex 32)
  cat > /etc/default/bestphone-pppoe <<EOF
# bestphone-pppoe runtime config — sửa rồi systemctl restart bestphone-pppoe
LISTEN_ADDR=0.0.0.0:8080
DB_PATH=/var/lib/bestphone-pppoe/data.db
PROXY_PORT_MIN=30000
PROXY_PORT_MAX=40000
ADMIN_TOKEN=$TOKEN
DIAL_CONCURRENT=5
# BESTPHONE_PPPOE_ROTATE_REQUIRE_NEW_IP=1   # uncomment để rotate đòi đổi IP
EOF
  chmod 0600 /etc/default/bestphone-pppoe
  NEW_TOKEN=1
else
  TOKEN=$(. /etc/default/bestphone-pppoe; echo "$ADMIN_TOKEN")
  NEW_TOKEN=0
fi

# token cho auto-update pull (optional, user điền sau nếu repo private)
if [ ! -f /etc/default/bestphone-pppoe-pull-token ]; then
  cat > /etc/default/bestphone-pppoe-pull-token <<EOF
# Set GITHUB_PULL_TOKEN nếu repo private. Để rỗng = update chỉ dùng public clone URL.
GITHUB_PULL_TOKEN=${INSTALL_GITHUB_TOKEN:-}
EOF
  chmod 0600 /etc/default/bestphone-pppoe-pull-token
fi

# ── 9. systemd ───────────────────────────────────────────────────────────────
log "Step 9: systemd"
install -m 0644 deploy/systemd/bestphone-pppoe.service /etc/systemd/system/
install -m 0644 deploy/systemd/bestphone-pppoe-update.service /etc/systemd/system/
install -m 0644 deploy/systemd/bestphone-pppoe-update.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now bestphone-pppoe
systemctl enable --now bestphone-pppoe-update.timer
sleep 2

# ── 10. nginx ────────────────────────────────────────────────────────────────
log "Step 10: nginx"
install -m 0644 deploy/nginx/bestphone-pppoe.conf /etc/nginx/sites-available/bestphone-pppoe
ln -sf /etc/nginx/sites-available/bestphone-pppoe /etc/nginx/sites-enabled/bestphone-pppoe
rm -f /etc/nginx/sites-enabled/default
nginx -t
systemctl reload nginx

# ── 11. Deploy UI ────────────────────────────────────────────────────────────
log "Step 11: deploy UI"
/usr/local/bin/bestphone-pppoe-deploy-ui

# ── 12. Verify ───────────────────────────────────────────────────────────────
log "Step 12: verify health"
sleep 1
if curl -fsS http://127.0.0.1:8080/api/v1/health | jq . 2>/dev/null; then
  log "✓ backend healthy"
else
  log "⚠ backend health check failed — journalctl -u bestphone-pppoe -n 30"
fi

# ── 13. Summary ──────────────────────────────────────────────────────────────
IP=$(hostname -I | awk '{print $1}')
echo
echo "═══════════════════════════════════════════════════════════════════════"
echo "  bestphone-pppoe installed."
echo "═══════════════════════════════════════════════════════════════════════"
echo
echo "  UI:           http://${IP}/"
if [ "$NEW_TOKEN" = "1" ]; then
  echo "  Admin token:  $TOKEN"
  echo "  (lưu ngay — token này chỉ hiển thị duy nhất lần này)"
else
  echo "  Admin token:  (đã có từ trước, xem /etc/default/bestphone-pppoe)"
fi
echo
echo "  Service:      systemctl status bestphone-pppoe"
echo "  Logs:         journalctl -u bestphone-pppoe -f"
echo "  Auto-update:  systemctl status bestphone-pppoe-update.timer"
echo "  Manual update: bestphone-pppoe-update"
echo
echo "═══════════════════════════════════════════════════════════════════════"
