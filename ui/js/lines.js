if (!ensureAuth()) {} else {
  renderNav('lines');
  loadLines();
  loadStats();
  // Refresh stats mỗi 5s — light endpoint (~5ms backend, không spam DB).
  setInterval(loadStats, 5000);
}

// ─── Dashboard stats ──────────────────────────────────────
function fmtDuration(sec) {
  sec = Math.floor(sec || 0);
  if (sec < 60)   return sec + ' giây';
  if (sec < 3600) return Math.floor(sec / 60) + ' phút';
  if (sec < 86400) {
    const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60);
    return `${h}g ${m}p`;
  }
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600);
  return `${d} ngày ${h}g`;
}

function setBar(elBar, elVal, pct, valText) {
  if (elVal) elVal.textContent = valText;
  if (!elBar) return;
  const p = Math.max(0, Math.min(100, pct || 0));
  elBar.style.width = p + '%';
  elBar.classList.remove('warn', 'danger');
  if (p >= 90) elBar.classList.add('danger');
  else if (p >= 75) elBar.classList.add('warn');
}

async function loadStats() {
  try {
    const s = await Api.getStats();
    // Service version
    document.getElementById('st-service-ver').textContent = 'v' + s.service.version;

    // System gauges
    setBar(
      document.getElementById('st-cpu-bar'),
      document.getElementById('st-cpu'),
      s.system.cpu_percent,
      `${s.system.cpu_percent}%`,
    );
    setBar(
      document.getElementById('st-mem-bar'),
      document.getElementById('st-mem'),
      s.system.memory_percent,
      `${s.system.memory_used_mb}/${s.system.memory_total_mb}MB · ${s.system.memory_percent}%`,
    );
    setBar(
      document.getElementById('st-disk-bar'),
      document.getElementById('st-disk'),
      s.system.disk_percent,
      `${s.system.disk_used_gb}/${s.system.disk_total_gb}GB · ${s.system.disk_percent}%`,
    );
    document.getElementById('st-load').textContent =
      `${s.system.load_1} · ${s.system.load_5} · ${s.system.load_15}`;
    document.getElementById('st-numcpu').textContent = s.system.num_cpu + ' core';
    document.getElementById('st-svc-up').textContent = fmtDuration(s.service.uptime_seconds);
    document.getElementById('st-os-up').textContent  = fmtDuration(s.system.uptime_seconds);

    // PPPoE
    document.getElementById('st-sess-total').textContent = s.sessions.total;
    document.getElementById('st-sess-conn').textContent  = s.sessions.by_status.connected || 0;
    document.getElementById('st-sess-dial').textContent  = s.sessions.by_status.dialing   || 0;
    document.getElementById('st-sess-err').textContent   = s.sessions.by_status.error     || 0;
    document.getElementById('st-sess-down').textContent  = s.sessions.by_status.disconnected || 0;
    document.getElementById('st-lines').textContent = s.lines.total;
    document.getElementById('st-pubip').textContent = s.sessions.with_public_ip;

    // Proxy & accounts
    document.getElementById('st-proxy-running').textContent = s.proxies.running;
    document.getElementById('st-proxy-total').textContent   = s.proxies.total;
    document.getElementById('st-proxy-stop').textContent    = s.proxies.stopped;
    document.getElementById('st-creds').textContent  = s.credentials.total;
    document.getElementById('st-users').textContent  = s.auth.users;
    document.getElementById('st-apitok').textContent = s.auth.api_tokens;
    document.getElementById('st-grt').textContent    = s.system.num_goroutine;

    // Rules
    document.getElementById('st-rules-total').textContent   = s.rules.total;
    document.getElementById('st-rules-global').textContent  = s.rules.global;
    document.getElementById('st-rules-session').textContent = s.rules.session;
  } catch (e) {
    // Silent fail trên auto-refresh — không spam toast
    console.warn('Stats fetch failed:', e.message);
  }
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
    tbody.innerHTML = rows.map(r => {
      const macList = (r.custom_macs || '').split('\n').map(s => s.trim()).filter(Boolean);
      const macCell = macList.length > 0
        ? `<span class="mono small" title="${escapeHTML(macList.join('\n'))}">${macList.length} MAC</span>`
        : '<span class="muted small">auto</span>';
      return `
      <tr>
        <td>${r.id}</td>
        <td>${escapeHTML(r.name)}</td>
        <td class="mono">${escapeHTML(r.iface)}</td>
        <td class="mono small">${r.username ? escapeHTML(r.username) : '<span class="muted">(chưa nhập)</span>'}</td>
        <td>${r.use_macvlan ? '✓' : '—'}</td>
        <td>${macCell}</td>
        <td>${r.max_sessions}</td>
        <td>${r.session_count}</td>
        <td class="actions">
          <a href="/sessions.html?line=${r.id}" title="Xem các session của đường truyền này">📡 Session</a>
          &nbsp;|&nbsp;
          <a href="#" onclick="event.preventDefault();editLine(${r.id})" title="Sửa tài khoản ISP">✎ Sửa</a>
          &nbsp;|&nbsp;
          <a href="#" onclick="event.preventDefault();deleteLine(${r.id},'${escapeHTML(r.name)}')" title="Xóa đường truyền" style="color:#fca5a5">✕ Xóa</a>
        </td>
      </tr>`;
    }).join('') || '<tr><td colspan="9" class="muted">Chưa có đường truyền nào. Bấm "⊕ Tạo đường truyền mới" để bắt đầu.</td></tr>';
  } catch (e) {
    Toast.error('Lỗi tải danh sách: ' + e.message);
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
    custom_macs: document.getElementById('nl-macs').value.trim(),
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
    Toast.success('Đã tạo đường truyền');
    closeModal('create-line-modal');
    loadLines();
  } catch (e) {
    Toast.error('Lỗi tạo đường truyền: ' + e.message);
  }
}

async function editLine(id) {
  const lines = await Api.listLines();
  const l = lines.find(x => x.id === id);
  if (!l) { Toast.error('Không tìm thấy đường truyền'); return; }
  const newUser = await Dialog.prompt(
    `Sửa tên đăng nhập ISP cho đường truyền <b>${escapeHTML(l.name)}</b>:`,
    { title: 'Sửa tài khoản ISP', defaultValue: l.username || '', okText: 'Tiếp tục' }
  );
  if (newUser === null) return;
  const newPass = await Dialog.prompt(
    `Sửa mật khẩu ISP cho đường truyền <b>${escapeHTML(l.name)}</b> (để trống nếu không đổi mật khẩu):`,
    { title: 'Sửa mật khẩu ISP', defaultValue: l.password || '', password: true, okText: 'Tiếp tục' }
  );
  if (newPass === null) return;

  // MAC pool — dùng Dialog.show với textarea (Dialog.prompt chỉ là input).
  let macsValue = l.custom_macs || '';
  const macAction = await Dialog.show({
    title: 'Danh sách MAC tự cấp',
    bodyHTML: `<label style="display:block;margin-bottom:4px">1 MAC/dòng, format <span class="mono">aa:bb:cc:dd:ee:ff</span>. Để trống = auto random.</label>
      <textarea id="el-macs" rows="6" style="width:100%;font-family:monospace;font-size:12px;background:#0f172a;color:#e2e8f0;border:1px solid #334155;border-radius:4px;padding:6px">${escapeHTML(macsValue)}</textarea>`,
    actions: [
      { label: 'Hủy', kind: 'secondary', value: null },
      { label: 'Lưu', kind: 'primary', value: 'SAVE' },
    ],
    dismissValue: null,
    onMount: (bg) => {
      const ta = bg.querySelector('#el-macs');
      if (ta) {
        ta.focus();
        ta.addEventListener('input', () => { macsValue = ta.value; });
      }
    },
  });
  if (macAction === null) return;

  try {
    await Api.updateLine(id, { username: newUser, password: newPass, custom_macs: macsValue.trim() });
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
