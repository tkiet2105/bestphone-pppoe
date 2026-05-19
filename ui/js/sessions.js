if (!ensureAuth()) {} else {
  renderNav('sessions');
  init();
}

let _lines = [];
let _sessions = [];
let _nicsPADO = {}; // iface → {pado, ac_name, ...}

const params = new URLSearchParams(location.search);
const initialLine = params.get('line') || '';

async function init() {
  await Promise.all([loadLines(), loadNics()]);
  if (initialLine) document.getElementById('filter-line').value = initialLine;
  await loadSessions();
  Api.subscribeEvents((type) => {
    if (['session.status', 'session.public_ip', 'session.rotate', 'proxy.started', 'proxy.stopped'].includes(type)) {
      loadSessions();
    }
  });
  document.getElementById('filter-line').addEventListener('change', loadSessions);
  document.getElementById('filter-status').addEventListener('change', loadSessions);
}

// ─── NIC panel ───
async function loadNics() {
  try { renderNics(await Api.listIfaces()); }
  catch (e) { Toast.error('Load NICs: ' + e.message); }
}

function renderNics(list) {
  if (!list.length) { document.getElementById('nic-list').textContent = '(không có NIC physical)'; return; }
  const html = list.map(n => {
    const tags = [];
    if (n.state === 'up') tags.push('<span class="tag up">UP</span>');
    if (n.carrier) tags.push(`<span class="tag carrier">LINK ${n.speed_mbps > 0 ? n.speed_mbps + 'M' : ''}</span>`);
    else tags.push('<span class="tag no-link">NO-CARRIER</span>');
    if (n.used_by_line) tags.push(`<span class="tag line">Line #${n.used_by_line} (${escapeHTML(n.used_by_name)})</span>`);
    const pado = _nicsPADO[n.name];
    if (pado && pado.pado) tags.push(`<span class="tag pado">PADO ${escapeHTML(pado.ac_name)}</span>`);
    const ips = n.ips && n.ips.length ? n.ips.join(', ') : '—';
    const klass = ['nic-tile'];
    if (!n.carrier) klass.push('no-carrier');
    if (pado && pado.pado) klass.push('has-pado');
    return `<div class="${klass.join(' ')}">
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
    loadNics();
  } catch (e) {
    Toast.error('Probe: ' + e.message);
  } finally {
    btn.disabled = false; btn.textContent = 'PADO probe';
  }
}

// ─── Lines load ───
async function loadLines() {
  try {
    _lines = await Api.listLines();
    const filterSel = document.getElementById('filter-line');
    filterSel.innerHTML = '<option value="">Tất cả lines</option>' +
      _lines.map(l => `<option value="${l.id}">${escapeHTML(l.name)} (${l.iface})</option>`).join('');
    const smSel = document.getElementById('sm-line');
    if (smSel) {
      smSel.innerHTML = _lines.map(l => `<option value="${l.id}">${escapeHTML(l.name)} — ${escapeHTML(l.iface)} — ${l.username ? escapeHTML(l.username) : '(no cred)'}</option>`).join('');
    }
  } catch (e) { Toast.error(e.message); }
}

// ─── Sessions table ───
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
         <a href="/logs.html?filter=${encodeURIComponent(s.username)}" target="_blank" class="small">log↗</a>`
      : '<span class="muted small">—</span>';
    return `<tr>
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
    </tr>`;
  }).join('');
}

// ─── Create Session(s) Modal (Mode 2 pattern) ───
function openCreateSession() {
  document.getElementById('create-sess-modal').classList.add('open');
  document.getElementById('sm-count').value = '1';
  document.getElementById('sm-proxy-mode').value = 'random';
  document.getElementById('sm-proxy-user').value = '';
  document.getElementById('sm-proxy-pass').value = '';
  updateSessModalUI();
}

function updateSessModalUI() {
  const lineId = parseInt(document.getElementById('sm-line').value);
  const line = _lines.find(l => l.id === lineId);
  const info = document.getElementById('sm-line-info');
  if (line) {
    if (line.username) {
      info.innerHTML = `Cred ISP của line: <span class="mono">${escapeHTML(line.username)}</span> / <span class="mono">••••</span>`;
      info.style.color = '#86efac';
    } else {
      info.innerHTML = '⚠ Line CHƯA SET cred ISP — Edit line trước hoặc tạo session sẽ fail.';
      info.style.color = '#fbbf24';
    }
  }
  const mode = document.getElementById('sm-proxy-mode').value;
  document.getElementById('sm-manual-rows').style.display = mode === 'manual' ? '' : 'none';
}

function closeModal(id) { document.getElementById(id).classList.remove('open'); }

async function submitCreateSession() {
  const lineId = parseInt(document.getElementById('sm-line').value);
  const count = parseInt(document.getElementById('sm-count').value) || 1;
  const mode = document.getElementById('sm-proxy-mode').value;
  if (count < 1 || count > 50) { Toast.error('Count phải 1..50'); return; }
  const proxyAuth = { mode };
  if (mode === 'manual') {
    proxyAuth.username = document.getElementById('sm-proxy-user').value.trim();
    proxyAuth.password = document.getElementById('sm-proxy-pass').value.trim();
    if (!proxyAuth.username || !proxyAuth.password) {
      Toast.error('Manual mode cần proxy username + password');
      return;
    }
  }
  try {
    if (count === 1) {
      const r = await Api.createSession(lineId, { proxy_auth: proxyAuth });
      Toast.success(`Session ${r.session.id} ${r.session.status}`);
    } else {
      Toast.info(`Bulk ${count} sessions...`);
      const r = await Api.bulkCreateSessions(lineId, { count, proxy_auth: proxyAuth });
      const ok = r.filter(x => x.status === 'connected').length;
      const err = r.filter(x => x.status === 'error').length;
      Toast.success(`Bulk: ${ok} connected · ${err} error / ${r.length}`);
      if (err > 0) {
        const errMsgs = [...new Set(r.filter(x => x.error).map(x => x.error))];
        errMsgs.slice(0, 2).forEach(m => Toast.info('Lý do: ' + m));
      }
    }
    closeModal('create-sess-modal');
    loadSessions();
  } catch (e) { Toast.error('Tạo session: ' + e.message); }
}

// ─── Session actions ───
async function rotateSession(id) {
  try {
    Toast.info('Rotate ' + id + '...');
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

// ─── Credentials modal (Mode 2 pattern: 2 section single + bulk) ───
let _curPid = 0;
let _curSid = 0;

function credRandHex(n) {
  const a = new Uint8Array(n);
  crypto.getRandomValues(a);
  return Array.from(a).map(b => b.toString(16).padStart(2, '0')).join('');
}

async function openCreds(sid, pid) {
  _curSid = sid; _curPid = pid;
  document.getElementById('cm-sid').textContent = sid;
  document.getElementById('cm-pid').textContent = pid;
  document.getElementById('cm-label').value = '';
  document.getElementById('cm-user').value = '';
  document.getElementById('cm-pass').value = '';
  document.getElementById('cm-bulk-count').value = '10';
  document.getElementById('cm-bulk-prefix').value = 'u';
  document.getElementById('creds-modal').classList.add('open');
  await loadCreds();
}

async function loadCreds() {
  try {
    const rows = await Api.listCreds(_curPid);
    const tbody = document.querySelector('#creds-table tbody');
    tbody.innerHTML = rows.map(r => `<tr>
      <td>${r.id}</td>
      <td>${escapeHTML(r.label)}</td>
      <td class="mono">${escapeHTML(r.username)}</td>
      <td class="mono">${escapeHTML(r.password)}</td>
      <td><input type="checkbox" ${r.enabled?'checked':''} onchange="toggleCred(${r.id},this.checked)"></td>
      <td><button class="small danger" onclick="delCred(${r.id})">Xóa</button></td>
    </tr>`).join('') || '<tr><td colspan="6" class="muted">(empty — proxy mở no-auth)</td></tr>';
  } catch (e) { Toast.error(e.message); }
}

async function submitCred() {
  const data = {
    label: document.getElementById('cm-label').value.trim(),
    username: document.getElementById('cm-user').value.trim(),
    password: document.getElementById('cm-pass').value.trim(),
  };
  if (!data.username || !data.password) { Toast.error('username + password bắt buộc'); return; }
  try {
    await Api.createCred(_curPid, data);
    document.getElementById('cm-label').value = '';
    document.getElementById('cm-user').value = '';
    document.getElementById('cm-pass').value = '';
    loadCreds();
    Toast.success('Thêm cred OK');
  } catch (e) { Toast.error(e.message); }
}

async function submitBulkCred() {
  const count = parseInt(document.getElementById('cm-bulk-count').value) || 0;
  const prefix = document.getElementById('cm-bulk-prefix').value.trim() || 'u';
  if (count < 1 || count > 200) { Toast.error('Count phải 1..200'); return; }
  try {
    await Api.bulkCreds(_curPid, { count, prefix });
    loadCreds();
    Toast.success(`Sinh ${count} cred`);
  } catch (e) { Toast.error(e.message); }
}

async function toggleCred(cid, enabled) {
  try { await Api.updateCred(_curPid, cid, { enabled }); }
  catch (e) { Toast.error(e.message); }
}

async function delCred(cid) {
  try { await Api.deleteCred(_curPid, cid); loadCreds(); }
  catch (e) { Toast.error(e.message); }
}

// ─── Export modal ───
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
    Toast.success('Copied ' + text.split('\n').filter(Boolean).length + ' dòng');
  } catch (e) { Toast.error('Copy: ' + e.message); }
}
