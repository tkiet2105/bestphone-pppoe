# bestphone-pppoe — kiến trúc hệ thống

PPPoE multi-line proxy gateway. Mỗi PPPoE session = 1 listener SOCKS5/HTTP riêng;
outbound bind socket vào iface `ppp<N>` qua `SO_BINDTODEVICE` — traffic đi đúng
line PPPoE, **không qua default route**. Tắt WAN vẫn truy cập được proxy.

Stack: Go (CGO=0), Gin, SQLite (glebarez/sqlite — pure Go) qua gorm, vanilla JS UI
(no build step), systemd + nginx.

> Tài liệu này mô tả **hiện trạng** (v1.9.4+). Đây không còn là design doc gốc —
> hệ thống đã implement đầy đủ và mở rộng nhiều (claim system, access rules,
> activity/audit log, stats, iface probe, login user, reply-path routing,
> SOCKS5 UDP ASSOCIATE). Đọc code trong `internal/` để biết chi tiết chính xác.

---

## 1. Repository layout

```
bestphone-pppoe/
├── VERSION                          # bump để trigger fleet auto-update (xem §8)
├── API.md                           # reference endpoint chi tiết
├── go.mod / go.sum
├── cmd/bestphone-pppoe/main.go      # entry: config → db → hub → managers → restore → gin
├── internal/
│   ├── config/config.go             # env load (LISTEN_ADDR, DB_PATH, PROXY_PORT_*, DIAL_CONCURRENT...)
│   ├── db/
│   │   ├── db.go                    # gorm.Open(sqlite WAL) + AutoMigrate + seed admin token/user
│   │   └── settings.go              # Get/Set Setting (key-value), GetSettingBool/Int
│   ├── models/models.go             # Line/Session/Proxy/ProxyCredential/Token/User/AccessRule/Setting/AuditLog/ActivityLog
│   ├── pppoe/
│   │   ├── manager.go               # Dial/Hangup/Rotate/Watchdog/AutoRotate/Reconnect/RestoreState
│   │   ├── config.go                # gen /etc/ppp/peers/bp-sess-<id> + chap/pap-secrets
│   │   ├── ifquery.go               # ip link parse, IfaceIPv4, public IP probe (no-DNS), CGNAT check
│   │   └── routing.go               # reply-path policy routing + MSS clamp (§4b)
│   ├── proxy/server/
│   │   ├── manager.go               # Start/Stop/ReloadCreds/Reload*Rules/RestoreAll/cred-cleanup, AllocPort random
│   │   ├── listener.go              # accept loop + peek-byte multiplex SOCKS5/HTTP
│   │   ├── socks5.go                # SOCKS5: CONNECT + UDP ASSOCIATE, USER/PASS auth
│   │   ├── udp.go                    # SOCKS5 UDP ASSOCIATE relay (§4c)  ← MỚI
│   │   ├── http.go                  # HTTP CONNECT + forward proxy
│   │   ├── dial.go                  # SO_BINDTODEVICE Dialer (TCP)       ← KEY
│   │   ├── auth.go                  # credSet match constant-time
│   │   └── access.go                # ruleSet allow/deny (deny-wins)
│   ├── api/                         # handlers_{health,token,line,session,cred,export,
│   │   │                            #   claim,rule,iface,stats,settings,activity,logs,events}.go
│   │   ├── routes.go                # đăng ký toàn bộ route /api/v1
│   │   └── middleware.go            # BearerAuth + CORS
│   ├── activity/activity.go         # ghi ActivityLog (persist) + publish event
│   ├── audit/audit.go               # ghi AuditLog (HTTP mutation) + publish event
│   ├── events/hub.go                # in-process pub/sub cho SSE
│   └── testutil/testutil.go         # SetupTestDB/Router, Seed*, DoJSON helper
├── ui/                              # vanilla JS, deploy ra /var/www/bestphone-pppoe
│   ├── index.html (login) + lines/sessions/rules/settings/users/activity/logs/api/export .html
│   ├── css/app.css
│   └── js/api-client.js (+ Dialog/Toast/SSE) + lines/sessions/rules/settings/users/activity/logs/api-page .js
└── deploy/
    ├── install.sh                   # single-shot installer (Debian 12, root)
    ├── systemd/bestphone-pppoe{,.update}.service + .update.timer
    ├── nginx/bestphone-pppoe.conf
    └── bin/bestphone-pppoe-update + bestphone-pppoe-deploy-ui
```

## 2. Database schema (SQLite, gorm AutoMigrate)

Nguồn chính xác: `internal/models/models.go`.

- **lines** — `id, name, iface, username, password` (ISP PPPoE cred dùng chung cho mọi
  session của line), `use_macvlan, max_sessions, custom_macs` (pool MAC tự cấp), `created_at`.
- **sessions** — `id, line_id, ppp_unit (uniq 0..999), iface (ppp<N>), username, password, mac,
  type (static|private|rotating), status (disconnected|dialing|connected|error), ip, public_ip,
  last_error, last_rotate_at, auto_rotate_seconds (0=tắt), auto_rotate_paused, connected_at,
  rotate_fail_count, reconnect_attempts, next_reconnect_at, created_at`.
- **proxies** — `id, session_id (uniq, 1-1), port (uniq, pool 30000–40000), status (running|stopped)`.
- **proxy_credentials** — `id, proxy_id (idx), label, username, password, enabled, iuser_id (idx),
  order_id (idx, idempotency claim), expires_at (TTL), created_at`. Listener auth khi `(user,pass)`
  match BẤT KỲ row `enabled=true`. Cred `iuser_id=''` = seed/default (hạ tầng), khác = claim cred.
- **tokens** — `id, token (uniq), label, user_id (NULL=API token, !NULL=login session), created_at`.
- **users** — `id, username (uniq), password_hash (bcrypt), created_at, updated_at`. Tài khoản login UI.
- **access_rules** — `id, scope (global|session), session_id, kind (domain|ip), action (allow|deny),
  value, note, created_at`. Xem §4 access control.
- **settings** — key-value (`reconnect_enabled`, `reconnect_max_retries`, `reconnect_pause_minutes`).
- **audit_logs** — mutation HTTP (token_id, user_id, client_ip, action, resource_type/id, old/new, summary).
- **activity_logs** — state transition của session/proxy/cred (kể cả background): `level, category
  (dial|rotate|reconnect|watchdog|claim|cred|session|line|auth|proxy), action, session/line/proxy/cred/user_id,
  iuser_id, client_ip, summary (tiếng Việt), details (JSON)`. Cleanup goroutine xóa entry > 30 ngày.

### Quota cred theo session type (`models.MaxCredsForType`)
`private` = 1, `static` = 5, `rotating` = 5. Mọi chức năng (rotate/change/...) giống nhau giữa các type;
type chỉ phân biệt để claim đúng + giới hạn số user.

## 3. PPPoE Manager (`internal/pppoe/manager.go`)

### Dial flow (Dial)
1. Lock per-line mutex (serialize dial cùng line) + acquire `dialSem` (global cap, env `DIAL_CONCURRENT`, default 5).
2. **Anti-lockout**: nếu session đang `error` + last_error chứa "AuthNak" → từ chối tự dial (chỉ rotate thủ công clear).
3. Nếu session có MAC → ensure macvlan `mvbp-<sid>` trên line.iface với MAC spoof; else peer iface = line.iface.
4. `WritePeerFile` → `/etc/ppp/peers/bp-sess-<sid>` với `nodefaultroute, noauth, persist, maxfail 0,
   holdoff 10, lcp-echo, mtu/mru 1492, unit <ppp_unit>, ipparam bp-sess-<sid>`; upsert chap/pap-secrets.
5. `pppd call bp-sess-<sid> updetach`, timeout 30s; poll `ip -o link show ppp<N>`. Lỗi → `classifyPppdError`
   (AuthNak/PADT/PADI-PADO timeout/Service-Name mismatch).
6. Lấy IPv4 trên ppp<N>. **Từ chối IP CGNAT/private** (100.64/10, RFC1918) → hangup + error (watchdog dial lại mong IP public).
7. `ApplyReplyRouting(iface, wanIP, ppp_unit)` (§4b) — non-fatal.
8. Set status=connected, lưu iface/ip/connected_at; async probe `public_ip` (Cloudflare trace IP-literal → fallback
   ipify DNS pinned, KHÔNG phụ thuộc DNS hệ thống). Publish event.

### Hangup
Kill pppd (`pkill -TERM` → `-KILL`), `RemoveReplyRouting`, bring down iface, status=disconnected, publish.

### Rotate
Per-session `rotateMu`. Hangup → sleep 3s (BRAS settle) → clear error/status → dial lại. Publish old_ip/new_ip/same_ip.
Env `BESTPHONE_PPPOE_ROTATE_REQUIRE_NEW_IP` (flag, hiện không enforce).

### Watchdog (tick 20s) + AutoRotate (tick 30s) + Reconnect
- Watchdog: demote session connected nhưng IP đã thành CGNAT; redial session connected nhưng iface DOWN; reconnect
  session error theo settings (`reconnect_enabled`, `reconnect_max_retries`, `reconnect_pause_minutes`) với backoff/pause.
- AutoRotate: session có `auto_rotate_seconds>0` && !paused && connected → rotate khi tới hạn; cap 3 song song;
  fail → pause + tăng counter (resume qua API).
- RestoreState (boot): adopt session có iface UP, dial lại session connected-trong-DB-nhưng-iface-DOWN.

### HARD RULE — anti-lockout
**Không tự retry sau AuthNak.** Watchdog SKIP session `error` do AuthNak. Chỉ user rotate thủ công mới redial.

## 4. Proxy listener (CORE — `internal/proxy/server/`)

### Multiplex (`listener.go`)
Accept loop (deadline 1s để graceful shutdown). `handle`: peek 1 byte → `0x05` = SOCKS5, `A-Z` = HTTP, else close.
`ifaceFn` closure lookup `session.iface` mỗi connection (iface đổi sau rotate).

### SO_BINDTODEVICE (`dial.go`) ← CORE
`newBoundDialer(iface)` set `SO_BINDTODEVICE` trong `Dialer.Control`. Mọi outbound TCP bind ppp<N>. Cần CAP_NET_RAW
(systemd `AmbientCapabilities`). Kết hợp `nodefaultroute` → kernel không add `0/0 dev ppp<N>` → socket bound vẫn route khi tắt WAN.

### Auth (`auth.go`)
SOCKS5 offer method `0x02` (USER/PASS) nếu có cred, else `0x00`. HTTP đọc `Proxy-Authorization: Basic`.
`credSet.match` constant-time (`subtle.ConstantTimeCompare`), iterate hết (không early-return). `ReloadCreds` hot-swap (RWMutex).

### Access control (`access.go`) — deny-wins
`ruleSet.allowed(dest, clientIP)`: (1) bất kỳ deny match → block; (2) có allow rule (tách theo kind domain/ip) →
strict mode, chỉ allow match mới qua; (3) không allow rule → mở. **kind=domain match `dest`** (host đích); **kind=ip
match `clientIP`** (IP client gọi proxy — "chặn IP X dùng proxy"). Hot-reload global + session-scope.

### 4b. Reply-path routing (`routing.go`)
Vấn đề: client kết nối trực tiếp tới `public_ip:port` của line → gói tới trên ppp<N> nhưng reply (src=public_ip) trace
main route → ra default route (eth0) → asymmetric → bulk data mất. Giải pháp per ppp_unit: table `1000+ppp_unit`,
`ip route default dev ppp<N> table T` + `ip rule from <public_ip> table T` + iptables TCPMSS clamp (MTU 1492). Áp trong
Dial + khi RestoreState adopt; gỡ trong Hangup/demote. Non-fatal (egress proxy vẫn chạy qua bound socket).

### 4c. SOCKS5 UDP ASSOCIATE (`udp.go`) ← MỚI, luôn bật
- Client gửi `CMD=0x03` trên control-conn TCP (đã auth) → tạo relay 2 socket:
  - **clientConn**: listen trên IP client đã reach được (`conn.LocalAddr`), port ephemeral — **KHÔNG bind ppp**
    (phải reach từ client). Báo về client làm `BND.ADDR:BND.PORT` (`socks5ReplyAddr`).
  - **targetConn**: bind ppp<N> qua `SO_BINDTODEVICE` (`listenPacketBound`/`newBoundListenConfig`, bản UDP của dial.go)
    → mọi UDP egress ra đúng line.
- Datagram header RFC1928 `RSV(2) FRAG(1) ATYP DST.ADDR DST.PORT DATA` (`parseUDPHeader`/`buildUDPHeader`):
  client→target forward DATA; target→client bọc header gửi về. ATYP IPv4/IPv6/Domain (resolve qua resolver bind iface,
  cache theo association, không leak DNS). Drop `FRAG!=0`.
- Lock địa chỉ client ở gói đầu (chống spoof). Access rule check TRƯỚC resolve (clientIP = IP control-conn).
- Vòng đời: sống cùng control-conn; control-conn đóng / idle 60s / listener Stop → teardown cả 2 socket.
- Caveat: port relay ephemeral → host không được chặn UDP inbound tới IP đó; proxy sau NAT có hạn chế cố hữu của UDP ASSOCIATE.

### Lifecycle (`manager.go`)
`Start(proxyID)` (idempotent, AllocPort random crypto), `Stop`, `ReloadCreds`, `ReloadGlobalRules`/`ReloadSessionRules`,
`RestoreAll` (boot), `StartCredCleanup` (60s xóa cred hết hạn + reload).

## 5. REST API

Base `/api/v1`. Auth header `Authorization: Bearer <token>` (hoặc `?token=` cho SSE). Public: `GET /health`,
`POST /auth/login`. Danh sách đầy đủ: `internal/api/routes.go` + `API.md`.

- **auth**: `/auth/me`, `/auth/logout`, `/auth/change-password`.
- **tokens** (API token): `GET/POST /tokens`, `DELETE /tokens/:id`.
- **ifaces**: `GET /ifaces`, `POST /ifaces/probe` (passive PADI/PADO discovery).
- **lines**: `GET/POST /lines`, `GET/PUT /lines/:id`, `POST /lines/:id/delete` (cascade), `POST /lines/:id/sessions[/bulk]`.
- **sessions**: `GET /sessions[/:id]`, `POST /sessions/:id/{delete,rotate,enabled,auto-rotate/resume}`,
  `PUT /sessions/:id/{auto-rotate,type}`, batch `POST /sessions/{auto-rotate/batch,auto-rotate/resume,type/batch}`,
  `POST /rotate` (bulk có concurrency), `GET /sessions/:id/activity`.
- **credentials**: `GET/POST /proxies/:id/credentials`, `POST .../bulk`, `PUT/DELETE .../:cid`.
- **export**: `GET /proxies/export?type=public|local&format=text|json` → `ip:port:user:pass` mỗi cred enabled 1 dòng.
- **claim** (§5b): `POST /{claim,change,release,prune,extend}`, `GET /user-creds`, `GET /claim/{status,user-status,users}`.
- **rules**: `GET/POST /rules`, `PUT/DELETE /rules/:id`.
- **misc**: `GET /stats`, `GET /logs` (journalctl backend+pppd), `GET /activity`, `GET /events` (SSE),
  `GET/PUT /settings`.

### 5b. Claim system (`handlers_claim.go`) — luồng đặt cred cho khách
Idempotency theo `(iuser_id, order_id, type)` (có order_id) hoặc `(iuser_id, type)` (legacy). `ClaimCredentials`:
chọn proxy theo type (`availableProxiesForType` shuffle để rải tải, lọc session connected + proxy running + còn slot
`MaxCredsForType - claimed`), cấp `ProxyCredential` label="claim" user/pass random + TTL tùy chọn; claim lại cùng key →
trả cred cũ (retry-safe). `ChangeCredentials` đổi sang proxy khác giữ order_id+TTL. `ExtendCredentials` cộng dồn TTL
(còn hạn → cộng; hết hạn → tính từ now); scope `cred_ids > order_id > iuser_id+type`. `ReleaseCredentials` xóa claim cred
(giữ seed cred). `PruneCredentials` main server gửi danh sách cred_ids còn dùng → proxy xóa các claim cred KHÁC
(empty list cần `confirm_delete_all`). `ClaimStatus`/`ClaimUserStatus`/`ClaimUsers` báo cáo slot + user.

## 6. UI (vanilla JS, no build)

10 trang: `index.html` (login → `bp_token` localStorage), `lines/sessions/rules/settings/users/activity/logs/api/export`.
`js/api-client.js` = `Api` object (`_req` thêm Bearer, 401 → clear token + redirect), helpers `Dialog`/`Toast`/
`renderNav`/`statusBadge`/`typeBadge`/`fmtTimestamp`/`copyText`, SSE `subscribeEvents`. Nginx phục vụ static + proxy `/api/`.

## 7. Install (`deploy/install.sh`)

Single-shot Debian 12, root. Bước: preflight (root + os-release) → apt deps (ppp pppoe iproute2 iptables nginx sqlite3
curl jq git rsync openssl tar) → cài Go `GO_VERSION` (hiện 1.22.6) nếu thiếu → clone/pull `/opt/bestphone-pppoe`
(hỗ trợ `INSTALL_GITHUB_TOKEN`) → `CGO_ENABLED=0 go build -o /usr/local/bin/bestphone-pppoe ./cmd/bestphone-pppoe` →
copy bin scripts → state dirs → `/etc/default/bestphone-pppoe` (admin token random `openssl rand -hex 32` + seed user
admin/admin) + `/etc/default/bestphone-pppoe-pull-token` → systemd enable+start → nginx config + reload → deploy-ui →
health check → in summary.

systemd service: `User=root`, `AmbientCapabilities=CAP_NET_RAW CAP_NET_ADMIN CAP_NET_BIND_SERVICE`,
`EnvironmentFile=/etc/default/bestphone-pppoe`, `Restart=on-failure`.

> **Caveat bảo trì**: `go.mod` khai báo `go 1.25.0` nhưng install.sh cài Go 1.22.6 → dựa vào GOTOOLCHAIN auto-download
> (cần mạng) để build. Khi đụng phần này nên đồng bộ GO_VERSION với go.mod.

## 8. Auto-update (`deploy/bin/bestphone-pppoe-update`, timer 1h)

So `local VERSION` vs `origin/main:VERSION`; chỉ pull+rebuild+restart+deploy-ui khi khác. Token private repo từ
`/etc/default/bestphone-pppoe-pull-token`. Timer: `OnBootSec=2min, OnUnitActiveSec=1h, RandomizedDelaySec=5min`.

### HARD RULE — bump VERSION
- Bump `VERSION` **chỉ khi ready ship** → fleet pull trong ≤1h. Push KHÔNG đụng VERSION → fleet không pull (debug an toàn).
- Mỗi bump VERSION **phải** sync const `appVersion` trong `cmd/bestphone-pppoe/main.go` cùng giá trị.

## 9. Verification

### Routing safety (tắt WAN vẫn dùng proxy) — TCP
```bash
curl -x socks5://u:p@host:30001 https://api.ipify.org   # → IP PPPoE
ip route del default; ip link set eth0 down
curl https://api.ipify.org                              # → fail
curl -x socks5://u:p@host:30001 https://api.ipify.org   # → vẫn IP PPPoE (socket bound ppp<N>)
```

### UDP ASSOCIATE
```bash
# DNS over UDP qua SOCKS5 UDP (client hỗ trợ UDP ASSOCIATE, vd curl mới):
curl --socks5 u:p@host:30001 https://example.com        # nếu client resolve qua proxy
# hoặc dùng tool gửi UDP qua relay tới STUN/echo server, xác nhận source IP == IP line PPPoE.
```

### Build/test (CI được — udp_test.go bind iface="" loopback)
```bash
CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...
```
Manual cần máy PPPoE thật: egress UDP đúng IP line, DNS resolve không leak, client SOCKS5 UDP interop.

### E2E (sau install trên Debian 12 fresh)
health → tạo line → tạo session (dial OK, `ip link` thấy ppp<N>) → listener spawn 30000+ → curl SOCKS5/HTTP →
thêm cred reload → tắt WAN vẫn work → rotate → claim/extend/release → bump VERSION → timer rebuild.
