if (!ensureAuth()) {} else {
  renderNav('logs');
  init();
}

let _autoTimer = null;

function init() {
  // ?filter=keyword trên URL → tự điền ô lọc
  const urlFilter = new URLSearchParams(location.search).get('filter');
  if (urlFilter) document.getElementById('lg-filter').value = urlFilter;

  ['lg-mode','lg-source','lg-since','lg-lines','lg-filter'].forEach(id => {
    document.getElementById(id).addEventListener('change', () => {
      if (id === 'lg-mode') updateModeUI();
      refresh();
    });
  });
  document.getElementById('lg-filter').addEventListener('keydown', e => { if (e.key === 'Enter') refresh(); });
  document.getElementById('lg-auto').addEventListener('change', toggleAuto);
  updateModeUI();
  refresh();
}

function updateModeUI() {
  const mode = document.getElementById('lg-mode').value;
  document.getElementById('events-wrap').style.display = mode === 'simple' ? '' : 'none';
  document.getElementById('log-view').style.display = mode === 'raw' ? '' : 'none';
}

async function refresh() {
  const params = {
    source: document.getElementById('lg-source').value,
    lines:  document.getElementById('lg-lines').value,
    since:  document.getElementById('lg-since').value,
  };
  const filter = document.getElementById('lg-filter').value.trim();
  if (filter) params.filter = filter;

  try {
    const text = await Api.getLogs(params);
    const mode = document.getElementById('lg-mode').value;
    if (mode === 'simple') renderSimple(text || '');
    else renderRaw(text || '(không có dữ liệu)');
    renderStats(text || '');
  } catch (e) {
    document.getElementById('log-view').textContent = 'Lỗi tải nhật ký: ' + e.message;
    document.getElementById('events-list').textContent = 'Lỗi tải nhật ký: ' + e.message;
  }
}

// ───────── Chế độ thô ─────────
function renderRaw(text) {
  const view = document.getElementById('log-view');
  const html = text
    .split('\n')
    .map(escapeHTML)
    .map(ln => {
      if (/AuthAck|authentication succeeded/i.test(ln)) return `<span class="l-auth-ack">${ln}</span>`;
      if (/AuthNak|authentication failed/i.test(ln)) return `<span class="l-auth-nak">${ln}</span>`;
      if (/PAD[IORST]\s|Send PPPOE|Recv PPPOE/i.test(ln)) return `<span class="l-pad">${ln}</span>`;
      if (/error|fail|killed|timeout/i.test(ln)) return `<span class="l-err">${ln}</span>`;
      return ln;
    })
    .join('\n');
  view.innerHTML = html;
  if (document.getElementById('lg-bottom').checked) view.scrollTop = view.scrollHeight;
}

// ───────── Thống kê chung ─────────
function renderStats(text) {
  const stat = document.getElementById('lg-stats');
  const lines = text.split('\n').filter(Boolean).length;
  const ack  = (text.match(/AuthAck/g) || []).length;
  const nak  = (text.match(/AuthNak/g) || []).length;
  const padi = (text.match(/Send PPPOE Discovery V1T1 PADI/g) || []).length;
  const pado = (text.match(/Recv PPPOE Discovery V1T1 PADO/g) || []).length;
  stat.innerHTML = `<span class="log-stat">${lines} dòng</span>`
    + (padi ? `<span class="log-stat">Gửi tìm ISP ${padi}</span>` : '')
    + (pado ? `<span class="log-stat">ISP phản hồi ${pado}</span>` : '')
    + (ack ? `<span class="log-stat" style="background:#16a34a;color:#fff">Đăng nhập OK ${ack}</span>` : '')
    + (nak ? `<span class="log-stat" style="background:#dc2626;color:#fff">Sai tài khoản ${nak}</span>` : '');
}

// ───────── Chế độ ĐƠN GIẢN — parser sự kiện Việt hóa ─────────
// Mỗi dòng journalctl được parse thành 1 event {time, ppp, tag, klass, msg}.
// Các dòng không nhận diện được → skip (giảm noise). Người cần đầy đủ chuyển sang chế độ "Chi tiết".
function renderSimple(text) {
  const list = document.getElementById('events-list');
  if (!text.trim()) { list.innerHTML = '<div class="muted" style="padding:16px">(Không có dòng nhật ký nào trong khoảng thời gian đã chọn)</div>'; return; }

  const events = [];
  for (const raw of text.split('\n')) {
    if (!raw.trim()) continue;
    const ev = parseLogLine(raw);
    if (ev) events.push(ev);
  }
  if (!events.length) {
    list.innerHTML = '<div class="muted" style="padding:16px">' +
      'Có nhật ký nhưng không nhận diện được sự kiện PPPoE/backend nào. ' +
      'Chuyển sang chế độ <b>Chi tiết</b> để xem nguyên văn.' +
      '</div>';
    return;
  }

  list.innerHTML = events.map(ev => `<div class="ev-row">
    <span class="ev-time">${escapeHTML(ev.time)}</span>
    <span class="ev-tag ${ev.klass}">${escapeHTML(ev.tag)}</span>
    <span class="ev-msg">${ev.ppp ? `<span class="ev-pp">${escapeHTML(ev.ppp)}</span> ` : ''}${ev.msg}</span>
  </div>`).join('');

  if (document.getElementById('lg-bottom').checked) {
    const wrap = document.getElementById('events-wrap');
    wrap.scrollTop = wrap.scrollHeight;
  }
}

// parseLogLine — chuyển 1 dòng journalctl thành {time, ppp, tag, klass, msg} hoặc null.
//
// Mẫu journalctl short-iso:
//   2026-05-19T08:29:26+0700 debian8 pppd[123]: msg...
//   2026-05-19T08:29:26+0700 debian8 bestphone-pppoe[456]: [pppd] ...
//
// Một số process pppd hệ thống dùng tag `pppd[<pid>]:`, một số gán identifier
// `pppd` riêng. Chúng tôi parse cả 2.
function parseLogLine(raw) {
  // Tách timestamp + remainder
  const m = raw.match(/^(\S+)\s+\S+\s+(?:pppd|bestphone-pppoe)(?:\[\d+\])?:\s*(.*)$/);
  if (!m) return null;
  const ts = m[1];
  const rest = m[2];
  // time HH:MM:SS từ ISO timestamp
  const tm = ts.match(/T(\d{2}:\d{2}:\d{2})/);
  const time = tm ? tm[1] : ts.slice(-8);

  // pppN unit nếu nhắc tới
  const pppMatch = rest.match(/\bppp(\d+)\b/);
  const ppp = pppMatch ? 'ppp' + pppMatch[1] : '';

  // Bộ rule nhận diện sự kiện. Thứ tự matter — pattern cụ thể trước, fallback sau.
  // klass điều khiển màu tag.
  const rules = [
    // PPPoE discovery
    { re: /Send PPPOE Discovery .*PADI/i,           tag: 'Tìm ISP',     klass: 'net',  msg: 'Đang tìm máy chủ ISP qua cổng mạng…' },
    { re: /Recv PPPOE Discovery .*PADO/i,           tag: 'ISP nhận',    klass: 'net',  msg: 'Máy chủ ISP đã phản hồi — dây ISP cắm đúng cổng' },
    { re: /Send PPPOE Discovery .*PADR/i,           tag: 'Yêu cầu',     klass: 'net',  msg: 'Gửi yêu cầu mở phiên kết nối (PADR)' },
    { re: /Recv PPPOE Discovery .*PADS/i,           tag: 'Chấp nhận',   klass: 'net',  msg: 'ISP chấp nhận mở phiên (PADS)' },
    { re: /Send PPPOE Discovery .*PADT/i,           tag: 'Đóng phiên',  klass: 'info', msg: 'Báo cho ISP đóng phiên PPPoE (PADT)' },
    { re: /Recv PPPOE Discovery .*PADT/i,           tag: 'ISP đóng',    klass: 'warn', msg: 'ISP yêu cầu đóng phiên PPPoE (PADT)' },
    { re: /Timeout waiting for PADO/i,              tag: 'Không thấy ISP', klass: 'err',  msg: 'Chờ ISP phản hồi PADO quá lâu — dây ISP sai cổng hoặc rớt link' },
    { re: /Timeout waiting for PADS/i,              tag: 'Lỗi mở phiên', klass: 'err',  msg: 'Chờ ISP chấp nhận (PADS) quá lâu' },
    { re: /No response to (\d+) echo-requests/i,    tag: 'Mất kết nối', klass: 'warn', msg: 'Không nhận phản hồi echo từ ISP — kết nối có thể đã rớt' },

    // PAP / CHAP authentication
    { re: /sent \[PAP AuthReq.*user="([^"]+)"/i,    tag: 'Đăng nhập',   klass: 'dial', msgFn: m => `Đang gửi tên đăng nhập <b>${escapeHTML(m[1])}</b> + mật khẩu cho ISP` },
    { re: /rcvd \[PAP AuthAck/i,                    tag: 'OK',          klass: 'ok',   msg: '✓ ISP chấp nhận — đăng nhập thành công' },
    { re: /rcvd \[PAP AuthNak/i,                    tag: 'Sai tài khoản', klass: 'err', msg: '✗ ISP từ chối — sai tên đăng nhập hoặc mật khẩu (không tự thử lại để tránh khóa tài khoản)' },
    { re: /CHAP authentication succeeded/i,         tag: 'OK',          klass: 'ok',   msg: '✓ Xác thực CHAP thành công' },
    { re: /CHAP authentication failed/i,            tag: 'Sai tài khoản', klass: 'err', msg: '✗ Xác thực CHAP thất bại' },
    { re: /authentication succeeded/i,              tag: 'OK',          klass: 'ok',   msg: '✓ Xác thực thành công' },
    { re: /authentication failed/i,                 tag: 'Sai tài khoản', klass: 'err', msg: '✗ Xác thực thất bại' },

    // IP layer / IPCP
    { re: /local\s+IP address\s+([\d.]+)/i,         tag: 'IP nội bộ',   klass: 'info', msgFn: m => `Đã nhận IP nội bộ trên đầu cuối: <b>${escapeHTML(m[1])}</b>` },
    { re: /remote\s+IP address\s+([\d.]+)/i,        tag: 'Public IP',klass: 'ok',   msgFn: m => `Public IP do ISP cấp: <b>${escapeHTML(m[1])}</b>` },
    { re: /primary\s+DNS address\s+([\d.]+)/i,      tag: 'DNS',         klass: 'info', msgFn: m => `DNS chính từ ISP: <b>${escapeHTML(m[1])}</b>` },
    { re: /secondary\s+DNS address\s+([\d.]+)/i,    tag: 'DNS',         klass: 'info', msgFn: m => `DNS phụ từ ISP: <b>${escapeHTML(m[1])}</b>` },

    // Lifecycle
    { re: /Using interface (ppp\d+)/i,              tag: 'Cấp ppp',     klass: 'info', msgFn: m => `Hệ thống cấp interface <b>${escapeHTML(m[1])}</b> cho phiên này` },
    { re: /Connect:\s+(ppp\d+)\s+<-->/i,            tag: 'Bắt đầu',     klass: 'info', msgFn: m => `Bắt đầu quay số trên <b>${escapeHTML(m[1])}</b>` },
    { re: /Connection terminated/i,                 tag: 'Ngắt',        klass: 'warn', msg: 'Kết nối PPPoE đã ngắt' },
    { re: /Modem hangup/i,                          tag: 'Treo máy',    klass: 'warn', msg: 'Modem/ISP treo phiên kết nối' },
    { re: /Hangup \(SIGTERM\)/i,                    tag: 'Ngắt thủ công',klass: 'info', msg: 'Người dùng/hệ thống yêu cầu ngắt (đổi IP hoặc xóa session)' },
    { re: /Terminating on signal/i,                 tag: 'Tắt',         klass: 'info', msg: 'pppd nhận tín hiệu kết thúc' },
    { re: /Exit\.?$/i,                              tag: 'Kết thúc',    klass: 'info', msg: 'pppd đã thoát' },
    { re: /pppd \d+\.\d+\.\d+ started/i,            tag: 'Khởi động',   klass: 'info', msg: 'pppd khởi động' },

    // Errors generic
    { re: /Plugin .* loaded/i,                      tag: 'Plugin',      klass: 'info', msgFn: m => 'pppd load plugin: ' + escapeHTML(m[0].split(' ').slice(1).join(' ')) },
    { re: /Couldn'?t .+/i,                          tag: 'Lỗi',         klass: 'err',  msgFn: m => 'Lỗi pppd: ' + escapeHTML(m[0]) },
    { re: /error|fail/i,                            tag: 'Lỗi',         klass: 'err',  msgFn: m => escapeHTML(m.input || m[0]) },
  ];

  for (const r of rules) {
    const mm = rest.match(r.re);
    if (!mm) continue;
    const msg = r.msgFn ? r.msgFn(mm) : r.msg;
    return { time, ppp, tag: r.tag, klass: r.klass, msg };
  }
  return null;
}

function toggleAuto() {
  if (_autoTimer) { clearInterval(_autoTimer); _autoTimer = null; }
  if (document.getElementById('lg-auto').checked) {
    _autoTimer = setInterval(refresh, 5000);
  }
}

async function copyLog() {
  const mode = document.getElementById('lg-mode').value;
  let text;
  if (mode === 'raw') {
    text = document.getElementById('log-view').textContent;
  } else {
    // Sao chép sự kiện đã việt hóa, mỗi dòng: "HH:MM:SS [tag] pppN — msg"
    text = [...document.querySelectorAll('#events-list .ev-row')].map(r => {
      const t   = r.querySelector('.ev-time')?.textContent || '';
      const tag = r.querySelector('.ev-tag')?.textContent  || '';
      const msg = r.querySelector('.ev-msg')?.textContent  || '';
      return `${t} [${tag}] ${msg}`;
    }).join('\n');
  }
  const ok = await copyText(text);
  if (ok) Toast.success(`Đã sao chép ${text.split('\n').filter(Boolean).length} dòng`);
  else Toast.error('Trình duyệt từ chối sao chép');
}
