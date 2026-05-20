if (!ensureAuth()) {} else {
  renderNav('lines');
  loadLines();
}

function openCreateLine() {
  document.getElementById('create-line-modal').classList.add('open');
  loadIfacesIntoSelect();
}

function closeModal(id) { document.getElementById(id).classList.remove('open'); }

async function loadIfacesIntoSelect() {
  try {
    const list = await Api.listIfaces();
    const sel = document.getElementById('nl-iface');
    // Sort carrier+free trước
    const score = i => (i.carrier ? 0 : 2) + (i.used_by_line ? 1 : 0);
    list.sort((a, b) => score(a) - score(b) || a.name.localeCompare(b.name));
    sel.innerHTML = list.map(i => {
      const parts = [i.name];
      parts.push(i.carrier ? `Có dây ${i.speed_mbps > 0 ? i.speed_mbps + 'M' : ''}` : 'Chưa cắm dây');
      if (i.ips && i.ips.length) parts.push(i.ips.join(','));
      if (i.used_by_line) parts.push(`Đã dùng cho Line #${i.used_by_line}`);
      const disabled = !i.carrier || !!i.used_by_line;
      const prefix = !i.carrier ? '⚪' : (i.used_by_line ? '🔒' : '🟢');
      return `<option value="${escapeHTML(i.name)}" ${disabled ? 'disabled' : ''}>${prefix} ${escapeHTML(parts.join(' · '))}</option>`;
    }).join('');
    const free = list.find(i => i.carrier && !i.used_by_line);
    if (free) sel.value = free.name;
    const counts = {
      free: list.filter(i => i.carrier && !i.used_by_line).length,
      used: list.filter(i => i.used_by_line).length,
      nolink: list.filter(i => !i.carrier).length,
    };
    document.getElementById('nl-iface-hint').innerHTML =
      `🟢 ${counts.free} sẵn sàng · 🔒 ${counts.used} đã dùng · ⚪ ${counts.nolink} chưa cắm dây. ` +
      `Vào trang <a href="/sessions.html">Session</a> bấm "Quét cổng có ISP" để biết cổng nào nhận PPPoE.`;
  } catch (e) { Toast.error('Lỗi tải cổng mạng: ' + e.message); }
}

async function loadLines() {
  try {
    const rows = await Api.listLines();
    const tbody = document.querySelector('#lines-table tbody');
    tbody.innerHTML = rows.map(r => `
      <tr>
        <td>${r.id}</td>
        <td>${escapeHTML(r.name)}</td>
        <td class="mono">${escapeHTML(r.iface)}</td>
        <td class="mono small">${r.username ? escapeHTML(r.username) : '<span class="muted">(chưa nhập)</span>'}</td>
        <td>${r.use_macvlan ? '✓' : '—'}</td>
        <td>${r.max_sessions}</td>
        <td>${r.session_count}</td>
        <td class="actions">
          <a href="/sessions.html?line=${r.id}">Session</a>
          &nbsp;|&nbsp;
          <a href="#" onclick="event.preventDefault();editLine(${r.id})">Sửa</a>
          &nbsp;|&nbsp;
          <a href="#" onclick="event.preventDefault();deleteLine(${r.id},'${escapeHTML(r.name)}')">Xóa</a>
        </td>
      </tr>
    `).join('') || '<tr><td colspan="8" class="muted">Chưa có line nào. Bấm "+ Tạo line mới" để bắt đầu.</td></tr>';
  } catch (e) {
    Toast.error('Load lines: ' + e.message);
  }
}


async function submitCreateLine() {
  const data = {
    name: document.getElementById('nl-name').value.trim(),
    iface: document.getElementById('nl-iface').value.trim(),
    username: document.getElementById('nl-user').value.trim(),
    password: document.getElementById('nl-pass').value.trim(),
    use_macvlan: document.getElementById('nl-macvlan').value === 'true',
    max_sessions: parseInt(document.getElementById('nl-max').value) || 8,
  };
  if (!data.name || !data.iface) { Toast.error('Tên + cổng mạng là bắt buộc'); return; }
  if (!data.username || !data.password) {
    const ok = await Dialog.confirm(
      'Bạn chưa nhập tài khoản ISP. Các phiên của đường truyền này sẽ <b>không quay số được</b>.\n\nVẫn tạo đường truyền trống?',
      { title: 'Thiếu tài khoản ISP', okText: 'Vẫn tạo', kind: 'warn' }
    );
    if (!ok) return;
  }
  try {
    await Api.createLine(data);
    Toast.success('Đã tạo line');
    closeModal('create-line-modal');
    loadLines();
  } catch (e) {
    Toast.error('Lỗi tạo line: ' + e.message);
  }
}

async function editLine(id) {
  const lines = await Api.listLines();
  const l = lines.find(x => x.id === id);
  if (!l) { Toast.error('Không tìm thấy line'); return; }
  const newUser = await Dialog.prompt(
    `Sửa tên đăng nhập ISP cho đường truyền <b>${escapeHTML(l.name)}</b>:`,
    { title: 'Sửa tài khoản ISP', defaultValue: l.username || '', okText: 'Tiếp tục' }
  );
  if (newUser === null) return;
  const newPass = await Dialog.prompt(
    `Sửa mật khẩu ISP cho đường truyền <b>${escapeHTML(l.name)}</b> (để trống nếu không đổi mật khẩu):`,
    { title: 'Sửa mật khẩu ISP', defaultValue: l.password || '', password: true, okText: 'Lưu' }
  );
  if (newPass === null) return;
  try {
    await Api.updateLine(id, { username: newUser, password: newPass });
    Toast.success('Đã cập nhật');
    loadLines();
  } catch (e) { Toast.error(e.message); }
}

async function deleteLine(id, name) {
  const ok = await Dialog.confirm(
    `Xóa đường truyền <b>"${escapeHTML(name)}"</b> và <b>TẤT CẢ</b> phiên / proxy thuộc đường truyền này?\n\nHành động không thể hoàn tác.`,
    { title: 'Xác nhận xóa đường truyền', okText: 'Xóa', danger: true }
  );
  if (!ok) return;
  try {
    await Api.deleteLine(id);
    Toast.success('Đã xóa đường truyền');
    loadLines();
  } catch (e) {
    Toast.error('Lỗi xóa: ' + e.message);
  }
}
