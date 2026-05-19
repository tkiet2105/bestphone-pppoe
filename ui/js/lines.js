if (!ensureAuth()) {} else {
  renderNav('lines');
  loadLines();
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
        <td>${r.use_macvlan ? '✓' : '—'}</td>
        <td>${r.max_sessions}</td>
        <td>${r.session_count}</td>
        <td class="actions">
          <a href="/sessions.html?line=${r.id}">Sessions</a>
          &nbsp;|&nbsp;
          <a href="#" onclick="event.preventDefault();deleteLine(${r.id},'${escapeHTML(r.name)}')">Xóa</a>
        </td>
      </tr>
    `).join('') || '<tr><td colspan="7" class="muted">Chưa có line. Bấm "+ Tạo Line" để bắt đầu.</td></tr>';
  } catch (e) {
    Toast.error('Load lines: ' + e.message);
  }
}

function openCreateLine() {
  document.getElementById('create-line-modal').classList.add('open');
}

function closeModal(id) {
  document.getElementById(id).classList.remove('open');
}

async function submitCreateLine() {
  const data = {
    name: document.getElementById('nl-name').value.trim(),
    iface: document.getElementById('nl-iface').value.trim(),
    use_macvlan: document.getElementById('nl-macvlan').value === 'true',
    max_sessions: parseInt(document.getElementById('nl-max').value) || 8,
  };
  if (!data.name || !data.iface) { Toast.error('Tên + iface bắt buộc'); return; }
  try {
    await Api.createLine(data);
    Toast.success('Tạo line OK');
    closeModal('create-line-modal');
    loadLines();
  } catch (e) {
    Toast.error('Tạo line: ' + e.message);
  }
}

async function deleteLine(id, name) {
  if (!confirm(`Xóa line "${name}" và TẤT CẢ sessions/proxies bên dưới?`)) return;
  try {
    await Api.deleteLine(id);
    Toast.success('Đã xóa');
    loadLines();
  } catch (e) {
    Toast.error('Xóa: ' + e.message);
  }
}
