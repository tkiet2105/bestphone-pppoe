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

async function openCreateLine() {
  document.getElementById('create-line-modal').classList.add('open');
  const sel = document.getElementById('nl-iface');
  const hint = document.getElementById('nl-iface-hint');
  sel.innerHTML = '<option>(đang load...)</option>';
  hint.innerHTML = '';
  try {
    const ifaces = await Api.listIfaces();
    // Sort: carrier+free trước, carrier+used giữa, no-carrier cuối
    const score = i => (i.carrier ? 0 : 2) + (i.used_by_line ? 1 : 0);
    ifaces.sort((a, b) => score(a) - score(b) || a.name.localeCompare(b.name));

    const opts = ifaces.map(i => {
      const parts = [i.name];
      // Link state
      if (i.carrier) {
        parts.push(`LINK${i.speed_mbps > 0 ? ' ' + i.speed_mbps + 'M' : ''}`);
      } else {
        parts.push('NO-CARRIER');
      }
      // IPs
      if (i.ips && i.ips.length) parts.push(i.ips.join(','));
      // Line tag
      if (i.used_by_line) parts.push(`★ đang dùng Line #${i.used_by_line} (${i.used_by_name})`);
      else if (i.carrier) parts.push('— free');
      const label = parts.join(' · ');

      const disabled = !i.carrier || !!i.used_by_line;
      const prefix = !i.carrier ? '⚪' : (i.used_by_line ? '🔒' : '🟢');
      return `<option value="${escapeHTML(i.name)}" ${disabled ? 'disabled' : ''}>${prefix} ${escapeHTML(label)}</option>`;
    }).join('');

    sel.innerHTML = opts;
    // Pick first non-disabled
    const firstOk = ifaces.find(i => i.carrier && !i.used_by_line);
    if (firstOk) sel.value = firstOk.name;

    const freeCount = ifaces.filter(i => i.carrier && !i.used_by_line).length;
    const usedCount = ifaces.filter(i => i.used_by_line).length;
    const noLinkCount = ifaces.filter(i => !i.carrier).length;
    hint.innerHTML = `
      <strong>🟢 ${freeCount} sẵn sàng</strong> ·
      🔒 ${usedCount} đã có line ·
      ⚪ ${noLinkCount} không cáp.
      Chỉ NIC có LINK (carrier) và chưa gắn line khác mới chọn được.
      Để biết NIC nào có ISP PPPoE cắm → vào trang <a href="/sessions.html">Sessions</a> bấm "PADO probe".
    `;
  } catch (e) {
    Toast.error('Load ifaces: ' + e.message);
    sel.innerHTML = '<option value="">(error)</option>';
  }
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
