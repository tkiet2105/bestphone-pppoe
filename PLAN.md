# bestphone-pppoe — design doc

PPPoE multi-line proxy gateway. Mỗi PPPoE session = 1 listener SOCKS5/HTTP riêng,
outbound bind socket vào iface `ppp<N>` qua `SO_BINDTODEVICE` — traffic đi đúng
line PPPoE, **không qua default route**. Tắt WAN vẫn truy cập được proxy.

Stack: Go 1.22+, SQLite (modernc/sqlite — pure Go, CGO=0), Gin, vanilla JS UI,
systemd, nginx.

> Repository này hiện chỉ chứa design doc. Engineer/Claude session kế tiếp đọc file này → tạo source theo cấu trúc §1 → implement theo §2–§8 → verify theo §9 + §12.

---

## 1. Repository layout

```
bestphone-pppoe/
├── VERSION                      # 1.0.0 — bump để trigger fleet auto-update
├── README.md                    # quickstart
├── PLAN.md                      # file này — design doc đầy đủ
├── go.mod / go.sum
├── cmd/
│   └── bestphone-pppoe/main.go  # entry: gin server + pppoe.Init + proxysrv.Init
├── internal/
│   ├── config/                  # env load, listen addr, port range
│   ├── db/                      # gorm.Open(sqlite) + AutoMigrate
│   ├── models/                  # Line / Session / Proxy / Credential / Token
│   ├── pppoe/
│   │   ├── manager.go           # Dial/Hangup/Rotate/Watchdog/RestoreState
│   │   ├── config.go            # gen /etc/ppp/peers/<sess> + chap/pap-secrets
│   │   └── ifquery.go           # ip link parse, public IP probe
│   ├── proxy/server/
│   │   ├── manager.go           # Start(proxyID)/Stop(proxyID)/ReloadCreds
│   │   ├── listener.go          # accept + protocol multiplex
│   │   ├── socks5.go            # SOCKS5 USER/PASS
│   │   ├── http.go              # HTTP CONNECT + forward
│   │   ├── dial.go              # SO_BINDTODEVICE Dialer  ← KEY
│   │   └── auth.go              # hasAuth/authMatch constant-time
│   ├── api/
│   │   ├── routes.go            # gin route registration
│   │   ├── middleware.go        # Bearer token check
│   │   ├── handlers_line.go     # /lines CRUD
│   │   ├── handlers_session.go  # /sessions CRUD + rotate
│   │   ├── handlers_cred.go     # /proxies/:id/credentials CRUD + bulk
│   │   ├── handlers_export.go   # /proxies/export text/plain ip:port:u:p
│   │   └── handlers_health.go   # /health
│   └── events/hub.go            # in-process pub/sub cho SSE
├── ui/
│   ├── index.html               # login + dashboard
│   ├── lines.html               # list/create lines
│   ├── sessions.html            # list/create sessions + multi-cred
│   ├── css/app.css
│   └── js/
│       ├── api-client.js        # fetch wrapper Bearer auth
│       ├── lines.js
│       └── sessions.js
├── deploy/
│   ├── install.sh               # single-shot installer (root)
│   ├── systemd/
│   │   ├── bestphone-pppoe.service
│   │   ├── bestphone-pppoe-update.service
│   │   └── bestphone-pppoe-update.timer
│   ├── nginx/bestphone-pppoe.conf
│   └── bin/
│       ├── bestphone-pppoe-update    # smart updater
│       └── bestphone-pppoe-deploy-ui # rsync ui/ → /var/www/bestphone-pppoe
└── docs/
    └── api.md                   # OpenAPI-style endpoint reference
```

## 2. Database schema (SQLite, gorm AutoMigrate)

### `lines`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | INTEGER PK | |
| name | TEXT | hiển thị (vd "FPT-Q1") |
| iface | TEXT | physical NIC, vd `enp3s0` |
| use_macvlan | BOOL | true → mỗi session có macvlan riêng |
| max_sessions | INTEGER | quota, default 8 |
| created_at | TIMESTAMP | |

### `sessions`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | INTEGER PK | |
| line_id | INTEGER FK | |
| ppp_unit | INTEGER UNIQUE | pppX, allocate từ pool 0..999 |
| iface | TEXT | `ppp<N>` sau khi dial xong |
| username | TEXT | PPPoE PAP/CHAP user |
| password | TEXT | |
| mac | TEXT INDEX | spoof MAC (rỗng = dùng NIC gốc) |
| status | TEXT | `disconnected\|dialing\|connected\|error` |
| ip | TEXT | inet trên `ppp<N>` |
| public_ip | TEXT | đo qua ipify với `--interface` |
| last_error | TEXT | |
| last_rotate_at | TIMESTAMP | |

### `proxies`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | INTEGER PK | |
| session_id | INTEGER UNIQUE | 1-1 với session |
| port | INTEGER UNIQUE | từ pool 30000–40000 |
| status | TEXT | `running\|stopped` |

### `proxy_credentials` (multi-cred per proxy)
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | INTEGER PK | |
| proxy_id | INTEGER INDEX FK | |
| label | TEXT | "client-A", "legacy"... |
| username | TEXT | |
| password | TEXT | |
| enabled | BOOL | default true |

Listener authenticate khi `(user, pass)` match BẤT KỲ row `enabled=true`.

### `tokens` (admin Bearer)
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | INTEGER PK | |
| token | TEXT UNIQUE | random 32 bytes hex |
| label | TEXT | "admin-default" |
| created_at | TIMESTAMP | |

Install script sinh 1 token mặc định, lưu vào `/etc/default/bestphone-pppoe` + insert DB.

## 3. PPPoE Manager (key behaviors)

### Dial flow (`internal/pppoe/manager.go`)

```
Dial(sessionID):
  lock per-line mutex (serialize concurrent dials cùng line)
  acquire dialSem (global concurrency cap, default 5)
  if session.MAC != "":
    ensure macvlan `mvbp-<sid>` on line.iface với MAC spoof
    set peer iface = macvlan name
  else:
    peer iface = line.iface
  write /etc/ppp/peers/bp-sess-<sid>:
    plugin rp-pppoe.so <peer_iface>
    unit <ppp_unit>
    name "<username>"; user "<username>"
    noipdefault; usepeerdns; persist; maxfail 0
    holdoff 10; lcp-echo-interval 20; lcp-echo-failure 3
    mtu 1492; mru 1492
    nodefaultroute         ← KEY: KHÔNG add default route, traffic chỉ qua khi bind
    noauth
    ipparam bp-sess-<sid>
  upsert /etc/ppp/chap-secrets + pap-secrets
  exec /usr/sbin/pppd call bp-sess-<sid> updetach
  poll `ip -o link show ppp<N>` mỗi 500ms, timeout 30s
  if iface UP:
    parse IP local, save session.iface + session.ip
    publish event session.status → connected
    async: curl --interface ppp<N> https://api.ipify.org → public_ip
  else:
    hangup, status=error, last_error
```

### HARD RULE (anti-lockout)
**Không retry sau AuthNak.** Status `error` với `last_error` chứa "AuthNak" → chỉ user manual rotate mới redial. Watchdog phải SKIP session đang ở `error`.

### Rotate
1. Hangup pppd (`kill -TERM` PID, fallback `pkill -f bp-sess-<id>`).
2. Sleep 3s (BRAS settle).
3. Dial lại.
4. Mặc định chấp nhận same-IP = success (env `BESTPHONE_PPPOE_ROTATE_REQUIRE_NEW_IP=1` để đổi).

### Watchdog
Goroutine 20s tick. Với mỗi session `status=connected`:
- Nếu iface KHÔNG còn UP → redial (trừ khi `disabled_at` set).
- Nếu pppd PID không tồn tại → redial.

### RestoreState (boot)
- Đọc tất cả `ppp.unit` hiện có (`ip -br link | grep ^ppp`).
- Adopt session nào đã có iface UP.
- Dial pending session (status=connected trong DB nhưng iface không UP).

## 4. Proxy listener (THE CORE)

### Multi-protocol multiplex (`internal/proxy/server/listener.go`)

```go
acceptLoop:
  conn := ln.Accept()
  go handle(conn):
    peek 1 byte
    if b == 0x05:        // SOCKS5
      handleSocks5(conn)
    else if isASCII(b):  // HTTP CONNECT or method
      handleHTTP(conn)
    else:
      close
```

### SO_BINDTODEVICE (`dial.go`) ← CORE BEHAVIOR

```go
func newBoundDialer(iface string) *net.Dialer {
    return &net.Dialer{
        Timeout:   15 * time.Second,
        KeepAlive: 30 * time.Second,
        Control: func(network, address string, c syscall.RawConn) error {
            var opErr error
            c.Control(func(fd uintptr) {
                opErr = syscall.SetsockoptString(int(fd),
                    syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
            })
            return opErr
        },
    }
}
```

- Tất cả outbound socket bind vào `ppp<N>` của session đó.
- Yêu cầu CAP_NET_RAW (service chạy root qua systemd).
- Kết hợp `nodefaultroute` trong peer config → kernel KHÔNG add `0.0.0.0/0 dev ppp<N>` → WAN traffic gốc giữ nguyên. Khi tắt WAN, traffic dial-out của user-app vẫn fail, nhưng socket BOUND ppp<N> vẫn route được vì bind explicit.

### Auth (`auth.go`)

```go
func (l *listener) hasAuth() bool {
    return len(l.creds) > 0
}

func (l *listener) authMatch(user, pass []byte) bool {
    matched := 0
    for _, c := range l.creds {
        u := subtle.ConstantTimeCompare(user, []byte(c.Username))
        p := subtle.ConstantTimeCompare(pass, []byte(c.Password))
        matched |= u & p
    }
    return matched == 1
}
```

- SOCKS5 offer method `0x02` (USER/PASS) nếu hasAuth, else `0x00`.
- HTTP đọc `Proxy-Authorization: Basic <b64>`.
- `ReloadCreds(proxyID)` re-query DB credentials cho listener đó, swap atomically (RW mutex).

### Lifecycle (`manager.go`)

| Method | Hành vi |
|---|---|
| `Start(proxyID)` | Load proxy + session từ DB, tạo `net.Listen("tcp", ":port")`, spawn acceptLoop. Status → `running`. |
| `Stop(proxyID)` | Cancel ctx, close listener, status → `stopped`. |
| `ReloadCreds(proxyID)` | Re-query `proxy_credentials WHERE proxy_id=X AND enabled=true`, swap. |
| `RestoreAll()` | Boot: với mỗi proxy status=`running` trong DB → `Start`. |

## 5. REST API

Base path: `/api/v1`. Auth: header `Authorization: Bearer <token>`.

### Health
- `GET /health` → `{service, version, uptime}`

### Lines
- `POST /lines` body `{name, iface, use_macvlan, max_sessions}` → 201
- `GET  /lines` → list + session_count
- `GET  /lines/:id` → detail
- `POST /lines/:id/delete` → cascade xóa sessions + proxies

### Sessions
- `POST /lines/:id/sessions` body `{username, password, mac?}` → tạo + dial + start proxy
- `POST /lines/:id/sessions/bulk` body `{count, creds:[{user,pass,mac?}]}` → tạo N song song
- `GET  /sessions` → list
- `GET  /sessions/:id` → detail (runtime status, public_ip)
- `POST /sessions/:id/delete` → hangup + remove peer + remove DB
- `POST /sessions/:id/rotate` → hangup → redial
- `POST /sessions/:id/enabled` body `{enabled}` → start/stop listener (giữ tunnel)

### Credentials (multi-cred per proxy)
- `GET    /proxies/:id/credentials` → list
- `POST   /proxies/:id/credentials` body `{label, username, password}` → tạo 1
- `POST   /proxies/:id/credentials/bulk` body `{count, label_prefix}` → tạo N random
- `PUT    /proxies/:id/credentials/:cid` body `{username?, password?, enabled?}`
- `DELETE /proxies/:id/credentials/:cid`

Mọi mutation gọi `proxysrv.M.ReloadCreds(proxyID)` (hot-reload, không close listener).

### Export proxy (BULK GET — endpoint "lấy proxy nhanh")
- `GET /proxies/export?type=public|local&format=text|json`
- `text/plain`:
  ```
  <ip>:<port>:<user>:<pass>
  <ip>:<port>:<user>:<pass>
  ```
  1 dòng / 1 cred enabled. Session có 3 cred → 3 dòng.
- `type=public` → dùng `session.public_ip`, `type=local` → IP LAN của host.
- `json`: array `[{session_id, line, port, ip, creds:[{user,pass,label}]}]`

### Rotate batch
- `POST /rotate` body `{session_ids:[...], concurrency?:5}` → fire-and-return, results streamed qua SSE.

### Events (SSE)
- `GET /events` (Bearer auth qua query `?token=` cho EventSource).
- Stream events: `session.status`, `session.public_ip`, `session.rotate`, `proxy.cred_changed`.

### Tokens
- `GET    /tokens` → list (mask token, only show last 4)
- `POST   /tokens` body `{label}` → trả full token 1 lần (không lưu plain)
- `DELETE /tokens/:id`

## 6. UI (vanilla JS, no build step)

3 trang chính:
- `index.html` — login form → POST `/api/v1/auth/login` với token, lưu localStorage, redirect.
- `lines.html` — danh sách lines + modal tạo + per-row "Sessions" link.
- `sessions.html` — bảng sessions với column: line, ppp_unit, iface, status, public_ip, creds_count, port. Actions: rotate, enable toggle, edit creds, copy export.
- Bonus: `events` SSE stream để live update status.

`api-client.js` wrapper:
```js
async function _req(method, path, body) {
  const token = localStorage.getItem('bp_token');
  const r = await fetch(`/api/v1${path}`, {
    method, headers: {
      'Authorization': `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (r.status === 401) { location.href='/'; return; }
  return r.json();
}
```

Style: minimal CSS, không framework, table + modal đơn giản. Tách `tklib` mini (toast, dialog) inline.

## 7. Install script (`deploy/install.sh`)

Single-shot, idempotent, chạy với root. Curl-pipe:

```bash
curl -fsSL https://raw.githubusercontent.com/tkiet2105/bestphone-pppoe/main/deploy/install.sh | sudo bash
```

Các step:

1. **Pre-flight**: check Debian 12 (`/etc/os-release`), check root.
2. **Apt deps**: `apt-get update && apt-get install -y ppp pppoe iproute2 iptables nginx sqlite3 curl ca-certificates jq git`
3. **Go install**: nếu `go version` không có hoặc `< 1.22` → download `go1.22.x.linux-amd64.tar.gz` từ go.dev → `/usr/local/go`, symlink `/usr/local/bin/go`.
4. **Clone repo**: `git clone https://github.com/tkiet2105/bestphone-pppoe /opt/bestphone-pppoe`. Nếu đã có → `git pull`.
5. **Build**:
   ```bash
   cd /opt/bestphone-pppoe
   CGO_ENABLED=0 go build -o /usr/local/bin/bestphone-pppoe ./cmd/bestphone-pppoe
   ```
6. **Bin scripts**: copy `deploy/bin/bestphone-pppoe-update` + `bestphone-pppoe-deploy-ui` → `/usr/local/bin/`, chmod 755.
7. **State dirs**: `mkdir -p /var/lib/bestphone-pppoe /var/log/bestphone-pppoe /var/www/bestphone-pppoe /etc/ppp/peers`.
8. **Initial config**:
   - Sinh admin token: `TOKEN=$(openssl rand -hex 32)`.
   - Ghi `/etc/default/bestphone-pppoe`:
     ```bash
     LISTEN_ADDR=0.0.0.0:8080
     PROXY_PORT_MIN=30000
     PROXY_PORT_MAX=40000
     ADMIN_TOKEN=<token>
     GITHUB_PULL_TOKEN=
     ```
     mode 0600.
   - First-run: binary detect DB rỗng → seed token row từ env `ADMIN_TOKEN`.
9. **systemd**:
   - Copy `bestphone-pppoe.service` → `/etc/systemd/system/`:
     ```ini
     [Service]
     EnvironmentFile=/etc/default/bestphone-pppoe
     ExecStart=/usr/local/bin/bestphone-pppoe
     Restart=on-failure
     User=root
     AmbientCapabilities=CAP_NET_RAW CAP_NET_ADMIN
     ```
   - Copy update service + timer (xem step 11).
   - `systemctl daemon-reload && systemctl enable --now bestphone-pppoe`.
10. **Nginx**: copy `bestphone-pppoe.conf` → `/etc/nginx/sites-available/`, symlink to `sites-enabled/`, `nginx -t && systemctl reload nginx`. Config: proxy `/api/` → `127.0.0.1:8080`, static `/` → `/var/www/bestphone-pppoe`.
11. **Auto-update**:
    - `bestphone-pppoe-update.service` (oneshot `ExecStart=/usr/local/bin/bestphone-pppoe-update`).
    - `bestphone-pppoe-update.timer`:
      ```ini
      [Timer]
      OnBootSec=2min
      OnUnitActiveSec=1h
      RandomizedDelaySec=5min
      Persistent=true
      ```
    - `systemctl enable --now bestphone-pppoe-update.timer`.
12. **Deploy UI**: `bestphone-pppoe-deploy-ui` (rsync `/opt/bestphone-pppoe/ui/` → `/var/www/bestphone-pppoe/`, cache-bust `?v=$(date +%s)` trên link script/css).
13. **Print summary**:
    - URL: `http://<host>/` (or HTTPS nếu có cert)
    - Admin token (echo 1 lần)
    - Lệnh check: `journalctl -u bestphone-pppoe -f`

## 8. Auto-update (`/usr/local/bin/bestphone-pppoe-update`)

```bash
#!/bin/bash
set -e
. /etc/default/bestphone-pppoe

REPO=/opt/bestphone-pppoe
cd "$REPO"

if [ -n "$GITHUB_PULL_TOKEN" ]; then
  AUTH="Authorization: Basic $(printf 'tkiet2105:%s' "$GITHUB_PULL_TOKEN" | base64 -w0)"
  FETCH_OPT="-c http.extraHeader=$AUTH"
else
  FETCH_OPT=""
fi

local_ver=$(cat VERSION 2>/dev/null | tr -d '[:space:]')
git $FETCH_OPT fetch origin main --quiet
remote_ver=$(git show origin/main:VERSION 2>/dev/null | tr -d '[:space:]')

if [ "$local_ver" = "$remote_ver" ]; then
  echo "version $local_ver (latest)"
  exit 0
fi

echo "version $local_ver → $remote_ver"
git $FETCH_OPT pull origin main
export PATH=$PATH:/usr/local/go/bin
CGO_ENABLED=0 go build -o /usr/local/bin/bestphone-pppoe ./cmd/bestphone-pppoe
systemctl restart bestphone-pppoe
bestphone-pppoe-deploy-ui
echo "updated"
```

### Quy tắc bump VERSION (HARD RULE)
- Bump VERSION **chỉ khi ready ship** → fleet client pull trong ≤1h.
- Push code KHÔNG đụng VERSION → debug an toàn, client không pull.
- Mỗi bump phải sync `cmd/bestphone-pppoe/main.go` const `appVersion` = `VERSION` file value.

## 9. Routing safety verification (CRITICAL E2E)

Khi build xong, MUST verify hành vi "tắt WAN vẫn dùng proxy":

```bash
# Setup: 1 line PPPoE đã connect, 1 session ppp0 IP public X.X.X.X
# Default route: 192.168.1.1 qua eth0 (WAN gateway nhà)
ip route        # 0.0.0.0/0 via 192.168.1.1 dev eth0
ip route show dev ppp0  # cụ thể subnet, KHÔNG có 0/0

# Test 1: proxy vẫn work
curl -x socks5://user:pass@host:30001 https://api.ipify.org
# → trả X.X.X.X (IP PPPoE)

# Test 2: tắt WAN
ip route del default
ip link set eth0 down

curl https://api.ipify.org  # → fail (no route)
curl -x socks5://user:pass@host:30001 https://api.ipify.org
# → vẫn trả X.X.X.X — vì socket bound ppp0 qua SO_BINDTODEVICE

# Recovery
ip link set eth0 up
dhclient eth0
```

Nếu test 2 fail → có nghĩa SO_BINDTODEVICE chưa apply, hoặc `nodefaultroute` thiếu, hoặc kernel route lookup sai → DEBUG.

## 10. Critical files để engineer/Claude session khác tạo

Thứ tự ưu tiên:

| Priority | File | Cần kế thừa từ `/opt/boxphone` (dự án cũ) |
|---|---|---|
| P0 | `internal/proxy/server/dial.go` | Sao gần nguyên (đơn giản, ~30 dòng) |
| P0 | `internal/pppoe/config.go` (peer template) | Sao có chỉnh `ipparam` |
| P0 | `internal/pppoe/manager.go` Dial flow | Strip hết VPN driver, chỉ giữ PPPoE path |
| P0 | `cmd/bestphone-pppoe/main.go` | New, gọn — không có license/best-ws |
| P1 | `internal/models/models.go` | Sao Line/Session/Proxy/Credential, BỎ tất cả VPN |
| P1 | `internal/proxy/server/listener.go + socks5.go + http.go + auth.go` | Sao, bỏ TPROXY/IP_TRANSPARENT |
| P1 | `internal/api/handlers_*.go` | Sao tương ứng (line/session/cred/export) |
| P2 | `deploy/install.sh` | Viết mới (ngắn hơn, không Mode 1/2/license) |
| P2 | `deploy/bin/bestphone-pppoe-update` | Sao từ `bestphone-update`, đổi path |
| P2 | `ui/` | Port BestControl subset (lines/sessions/creds page only) |
| P3 | `docs/api.md` | OpenAPI reference |

## 11. Khác biệt CHÍNH so với `bestphone-gateway-v2`

| Aspect | bestphone-gateway-v2 (cũ) | bestphone-pppoe (mới) |
|---|---|---|
| Modes | Mode 1 + Mode 2 + license | KHÔNG mode, KHÔNG license |
| VPN drivers | wireguard/openvpn/singbox/ipsec/nordvpn | KHÔNG có |
| Streaming | full subsystem | KHÔNG có |
| Devices/UID/best-ws | có | KHÔNG có |
| TPROXY/IP_TRANSPARENT | có (transparent mode) | KHÔNG — chỉ SOCKS5/HTTP explicit |
| Network hardening | EnsureLANIsolation, MASQUERADE, DNS redirect | KHÔNG — minimal |
| DB | nhiều bảng, có VPN lines | chỉ 5 bảng (lines/sessions/proxies/creds/tokens) |
| Auth | JWT + license token | Bearer token tĩnh |
| Repo deps | best-ws, BestControl, Central, bestphone-wiper | standalone |

## 12. Verification end-to-end (sau khi build xong)

1. `curl -fsSL .../install.sh | sudo bash` trên Debian 12 fresh → service `bestphone-pppoe` active.
2. `curl http://localhost:8080/api/v1/health` → 200 `{version, uptime}`.
3. Tạo line: `POST /api/v1/lines` với iface eth1.
4. Tạo session với PPPoE creds → dial OK, `ip link` thấy `ppp0`.
5. Listener spawn trên port 30000+.
6. `curl -x socks5://u:p@host:30001 https://api.ipify.org` → trả public IP của PPPoE.
7. Thêm cred thứ 2 qua API → reload, curl với cred 2 → vẫn OK.
8. Tắt WAN → curl SOCKS5 vẫn work (routing safety verify §9).
9. Rotate session → public_ip có thể đổi (BRAS dependent), status `connected`.
10. Bump VERSION 1.0.0 → 1.0.1 + push → trong ≤1h, timer fire → binary rebuild + restart.
11. Auto-clean: `systemctl restart bestphone-pppoe` → RestoreState adopt iface UP → listener resume.

---

**Status**: design doc — chưa implement. Engineer/Claude tiếp theo đọc file này, tạo source theo cấu trúc §1, viết theo §2–§8. E2E test theo §9 + §12.
