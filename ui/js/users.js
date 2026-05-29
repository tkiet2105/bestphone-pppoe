if (!ensureAuth()) {} else {
  renderNav('users');
  loadStatus();
  loadUsers();
}

let _detailUser = '';

async function loadStatus() {
  try {
    const s = await Api.claimStatus();
    document.getElementById('st-sessions').textContent = s.total_connected_sessions;
    document.getElementById('st-users').textContent = s.active_users;
    document.getElementById('st-creds').textContent = s.total_claimed_creds;
    const bt = s.by_type || {};
    const labels = { static: 'Tĩnh', private: 'Private', rotating: 'Xoay' };
    const styles = {
      static:   { bg: '#155e75', border: '#0891b2', color: '#a5f3fc' },
      private:  { bg: '#581c87', border: '#9333ea', color: '#e9d5ff' },
      rotating: { bg: '#9a3412', border: '#ea580c', color: '#fed7aa' },
    };
    const tb = document.getElementById('type-breakdown');
    tb.innerHTML = ['static', 'private', 'rotating'].map(t => {
      const d = bt[t] || { sessions: 0, claimed_creds: 0, available_slots: 0 };
      const st = styles[t];
      return `<div style="border:1px solid ${st.border};background:${st.bg};border-radius:8px;padding:10px 14px;min-width:170px">
        <div style="color:${st.color};font-weight:600;font-size:13px;margin-bottom:4px">${labels[t]} (${t})</div>
        <div class="mono" style="color:#e2e8f0">Sessions: <strong>${d.sessions}</strong></div>
        <div class="mono" style="color:#e2e8f0">Đã claim: <strong>${d.claimed_creds}</strong></div>
        <div class="mono" style="color:#e2e8f0">Còn slot: <strong style="color:#86efac">${d.available_slots}</strong></div>
      </div>`;
    }).join('');
  } catch (e) { Toast.error(e.message); }
}

async function loadUsers() {
  try {
    const rows = await Api.claimUsers();
    const tbody = document.querySelector('#users-table tbody');
    if (!rows || rows.length === 0) {
      tbody.innerHTML = '<tr><td colspan="5" class="muted">Chưa có user nào claim.</td></tr>';
      return;
    }
    tbody.innerHTML = rows.map(u => {
      const exp = u.earliest_expiry
        ? `<span class="mono small">${fmtTime(u.earliest_expiry)}</span>`
        : '<span class="muted small">Vĩnh viễn</span>';
      const bt = u.by_type || {};
      const splits = `<span class="mono small">${bt.static||0}T / ${bt.private||0}P / ${bt.rotating||0}X</span>`;
      return `<tr>
        <td class="mono">${escapeHTML(u.iuser_id)}</td>
        <td>${u.cred_count}</td>
        <td>${splits}</td>
        <td>${exp}</td>
        <td class="actions">
          <button class="small" onclick="showDetail('${escapeHTML(u.iuser_id)}')">Chi tiết</button>
          <button class="small danger" onclick="releaseUserById('${escapeHTML(u.iuser_id)}')">Release</button>
        </td>
      </tr>`;
    }).join('');
  } catch (e) { Toast.error(e.message); }
}

function fmtTime(s) {
  if (!s) return '';
  try {
    const d = new Date(s);
    if (isNaN(d.getTime())) return s;
    return d.toLocaleString('vi-VN');
  } catch (_) { return s; }
}

function fmtExpiry(expiresAt) {
  if (!expiresAt) return '<span class="muted">Vĩnh viễn</span>';
  const remain = Math.round((new Date(expiresAt) - Date.now()) / 1000);
  if (remain <= 0) return '<span class="badge error">Hết hạn</span>';
  if (remain < 3600) return `<span class="badge warn">${Math.ceil(remain/60)}p</span>`;
  if (remain < 86400) return `<span class="badge">${Math.round(remain/3600)}h</span>`;
  return `<span class="badge">${Math.round(remain/86400)}d</span>`;
}

async function doClaim() {
  const iuserId = document.getElementById('cl-iuserid').value.trim();
  const count = parseInt(document.getElementById('cl-count').value) || 1;
  const ttl = parseInt(document.getElementById('cl-ttl').value) || 0;
  const sessType = document.getElementById('cl-type').value || 'rotating';
  if (!iuserId) { Toast.error('Nhập iuser_id'); return; }
  try {
    const r = await Api.claim({ iuser_id: iuserId, count, ttl, type: sessType });
    const div = document.getElementById('claim-result');
    const creds = r.credentials || [];
    if (creds.length === 0) {
      div.innerHTML = '<span class="muted">Không có creds nào.</span>';
      return;
    }
    const lines = creds.map(c => `${c.ip}:${c.port}:${c.username}:${c.password}`);
    div.innerHTML = `<pre class="export-text" style="max-height:200px;overflow:auto;font-size:12px">${escapeHTML(lines.join('\n'))}</pre>
      <button class="secondary small" onclick="copyText('${escapeHTML(lines.join('\\n'))}').then(()=>Toast.success('Đã sao chép'))">Sao chép</button>`;
    Toast.success(`Đã claim ${creds.length} creds`);
    loadStatus();
    loadUsers();
  } catch (e) { Toast.error(e.message); }
}

async function showDetail(iuserId) {
  _detailUser = iuserId;
  document.getElementById('det-uid').textContent = iuserId;
  document.getElementById('detail-card').style.display = '';
  await loadDetail();
}

function closeDetail() {
  document.getElementById('detail-card').style.display = 'none';
  _detailUser = '';
}

async function loadDetail() {
  if (!_detailUser) return;
  try {
    const r = await Api.claimUserStatus(_detailUser);
    const creds = r.credentials || [];
    const tbody = document.querySelector('#detail-table tbody');
    if (creds.length === 0) {
      tbody.innerHTML = '<tr><td colspan="8" class="muted">Không có creds.</td></tr>';
      document.getElementById('det-connstr').textContent = '';
      return;
    }
    tbody.innerHTML = creds.map(c => `<tr>
      <td>${c.cred_id}</td>
      <td>#${c.session_id}</td>
      <td>${typeof typeBadge === 'function' ? typeBadge(c.type) : escapeHTML(c.type || '')}</td>
      <td class="mono">${escapeHTML(c.ip)}:${c.port}</td>
      <td class="mono">${escapeHTML(c.username)}</td>
      <td class="mono">${escapeHTML(c.password)}</td>
      <td>${fmtExpiry(c.expires_at)}</td>
      <td class="actions">
        <button class="small" onclick="changeCred(${c.cred_id})">Đổi IP</button>
        <button class="small secondary" onclick="extendCred(${c.cred_id})" title="Gia hạn riêng cred này">⏱ Gia hạn</button>
        <button class="small danger" onclick="deleteCred(${c.cred_id})">Xóa</button>
      </td>
    </tr>`).join('');
    const lines = creds.map(c => `${c.ip}:${c.port}:${c.username}:${c.password}`);
    document.getElementById('det-connstr').textContent = lines.join('\n');
  } catch (e) { Toast.error(e.message); }
}

async function copyConnStr() {
  const text = document.getElementById('det-connstr').textContent;
  if (await copyText(text)) Toast.success('Đã sao chép');
}

async function extendCred(credId) {
  if (!_detailUser) return;
  const ttlStr = await Dialog.prompt(
    `Cộng dồn TTL (giây) cho cred <b>#${credId}</b>:\n\nVD: cred còn 1h, nhập 7200 → cred mới còn 3h (1h cũ + 2h cộng).\nCred chưa có TTL (vô thời hạn) sẽ được SET = now + ttl.`,
    { title: 'Gia hạn 1 cred (cộng dồn)', defaultValue: '7200', okText: 'Cộng dồn' }
  );
  if (ttlStr === null) return;
  const ttl = parseInt(ttlStr) || 0;
  if (ttl <= 0) { Toast.error('TTL phải > 0'); return; }
  try {
    await Api.extendCreds({ iuser_id: _detailUser, ttl, cred_ids: [credId] });
    Toast.success(`Đã cộng dồn ${ttl}s vào cred #${credId}`);
    loadDetail();
    loadStatus();
  } catch (e) { Toast.error(e.message); }
}

async function changeCred(credId) {
  if (!_detailUser) return;
  try {
    await Api.changeCreds({ iuser_id: _detailUser, cred_ids: [credId] });
    Toast.success('Đã đổi IP');
    loadDetail();
    loadStatus();
  } catch (e) { Toast.error(e.message); }
}

async function deleteCred(credId) {
  if (!_detailUser) return;
  const ok = await Dialog.confirm('Xóa credential #' + credId + '?', { danger: true });
  if (!ok) return;
  try {
    await Api.changeCreds({ iuser_id: _detailUser, cred_ids: [credId] });
    Toast.success('Đã xóa');
    loadDetail();
    loadUsers();
    loadStatus();
  } catch (e) { Toast.error(e.message); }
}

async function extendUser() {
  if (!_detailUser) return;
  const ttl = parseInt(document.getElementById('det-ttl').value) || 0;
  if (ttl <= 0) { Toast.error('TTL phải > 0'); return; }
  try {
    const r = await Api.extendCreds({ iuser_id: _detailUser, ttl });
    const n = (r.credentials || []).length;
    Toast.success(`Đã cộng dồn ${ttl}s vào ${n} cred`);
    loadDetail();
    loadUsers();
  } catch (e) { Toast.error(e.message); }
}

async function releaseUser() {
  if (!_detailUser) return;
  const ok = await Dialog.confirm(`Release tất cả creds của ${_detailUser}?`, { danger: true });
  if (!ok) return;
  try {
    const r = await Api.releaseCreds({ iuser_id: _detailUser });
    Toast.success(`Đã release ${r.released} creds`);
    closeDetail();
    loadUsers();
    loadStatus();
  } catch (e) { Toast.error(e.message); }
}

async function releaseUserById(iuserId) {
  const ok = await Dialog.confirm(`Release tất cả creds của ${iuserId}?`, { danger: true });
  if (!ok) return;
  try {
    const r = await Api.releaseCreds({ iuser_id: iuserId });
    Toast.success(`Đã release ${r.released} creds`);
    loadUsers();
    loadStatus();
  } catch (e) { Toast.error(e.message); }
}
