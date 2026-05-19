if (!ensureAuth()) {} else {
  renderNav('sessions');
  init();
}

let _lines = [];
let _sessions = [];

const params = new URLSearchParams(location.search);
const initialLine = params.get('line') || '';

async function init() {
  await loadLines();
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
    tbody.innerHTML = '<tr><td colspan="10" class="muted">Chưa có session.</td></tr>';
    return;
  }
  tbody.innerHTML = _sessions.map(s => `
    <tr>
      <td>${s.id}</td>
      <td>${escapeHTML(lineName(s.line_id))}</td>
      <td class="mono">ppp${s.ppp_unit}</td>
      <td class="mono">${escapeHTML(s.iface || '—')}</td>
      <td class="mono small">${escapeHTML(s.username)}</td>
      <td>${statusBadge(s.status)} ${s.last_error ? `<span class="muted small" title="${escapeHTML(s.last_error)}">⚠</span>` : ''}</td>
      <td class="mono">${escapeHTML(s.public_ip || s.ip || '—')}</td>
      <td class="mono">${s.proxy_port || '—'}</td>
      <td>${s.creds_count} <a href="#" onclick="event.preventDefault();openCreds(${s.id},${s.proxy_id})">edit</a></td>
      <td class="actions">
        <button class="small" onclick="rotateSession(${s.id})">Rotate</button>
        <button class="small secondary" onclick="toggleEnabled(${s.id},'${s.proxy_status}')">${s.proxy_status === 'running' ? 'Tắt' : 'Bật'}</button>
        <button class="small danger" onclick="deleteSession(${s.id})">Xóa</button>
      </td>
    </tr>
  `).join('');
}

function openCreateSession() {
  document.getElementById('create-sess-modal').classList.add('open');
}

function closeModal(id) { document.getElementById(id).classList.remove('open'); }

async function submitCreateSession() {
  const data = {
    username: document.getElementById('ns-user').value.trim(),
    password: document.getElementById('ns-pass').value.trim(),
    mac: document.getElementById('ns-mac').value.trim(),
  };
  const lineId = parseInt(document.getElementById('ns-line').value);
  if (!data.username || !data.password) { Toast.error('user/pass bắt buộc'); return; }
  try {
    const r = await Api.createSession(lineId, data);
    Toast.success(`Session ${r.session.id} ${r.session.status}`);
    closeModal('create-sess-modal');
    loadSessions();
  } catch (e) { Toast.error('Tạo session: ' + e.message); }
}

function openBulkSession() {
  document.getElementById('bulk-sess-modal').classList.add('open');
}

async function submitBulk() {
  const lineId = parseInt(document.getElementById('bs-line').value);
  const raw = document.getElementById('bs-creds').value.trim();
  const creds = raw.split('\n').map(l => l.trim()).filter(Boolean).map(line => {
    const parts = line.split(/\s+/);
    return { username: parts[0], password: parts[1], mac: parts[2] || '' };
  });
  if (!creds.length) { Toast.error('Nhập creds'); return; }
  try {
    const r = await Api.bulkCreateSessions(lineId, { count: creds.length, creds });
    const ok = r.filter(x => x.status === 'connected').length;
    Toast.success(`Bulk: ${ok}/${r.length} OK`);
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
