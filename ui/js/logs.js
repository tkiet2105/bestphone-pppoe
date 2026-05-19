if (!ensureAuth()) {} else {
  renderNav('logs');
  init();
}

let _autoTimer = null;

function init() {
  // URL param ?filter=keyword → auto-fill filter input
  const urlFilter = new URLSearchParams(location.search).get('filter');
  if (urlFilter) document.getElementById('lg-filter').value = urlFilter;

  ['lg-source','lg-since','lg-lines','lg-filter'].forEach(id => {
    document.getElementById(id).addEventListener('change', () => refresh());
  });
  document.getElementById('lg-filter').addEventListener('keydown', e => { if (e.key === 'Enter') refresh(); });
  document.getElementById('lg-auto').addEventListener('change', toggleAuto);
  refresh();
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
    renderLog(text || '(empty)');
  } catch (e) {
    document.getElementById('log-view').textContent = 'Lỗi: ' + e.message;
  }
}

function renderLog(text) {
  const view = document.getElementById('log-view');
  // Highlight
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

  // Stats
  const stat = document.getElementById('lg-stats');
  const lines = text.split('\n').filter(Boolean).length;
  const ack = (text.match(/AuthAck/g) || []).length;
  const nak = (text.match(/AuthNak/g) || []).length;
  const padi = (text.match(/Send PPPOE Discovery V1T1 PADI/g) || []).length;
  const pado = (text.match(/Recv PPPOE Discovery V1T1 PADO/g) || []).length;
  stat.innerHTML = `<span class="log-stat">${lines} lines</span>`
    + (padi ? `<span class="log-stat">PADI ${padi}</span>` : '')
    + (pado ? `<span class="log-stat">PADO ${pado}</span>` : '')
    + (ack ? `<span class="log-stat" style="background:#16a34a;color:#fff">AuthAck ${ack}</span>` : '')
    + (nak ? `<span class="log-stat" style="background:#dc2626;color:#fff">AuthNak ${nak}</span>` : '');

  if (document.getElementById('lg-bottom').checked) {
    view.scrollTop = view.scrollHeight;
  }
}

function toggleAuto() {
  if (_autoTimer) { clearInterval(_autoTimer); _autoTimer = null; }
  if (document.getElementById('lg-auto').checked) {
    _autoTimer = setInterval(refresh, 5000);
  }
}

async function copyLog() {
  const text = document.getElementById('log-view').textContent;
  const ok = await copyText(text);
  if (ok) Toast.success('Copied ' + text.split('\n').length + ' lines');
  else Toast.error('Browser từ chối copy');
}
