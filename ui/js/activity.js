let _offset = 0;
let _total = 0;
let _autoTimer = null;

if (!ensureAuth()) {} else {
  renderNav('activity');
  // ?session=N hoặc ?iuser=X tự fill filter
  const u = new URLSearchParams(location.search);
  if (u.get('session')) document.getElementById('ac-session').value = u.get('session');
  if (u.get('iuser'))   document.getElementById('ac-iuser').value = u.get('iuser');
  if (u.get('level'))   document.getElementById('ac-level').value = u.get('level');
  if (u.get('category')) document.getElementById('ac-category').value = u.get('category');

  ['ac-level','ac-category','ac-session','ac-iuser','ac-q','ac-limit'].forEach(id => {
    document.getElementById(id).addEventListener('change', () => { _offset = 0; loadActivity(); });
  });
  document.getElementById('ac-q').addEventListener('keydown', e => {
    if (e.key === 'Enter') { _offset = 0; loadActivity(); }
  });
  document.getElementById('ac-auto').addEventListener('change', toggleAuto);
  loadActivity();
}

const CAT_LABEL = {
  dial: 'Quay số', rotate: 'Đổi IP', reconnect: 'Reconnect',
  watchdog: 'Watchdog', claim: 'Claim', cred: 'Tài khoản',
  session: 'Session', line: 'Line', auth: 'Đăng nhập', proxy: 'Proxy',
};

function buildParams() {
  const p = {};
  const level = document.getElementById('ac-level').value;
  const category = document.getElementById('ac-category').value;
  const sid = document.getElementById('ac-session').value;
  const iuser = document.getElementById('ac-iuser').value.trim();
  const q = document.getElementById('ac-q').value.trim();
  const limit = document.getElementById('ac-limit').value;
  if (level) p.level = level;
  if (category) p.category = category;
  if (sid) p.session_id = sid;
  if (iuser) p.iuser_id = iuser;
  if (q) p.q = q;
  if (limit) p.limit = limit;
  if (_offset > 0) p.offset = _offset;
  return p;
}

async function loadActivity() {
  try {
    const res = await Api.listActivity(buildParams());
    _total = res.total;
    renderRows(res.items || []);
    renderStats(res);
  } catch (e) {
    document.getElementById('ac-body').innerHTML =
      `<tr><td colspan="4" class="ac-empty" style="color:#fca5a5">Lỗi tải hoạt động: ${escapeHTML(e.message)}</td></tr>`;
  }
}

function renderRows(items) {
  const tbody = document.getElementById('ac-body');
  if (!items.length) {
    tbody.innerHTML = '<tr><td colspan="4" class="ac-empty">(Không có hoạt động nào khớp bộ lọc)</td></tr>';
    return;
  }
  tbody.innerHTML = items.map(it => {
    const t = new Date(it.created_at);
    const pad = (n) => String(n).padStart(2, '0');
    const time = isNaN(t.getTime()) ? escapeHTML(String(it.created_at ?? ''))
      : `${pad(t.getHours())}:${pad(t.getMinutes())}:${pad(t.getSeconds())}` +
        ` <span class="muted" style="color:#475569">${pad(t.getDate())}/${pad(t.getMonth()+1)}/${t.getFullYear()}</span>`;
    const refs = [];
    if (it.session_id) refs.push(`<span class="ac-ref"><a href="/activity.html?session=${it.session_id}">Session #${it.session_id}</a></span>`);
    if (it.line_id) refs.push(`<span class="ac-ref">Line #${it.line_id}</span>`);
    if (it.proxy_id) refs.push(`<span class="ac-ref">Proxy #${it.proxy_id}</span>`);
    if (it.cred_id) refs.push(`<span class="ac-ref">Cred #${it.cred_id}</span>`);
    if (it.iuser_id) refs.push(`<span class="ac-ref"><a href="/activity.html?iuser=${encodeURIComponent(it.iuser_id)}">iuser: ${escapeHTML(it.iuser_id)}</a></span>`);
    if (it.client_ip) refs.push(`<span class="ac-ref">IP: ${escapeHTML(it.client_ip)}</span>`);
    let details = '';
    if (it.details) {
      try {
        const obj = JSON.parse(it.details);
        const lines = Object.entries(obj).map(([k,v]) => `${k}=${typeof v === 'object' ? JSON.stringify(v) : v}`);
        if (lines.length) details = `<span class="ac-details">${escapeHTML(lines.join(' · '))}</span>`;
      } catch {}
    }
    return `<tr>
      <td class="ac-time">${time}</td>
      <td><span class="lvl-badge lvl-${escapeHTML(it.level)}">${escapeHTML(it.level)}</span></td>
      <td><span class="cat-badge cat-${escapeHTML(it.category)}">${escapeHTML(CAT_LABEL[it.category] || it.category)}</span><br>
        <span class="muted small" style="font-family:monospace;font-size:10.5px">${escapeHTML(it.action)}</span></td>
      <td class="ac-summary">${escapeHTML(it.summary)}<br>${refs.join('')}${details}</td>
    </tr>`;
  }).join('');
}

function renderStats(res) {
  const showing = (res.items || []).length;
  const limit = res.limit || 200;
  const offset = res.offset || 0;
  const page = Math.floor(offset / limit) + 1;
  const totalPages = Math.max(1, Math.ceil(res.total / limit));
  document.getElementById('ac-stats').textContent =
    `Hiển thị ${showing} / ${res.total} entries`;
  document.getElementById('ac-page-info').textContent =
    `trang ${page} / ${totalPages}`;
}

function prevPage() {
  const limit = parseInt(document.getElementById('ac-limit').value) || 200;
  _offset = Math.max(0, _offset - limit);
  loadActivity();
}
function nextPage() {
  const limit = parseInt(document.getElementById('ac-limit').value) || 200;
  if (_offset + limit < _total) {
    _offset += limit;
    loadActivity();
  }
}

function toggleAuto() {
  if (_autoTimer) { clearInterval(_autoTimer); _autoTimer = null; }
  if (document.getElementById('ac-auto').checked) {
    _autoTimer = setInterval(loadActivity, 5000);
  }
}
