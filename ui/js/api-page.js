if (!ensureAuth()) {} else {
  renderNav('api');
  init();
}

async function init() {
  try {
    const h = await Api.health();
    document.getElementById('api-version').textContent = 'phiên bản ' + h.version;
  } catch (_) {}
  render();
  setupScrollSpy();
}

// Danh sách endpoint hiện có, gom theo nhóm. Tên + mô tả việt hóa.
const SPEC = [
  {
    group: 'Health & Auth',
    items: [
      { method: 'GET',  path: '/api/v1/health', desc: 'Kiểm tra dịch vụ + phiên bản. KHÔNG cần token.', auth: false,
        resp: '{ "service":"bestphone-pppoe", "status":"ok", "version":"1.6.x", "uptime": 123 }' },
      { method: 'POST', path: '/api/v1/auth/login', desc: 'Đăng nhập bằng tên đăng nhập + mật khẩu. Trả về token phiên (lưu vào header Bearer cho các call sau).', auth: false,
        body: '{ "username": "admin", "password": "bestphone" }',
        resp: '{ "success":true, "data": { "token":"<32-byte-hex>", "username":"admin", "user_id":1 } }' },
      { method: 'GET',  path: '/api/v1/auth/me', desc: 'Thông tin tài khoản đang đăng nhập (qua token Bearer).',
        resp: '{ "username":"admin", "user_id":1, "is_api_token":false, "created_at":"...", "updated_at":"..." }' },
      { method: 'POST', path: '/api/v1/auth/logout', desc: 'Đăng xuất — xóa token phiên hiện tại. (Không dùng được cho API token tự sinh.)' },
      { method: 'POST', path: '/api/v1/auth/change-password', desc: 'Đổi mật khẩu. Sau khi đổi, tất cả phiên đăng nhập KHÁC của tài khoản sẽ bị thu hồi (token hiện tại giữ nguyên).',
        body: '{ "current_password": "...", "new_password": "..." }' },
    ],
  },
  {
    group: 'Mạng — Cổng vật lý',
    items: [
      { method: 'GET',  path: '/api/v1/ifaces', desc: 'Liệt kê cổng mạng vật lý: trạng thái, có dây hay không, tốc độ, danh sách IP, đang được đường truyền nào dùng.',
        resp: '[ { "name":"enp4s0", "mac":"...", "ips":["..."], "state":"up", "carrier":true, "speed_mbps":1000, "used_by_line":4 } ]' },
      { method: 'POST', path: '/api/v1/ifaces/probe', desc: 'Quét tuần tự các cổng để phát hiện cổng nào có dây ISP (gửi PADI, ~5-7 giây/cổng). AN TOÀN — không gửi tài khoản, không có nguy cơ AuthNak.',
        body: '{ "ifaces": ["enp1s0","enp4s0"] }  // tùy chọn, mặc định quét tất cả cổng có link',
        resp: '[ { "name":"enp4s0", "pado":true, "ac_name":"BRAS-HBT-07-RE0", "ac_source_mac":"f4:a7:39:..." } ]' },
    ],
  },
  {
    group: 'Đường truyền',
    items: [
      { method: 'GET',  path: '/api/v1/lines', desc: 'Liệt kê đường truyền + số phiên đang chạy.',
        resp: '[ { "id":4, "name":"fpt-enp4s0", "iface":"enp4s0", "use_macvlan":true, "max_sessions":8, "session_count":0 } ]' },
      { method: 'GET',  path: '/api/v1/lines/:id', desc: 'Xem chi tiết 1 đường truyền.' },
      { method: 'POST', path: '/api/v1/lines', desc: 'Tạo đường truyền mới.',
        body: '{ "name":"fpt-1", "iface":"enp4s0", "username":"...", "password":"...", "use_macvlan":true, "max_sessions":8 }' },
      { method: 'PUT',  path: '/api/v1/lines/:id', desc: 'Cập nhật tên / tài khoản ISP / số phiên tối đa.',
        body: '{ "username":"new-isp-user", "password":"new-isp-pass" }' },
      { method: 'POST', path: '/api/v1/lines/:id/delete', desc: 'Xóa đường truyền (cascade): ngắt pppd → tắt proxy → xóa phiên/proxy/tài khoản → xóa đường truyền.' },
    ],
  },
  {
    group: 'Phiên (sessions)',
    items: [
      { method: 'GET',  path: '/api/v1/sessions', desc: 'Liệt kê phiên + cổng proxy + số tài khoản. Tham số: line_id, status.',
        resp: '[ { "id":1, "line_id":4, "ppp_unit":0, "iface":"ppp0", "username":"...", "status":"connected", "public_ip":"...", "proxy_port":30000, "creds_count":3 } ]' },
      { method: 'GET',  path: '/api/v1/sessions/:id', desc: 'Chi tiết 1 phiên + thông tin proxy.' },
      { method: 'POST', path: '/api/v1/lines/:id/sessions', desc: 'Tạo 1 phiên — tài khoản ISP lấy từ đường truyền, MAC tự sinh nếu line.use_macvlan. proxy_auth điều khiển cách khách hàng đăng nhập proxy (SOCKS5/HTTP).',
        body: '{ "proxy_auth": { "mode": "random" } }   // hoặc {"mode":"manual","username":"X","password":"Y"} hoặc {"mode":"none"} (proxy mở)' },
      { method: 'POST', path: '/api/v1/lines/:id/sessions/bulk', desc: 'Tạo N phiên cùng lúc, song song (1..50). Mỗi phiên dùng tài khoản ISP + MAC ngẫu nhiên riêng. proxy_auth dùng chung.',
        body: '{ "count": 10, "proxy_auth": { "mode": "random" } }' },
      { method: 'POST', path: '/api/v1/sessions/:id/delete', desc: 'Ngắt pppd + xóa file cấu hình peer + xóa proxy + xóa phiên.' },
      { method: 'POST', path: '/api/v1/sessions/:id/rotate', desc: 'Đổi IP — ngắt → chờ 3 giây cho BRAS giải phóng → quay số lại. Trả về IP cũ + IP mới.',
        resp: '{ "session_id":1, "old_ip":"1.2.3.4", "new_ip":"5.6.7.8", "same_ip":false }' },
      { method: 'POST', path: '/api/v1/sessions/:id/enabled', desc: 'Bật/tắt proxy (giữ nguyên tunnel PPPoE).',
        body: '{ "enabled": true }' },
      { method: 'PUT', path: '/api/v1/sessions/:id/auto-rotate', desc: 'Cấu hình tự đổi IP cho phiên (0 = tắt; ≥60 = chu kỳ giây). Background goroutine tick 30s, rotate khi đến hạn (concurrency 3).',
        body: '{ "seconds": 900 }   // 15 phút' },
      { method: 'POST', path: '/api/v1/sessions/auto-rotate/batch', desc: 'Bulk set tự đổi IP cho nhiều phiên cùng lúc.',
        body: '{ "session_ids":[1,2,3], "seconds": 1800 }' },
      { method: 'POST', path: '/api/v1/rotate', desc: 'Đổi IP hàng loạt cho nhiều phiên. concurrency mặc định 5.',
        body: '{ "session_ids":[1,2,3], "concurrency": 5 }' },
    ],
  },
  {
    group: 'Tài khoản proxy (nhiều tài khoản / proxy)',
    items: [
      { method: 'GET',    path: '/api/v1/proxies/:id/credentials', desc: 'Liệt kê tài khoản đăng nhập proxy. Listener chấp nhận BẤT KỲ tài khoản nào đang bật.' },
      { method: 'POST',   path: '/api/v1/proxies/:id/credentials', desc: 'Thêm 1 tài khoản. Nóng — không cần tắt listener.',
        body: '{ "label":"khach-A", "username":"user1", "password":"pass1" }' },
      { method: 'POST',   path: '/api/v1/proxies/:id/credentials/bulk', desc: 'Sinh N tài khoản ngẫu nhiên. username = prefix+randHex(4), password = randHex(8).',
        body: '{ "count": 10, "prefix": "u" }' },
      { method: 'PUT',    path: '/api/v1/proxies/:id/credentials/:cid', desc: 'Sửa tài khoản (label/username/password/enabled).',
        body: '{ "enabled": false }' },
      { method: 'DELETE', path: '/api/v1/proxies/:id/credentials/:cid', desc: 'Xóa 1 tài khoản proxy.' },
    ],
  },
  {
    group: 'Xuất proxy (lấy danh sách nhanh)',
    items: [
      { method: 'GET', path: '/api/v1/proxies/export?type=public&format=text', desc: 'Xuất văn bản thuần "ip:port:user:pass" — 1 dòng / 1 tài khoản đang bật.',
        resp: '1.2.3.4:30000:user1:pass1\n5.6.7.8:30001:user2:pass2\n...' },
      { method: 'GET', path: '/api/v1/proxies/export?type=local&format=json', desc: 'Xuất JSON lồng. type: public|local. format: text|json.',
        resp: '[ { "session_id":1, "proxy_id":1, "port":30000, "ip":"...", "creds":[ { "username":"...", "password":"..." } ] } ]' },
    ],
  },
  {
    group: 'Logs',
    items: [
      { method: 'GET', path: '/api/v1/logs?source=pppd&lines=200&since=15+minutes+ago&filter=AuthNak', desc: 'Đọc journalctl. source=backend|pppd|all. since theo định dạng systemd. Tùy chọn filter (so khớp chuỗi, không phân biệt hoa thường). Trả text/plain.',
        resp: '2026-05-19T08:29:26 debian8 pppd[532120]: sent [PAP AuthReq id=0x1 user="u_xxx" ...]\\n... [PAP AuthNak id=0x1 ""]' },
    ],
  },
  {
    group: 'Rules (cho phép / từ chối)',
    items: [
      { method: 'GET', path: '/api/v1/rules', desc: 'Liệt kê rule. Tham số: scope=global|session, session_id, action=allow|deny, kind=domain|ip.' },
      { method: 'POST', path: '/api/v1/rules', desc: 'Tạo rule. scope=global → áp dụng toàn cluster. scope=session → chỉ áp dụng cho phiên session_id đó.',
        body: '{ "scope":"global", "kind":"domain", "action":"deny", "value":"*.facebook.com", "note":"chặn mạng xã hội" }',
        resp: '{ "id":1, "scope":"global", "kind":"domain", "action":"deny", "value":"*.facebook.com" }' },
      { method: 'PUT', path: '/api/v1/rules/:id', desc: 'Cập nhật hành động/giá trị/ghi chú.',
        body: '{ "action":"allow" }' },
      { method: 'DELETE', path: '/api/v1/rules/:id', desc: 'Xóa rule. Tự reload nóng — không tắt listener.' },
    ],
    note: 'Chặn ưu tiên (Deny-wins). Listener cache rule theo từng proxy; mọi thay đổi tự reload nóng. Tên miền hỗ trợ *.suffix; IP hỗ trợ đơn lẻ hoặc dạng CIDR.',
  },
  {
    group: 'Token API (cho automation)',
    items: [
      { method: 'GET',    path: '/api/v1/tokens', desc: 'Liệt kê API token. Chỉ hiển thị 4 ký tự cuối (token đầy đủ không lưu plain). Session đăng nhập KHÔNG xuất hiện ở đây.' },
      { method: 'POST',   path: '/api/v1/tokens', desc: 'Sinh API token mới 32-byte hex. **Token đầy đủ chỉ trả 1 lần** (không xem lại được).',
        body: '{ "label":"script-backup" }',
        resp: '{ "id":2, "label":"script-backup", "token":"<32-byte-hex>" }' },
      { method: 'DELETE', path: '/api/v1/tokens/:id', desc: 'Xóa API token (KHÔNG dùng để logout session UI — dùng /auth/logout).' },
    ],
  },
  {
    group: 'Claim System (cấp proxy cho end-user)',
    items: [
      { method: 'POST', path: '/api/v1/claim', desc: 'Claim N credentials trên N sessions khác nhau cho 1 iuser_id. Gọi lại cùng iuser_id → trả creds cũ (idempotent).',
        body: '{ "iuser_id":"user-1", "count":3, "ttl":3600 }',
        resp: '{ "iuser_id":"user-1", "credentials":[{ "cred_id":1, "session_id":129, "ip":"...", "port":30000, "username":"u...", "password":"...", "expires_at":"..." }] }' },
      { method: 'POST', path: '/api/v1/change', desc: 'Đổi creds sang sessions mới (đổi IP). Giữ TTL còn lại, xóa creds cũ.',
        body: '{ "iuser_id":"user-1", "cred_ids":[1,2] }' },
      { method: 'GET', path: '/api/v1/user-creds?iuser_id=xxx', desc: 'Liệt kê tất cả creds active của 1 iuser_id kèm thông tin proxy connection.' },
      { method: 'POST', path: '/api/v1/release', desc: 'Xóa toàn bộ creds của 1 iuser_id (trả sessions cho người khác).',
        body: '{ "iuser_id":"user-1" }',
        resp: '{ "iuser_id":"user-1", "released":3 }' },
      { method: 'POST', path: '/api/v1/extend', desc: 'Gia hạn TTL cho tất cả creds active của 1 iuser_id.',
        body: '{ "iuser_id":"user-1", "ttl":7200 }' },
      { method: 'GET', path: '/api/v1/claim/status', desc: 'Tổng quan capacity: sessions connected, active users, claimed creds.' },
      { method: 'GET', path: '/api/v1/claim/user-status?iuser_id=xxx', desc: 'Status chi tiết của 1 user: số creds active + danh sách creds.' },
      { method: 'GET', path: '/api/v1/claim/users', desc: 'Danh sách tất cả iuser_id đang active: số creds, hết hạn sớm nhất.' },
    ],
  },
  {
    group: 'Realtime (SSE)',
    items: [
      { method: 'GET', path: '/api/v1/events?token=<token>', desc: 'Luồng Server-Sent Events. Các sự kiện: session.status, session.public_ip, session.rotate, proxy.cred_changed, proxy.started, proxy.stopped. Keepalive mỗi 15 giây.',
        note: 'JS: <code>new EventSource("/api/v1/events?token=" + token)</code>' },
    ],
  },
];

function groupId(g) { return 'grp-' + g.replace(/[^a-zA-Z0-9]/g, '_'); }

function render() {
  const root = document.getElementById('api-content');
  root.innerHTML = SPEC.map(g => `
    <div class="card api-group" id="${groupId(g.group)}" data-group="${escapeHTML(g.group)}">
      <h3>${escapeHTML(g.group)}</h3>
      ${g.items.map((it, idx) => renderRow(g.group, it, idx)).join('')}
      ${g.note ? `<div class="muted small" style="margin-top:10px;padding-top:10px;border-top:1px dashed #334155">📌 ${g.note}</div>` : ''}
    </div>
  `).join('');

  const side = document.getElementById('api-side');
  side.innerHTML = `<h4>Mục lục</h4>` + SPEC.map(g => `
    <a href="#${groupId(g.group)}" data-side="${groupId(g.group)}">
      <span>${escapeHTML(g.group)}</span><span class="count">${g.items.length}</span>
    </a>
  `).join('');
}

// scrollspy: highlight nhóm đang hiển thị + click handler.
// Bí kíp 2 trường hợp biên hay miss:
//   1. Cuộn tới đáy trang nhưng section cuối (SSE) không bao giờ vượt qua mốc
//      `scrollY + 100` vì section ngắn → bottom-of-page detect riêng.
//   2. Click sidebar link cho section cuối: smooth-scroll kẹt ở đáy chưa kịp
//      kích hoạt scroll listener → tự set active ngay khi click.
function setupScrollSpy() {
  const sideLinks = [...document.querySelectorAll('#api-side a')];
  const sections = SPEC.map(g => document.getElementById(groupId(g.group))).filter(Boolean);
  if (!sideLinks.length || !sections.length) return;

  function setActive(sectionId) {
    sideLinks.forEach(a => a.classList.toggle('active', a.dataset.side === sectionId));
  }

  function update() {
    // Bottom-of-page: nếu gần đáy, ép active = section cuối (xử lý các section
    // ngắn ở cuối không vượt qua mốc scrollY+100, vd Token API / SSE).
    const docH = document.documentElement.scrollHeight;
    if (window.innerHeight + window.scrollY >= docH - 8) {
      // Lấy section cuối CÒN VISIBLE (filter có thể ẩn nhóm)
      for (let i = sections.length - 1; i >= 0; i--) {
        if (sections[i].style.display !== 'none') { setActive(sections[i].id); return; }
      }
      return;
    }
    const y = window.scrollY + 100;
    let active = null;
    for (const s of sections) {
      if (s.style.display === 'none') continue;
      if (s.offsetTop <= y) active = s;
      else break;
    }
    if (!active) {
      // Trên cùng — highlight section đầu visible
      active = sections.find(s => s.style.display !== 'none');
    }
    if (active) setActive(active.id);
  }

  // Click handler — set active NGAY (không chờ scroll listener)
  sideLinks.forEach(a => {
    a.addEventListener('click', (e) => {
      e.preventDefault();
      const id = a.dataset.side;
      const el = document.getElementById(id);
      if (!el) return;
      const top = el.offsetTop - 64;
      window.scrollTo({ top, behavior: 'smooth' });
      history.replaceState(null, '', '#' + id);
      setActive(id);
      // Lock active 600ms để smooth-scroll không bị scrollspy đè lại
      const locked = id;
      const releaseAt = Date.now() + 600;
      const origUpdate = update;
      window.removeEventListener('scroll', _spyHandler, { passive: true });
      _spyHandler = () => {
        if (Date.now() < releaseAt) { setActive(locked); return; }
        origUpdate();
      };
      window.addEventListener('scroll', _spyHandler, { passive: true });
      // Sau timeout, chuyển về handler gốc
      setTimeout(() => {
        window.removeEventListener('scroll', _spyHandler, { passive: true });
        _spyHandler = origUpdate;
        window.addEventListener('scroll', _spyHandler, { passive: true });
      }, 700);
    });
  });

  let _spyHandler = update;
  window.addEventListener('scroll', _spyHandler, { passive: true });
  window.addEventListener('resize', update);
  update();

  // Nếu URL có hash (vd #grp-Token_API_cho_automation_), scroll tới ngay
  if (location.hash) {
    const target = document.getElementById(location.hash.slice(1));
    if (target) {
      setTimeout(() => {
        window.scrollTo({ top: target.offsetTop - 64, behavior: 'instant' });
        setActive(target.id);
      }, 50);
    }
  }
}

function renderRow(group, it, idx) {
  const noAuth = it.auth === false;
  return `
    <div class="api-row" data-row="${escapeHTML(it.method + ' ' + it.path)}" data-text="${escapeHTML((it.desc || '') + ' ' + it.path)}">
      <div>
        <span class="method ${it.method}">${it.method}</span>
        <span class="path">${escapeHTML(it.path)}</span>
        ${noAuth ? '<span class="badge stopped" style="margin-left:8px">không cần token</span>' : ''}
      </div>
      <div class="desc">${escapeHTML(it.desc || '')}</div>
      ${it.body ? `<div class="extras"><div class="muted small">Nội dung gửi đi (body):</div><pre>${escapeHTML(it.body)}</pre></div>` : ''}
      ${it.resp ? `<div class="extras"><div class="muted small">Nội dung phản hồi (data):</div><pre>${escapeHTML(it.resp)}</pre></div>` : ''}
      ${it.note ? `<div class="extras muted small">${it.note}</div>` : ''}
      <div class="row-actions">
        <button class="secondary" onclick="copyCurl('${escapeJS(it.method)}', '${escapeJS(it.path)}', ${JSON.stringify(it.body || '').replace(/"/g, '&quot;')}, ${noAuth ? 'true' : 'false'})">Sao chép lệnh curl</button>
      </div>
    </div>
  `;
}

function escapeJS(s) { return String(s).replace(/'/g, "\\'"); }

async function copyCurl(method, path, body, noAuth) {
  const token = Api.getToken();
  const url = location.origin + path;
  let curl = `curl -sS -X ${method} '${url}'`;
  if (!noAuth) curl += ` \\\n  -H 'Authorization: Bearer ${token}'`;
  if (body) {
    curl += ` \\\n  -H 'Content-Type: application/json' \\\n  -d '${body}'`;
  }
  curl += ' | jq .';
  const ok = await copyText(curl);
  if (ok) Toast.success('Đã sao chép curl: ' + method + ' ' + path);
  else Toast.error('Trình duyệt từ chối sao chép');
}

function filterApi() {
  const q = (document.getElementById('api-search').value || '').toLowerCase().trim();
  document.querySelectorAll('.api-row').forEach(r => {
    const t = (r.getAttribute('data-text') || '').toLowerCase();
    r.style.display = (!q || t.includes(q)) ? '' : 'none';
  });
  // Ẩn group nếu mọi row đều bị ẩn
  document.querySelectorAll('.api-group').forEach(g => {
    const anyVisible = Array.from(g.querySelectorAll('.api-row')).some(r => r.style.display !== 'none');
    g.style.display = anyVisible ? '' : 'none';
  });
  // Cập nhật badge số endpoint hiển thị trong sidebar
  document.querySelectorAll('#api-side a').forEach(a => {
    const grp = document.getElementById(a.dataset.side);
    if (!grp) return;
    const visible = [...grp.querySelectorAll('.api-row')].filter(r => r.style.display !== 'none').length;
    const cnt = a.querySelector('.count');
    if (cnt) cnt.textContent = visible;
    a.style.opacity = visible ? '1' : '0.4';
  });
}
