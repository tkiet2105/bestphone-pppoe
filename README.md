# bestphone-pppoe

PPPoE multi-line proxy gateway cho Debian 12.

- Mỗi PPPoE session = 1 listener SOCKS5/HTTP riêng (port từ pool 30000–40000).
- Outbound socket bind vào iface `ppp<N>` qua `SO_BINDTODEVICE` + peer config `nodefaultroute` → **tắt WAN vẫn truy cập được proxy**.
- Mỗi proxy hỗ trợ **nhiều credentials** (user/pass) cùng lúc.
- REST API đầy đủ + endpoint export proxy text nhanh (`/api/v1/proxies/export`).
- UI vanilla JS (no build step).
- Auto-update qua file `VERSION` (bump = fleet pull trong ≤1h).

## Quickstart

```bash
curl -fsSL https://raw.githubusercontent.com/tkiet2105/bestphone-pppoe/main/deploy/install.sh | sudo bash
```

Hoặc với token (repo private):
```bash
curl -fsSL -H "Authorization: Bearer <pat>" \
  https://raw.githubusercontent.com/tkiet2105/bestphone-pppoe/main/deploy/install.sh \
  | INSTALL_GITHUB_TOKEN=<pat> sudo -E bash
```

Sau khi cài: `http://<host>/`, đăng nhập bằng token in ở cuối install (cũng nằm trong `/etc/default/bestphone-pppoe`).

## Source layout

Xem [PLAN.md](PLAN.md) để biết kiến trúc chi tiết.

- `cmd/bestphone-pppoe/` — entry point
- `internal/pppoe/` — PPPoE manager (dial/hangup/rotate/watchdog)
- `internal/proxy/server/` — SOCKS5+HTTP listener với SO_BINDTODEVICE
- `internal/api/` — REST handlers
- `ui/` — vanilla JS UI (deploy ra `/var/www/bestphone-pppoe`)
- `deploy/` — install.sh, systemd, nginx, updater

## Build local

```bash
CGO_ENABLED=0 go build -o bestphone-pppoe ./cmd/bestphone-pppoe
ADMIN_TOKEN=devtoken ./bestphone-pppoe
```

## Auto-update — quy tắc ship

- **Bump `VERSION`** + sync `appVersion` ở `cmd/bestphone-pppoe/main.go` → fleet client pull trong ≤1h.
- Push code KHÔNG đụng VERSION → fleet KHÔNG pull → debug an toàn.

## License

Private. © tkiet2105.
