if (!ensureAuth()) {} else {
  renderNav('settings');
  init();
}

async function init() {
  await loadSettings();
  await loadMe();
  await loadApiTokens();
}

async function loadSettings() {
  try {
    const s = await Api.getSettings();
    document.getElementById('cfg-reconnect-enabled').value = s.reconnect_enabled || 'true';
    document.getElementById('cfg-reconnect-max-retries').value = s.reconnect_max_retries || '5';
    document.getElementById('cfg-reconnect-pause-minutes').value = s.reconnect_pause_minutes || '60';
  } catch (e) { Toast.error('Lỗi tải cấu hình: ' + e.message); }
}

async function saveSettings() {
  const data = {
    reconnect_enabled: document.getElementById('cfg-reconnect-enabled').value,
    reconnect_max_retries: document.getElementById('cfg-reconnect-max-retries').value,
    reconnect_pause_minutes: document.getElementById('cfg-reconnect-pause-minutes').value,
  };
  try {
    await Api.updateSettings(data);
    Toast.success('Đã lưu cấu hình');
  } catch (e) { Toast.error('Lỗi lưu: ' + e.message); }
}

function fmtTime(s) {
  if (!s) return '—';
  try {
    const d = new Date(s);
    if (isNaN(d.getTime())) return s;
    const pad = (n) => String(n).padStart(2, '0');
    return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())} ${pad(d.getDate())}/${pad(d.getMonth()+1)}/${d.getFullYear()}`;
  } catch (_) { return s; }
}

async function loadMe() {
  try {
    const u = await Api.me();
    if (u.is_api_token) {
      document.getElementById('me-username').textContent = '(đang dùng API token: ' + (u.label || '—') + ')';
      document.getElementById('me-id').textContent = '—';
      document.getElementById('me-created').textContent = '—';
      document.getElementById('me-updated').textContent = '—';
      // Disable change-pass form
      ['cp-current','cp-new','cp-new2'].forEach(id => document.getElementById(id).disabled = true);
      Toast.info('Đang đăng nhập bằng API token — không thể đổi mật khẩu user qua trang này.');
      return;
    }
    document.getElementById('me-username').textContent = u.username;
    document.getElementById('me-id').textContent = u.user_id;
    document.getElementById('me-created').textContent = fmtTime(u.created_at);
    document.getElementById('me-updated').textContent = fmtTime(u.updated_at);
  } catch (e) { Toast.error('Lỗi tải tài khoản: ' + e.message); }
}

async function submitChangePass() {
  const cur = document.getElementById('cp-current').value;
  const n1 = document.getElementById('cp-new').value;
  const n2 = document.getElementById('cp-new2').value;
  if (!cur || !n1 || !n2) { Toast.error('Vui lòng điền đủ 3 ô'); return; }
  if (n1 !== n2) { Toast.error('Mật khẩu mới và xác nhận không khớp'); return; }
  if (n1.length < 6) { Toast.error('Mật khẩu mới phải ≥ 6 ký tự'); return; }
  if (n1 === cur) { Toast.error('Mật khẩu mới trùng mật khẩu cũ'); return; }
  try {
    await Api.changePassword(cur, n1);
    Toast.success('Đã đổi mật khẩu. Các phiên khác đã bị đăng xuất.');
    document.getElementById('cp-current').value = '';
    document.getElementById('cp-new').value = '';
    document.getElementById('cp-new2').value = '';
  } catch (e) {
    Toast.error('Lỗi đổi mật khẩu: ' + e.message);
  }
}

async function loadApiTokens() {
  try {
    const rows = await Api.listTokens();
    const tbody = document.querySelector('#api-tokens-table tbody');
    if (!rows.length) {
      tbody.innerHTML = '<tr><td colspan="5" class="muted">Chưa có API token nào. Tạo bên dưới nếu cần.</td></tr>';
      return;
    }
    tbody.innerHTML = rows.map(t => `<tr>
      <td>${t.id}</td>
      <td>${escapeHTML(t.label || '(không nhãn)')}</td>
      <td class="mono">…${escapeHTML(t.last4)}</td>
      <td class="mono small">${fmtTime(t.created_at)}</td>
      <td class="actions">
        <button class="small danger" onclick="deleteApiToken(${t.id})">✕ Xóa</button>
      </td>
    </tr>`).join('');
  } catch (e) { Toast.error(e.message); }
}

async function createApiToken() {
  const label = document.getElementById('tk-label').value.trim();
  try {
    const r = await Api.createToken(label);
    // Hiển thị token đầy đủ 1 lần qua Dialog (kèm sao chép tự động)
    const copied = await copyText(r.token);
    await Dialog.show({
      title: 'API token mới đã tạo',
      bodyHTML: `<div class="dlg-msg dlg-warn">
          ⚠ Token đầy đủ chỉ hiển thị <b>1 lần duy nhất</b>. Hãy lưu lại ngay vào nơi an toàn — sau khi đóng hộp thoại này, hệ thống không thể hiển thị lại.
        </div>
        <div style="margin-top:12px">
          <div class="muted small" style="margin-bottom:4px">Nhãn:</div>
          <div class="mono">${escapeHTML(r.label || '(không nhãn)')}</div>
        </div>
        <div style="margin-top:10px">
          <div class="muted small" style="margin-bottom:4px">Token:</div>
          <textarea readonly style="width:100%;min-height:60px;font-family:'SF Mono',Monaco,monospace;font-size:12px">${escapeHTML(r.token)}</textarea>
        </div>
        ${copied ? '<div class="dlg-msg dlg-success" style="margin-top:8px">✓ Đã tự động sao chép vào clipboard.</div>' : ''}`,
      actions: [{ label: 'Sao chép lại', kind: 'secondary', value: 'copy' }, { label: 'Tôi đã lưu, đóng', kind: 'primary', value: 'close' }],
      dismissValue: 'close',
    }).then(async (v) => {
      if (v === 'copy') {
        const ok2 = await copyText(r.token);
        if (ok2) Toast.success('Đã sao chép token');
      }
    });
    document.getElementById('tk-label').value = '';
    loadApiTokens();
  } catch (e) { Toast.error('Lỗi tạo token: ' + e.message); }
}

async function deleteApiToken(id) {
  const ok = await Dialog.confirm(
    `Xóa API token #${id}?\n\nMọi script đang dùng token này sẽ ngừng hoạt động ngay lập tức.`,
    { title: 'Xác nhận xóa token', okText: 'Xóa', danger: true }
  );
  if (!ok) return;
  try {
    await Api.deleteToken(id);
    Toast.success('Đã xóa');
    loadApiTokens();
  } catch (e) { Toast.error(e.message); }
}
