if (!ensureAuth()) {} else {
  renderNav('api');
  init();
}

async function init() {
  try {
    const h = await Api.health();
    document.getElementById('api-version').textContent = 'v' + h.version;
  } catch (_) {}
  render();
}

// Toàn bộ endpoint hiện có. Group theo resource.
const SPEC = [
  {
    group: 'Health & Auth',
    items: [
      { method: 'GET',  path: '/api/v1/health', desc: 'Health check + version. KHÔNG cần token.', auth: false,
        resp: '{ "service":"bestphone-pppoe", "status":"ok", "version":"1.0.x", "uptime": 123 }' },
      { method: 'POST', path: '/api/v1/auth/login', desc: 'Verify token. Trả label nếu hợp lệ. KHÔNG cần token header.', auth: false,
        body: '{ "token": "<admin-token>" }',
        resp: '{ "success":true, "data": { "id":1, "label":"admin-default" } }' },
    ],
  },
  {
    group: 'Network — Iface',
    items: [
      { method: 'GET',  path: '/api/v1/ifaces', desc: 'Liệt kê NIC physical: state, carrier, speed, IPs, line đang dùng.',
        resp: '[ { "name":"enp4s0", "mac":"...", "ips":["..."], "state":"up", "carrier":true, "speed_mbps":1000, "used_by_line":4 } ]' },
      { method: 'POST', path: '/api/v1/ifaces/probe', desc: 'PADO probe sequential. ~5-7s/NIC. KHÔNG gửi auth, an toàn không AuthNak.',
        body: '{ "ifaces": ["enp1s0","enp4s0"] }  // optional, mặc định probe all carrier=up',
        resp: '[ { "name":"enp4s0", "pado":true, "ac_name":"BRAS-HBT-07-RE0", "ac_source_mac":"f4:a7:39:..." } ]' },
    ],
  },
  {
    group: 'Lines',
    items: [
      { method: 'GET',  path: '/api/v1/lines', desc: 'List lines + session count.',
        resp: '[ { "id":4, "name":"fpt-enp4s0", "iface":"enp4s0", "use_macvlan":true, "max_sessions":8, "session_count":0 } ]' },
      { method: 'GET',  path: '/api/v1/lines/:id', desc: 'Detail 1 line.' },
      { method: 'POST', path: '/api/v1/lines', desc: 'Tạo line mới.',
        body: '{ "name":"fpt-1", "iface":"enp4s0", "use_macvlan":true, "max_sessions":8 }' },
      { method: 'POST', path: '/api/v1/lines/:id/delete', desc: 'Cascade xóa: hangup pppd → stop proxy listener → xóa session/proxy/cred → xóa line.' },
    ],
  },
  {
    group: 'Sessions',
    items: [
      { method: 'GET',  path: '/api/v1/sessions', desc: 'List sessions + proxy port + creds_count. Query: line_id, status.',
        resp: '[ { "id":1, "line_id":4, "ppp_unit":0, "iface":"ppp0", "username":"...", "status":"connected", "public_ip":"...", "proxy_port":30000, "creds_count":3 } ]' },
      { method: 'GET',  path: '/api/v1/sessions/:id', desc: 'Detail 1 session + proxy info.' },
      { method: 'POST', path: '/api/v1/lines/:id/sessions', desc: 'Tạo 1 session + dial + start proxy listener.',
        body: '{ "username":"fbr00chgt", "password":"3b4r7U5L", "mac":"02:aa:bb:cc:dd:ee" }' },
      { method: 'POST', path: '/api/v1/lines/:id/sessions/bulk', desc: 'Bulk tạo N session song song. dialSem cap 5.',
        body: '{ "count":32, "creds":[ { "username":"...", "password":"...", "mac":"..." }, ... ] }' },
      { method: 'POST', path: '/api/v1/sessions/:id/delete', desc: 'Hangup pppd + xóa peer file + xóa proxy + xóa session.' },
      { method: 'POST', path: '/api/v1/sessions/:id/rotate', desc: 'Hangup → 3s BRAS settle → dial. Trả old_ip/new_ip.',
        resp: '{ "session_id":1, "old_ip":"1.2.3.4", "new_ip":"5.6.7.8", "same_ip":false }' },
      { method: 'POST', path: '/api/v1/sessions/:id/enabled', desc: 'Bật/tắt proxy LISTENER (giữ nguyên tunnel PPPoE).',
        body: '{ "enabled": true }' },
      { method: 'POST', path: '/api/v1/rotate', desc: 'Batch rotate. concurrency mặc định 5.',
        body: '{ "session_ids":[1,2,3], "concurrency": 5 }' },
    ],
  },
  {
    group: 'Credentials (Multi-cred per proxy)',
    items: [
      { method: 'GET',    path: '/api/v1/proxies/:id/credentials', desc: 'List creds của proxy. Listener match BẤT KỲ cred enabled=true.' },
      { method: 'POST',   path: '/api/v1/proxies/:id/credentials', desc: 'Thêm 1 cred. Hot reload không close listener.',
        body: '{ "label":"client-A", "username":"user1", "password":"pass1" }' },
      { method: 'POST',   path: '/api/v1/proxies/:id/credentials/bulk', desc: 'Sinh N cred ngẫu nhiên.',
        body: '{ "count":10, "label_prefix":"auto" }' },
      { method: 'PUT',    path: '/api/v1/proxies/:id/credentials/:cid', desc: 'Update cred (label/username/password/enabled).',
        body: '{ "enabled": false }' },
      { method: 'DELETE', path: '/api/v1/proxies/:id/credentials/:cid', desc: 'Xóa cred.' },
    ],
  },
  {
    group: 'Export (lấy proxy nhanh)',
    items: [
      { method: 'GET', path: '/api/v1/proxies/export?type=public&format=text', desc: 'Export text/plain "ip:port:user:pass" — 1 dòng / 1 cred enabled.',
        resp: '1.2.3.4:30000:fbr00chgt:3b4r7U5L\n5.6.7.8:30001:user2:pass2\n...' },
      { method: 'GET', path: '/api/v1/proxies/export?type=local&format=json', desc: 'Export JSON nested. type: public|local. format: text|json.',
        resp: '[ { "session_id":1, "proxy_id":1, "port":30000, "ip":"...", "creds":[ { "username":"...", "password":"..." } ] } ]' },
    ],
  },
  {
    group: 'Tokens',
    items: [
      { method: 'GET',    path: '/api/v1/tokens', desc: 'List token (mask, chỉ show last4).' },
      { method: 'POST',   path: '/api/v1/tokens', desc: 'Sinh token mới 32-byte hex. **Trả full token 1 lần** (không lưu plain).',
        body: '{ "label":"app-readonly" }',
        resp: '{ "id":2, "label":"app-readonly", "token":"<32-byte-hex>" }' },
      { method: 'DELETE', path: '/api/v1/tokens/:id', desc: 'Xóa token. KHÔNG cho xóa token cuối (chống lockout).' },
    ],
  },
  {
    group: 'Realtime (SSE)',
    items: [
      { method: 'GET', path: '/api/v1/events?token=<token>', desc: 'Server-Sent Events stream. Event types: session.status, session.public_ip, session.rotate, proxy.cred_changed, proxy.started, proxy.stopped. Keepalive 15s.',
        note: 'JS: <code>new EventSource("/api/v1/events?token=" + token)</code>' },
    ],
  },
];

function render() {
  const root = document.getElementById('api-content');
  root.innerHTML = SPEC.map(g => `
    <div class="card api-group" data-group="${escapeHTML(g.group)}">
      <h3>${escapeHTML(g.group)}</h3>
      ${g.items.map((it, idx) => renderRow(g.group, it, idx)).join('')}
    </div>
  `).join('');
}

function renderRow(group, it, idx) {
  const rid = `${group}-${idx}`.replace(/[^a-zA-Z0-9]/g, '_');
  const noAuth = it.auth === false;
  return `
    <div class="api-row" data-row="${escapeHTML(it.method + ' ' + it.path)}" data-text="${escapeHTML((it.desc || '') + ' ' + it.path)}">
      <div>
        <span class="method ${it.method}">${it.method}</span>
        <span class="path">${escapeHTML(it.path)}</span>
        ${noAuth ? '<span class="badge stopped" style="margin-left:8px">no-auth</span>' : ''}
      </div>
      <div class="desc">${escapeHTML(it.desc || '')}</div>
      ${it.body ? `<div class="extras"><div class="muted small">Body:</div><pre>${escapeHTML(it.body)}</pre></div>` : ''}
      ${it.resp ? `<div class="extras"><div class="muted small">Response (data):</div><pre>${escapeHTML(it.resp)}</pre></div>` : ''}
      ${it.note ? `<div class="extras muted small">${it.note}</div>` : ''}
      <div class="row-actions">
        <button class="secondary" onclick="copyCurl('${escapeJS(it.method)}', '${escapeJS(it.path)}', ${JSON.stringify(it.body || '').replace(/"/g, '&quot;')}, ${noAuth ? 'true' : 'false'})">Copy curl</button>
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
  try {
    await navigator.clipboard.writeText(curl);
    Toast.success('Copied curl: ' + method + ' ' + path);
  } catch (e) { Toast.error('Copy: ' + e.message); }
}

function filterApi() {
  const q = (document.getElementById('api-search').value || '').toLowerCase().trim();
  document.querySelectorAll('.api-row').forEach(r => {
    const t = (r.getAttribute('data-text') || '').toLowerCase();
    r.style.display = (!q || t.includes(q)) ? '' : 'none';
  });
  // Hide group nếu tất cả row hidden
  document.querySelectorAll('.api-group').forEach(g => {
    const anyVisible = Array.from(g.querySelectorAll('.api-row')).some(r => r.style.display !== 'none');
    g.style.display = anyVisible ? '' : 'none';
  });
}
