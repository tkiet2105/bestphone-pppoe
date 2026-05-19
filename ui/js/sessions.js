if (!ensureAuth()) {} else {
  renderNav('sessions');
  init();
}

// --- NIC panel ---
let _nicsPADO = {}; // iface → {ac_name, ac_source_mac}

async function loadNics() {
  try {
    const list = await Api.listIfaces();
    renderNics(list);
  } catch (e) { Toast.error('Load NICs: ' + e.message); }
}

function renderNics(list) {
  if (!list.length) {
    document.getElementById('nic-list').textContent = '(không có NIC physical)';
    return;
  }
  const html = list.map(n => {
    const tags = [];
    if (n.state === 'up') tags.push('<span class="tag up">UP</span>');
    if (n.carrier) tags.push(`<span class="tag carrier">LINK ${n.speed_mbps > 0 ? n.speed_mbps + 'M' : ''}</span>`);
    else tags.push('<span class="tag no-link">NO-CARRIER</span>');
    if (n.used_by_line) tags.push(`<span class="tag line">Line #${n.used_by_line} (${escapeHTML(n.used_by_name)})</span>`);
    const pado = _nicsPADO[n.name];
    if (pado && pado.pado) {
      tags.push(`<span class="tag pado">PADO ${escapeHTML(pado.ac_name)}</span>`);
    }
    const ips = n.ips && n.ips.length ? n.ips.join(', ') : '—';
    const klass = ['nic-tile'];
    if (!n.carrier) klass.push('no-carrier');
    if (pado && pado.pado) klass.push('has-pado');
    return `
      <div class="${klass.join(' ')}">
        <div class="nic-name">${escapeHTML(n.name)}</div>
        <div class="nic-meta">${escapeHTML(n.mac)}</div>
        <div class="nic-meta">${escapeHTML(ips)}</div>
        <div class="nic-tags">${tags.join('')}</div>
      </div>`;
  }).join('');
  document.getElementById('nic-list').innerHTML = '<div class="nic-grid">' + html + '</div>';
}

async function probeNics() {
  const btn = document.getElementById('probe-btn');
  btn.disabled = true; btn.textContent = 'Probing...';
  try {
    const results = await Api.probeIfaces();
    _nicsPADO = {};
    results.forEach(r => _nicsPADO[r.name] = r);
    const padoCount = results.filter(r => r.pado).length;
    Toast.success(`Probe xong: ${padoCount}/${results.length} NIC có PADO`);
    loadNics(); // re-render để hiển thị tag PADO
  } catch (e) {
    Toast.error('Probe: ' + e.message);
  } finally {
    btn.disabled = false; btn.textContent = 'PADO probe';
  }
}

let _lines = [];
let _sessions = [];

const params = new URLSearchParams(location.search);
const initialLine = params.get('line') || '';

async function init() {
  await Promise.all([loadLines(), loadNics()]);
  if (initialLine) document.getElementById('filter-line').value = initialLine;
  await loadSessions();
  // SSE live update
  Api.subscribeEvents((type, data) => {
    if (type === 'session.status' || type === 'session.public_ip' || type === 'session.rotate' || type === 'proxy.started' || type === 'proxy.stopped') {
      loadSessions();
    }
  });
  document.getElementById('filter-line').addEventListener('change', loadSessions);
  document.getElementById('filter-status').addEventListener('change', loadSessions);
}

async function loadLines() {
  try {
    _lines = await Api.listLines();
    const sel = document.getElementById('filter-line');
    sel.innerHTML = '<option value="">Tất cả lines</option>' + _lines.map(l => `<option value="${l.id}">${escapeHTML(l.name)} (${l.iface})</option>`).join('');
    document.getElementById('ns-line').innerHTML = _lines.map(l => `<option value="${l.id}">${escapeHTML(l.name)}</option>`).join('');
    document.getElementById('bs-line').innerHTML = _lines.map(l => `<option value="${l.id}">${escapeHTML(l.name)}</option>`).join('');
  } catch (e) { Toast.error(e.message); }
}

async function loadSessions() {
  const params = {};
  const lineId = document.getElementById('filter-line').value;
  const status = document.getElementById('filter-status').value;
  if (lineId) params.line_id = lineId;
  if (status) params.status = status;
  try {
    _sessions = await Api.listSessions(params);
    renderSessions();
  } catch (e) { Toast.error('Load sessions: ' + e.message); }
}

function lineName(id) {
  const l = _lines.find(x => x.id === id);
  return l ? l.name : '?';
}

function renderSessions() {
  const tbody = document.querySelector('#sessions-table tbody');
  if (!_sessions.length) {
    tbody.innerHTML = '<tr><td colspan="11" class="muted">Chưa có session.</td></tr>';
    return;
  }
  tbody.innerHTML = _sessions.map(s => {
    const errCell = s.last_error
      ? `<span class="mono small" style="color:#fbbf24" title="${escapeHTML(s.last_error)}">${escapeHTML(s.last_error.length > 50 ? s.last_error.slice(0,50) + '…' : s.last_error)}</span>
         <a href="/logs.html?filter=${encodeURIComponent(s.username)}" target="_blank" class="small" title="Xem log pppd cho session này">log↗</a>`
      : '<span class="muted small">—</span>';
    return `
    <tr>
      <td>${s.id}</td>
      <td>${escapeHTML(lineName(s.line_id))}</td>
      <td class="mono">ppp${s.ppp_unit}</td>
      <td class="mono">${escapeHTML(s.iface || '—')}</td>
      <td class="mono small">${escapeHTML(s.username)}</td>
      <td>${statusBadge(s.status)}</td>
      <td class="mono">${escapeHTML(s.public_ip || s.ip || '—')}</td>
      <td class="mono">${s.proxy_port || '—'}</td>
      <td>${s.creds_count} <a href="#" onclick="event.preventDefault();openCreds(${s.id},${s.proxy_id})">edit</a></td>
      <td>${errCell}</td>
      <td class="actions">
        <button class="small" onclick="rotateSession(${s.id})">Rotate</button>
        <button class="small secondary" onclick="toggleEnabled(${s.id},'${s.proxy_status}')">${s.proxy_status === 'running' ? 'Tắt' : 'Bật'}</button>
        <button class="small danger" onclick="deleteSession(${s.id})">Xóa</button>
      </td>
    </tr>
    `;
  }).join('');
}

function openCreateSession() {
  document.getElementById('create-sess-modal').classList.add('open');
  document.getElementById('ns-user').value = '';
  document.getElementById('ns-pass').value = '';
  document.getElementById('ns-mac').value = '';
  updateSingleUI();
}

function randHex(n) {
  const a = new Uint8Array(n);
  crypto.getRandomValues(a);
  return Array.from(a).map(b => b.toString(16).padStart(2, '0')).join('');
}
function randomUser() { return 'u_' + randHex(4); }
function randomPass() { return randHex(6); }
function randomMac() {
  const a = new Uint8Array(5);
  crypto.getRandomValues(a);
  return '02:' + Array.from(a).map(b => b.toString(16).padStart(2, '0')).join(':');
}

function updateSingleUI() {
  const lineId = parseInt(document.getElementById('ns-line').value);
  const line = _lines.find(l => l.id === lineId);
  const info = document.getElementById('ns-line-info');
  if (line) {
    if (line.isp_username) {
      info.innerHTML = `Sẽ dùng cred ISP của line: <span class="mono">${escapeHTML(line.isp_username)}</span> / <span class="mono">••••</span>`;
      info.style.color = '#86efac';
    } else {
      info.innerHTML = '⚠ Line CHƯA SET cred ISP — cần override bên dưới hoặc Edit line trước.';
      info.style.color = '#fbbf24';
    }
  }
}

function rollRandomMac() {
  document.getElementById('ns-mac').value = randomMac();
}

function closeModal(id) { document.getElementById(id).classList.remove('open'); }

async function submitCreateSession() {
  const data = {
    username: document.getElementById('ns-user').value.trim(),
    password: document.getElementById('ns-pass').value.trim(),
    mac: document.getElementById('ns-mac').value.trim() || randomMac(),
  };
  const lineId = parseInt(document.getElementById('ns-line').value);
  // Cred resolve: empty → backend dùng line cred
  try {
    const r = await Api.createSession(lineId, data);
    const credLabel = data.username || '(line cred)';
    Toast.success(`Session ${r.session.id} ${r.session.status} (cred: ${credLabel}, MAC: ${data.mac})`);
    closeModal('create-sess-modal');
    loadSessions();
  } catch (e) { Toast.error('Tạo session: ' + e.message); }
}

function openBulkSession() {
  document.getElementById('bulk-sess-modal').classList.add('open');
  document.getElementById('bs-creds').value = '';
  document.getElementById('bs-count').value = '10';
  updateBulkUI();
}

function updateBulkUI() {
  const lineId = parseInt(document.getElementById('bs-line').value);
  const line = _lines.find(l => l.id === lineId);
  const info = document.getElementById('bs-line-info');
  if (line) {
    if (line.isp_username) {
      info.innerHTML = `Sẽ dùng cred ISP của line: <span class="mono">${escapeHTML(line.isp_username)}</span> / <span class="mono">••••</span> × N MAC random.`;
      info.style.color = '#86efac';
    } else {
      info.innerHTML = '⚠ Line CHƯA SET cred ISP — cần Edit line hoặc dùng textarea override.';
      info.style.color = '#fbbf24';
    }
  }
}

async function submitBulk() {
  const lineId = parseInt(document.getElementById('bs-line').value);
  const n = parseInt(document.getElementById('bs-count').value) || 0;
  const raw = document.getElementById('bs-creds').value.trim();

  let payload;
  if (raw) {
    // Override mode: textarea custom
    const creds = raw.split('\n').map(l => l.trim()).filter(Boolean).map(line => {
      const parts = line.split(/\s+/);
      return { username: parts[0], password: parts[1], mac: parts[2] || randomMac() };
    });
    if (!creds.length) { Toast.error('Textarea trống'); return; }
    payload = { count: creds.length, creds };
  } else {
    // Default mode: N + line cred + MAC random
    if (n <= 0 || n > 100) { Toast.error('N phải trong 1..100'); return; }
    payload = { count: n, auto_mac: true };
  }
  try {
    Toast.info(`Bulk ${payload.count} sessions — chạy nền (mỗi dial ~3s)...`);
    const r = await Api.bulkCreateSessions(lineId, payload);
    const connected = r.filter(x => x.status === 'connected').length;
    const errored = r.filter(x => x.status === 'error').length;
    const created = r.filter(x => x.session_id > 0).length;
    Toast.success(`Bulk: ${created}/${r.length} created in DB · ${connected} connected · ${errored} dial-fail`);
    if (errored > 0) {
      // Show distinct error messages cho user
      const errMsgs = [...new Set(r.filter(x => x.error).map(x => x.error))];
      errMsgs.slice(0, 3).forEach(m => Toast.info('Lý do: ' + m));
    }
    closeModal('bulk-sess-modal');
    loadSessions();
  } catch (e) { Toast.error('Bulk: ' + e.message); }
}

async function rotateSession(id) {
  try {
    Toast.info('Đang rotate ' + id + '...');
    const r = await Api.rotateSession(id);
    Toast.success(`Rotate OK: ${r.old_ip || '—'} → ${r.new_ip || '—'}${r.same_ip ? ' (same)' : ''}`);
    loadSessions();
  } catch (e) { Toast.error('Rotate: ' + e.message); }
}

async function toggleEnabled(id, currentStatus) {
  try {
    const enabled = currentStatus !== 'running';
    await Api.setSessionEnabled(id, enabled);
    Toast.success(enabled ? 'Bật' : 'Tắt');
    loadSessions();
  } catch (e) { Toast.error(e.message); }
}

async function deleteSession(id) {
  if (!confirm(`Xóa session ${id} (hangup + remove proxy)?`)) return;
  try {
    await Api.deleteSession(id);
    Toast.success('Đã xóa');
    loadSessions();
  } catch (e) { Toast.error(e.message); }
}

let _curPid = 0;
let _curSid = 0;

async function openCreds(sid, pid) {
  _curSid = sid; _curPid = pid;
  document.getElementById('cm-sid').textContent = sid;
  document.getElementById('cm-pid').textContent = pid;
  document.getElementById('creds-modal').classList.add('open');
  await loadCreds();
}

async function loadCreds() {
  try {
    const rows = await Api.listCreds(_curPid);
    const tbody = document.querySelector('#creds-table tbody');
    tbody.innerHTML = rows.map(r => `
      <tr>
        <td>${r.id}</td>
        <td>${escapeHTML(r.label)}</td>
        <td class="mono">${escapeHTML(r.username)}</td>
        <td class="mono">${escapeHTML(r.password)}</td>
        <td><input type="checkbox" ${r.enabled?'checked':''} onchange="toggleCred(${r.id},this.checked)"></td>
        <td><button class="small danger" onclick="delCred(${r.id})">Xóa</button></td>
      </tr>
    `).join('') || '<tr><td colspan="6" class="muted">(empty)</td></tr>';
  } catch (e) { Toast.error(e.message); }
}

async function submitCred() {
  const data = {
    label: document.getElementById('cm-label').value.trim() || 'manual',
    username: document.getElementById('cm-user').value.trim(),
    password: document.getElementById('cm-pass').value.trim(),
  };
  if (!data.username || !data.password) { Toast.error('user/pass'); return; }
  try {
    await Api.createCred(_curPid, data);
    document.getElementById('cm-label').value = '';
    document.getElementById('cm-user').value = '';
    document.getElementById('cm-pass').value = '';
    loadCreds();
    Toast.success('Thêm cred OK');
  } catch (e) { Toast.error(e.message); }
}

async function bulkCredsPrompt() {
  const n = prompt('Số cred ngẫu nhiên muốn sinh?', '10');
  if (!n) return;
  try {
    await Api.bulkCreds(_curPid, { count: parseInt(n), label_prefix: 'auto' });
    loadCreds();
    Toast.success('Sinh ' + n + ' cred');
  } catch (e) { Toast.error(e.message); }
}

async function toggleCred(cid, enabled) {
  try {
    await Api.updateCred(_curPid, cid, { enabled });
  } catch (e) { Toast.error(e.message); }
}

async function delCred(cid) {
  try {
    await Api.deleteCred(_curPid, cid);
    loadCreds();
  } catch (e) { Toast.error(e.message); }
}

async function showExport() {
  document.getElementById('export-modal').classList.add('open');
  await refreshExport();
}

async function refreshExport() {
  const type = document.getElementById('ex-type').value;
  try {
    const text = await Api.exportProxies(type, 'text');
    document.getElementById('ex-text').textContent = text;
  } catch (e) { Toast.error(e.message); }
}

async function copyExport() {
  const text = document.getElementById('ex-text').textContent;
  try {
    await navigator.clipboard.writeText(text);
    Toast.success('Đã copy ' + text.split('\n').filter(Boolean).length + ' dòng');
  } catch (e) { Toast.error('Copy: ' + e.message); }
}
