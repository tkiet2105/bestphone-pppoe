if (!ensureAuth()) {} else {
  renderNav('sessions');
  init();
}

let _lines = [];
let _sessions = [];
let _nicsPADO = {}; // iface → {pado, ac_name, ...}
let _selected = new Set(); // session ids đã chọn

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
  _initSelectionUX();
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
    tbody.innerHTML = '<tr><td colspan="12" class="muted">Chưa có session nào. Bấm "+ Tạo session" để tạo.</td></tr>';
    _updateSelUI();
    return;
  }
  // Đồng bộ selection — bỏ id không còn tồn tại
  const currentIds = new Set(_sessions.map(s => s.id));
  [..._selected].forEach(id => { if (!currentIds.has(id)) _selected.delete(id); });

  tbody.innerHTML = _sessions.map((s, idx) => {
    const errCell = s.last_error
      ? `<span class="mono small" style="color:#fbbf24" title="${escapeHTML(s.last_error)}">${escapeHTML(s.last_error.length > 50 ? s.last_error.slice(0,50) + '…' : s.last_error)}</span>
         <a href="/logs.html?filter=${encodeURIComponent(s.username)}" target="_blank" class="small">log↗</a>`
      : '<span class="muted small">—</span>';
    const isSel = _selected.has(s.id);
    return `<tr class="pp-row${isSel ? ' sel' : ''}" data-sess-id="${s.id}">
      <td class="cb-cell"><input type="checkbox" class="row-check" ${isSel ? 'checked' : ''} onchange="toggleRowSel(${s.id}, this.checked)"></td>
      <td title="DB id: ${s.id}">${idx + 1}</td>
      <td>${escapeHTML(lineName(s.line_id))}</td>
      <td class="mono">ppp${s.ppp_unit}</td>
      <td class="mono">${escapeHTML(s.iface || '—')}</td>
      <td class="mono small">${escapeHTML(s.username)}</td>
      <td>${statusBadge(s.status)}</td>
      <td class="mono">${escapeHTML(s.public_ip || s.ip || '—')}</td>
      <td class="mono">${s.proxy_port || '—'}</td>
      <td>${s.creds_count} <a href="#" onclick="event.preventDefault();openCreds(${s.id},${s.proxy_id})">sửa</a></td>
      <td>${errCell}</td>
      <td class="actions">
        <button class="small" onclick="rotateSession(${s.id})">Đổi IP</button>
        <button class="small secondary" onclick="toggleEnabled(${s.id},'${s.proxy_status}')">${s.proxy_status === 'running' ? 'Tắt' : 'Bật'}</button>
        <button class="small danger" onclick="deleteSession(${s.id})">Xóa</button>
      </td>
    </tr>`;
  }).join('');
  _updateSelUI();
}

// ─── Selection / rubber-band / context menu (mirror Mode 2) ───
function toggleRowSel(id, checked) {
  if (checked) _selected.add(id); else _selected.delete(id);
  const tr = document.querySelector(`tr.pp-row[data-sess-id="${id}"]`);
  if (tr) tr.classList.toggle('sel', checked);
  _updateSelUI();
}

function toggleSelectAll(checked) {
  if (checked) _sessions.forEach(s => _selected.add(s.id));
  else _selected.clear();
  renderSessions();
}

function clearSelection() {
  _selected.clear();
  document.querySelectorAll('tr.pp-row.sel').forEach(tr => tr.classList.remove('sel'));
  document.querySelectorAll('input.row-check:checked').forEach(cb => cb.checked = false);
  const h = document.querySelector('input.head-check');
  if (h) h.checked = false;
  _updateSelUI();
}

function _updateSelUI() {
  const n = _selected.size;
  const h = document.querySelector('input.head-check');
  if (h) {
    const total = _sessions.length;
    h.checked = n > 0 && n === total;
    h.indeterminate = n > 0 && n < total;
  }
}

function _initSelectionUX() {
  const table = document.getElementById('sessions-table');
  if (!table) return;
  const tbody = table.querySelector('tbody');

  // Rubber-band drag + single-click row toggle.
  // Phân biệt drag vs click qua threshold 5px:
  //  - mousemove < 5px → coi như click row → toggle selection row đó
  //  - mousemove ≥ 5px → init band, drag select
  let startX = 0, startY = 0, additive = false;
  let band = null, dragStarted = false;
  let pendingRowId = null;
  const interactiveSel = 'button, input, select, textarea, label, a';
  const DRAG_THRESHOLD = 5;

  tbody.addEventListener('mousedown', (e) => {
    if (e.button !== 0) return;
    if (e.target.closest(interactiveSel)) return;
    additive = e.ctrlKey || e.metaKey || e.shiftKey;
    startX = e.clientX; startY = e.clientY;
    dragStarted = false;
    const tr = e.target.closest('tr.pp-row[data-sess-id]');
    pendingRowId = tr ? parseInt(tr.dataset.sessId) : null;
    e.preventDefault();
  });

  window.addEventListener('mousemove', (e) => {
    if (startX === 0 && startY === 0) return;
    if (!dragStarted) {
      const dx = Math.abs(e.clientX - startX);
      const dy = Math.abs(e.clientY - startY);
      if (dx < DRAG_THRESHOLD && dy < DRAG_THRESHOLD) return;
      // Bắt đầu drag → init band + clear selection nếu !additive
      dragStarted = true;
      if (!additive) clearSelection();
      band = document.createElement('div');
      band.id = 'sel-band';
      band.style.left = startX + 'px'; band.style.top = startY + 'px';
      band.style.width = '0px'; band.style.height = '0px';
      document.body.appendChild(band);
    }
    if (!band) return;
    const x = Math.min(startX, e.clientX);
    const y = Math.min(startY, e.clientY);
    const w = Math.abs(e.clientX - startX);
    const h = Math.abs(e.clientY - startY);
    band.style.left = x + 'px'; band.style.top = y + 'px';
    band.style.width = w + 'px'; band.style.height = h + 'px';
    const rect = { left: x, top: y, right: x + w, bottom: y + h };
    document.querySelectorAll('tr.pp-row[data-sess-id]').forEach(tr => {
      const r = tr.getBoundingClientRect();
      const intersect = !(r.right < rect.left || r.left > rect.right || r.bottom < rect.top || r.top > rect.bottom);
      const id = parseInt(tr.dataset.sessId);
      if (intersect) _selected.add(id);
      else if (!additive) _selected.delete(id);
      tr.classList.toggle('sel', _selected.has(id));
      const cb = tr.querySelector('input.row-check');
      if (cb) cb.checked = _selected.has(id);
    });
    _updateSelUI();
  });

  window.addEventListener('mouseup', () => {
    if (band) {
      // Drag commit
      band.remove(); band = null;
    } else if (pendingRowId !== null) {
      // Click row → TOGGLE (giữ selection khác — multi-select bằng click)
      if (_selected.has(pendingRowId)) _selected.delete(pendingRowId);
      else _selected.add(pendingRowId);
      // Re-render sel state
      document.querySelectorAll('tr.pp-row[data-sess-id]').forEach(tr => {
        const id = parseInt(tr.dataset.sessId);
        const sel = _selected.has(id);
        tr.classList.toggle('sel', sel);
        const cb = tr.querySelector('input.row-check');
        if (cb) cb.checked = sel;
      });
      _updateSelUI();
    } else if (startX !== 0 && !dragStarted) {
      // mousedown trên vùng tbody trống + mouseup không drag → clear all
      clearSelection();
    }
    startX = 0; startY = 0; pendingRowId = null; dragStarted = false;
  });

  // Context menu (right-click)
  tbody.addEventListener('contextmenu', (e) => {
    const tr = e.target.closest('tr.pp-row[data-sess-id]');
    if (!tr) return;
    e.preventDefault();
    const id = parseInt(tr.dataset.sessId);
    if (!_selected.has(id)) {
      // Right-click vào row chưa chọn → select chỉ row này
      clearSelection();
      _selected.add(id);
      tr.classList.add('sel');
      const cb = tr.querySelector('input.row-check');
      if (cb) cb.checked = true;
      _updateSelUI();
    }
    _showCtxMenu(e.clientX, e.clientY);
  });
}

let _ctxEl = null;
function _showCtxMenu(x, y) {
  const ids = [..._selected];
  if (!ids.length) return;
  const sel = _sessions.filter(s => _selected.has(s.id));
  const runCount = sel.filter(s => s.proxy_status === 'running').length;
  const stopCount = sel.length - runCount;
  const n = sel.length;

  if (!_ctxEl) {
    _ctxEl = document.createElement('div');
    _ctxEl.className = 'ctx-menu';
    document.body.appendChild(_ctxEl);
    document.addEventListener('click', (ev) => {
      if (_ctxEl && !_ctxEl.contains(ev.target)) _ctxEl.style.display = 'none';
    });
    document.addEventListener('keydown', (ev) => {
      if (ev.key === 'Escape' && _ctxEl) _ctxEl.style.display = 'none';
    });
  }
  _ctxEl.innerHTML = `
    <div class="ctx-header">Đã chọn ${n} session</div>
    <div class="ctx-item${stopCount > 0 ? '' : ' disabled'}" data-act="enable-on">
      <span class="ctx-icon">▶</span><span style="flex:1">Bật proxy${stopCount > 0 ? ` (${stopCount})` : ''}</span>
    </div>
    <div class="ctx-item${runCount > 0 ? '' : ' disabled'}" data-act="enable-off">
      <span class="ctx-icon">■</span><span style="flex:1">Tắt proxy${runCount > 0 ? ` (${runCount})` : ''}</span>
    </div>
    <div class="ctx-sep"></div>
    <div class="ctx-item" data-act="rotate">
      <span class="ctx-icon">↻</span><span style="flex:1">Đổi IP (chạy song song)</span>
    </div>
    <div class="ctx-sep"></div>
    <div class="ctx-item" data-act="copy-default-pub">
      <span class="ctx-icon">⎘</span><span style="flex:1">Sao chép tài khoản <b>mặc định</b> (IP công cộng)</span>
    </div>
    <div class="ctx-item" data-act="copy-default-local">
      <span class="ctx-icon">⎘</span><span style="flex:1">Sao chép tài khoản <b>mặc định</b> (IP nội bộ)</span>
    </div>
    <div class="ctx-sep"></div>
    <div class="ctx-item" data-act="copy-all-pub">
      <span class="ctx-icon">⎘</span><span style="flex:1">Sao chép <b>TẤT CẢ</b> tài khoản (IP công cộng)</span>
    </div>
    <div class="ctx-item" data-act="copy-all-local">
      <span class="ctx-icon">⎘</span><span style="flex:1">Sao chép <b>TẤT CẢ</b> tài khoản (IP nội bộ)</span>
    </div>
    <div class="ctx-sep"></div>
    <div class="ctx-item danger" data-act="delete">
      <span class="ctx-icon">✕</span><span style="flex:1">Xóa ${n} session đã chọn</span>
    </div>`;
  _ctxEl.querySelectorAll('[data-act]').forEach(item => {
    item.addEventListener('click', async () => {
      _ctxEl.style.display = 'none';
      await bulkAction(item.dataset.act);
    });
  });
  _ctxEl.style.display = 'block';
  // Clamp vào viewport
  _ctxEl.style.left = '0px'; _ctxEl.style.top = '0px';
  const mw = _ctxEl.offsetWidth, mh = _ctxEl.offsetHeight;
  _ctxEl.style.left = Math.min(x, window.innerWidth - mw - 8) + 'px';
  _ctxEl.style.top = Math.min(y, window.innerHeight - mh - 8) + 'px';
}

async function bulkAction(act) {
  const ids = [..._selected];
  if (!ids.length) return;
  switch (act) {
    case 'enable-on':
    case 'enable-off': {
      const want = act === 'enable-on';
      const targets = _sessions.filter(s => _selected.has(s.id) && (s.proxy_status === (want ? 'stopped' : 'running')));
      if (!targets.length) { Toast.info(`Đã ${want ? 'bật' : 'tắt'} hết rồi`); return; }
      Toast.info(`Đang ${want ? 'bật' : 'tắt'} proxy cho ${targets.length} session...`);
      const results = await Promise.allSettled(targets.map(s => Api.setSessionEnabled(s.id, want)));
      const ok = results.filter(r => r.status === 'fulfilled').length;
      Toast.success(`Đã ${want ? 'bật' : 'tắt'} ${ok}/${targets.length}`);
      loadSessions();
      return;
    }
    case 'rotate': {
      Toast.info(`Đang đổi IP cho ${ids.length} session...`);
      try {
        const r = await Api.rotateBatch(ids, 5);
        const okN = r.filter(x => !x.error).length;
        Toast.success(`Đổi IP xong: ${okN}/${ids.length} thành công`);
      } catch (e) { Toast.error(e.message); }
      loadSessions();
      return;
    }
    case 'copy-all-pub':       return copyCredsByType('public', ids, false);
    case 'copy-all-local':     return copyCredsByType('local',  ids, false);
    case 'copy-default-pub':   return copyCredsByType('public', ids, true);
    case 'copy-default-local': return copyCredsByType('local',  ids, true);
    case 'delete': {
      const okDel = await Dialog.confirm(
        `Xóa <b>${ids.length}</b> phiên đã chọn?\n\nHành động không thể hoàn tác.`,
        { title: 'Xác nhận xóa nhiều phiên', okText: `Xóa ${ids.length} phiên`, danger: true }
      );
      if (!okDel) return;
      Toast.info(`Đang xóa ${ids.length} session...`);
      const results = await Promise.allSettled(ids.map(id => Api.deleteSession(id)));
      const okN = results.filter(r => r.status === 'fulfilled').length;
      _selected.clear();
      Toast.success(`Đã xóa ${okN}/${ids.length} session`);
      loadSessions();
      return;
    }
  }
}

// copyCredsByType — sao chép tài khoản proxy theo định dạng `ip:port:user:pass`.
//   type: "public" | "local" — chọn nguồn IP
//   defaultOnly: true → chỉ lấy cred label="default" (1 dòng / session)
//                false → lấy tất cả cred enabled
async function copyCredsByType(type, sessionIds, defaultOnly) {
  try {
    const all = await Api.exportProxies(type, 'json');
    const set = new Set(sessionIds);
    const filtered = all.filter(p => set.has(p.session_id));
    const lines = filtered.flatMap(p => (p.creds || [])
      .filter(c => !defaultOnly || c.label === 'default')
      .map(c => `${p.ip}:${p.port}:${c.username}:${c.password}`));
    const ipLabel = type === 'public' ? 'IP công cộng' : 'IP nội bộ';
    const credLabel = defaultOnly ? 'mặc định' : 'tất cả';
    if (!lines.length) { Toast.info(`Không có tài khoản nào để sao chép (${credLabel} · ${ipLabel})`); return; }
    const ok = await copyText(lines.join('\n'));
    if (ok) Toast.success(`Đã sao chép ${lines.length} dòng · ${filtered.length} session · ${credLabel} · ${ipLabel}`);
    else Toast.error('Trình duyệt từ chối sao chép');
  } catch (e) { Toast.error('Lỗi sao chép: ' + e.message); }
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
      info.innerHTML = `Tài khoản ISP: <span class="mono">${escapeHTML(line.username)}</span> / <span class="mono">••••</span>`;
      info.style.color = '#86efac';
    } else {
      info.innerHTML = '⚠ Line này CHƯA có tài khoản ISP — sửa line trước, nếu không session sẽ không quay số được.';
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
      Toast.error('Chế độ tự nhập cần điền cả tên đăng nhập và mật khẩu');
      return;
    }
  }
  try {
    if (count === 1) {
      const r = await Api.createSession(lineId, { proxy_auth: proxyAuth });
      Toast.success(`Đã tạo session ${r.session.id} (${r.session.status})`);
    } else {
      Toast.info(`Đang tạo ${count} session...`);
      const r = await Api.bulkCreateSessions(lineId, { count, proxy_auth: proxyAuth });
      const ok = r.filter(x => x.status === 'connected').length;
      const err = r.filter(x => x.status === 'error').length;
      Toast.success(`Đã tạo: ${ok} thành công · ${err} lỗi / ${r.length}`);
      if (err > 0) {
        const errMsgs = [...new Set(r.filter(x => x.error).map(x => x.error))];
        errMsgs.slice(0, 2).forEach(m => Toast.info('Lý do: ' + m));
      }
    }
    closeModal('create-sess-modal');
    loadSessions();
  } catch (e) { Toast.error('Lỗi tạo session: ' + e.message); }
}

// ─── Session actions ───
async function rotateSession(id) {
  try {
    Toast.info('Đang đổi IP session ' + id + '...');
    const r = await Api.rotateSession(id);
    Toast.success(`Đổi IP xong: ${r.old_ip || '—'} → ${r.new_ip || '—'}${r.same_ip ? ' (cùng IP)' : ''}`);
    loadSessions();
  } catch (e) { Toast.error('Lỗi đổi IP: ' + e.message); }
}

async function toggleEnabled(id, currentStatus) {
  try {
    const enabled = currentStatus !== 'running';
    await Api.setSessionEnabled(id, enabled);
    Toast.success(enabled ? 'Đã bật proxy' : 'Đã tắt proxy');
    loadSessions();
  } catch (e) { Toast.error(e.message); }
}

async function deleteSession(id) {
  const ok = await Dialog.confirm(
    `Xóa phiên <b>#${id}</b>?\n\nHành động không thể hoàn tác.`,
    { title: 'Xác nhận xóa phiên', okText: 'Xóa', danger: true }
  );
  if (!ok) return;
  try {
    await Api.deleteSession(id);
    Toast.success('Đã xóa session');
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
    </tr>`).join('') || '<tr><td colspan="6" class="muted">(chưa có tài khoản — proxy đang mở, không cần đăng nhập)</td></tr>';
  } catch (e) { Toast.error(e.message); }
}

async function submitCred() {
  const data = {
    label: document.getElementById('cm-label').value.trim(),
    username: document.getElementById('cm-user').value.trim(),
    password: document.getElementById('cm-pass').value.trim(),
  };
  if (!data.username || !data.password) { Toast.error('Cần điền cả tên đăng nhập và mật khẩu'); return; }
  try {
    await Api.createCred(_curPid, data);
    document.getElementById('cm-label').value = '';
    document.getElementById('cm-user').value = '';
    document.getElementById('cm-pass').value = '';
    loadCreds();
    Toast.success('Đã thêm tài khoản');
  } catch (e) { Toast.error(e.message); }
}

async function submitBulkCred() {
  const count = parseInt(document.getElementById('cm-bulk-count').value) || 0;
  const prefix = document.getElementById('cm-bulk-prefix').value.trim() || 'u';
  if (count < 1 || count > 200) { Toast.error('Số lượng phải từ 1 đến 200'); return; }
  try {
    await Api.bulkCreds(_curPid, { count, prefix });
    loadCreds();
    Toast.success(`Đã sinh ${count} tài khoản`);
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
  const ok = await copyText(text);
  if (ok) Toast.success('Copied ' + text.split('\n').filter(Boolean).length + ' dòng');
  else Toast.error('Browser từ chối copy');
}
