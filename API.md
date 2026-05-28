# bestphone-pppoe — API Reference

Tài liệu API đầy đủ cho services tích hợp. Tất cả endpoint nằm dưới prefix `/api/v1`.

- **Base URL** ví dụ: `http://14.224.245.142/api/v1`
- **Content-Type**: `application/json` (trừ các endpoint trả về `text/plain` được ghi rõ)

---

## Mục lục

1. [Conventions](#1-conventions)
2. [Authentication](#2-authentication)
3. [Health](#3-health)
4. [Events (SSE)](#4-events-sse)
5. [Interfaces](#5-interfaces)
6. [Lines](#6-lines)
7. [Sessions](#7-sessions)
8. [Proxies & Credentials](#8-proxies--credentials)
9. [Claim System](#9-claim-system-multi-tenant)
10. [Access Rules](#10-access-rules)
11. [Settings](#11-settings)
12. [Tokens](#12-tokens)
13. [Logs](#13-logs)
14. [Stats](#14-stats)
15. [Object schemas](#15-object-schemas)
16. [Error reference](#16-error-reference)

---

## 1. Conventions

### Response envelope

Trừ một vài endpoint trả raw text/JSON (được ghi rõ), tất cả response dùng envelope:

```jsonc
// success
{ "success": true, "data": <payload> }

// failed
{ "success": false, "error": "<message>" }
```

### HTTP status codes

| Code | Ý nghĩa |
|------|---------|
| 200 | Thành công |
| 400 | Bad request (body sai, validation fail) |
| 401 | Thiếu/sai token |
| 403 | Không có quyền cho hành động này |
| 404 | Resource không tồn tại |
| 409 | Conflict (ví dụ trùng) |
| 429 | Too many requests (single-flight đang chạy) |
| 500 | Lỗi server / DB / external command |

### CORS

Mọi origin được chấp nhận (reflect `Origin`, fallback `*`), credentials allowed, headers `Authorization, Content-Type`. Preflight `OPTIONS` trả 204.

---

## 2. Authentication

### Token

Mọi endpoint (trừ `health` và `login`) yêu cầu Bearer token. Có 2 loại token:

- **Session token** — sinh ra khi gọi `POST /auth/login`, gắn với 1 user. Có thể `logout`, `change-password`.
- **API token** — sinh ra qua `POST /tokens`, không gắn user (dùng cho services). Không logout được, chỉ xóa qua `DELETE /tokens/:id`.

### Cách truyền token

```http
Authorization: Bearer <token>
```

Hoặc qua query param (chỉ khuyến nghị cho SSE/EventSource):
```
GET /api/v1/events?token=<token>
```

### POST /api/v1/auth/login

**Auth**: public

**Body**:
```json
{ "username": "admin", "password": "admin" }
```

**Success 200** (raw JSON, không qua envelope):
```json
{
  "success": true,
  "data": {
    "token": "<hex string>",
    "username": "admin",
    "user_id": 1
  }
}
```

**Errors**:
- `400` — `"thiếu tên đăng nhập hoặc mật khẩu"`
- `401` — `"sai tên đăng nhập hoặc mật khẩu"`
- `500` — `"không thể tạo session: <err>"`

### GET /api/v1/auth/me

**Auth**: Bearer

**Success 200** `data`:
```jsonc
// session token
{ "username": "admin", "user_id": 1, "is_api_token": false, "created_at": "...", "updated_at": "..." }

// API token
{ "username": "", "is_api_token": true, "label": "service-x" }
```

**Errors**: `401 "token không hợp lệ"`, `401 "user không tồn tại"`

### POST /api/v1/auth/logout

**Auth**: Bearer (session token only)

**Body**: none

**Success 200**: `{ "logged_out": true }`

**Errors**: `400 "đây là API token, không thể logout — xóa qua /tokens/:id"`

### POST /api/v1/auth/change-password

**Auth**: Bearer (session token only)

**Body**:
```json
{ "current_password": "old", "new_password": "newpass123" }
```

**Success 200**: `{ "changed": true }`

**Errors**:
- `400 "thiếu mật khẩu hiện tại hoặc mật khẩu mới"`
- `400 "mật khẩu mới phải ≥ 6 ký tự"`
- `403 "API token không thể đổi mật khẩu"`
- `401 "sai mật khẩu hiện tại"`

**Side effect**: Xóa tất cả token KHÁC của user (force re-login), giữ lại token hiện tại.

---

## 3. Health

### GET /api/v1/health

**Auth**: public

**Success 200** (raw JSON):
```json
{
  "service": "bestphone-pppoe",
  "status": "ok",
  "version": "1.0.0",
  "uptime": 12345
}
```

---

## 4. Events (SSE)

### GET /api/v1/events

**Auth**: Bearer (qua header hoặc `?token=`)

**Response**: `text/event-stream` (giữ kết nối)

**Format**:
```
event: hello
data: {"ts": 1717000000}

event: <type>
data: <json>

: keepalive

```

**Event types** (gửi từ backend):
- `settings.updated` — khi `PUT /settings`
- Các event khác từ internal services (session status change, dial result, ...)

**Lưu ý**: Connection có comment `: keepalive\n\n` mỗi 15s.

---

## 5. Interfaces

### GET /api/v1/ifaces

Liệt kê NIC vật lý (loại bỏ loopback, ppp, mvbp, mv-, docker, veth, br-, tun, tap, wg, virbr).

**Auth**: Bearer

**Success 200** `data`:
```json
[
  {
    "name": "enp4s0",
    "mac": "aa:bb:cc:dd:ee:ff",
    "ips": ["192.168.1.10"],
    "state": "up",
    "carrier": true,
    "speed_mbps": 1000,
    "used_by_line": 1,
    "used_by_name": "VNPT-Line-1"
  }
]
```

### POST /api/v1/ifaces/probe

Chạy `pppoe-discovery` để kiểm tra BRAS có phản hồi không.

**Auth**: Bearer

**Body** (optional):
```json
{ "ifaces": ["enp4s0", "enp5s0"] }
```
Bỏ trống → probe tất cả NIC up + có carrier.

**Success 200** `data`:
```json
[
  {
    "name": "enp4s0",
    "pado": true,
    "ac_name": "BRAS-HCM-01",
    "ac_source_mac": "00:11:22:33:44:55"
  },
  { "name": "enp5s0", "pado": false, "error": "timeout" }
]
```

**Errors**: `429 "probe already running"` — single-flight đang chạy

**Concurrency**: Mỗi iface 7s timeout, sleep 1.5s giữa các iface.

---

## 6. Lines

### POST /api/v1/lines

**Auth**: Bearer

**Body**:
```json
{
  "name": "VNPT-Line-1",
  "iface": "enp4s0",
  "username": "fbr00chgt@vnn",
  "password": "3b4r7U5L",
  "use_macvlan": true,
  "max_sessions": 32
}
```

| Field | Type | Required | Note |
|-------|------|----------|------|
| name | string | ✓ | |
| iface | string | ✓ | tên NIC |
| username | string | optional | ISP PPPoE PAP/CHAP |
| password | string | optional | |
| use_macvlan | bool | optional | mặc định false |
| max_sessions | int | optional | mặc định 8 |

**Success 200** `data` = [Line object](#line-object)

### GET /api/v1/lines

**Success 200** `data`: array Line, mỗi item có thêm `session_count` (int64).

### GET /api/v1/lines/:id

**Success 200** `data` = Line object

**Errors**: `404 "not found"`

### PUT /api/v1/lines/:id

**Body** (tất cả optional, pointer):
```json
{ "name": "...", "username": "...", "password": "...", "use_macvlan": true, "max_sessions": 16 }
```

**Success 200** `data` = Line updated

### POST /api/v1/lines/:id/delete

**Body**: none

**Success 200**: `{ "deleted": 1, "sessions": 32 }`

**Side effects**: Stop proxy, xóa creds, hangup pppd, xóa peer/macvlan/secrets cho từng session của line.

### POST /api/v1/lines/:id/sessions

Tạo 1 session.

**Body** (tất cả optional):
```json
{
  "username": "fbr00chgt@vnn",
  "password": "3b4r7U5L",
  "type": "rotating",
  "proxy_auth": {
    "mode": "random",
    "username": "manual_user",
    "password": "manual_pass"
  }
}
```

| Field | Type | Required | Note |
|-------|------|----------|------|
| username | string | optional | override line ISP cred |
| password | string | optional | override line ISP cred |
| type | string | optional | `static` \| `private` \| `rotating` (default `rotating`) |
| proxy_auth | object | optional | xem bên dưới |

`proxy_auth.mode`:
- `"random"` (mặc định) — auto-gen `u<4hex>` / `<8hex>`
- `"manual"` — bắt buộc có username + password
- `"none"` — không tạo cred seed

**Success 200**:
```json
{ "session": { Session object }, "proxy": { Proxy object } }
```

**Errors**:
- `404 "line not found"`
- `400 "cần ISP cred: ..."`, `"proxy_auth.mode=manual cần username + password"`, `"proxy_auth.mode phải là random|manual|none"`
- `500 "dial: <err>"`, `"proxy start: <err>"`

### POST /api/v1/lines/:id/sessions/bulk

Tạo N session tuần tự (2s/lần) + 3 retry trên PADI timeout.

**Body**:
```json
{ "count": 32, "type": "rotating", "proxy_auth": { "mode": "random" } }
```

| Field | Type | Required | Note |
|-------|------|----------|------|
| count | int | ✓ | 1..50 |
| type | string | optional | `static` \| `private` \| `rotating` (default `rotating`); áp dụng cho mọi session tạo ra |
| proxy_auth | object | optional | dùng chung cho mọi session |

**Success 200** `data`:
```json
[
  { "session_id": 100, "status": "connected" },
  { "session_id": 101, "status": "error", "error": "PADI timeout" }
]
```

---

## 7. Sessions

### GET /api/v1/sessions

**Query** (tất cả optional):
- `line_id` int — filter theo line
- `status` string — filter theo status
- `type` string — filter theo type (`static|private|rotating`)

**Success 200** `data`: array Session với thêm fields:
- `proxy_port` int
- `proxy_id` uint
- `proxy_status` string
- `creds_count` int64

### GET /api/v1/sessions/:id

**Success 200**: `{ "session": { Session }, "proxy": { Proxy } }`

### POST /api/v1/sessions/:id/delete

**Success 200**: `{ "deleted": 100 }`

### POST /api/v1/sessions/:id/rotate

Rotate IP (hangup + redial).

**Success 200**:
```json
{ "session_id": 100, "old_ip": "1.2.3.4", "new_ip": "1.2.3.5", "same_ip": false }
```

### POST /api/v1/sessions/:id/enabled

Bật/tắt proxy listener của session.

**Body**: `{ "enabled": true }`

**Success 200**: `{ "session_id": 100, "enabled": true }`

### PUT /api/v1/sessions/:id/auto-rotate

Cấu hình auto-rotate.

**Body**: `{ "seconds": 300 }`  (0 = tắt, >0 phải >= 60)

**Success 200**: `{ "session_id": 100, "auto_rotate_seconds": 300 }`

### POST /api/v1/sessions/:id/auto-rotate/resume

Resume auto-rotate bị paused (reset `auto_rotate_paused=false`, `rotate_fail_count=0`).

**Success 200**: `{ "session_id": 100, "auto_rotate_paused": false }`

### POST /api/v1/sessions/auto-rotate/batch

**Body**: `{ "session_ids": [1, 2, 3], "seconds": 600 }`

**Success 200**: `{ "updated": 3, "seconds": 600 }`

### POST /api/v1/sessions/auto-rotate/resume

**Body**: `{ "session_ids": [1, 2, 3] }`

**Success 200**: `{ "updated": 3 }`

### POST /api/v1/rotate

Rotate batch song song.

**Body**:
```json
{ "session_ids": [1, 2, 3], "concurrency": 5 }
```

**Success 200** `data`:
```json
[
  { "session_id": 1, "old_ip": "1.1.1.1", "new_ip": "2.2.2.2" },
  { "session_id": 2, "old_ip": "3.3.3.3", "new_ip": "4.4.4.4" },
  { "session_id": 3, "old_ip": "", "new_ip": "", "error": "session not connected" }
]
```

---

## 8. Proxies & Credentials

### GET /api/v1/proxies/export

Export tất cả credentials sang text/JSON để import vào tool khác.

**Query**:
- `type`: `"public"` (mặc định) | `"local"` — chọn PublicIP hay first local IPv4
- `format`: `"text"` (mặc định) | `"json"`

**format=text** → `Content-Type: text/plain`:
```
14.224.245.142:30000:user1:pass1
14.224.245.142:30001:user2:pass2
```

**format=json** → `data`:
```json
[
  {
    "session_id": 100,
    "proxy_id": 50,
    "port": 30000,
    "ip": "14.224.245.142",
    "line_id": 1,
    "iface": "enp4s0",
    "creds": [
      { "username": "u1", "password": "p1", "label": "default" }
    ]
  }
]
```

### GET /api/v1/proxies/:id/credentials

**Success 200** `data`: array [ProxyCredential](#proxycredential-object)

### POST /api/v1/proxies/:id/credentials

**Body**:
```json
{ "label": "default", "username": "u1", "password": "p1", "ttl": 3600 }
```

| Field | Type | Required | Note |
|-------|------|----------|------|
| label | string | optional | |
| username | string | ✓ | |
| password | string | ✓ | |
| ttl | int | optional | giây; 0 hoặc không có = vĩnh viễn |

**Success 200** `data` = ProxyCredential

**Errors**:
- `400 "vượt giới hạn creds: session type=<X> tối đa <N>, đang có <Y>, thêm <Z>"` — số creds active vượt max của type

### POST /api/v1/proxies/:id/credentials/bulk

Tạo N creds random.

**Body**:
```json
{ "count": 10, "prefix": "u", "ttl": 0 }
```

| Field | Type | Required | Note |
|-------|------|----------|------|
| count | int | ✓ | 1..200 |
| prefix | string | optional | mặc định "u". Username = `<prefix><4hex>`, password = `<8hex>` |
| ttl | int | optional | |

**Success 200** `data`: array ProxyCredential

**Errors**:
- `400 "count must be 1..200"`
- `400 "vượt giới hạn creds: session type=<X> tối đa <N>, đang có <Y>, thêm <Z>"`

### PUT /api/v1/proxies/:id/credentials/:cid

**Body** (tất cả optional, pointer):
```json
{ "username": "...", "password": "...", "enabled": true, "label": "...", "ttl": 3600 }
```

`ttl > 0` → set `expires_at = now + ttl`. `ttl <= 0` → clear `expires_at`.

**Success 200** `data` = ProxyCredential updated

### DELETE /api/v1/proxies/:id/credentials/:cid

**Success 200**: `{ "deleted": 99 }`

---

## 9. Claim System (multi-tenant)

System cho phép nhiều services/users (`iuser_id`) cùng claim creds từ pool sessions.

**Ràng buộc**:
- 1 cred chỉ thuộc 1 `iuser_id`.
- 1 user không thể có 2 creds trên cùng 1 session.
- 1 session có thể cấp creds cho nhiều users khác nhau.
- "Active cred" = `enabled=true AND (expires_at IS NULL OR expires_at > now)`.

### Session types (loại proxy)

Mỗi session có 1 trong 3 type. Type chỉ ảnh hưởng tới **giới hạn số user/session khi claim**, mọi chức năng (rotate/change) hoạt động giống nhau:

| Type | Mô tả | Max user/session |
|------|-------|-------------------|
| `static` | Tĩnh — IP không đổi (admin tự quản lý rotate) | 5 |
| `private` | Riêng — chỉ bán cho 1 user | 1 |
| `rotating` | Xoay — IP có thể đổi | 5 |

- Khi `POST /claim` với `type=X`, system chỉ pick session có `type=X` và còn slot (active creds < max).
- Khi `POST /change`, cred mới được cấp trên session **cùng type** với cred cũ.
- Khi `POST /proxies/:id/credentials` (admin tạo cred thủ công), số creds active không được vượt max của type.

### POST /api/v1/claim

Claim N creds cho user. Mỗi cred được cấp trên 1 session khác nhau, type khớp với `type` request.

**Body**:
```json
{ "iuser_id": "user_abc", "count": 5, "type": "rotating", "ttl": 3600 }
```

| Field | Type | Required | Note |
|-------|------|----------|------|
| iuser_id | string | ✓ | external user/tenant id |
| count | int | ✓ | >= 1 |
| type | string | optional | `static` \| `private` \| `rotating` (default `rotating`) |
| ttl | int | optional | giây; 0/missing = vĩnh viễn |

**Success 200**:
```json
{
  "iuser_id": "user_abc",
  "type": "rotating",
  "credentials": [
    {
      "cred_id": 1001,
      "proxy_id": 50,
      "session_id": 100,
      "type": "rotating",
      "ip": "14.224.245.142",
      "port": 30000,
      "username": "u1a2b",
      "password": "abcdef12",
      "expires_at": "2026-05-27T15:30:00Z"
    }
  ]
}
```

**Errors**:
- `400 "count phải >= 1"`
- `400 "type phải là static|private|rotating"`
- `400 "không đủ sessions type=<X>: đã có Y creds, cần thêm Z, chỉ còn W slot"`

**Concurrency**: Mutex `claimMu` — serialize với `/change`.

### POST /api/v1/change

Đổi IP cho creds (xóa cũ, cấp mới trên session khác, **cùng type** với cred cũ).

**Body**:
```json
{ "iuser_id": "user_abc", "cred_ids": [1001, 1002] }
```

**Success 200**: Cùng shape với `/claim`, chỉ trả creds MỚI.

**Errors**:
- `400 "cred_ids không được rỗng"`
- `400 "một số cred_ids không tồn tại hoặc không thuộc iuser_id này"`
- `400 "không đủ sessions type=<X> khả dụng để đổi cho cred #N"`

**Side effects**:
- Preserve remaining TTL (cred mới có TTL còn lại = cred cũ).
- Type được preserve tự động — không cần truyền.

### GET /api/v1/user-creds

**Query**:
- `iuser_id` string, required
- `type` string, optional (`static|private|rotating`) — filter theo type

**Success 200**: Cùng shape với `/claim`.

### POST /api/v1/release

Xóa creds của user.

**Body**:
```json
{ "iuser_id": "user_abc", "type": "static" }
```

| Field | Type | Required | Note |
|-------|------|----------|------|
| iuser_id | string | ✓ | |
| type | string | optional | nếu có → chỉ release creds thuộc type này; nếu bỏ trống → release tất cả |

**Success 200**: `{ "iuser_id": "user_abc", "type": "static", "released": 3 }`

### POST /api/v1/extend

Gia hạn TTL cho active creds của user.

**Body**:
```json
{ "iuser_id": "user_abc", "ttl": 7200, "type": "rotating" }
```

| Field | Type | Required | Note |
|-------|------|----------|------|
| iuser_id | string | ✓ | |
| ttl | int | ✓ | giây, phải > 0 |
| type | string | optional | nếu có → chỉ extend creds thuộc type này |

**Success 200**: Cùng shape với `/claim` (creds sau gia hạn).

**Errors**:
- `400 "ttl phải > 0"`
- `400 "type phải là static|private|rotating"`
- `404 "không tìm thấy credentials active cho iuser_id này"`

### GET /api/v1/claim/status

Tổng quan hệ thống, kèm breakdown theo type.

**Success 200**:
```json
{
  "total_connected_sessions": 32,
  "active_users": 5,
  "total_claimed_creds": 25,
  "by_type": {
    "static":   { "sessions": 10, "claimed_creds": 15, "available_slots": 35 },
    "private":  { "sessions": 5,  "claimed_creds": 3,  "available_slots": 2 },
    "rotating": { "sessions": 17, "claimed_creds": 7,  "available_slots": 78 }
  }
}
```

`available_slots = sessions * max_per_type - claimed_creds`.

### GET /api/v1/claim/user-status

**Query**: `iuser_id` (required)

**Success 200**:
```json
{
  "iuser_id": "user_abc",
  "active_creds": 5,
  "by_type": { "static": 2, "private": 0, "rotating": 3 },
  "credentials": [ ... ]
}
```

### GET /api/v1/claim/users

Danh sách tất cả users đang có creds, kèm breakdown theo type.

**Success 200** `data`:
```json
[
  {
    "iuser_id": "user_abc",
    "cred_count": 5,
    "earliest_expiry": "2026-05-27T15:30:00Z",
    "by_type": { "static": 2, "private": 0, "rotating": 3 }
  },
  {
    "iuser_id": "user_xyz",
    "cred_count": 3,
    "earliest_expiry": null,
    "by_type": { "static": 0, "private": 1, "rotating": 2 }
  }
]
```

---

## 10. Access Rules

Whitelist/blacklist cho proxy listener. **Deny-wins**: 1 match deny → block. Nếu có entry allow trong scope → strict mode (chỉ host match allow mới qua).

### GET /api/v1/rules

**Query** (optional filters):
- `scope` — `"global"` | `"session"`
- `session_id` int
- `action` — `"allow"` | `"deny"`
- `kind` — `"domain"` | `"ip"`

**Success 200** `data`: array [AccessRule](#accessrule-object)

### POST /api/v1/rules

**Body**:
```json
{
  "scope": "session",
  "session_id": 100,
  "kind": "domain",
  "action": "deny",
  "value": "*.facebook.com",
  "note": "block social"
}
```

| Field | Type | Required | Note |
|-------|------|----------|------|
| scope | string | ✓ | `"global"` hoặc `"session"` |
| session_id | uint | ✓ nếu scope=session | nil khi scope=global |
| kind | string | ✓ | `"domain"` hoặc `"ip"` |
| action | string | ✓ | `"allow"` hoặc `"deny"` |
| value | string | ✓ | domain (exact hoặc `*.suffix`) hoặc IP/CIDR (bare IP auto `/32`) |
| note | string | optional | |

**Success 200** `data` = AccessRule

### PUT /api/v1/rules/:id

**Body** (tất cả optional, pointer): `action`, `value`, `note`

**Success 200** `data` = AccessRule updated

### DELETE /api/v1/rules/:id

**Success 200**: `{ "deleted": 5 }`

---

## 11. Settings

Cấu hình toàn hệ thống (key-value).

### GET /api/v1/settings

**Success 200** `data`: `map[string]string`
```json
{
  "reconnect_enabled": "true",
  "reconnect_max_retries": "1",
  "reconnect_pause_minutes": "60"
}
```

### PUT /api/v1/settings

**Body**: flat `map[string]string`. Chỉ các key sau được chấp nhận:

| Key | Validation |
|-----|------------|
| `reconnect_enabled` | `"true"` hoặc `"false"` |
| `reconnect_max_retries` | int 1..100 |
| `reconnect_pause_minutes` | int 1..1440 |

**Success 200** `data` = tất cả settings sau update.

**Errors**:
- `400 "setting \"<key>\" không hợp lệ"` — key không trong whitelist
- `400 "reconnect_enabled phải là true hoặc false"`
- `400 "reconnect_max_retries phải là số 1-100"`
- `400 "reconnect_pause_minutes phải là số 1-1440"`

**Side effect**: Publish SSE event `settings.updated` với body input.

---

## 12. Tokens

Quản lý API token (`UserId=NULL`). Session token (`UserId!=NULL`) không liệt kê ở đây.

### GET /api/v1/tokens

**Success 200** `data`:
```json
[
  { "id": 5, "label": "service-x", "last4": "ab12", "created_at": "..." }
]
```
**Lưu ý**: Token value (raw) KHÔNG bao giờ trả về ở endpoint này.

### POST /api/v1/tokens

**Body** (optional): `{ "label": "service-x" }`

**Success 200** (raw, có envelope `success`):
```json
{
  "success": true,
  "data": {
    "id": 5,
    "label": "service-x",
    "token": "<hex 64 chars>"
  }
}
```
**Đây là lần DUY NHẤT** token value xuất hiện — lưu ngay.

### DELETE /api/v1/tokens/:id

**Success 200**: `{ "deleted": 5 }`

**Errors**:
- `404 "token không tồn tại"`
- `400 "không thể xóa session đăng nhập qua endpoint này"` — phải logout, không delete

---

## 13. Logs

### GET /api/v1/logs

Stream `journalctl` của backend hoặc pppd.

**Query**:
- `source`: `"backend"` | `"pppd"` | `"all"` (default `"all"`)
- `lines`: int (default 200, clamped 1..2000)
- `since`: string (default `"30 minutes ago"`) — format theo journalctl
- `filter`: string (case-insensitive substring filter)

**Response**: `Content-Type: text/plain; charset=utf-8` (raw output)

**Errors**:
- `400 "source must be backend|pppd|all"`
- `500 "journalctl: <err> / <output>"`

---

## 14. Stats

### GET /api/v1/stats

**Success 200** `data`:
```json
{
  "service": {
    "version": "1.0.0",
    "uptime_seconds": 12345,
    "started_at": 1717000000
  },
  "system": {
    "cpu_percent": 12.3,
    "load_1": 0.5, "load_5": 0.4, "load_15": 0.3,
    "memory_total_mb": 8192, "memory_used_mb": 2048, "memory_percent": 25.0,
    "uptime_seconds": 86400,
    "disk_path": "/var", "disk_total_gb": 50.0, "disk_used_gb": 10.0, "disk_percent": 20.0,
    "num_cpu": 4, "num_goroutine": 50
  },
  "lines": { "total": 1 },
  "sessions": {
    "total": 32,
    "by_status": { "connected": 30, "dialing": 0, "error": 1, "disconnected": 1 },
    "with_public_ip": 30
  },
  "proxies": { "total": 32, "running": 30, "stopped": 2 },
  "credentials": { "total": 64 },
  "rules": { "total": 3, "global": 1, "session": 2 },
  "auth": { "users": 1, "api_tokens": 2 }
}
```

---

## 15. Object schemas

### Line object
```json
{
  "id": 1,
  "name": "VNPT-Line-1",
  "iface": "enp4s0",
  "username": "fbr00chgt@vnn",
  "password": "3b4r7U5L",
  "use_macvlan": true,
  "max_sessions": 32,
  "created_at": "2026-05-27T10:00:00Z"
}
```

### Session object
```json
{
  "id": 100,
  "line_id": 1,
  "ppp_unit": 100,
  "iface": "ppp100",
  "username": "fbr00chgt@vnn",
  "password": "3b4r7U5L",
  "mac": "aa:bb:cc:dd:ee:01",
  "type": "rotating",
  "status": "connected",
  "ip": "10.0.0.1",
  "public_ip": "14.224.245.142",
  "last_error": "",
  "last_rotate_at": "2026-05-27T11:00:00Z",
  "auto_rotate_seconds": 0,
  "auto_rotate_paused": false,
  "connected_at": "2026-05-27T10:30:00Z",
  "rotate_fail_count": 0,
  "reconnect_attempts": 0,
  "next_reconnect_at": null,
  "created_at": "2026-05-27T10:00:00Z"
}
```

- `status` ∈ `disconnected | dialing | connected | error`
- `type` ∈ `static | private | rotating` (default `rotating`)

### Proxy object
```json
{ "id": 50, "session_id": 100, "port": 30000, "status": "running" }
```

`status` ∈ `running | stopped`

### ProxyCredential object
```json
{
  "id": 1001,
  "proxy_id": 50,
  "label": "claim",
  "username": "u1a2b",
  "password": "abcdef12",
  "enabled": true,
  "iuser_id": "user_abc",
  "expires_at": "2026-05-27T15:30:00Z",
  "created_at": "2026-05-27T14:30:00Z"
}
```

### AccessRule object
```json
{
  "id": 1,
  "scope": "global",
  "session_id": null,
  "kind": "domain",
  "action": "deny",
  "value": "*.facebook.com",
  "note": "block social",
  "created_at": "2026-05-27T10:00:00Z"
}
```

---

## 16. Error reference

### Common error responses

| Status | Khi nào |
|--------|---------|
| `401 "missing token"` | Không có Bearer header và không có `?token=` |
| `401 "invalid token"` | Token không tồn tại trong DB |
| `404 "not found"` | Resource id không tồn tại |
| `400 "<bind error message>"` | Body JSON sai format |
| `500 "<error message>"` | DB/IO/external command lỗi |

### Validation error patterns (Vietnamese messages từ backend)

Các message hiển thị bằng tiếng Việt do backend trả về trực tiếp, không có error code. Services tích hợp nên parse `success: false` + `error: <string>` để xử lý generic, hoặc match substring nếu cần logic cụ thể.

Ví dụ:
```json
{ "success": false, "error": "không đủ sessions: đã có 5 creds, cần thêm 10, chỉ còn 3 sessions trống" }
```

---

## Phụ lục: Quick examples (curl)

### Login + lấy token
```bash
TOKEN=$(curl -sS -X POST http://14.224.245.142/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}' | jq -r '.data.token')
```

### Claim 5 creds type=rotating cho user "abc"
```bash
curl -sS -X POST http://14.224.245.142/api/v1/claim \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"iuser_id":"abc","count":5,"type":"rotating","ttl":3600}'
```

### Claim 1 cred type=private
```bash
curl -sS -X POST http://14.224.245.142/api/v1/claim \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"iuser_id":"abc","count":1,"type":"private","ttl":86400}'
```

### Change IP cho cred 1001
```bash
curl -sS -X POST http://14.224.245.142/api/v1/change \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"iuser_id":"abc","cred_ids":[1001]}'
```

### Export tất cả proxies sang text
```bash
curl -sS "http://14.224.245.142/api/v1/proxies/export?format=text" \
  -H "Authorization: Bearer $TOKEN"
```

### SSE events stream
```bash
curl -N "http://14.224.245.142/api/v1/events?token=$TOKEN"
```
