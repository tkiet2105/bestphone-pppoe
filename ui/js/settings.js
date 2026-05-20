if (!ensureAuth()) {} else {
  renderNav('settings');
  init();
}

async function init() {
  await loadMe();
  await loadApiTokens();
}

function fmtTime(s) {
  if (!s) return '—';
  try {
    const d = new Date(s);
    if (isNaN(d.getTime())) return s;
    return d.toLocaleString('vi-VN');
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
        <button class="small danger" onclick="deleteApiToken(${t.id})">Xóa</button>
      </td>
    </tr>`).join('');
  } catch (e) { Toast.error(e.message); }
}

async function createApiToken() {
  const label = document.getElementById('tk-label').value.trim();
  try {
    const r = await Api.createToken(label);
    // Hiển thị token đầy đủ 1 lần
    const ok = await copyText(r.token);
    if (ok) Toast.success(`Đã tạo + sao chép token vào clipboard (id=${r.id}). Dán + lưu lại ngay.`);
    alert('Token đã tạo (lưu ngay, không xem lại được):\n\n' + r.token);
    document.getElementById('tk-label').value = '';
    loadApiTokens();
  } catch (e) { Toast.error('Lỗi tạo token: ' + e.message); }
}

async function deleteApiToken(id) {
  if (!confirm(`Xóa API token #${id}? Mọi script đang dùng token này sẽ ngừng hoạt động.`)) return;
  try {
    await Api.deleteToken(id);
    Toast.success('Đã xóa');
    loadApiTokens();
  } catch (e) { Toast.error(e.message); }
}
