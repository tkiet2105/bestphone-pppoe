if (!ensureAuth()) {} else {
  renderNav('rules');
  init();
}

let _sessions = [];

async function init() {
  await Promise.all([loadGlobal(), loadSessionPicker()]);
}

async function loadGlobal() {
  try {
    const rs = await Api.listRules({ scope: 'global' });
    document.getElementById('g-count').textContent = `(${rs.length})`;
    document.querySelector('#g-table tbody').innerHTML = renderRows(rs);
  } catch (e) { Toast.error('Load global: ' + e.message); }
}

async function loadSessionPicker() {
  try {
    _sessions = await Api.listSessions();
    const sel = document.getElementById('s-pick');
    sel.innerHTML = '<option value="">— chọn session —</option>' +
      _sessions.map(s => `<option value="${s.id}">#${s.id} — ${escapeHTML(s.username)} · ${escapeHTML(s.status)} · port ${s.proxy_port || '?'}</option>`).join('');
  } catch (e) { Toast.error('Load sessions: ' + e.message); }
}

async function loadSessionRules() {
  const sid = document.getElementById('s-pick').value;
  const btn = document.getElementById('s-add-btn');
  if (!sid) {
    btn.disabled = true;
    document.querySelector('#s-table tbody').innerHTML = '<tr><td colspan="6" class="muted">Chọn session để xem rules.</td></tr>';
    return;
  }
  btn.disabled = false;
  try {
    const rs = await Api.listRules({ scope: 'session', session_id: sid });
    document.querySelector('#s-table tbody').innerHTML = renderRows(rs) || '<tr><td colspan="6" class="muted">(empty)</td></tr>';
  } catch (e) { Toast.error('Load session rules: ' + e.message); }
}

function renderRows(rs) {
  return rs.map(r => `
    <tr>
      <td>${r.id}</td>
      <td><span class="badge ${r.action === 'deny' ? 'error' : 'connected'}">${escapeHTML(r.action)}</span></td>
      <td>${escapeHTML(r.kind)}</td>
      <td class="mono">${escapeHTML(r.value)}</td>
      <td class="muted small">${escapeHTML(r.note || '')}</td>
      <td class="actions">
        <button class="small secondary" onclick="toggleAction(${r.id}, '${r.action}')">Flip</button>
        <button class="small danger" onclick="deleteRule(${r.id}, '${r.scope}')">Xóa</button>
      </td>
    </tr>`).join('');
}

function openCreateRule(scope) {
  document.getElementById('rm-scope').value = scope;
  if (scope === 'session') {
    const sid = document.getElementById('s-pick').value;
    if (!sid) { Toast.error('Chọn session trước'); return; }
    document.getElementById('rm-session-id').value = sid;
    document.getElementById('rm-title').textContent = `Tạo rule cho session #${sid}`;
  } else {
    document.getElementById('rm-session-id').value = '';
    document.getElementById('rm-title').textContent = 'Tạo rule global';
  }
  document.getElementById('rm-action').value = 'deny';
  document.getElementById('rm-kind').value = 'domain';
  document.getElementById('rm-value').value = '';
  document.getElementById('rm-note').value = '';
  updateRulePlaceholder();
  document.getElementById('rule-modal').classList.add('open');
}

function updateRulePlaceholder() {
  const k = document.getElementById('rm-kind').value;
  document.getElementById('rm-value').placeholder = k === 'domain'
    ? 'example.com hoặc *.example.com'
    : '1.2.3.4 hoặc 10.0.0.0/8';
}

async function submitRule() {
  const data = {
    scope: document.getElementById('rm-scope').value,
    kind: document.getElementById('rm-kind').value,
    action: document.getElementById('rm-action').value,
    value: document.getElementById('rm-value').value.trim(),
    note: document.getElementById('rm-note').value.trim(),
  };
  if (data.scope === 'session') {
    data.session_id = parseInt(document.getElementById('rm-session-id').value);
  }
  if (!data.value) { Toast.error('Value bắt buộc'); return; }
  try {
    await Api.createRule(data);
    Toast.success('Đã tạo rule');
    closeModal('rule-modal');
    refresh(data.scope);
  } catch (e) { Toast.error('Tạo: ' + e.message); }
}

async function toggleAction(id, current) {
  const next = current === 'allow' ? 'deny' : 'allow';
  if (!confirm(`Đổi action rule ${id}: ${current} → ${next}?`)) return;
  try {
    await Api.updateRule(id, { action: next });
    Toast.success('Đã đổi action');
    refreshAll();
  } catch (e) { Toast.error(e.message); }
}

async function deleteRule(id, scope) {
  if (!confirm(`Xóa rule ${id}?`)) return;
  try {
    await Api.deleteRule(id);
    Toast.success('Đã xóa');
    refresh(scope);
  } catch (e) { Toast.error(e.message); }
}

function refresh(scope) {
  if (scope === 'global') loadGlobal();
  else loadSessionRules();
}
function refreshAll() { loadGlobal(); loadSessionRules(); }
function closeModal(id) { document.getElementById(id).classList.remove('open'); }
