// API client + helpers. Bearer token từ localStorage.

const Api = (() => {
  const BASE = '/api/v1';
  const TOKEN_KEY = 'bp_token';

  function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }
  function setToken(t) { localStorage.setItem(TOKEN_KEY, t); }
  function clearToken() { localStorage.removeItem(TOKEN_KEY); }

  async function _req(method, path, body) {
    const headers = { 'Authorization': 'Bearer ' + getToken() };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    const r = await fetch(BASE + path, {
      method, headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (r.status === 401) {
      clearToken();
      if (!location.pathname.endsWith('/index.html') && location.pathname !== '/') {
        location.href = '/';
      }
      throw new Error('Unauthorized');
    }
    const ct = r.headers.get('content-type') || '';
    if (ct.includes('text/plain')) return r.text();
    const j = await r.json();
    if (!j.success) throw new Error(j.error || 'request failed');
    return j.data;
  }

  return {
    getToken, setToken, clearToken,

    // auth — user/password
    login: (username, password) => fetch(BASE + '/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    }).then(r => r.json()),
    me: () => _req('GET', '/auth/me'),
    logout: () => _req('POST', '/auth/logout'),
    changePassword: (current_password, new_password) =>
      _req('POST', '/auth/change-password', { current_password, new_password }),

    // health
    health: () => fetch(BASE + '/health').then(r => r.json()),

    // ifaces
    listIfaces: () => _req('GET', '/ifaces'),
    probeIfaces: (ifaces) => _req('POST', '/ifaces/probe', ifaces ? { ifaces } : {}),

    // logs
    getLogs: (params) => {
      const q = params ? '?' + new URLSearchParams(params).toString() : '';
      return _req('GET', '/logs' + q);
    },

    // rules
    listRules: (params) => {
      const q = params ? '?' + new URLSearchParams(params).toString() : '';
      return _req('GET', '/rules' + q);
    },
    createRule: (data) => _req('POST', '/rules', data),
    updateRule: (id, data) => _req('PUT', `/rules/${id}`, data),
    deleteRule: (id) => _req('DELETE', `/rules/${id}`),

    // stats (dashboard)
    getStats: () => _req('GET', '/stats'),

    // lines
    listLines: () => _req('GET', '/lines'),
    createLine: (data) => _req('POST', '/lines', data),
    updateLine: (id, data) => _req('PUT', `/lines/${id}`, data),
    deleteLine: (id) => _req('POST', `/lines/${id}/delete`),

    // sessions
    listSessions: (params) => {
      const q = params ? '?' + new URLSearchParams(params).toString() : '';
      return _req('GET', '/sessions' + q);
    },
    getSession: (id) => _req('GET', `/sessions/${id}`),
    createSession: (lineId, data) => _req('POST', `/lines/${lineId}/sessions`, data),
    bulkCreateSessions: (lineId, data) => _req('POST', `/lines/${lineId}/sessions/bulk`, data),
    deleteSession: (id) => _req('POST', `/sessions/${id}/delete`),
    rotateSession: (id) => _req('POST', `/sessions/${id}/rotate`),
    setSessionEnabled: (id, enabled) => _req('POST', `/sessions/${id}/enabled`, { enabled }),
    setAutoRotate: (id, seconds) => _req('PUT', `/sessions/${id}/auto-rotate`, { seconds }),
    setAutoRotateBatch: (ids, seconds) => _req('POST', '/sessions/auto-rotate/batch', { session_ids: ids, seconds }),
    rotateBatch: (ids, concurrency) => _req('POST', '/rotate', { session_ids: ids, concurrency: concurrency || 5 }),

    // creds
    listCreds: (pid) => _req('GET', `/proxies/${pid}/credentials`),
    createCred: (pid, data) => _req('POST', `/proxies/${pid}/credentials`, data),
    bulkCreds: (pid, data) => _req('POST', `/proxies/${pid}/credentials/bulk`, data),
    updateCred: (pid, cid, data) => _req('PUT', `/proxies/${pid}/credentials/${cid}`, data),
    deleteCred: (pid, cid) => _req('DELETE', `/proxies/${pid}/credentials/${cid}`),

    // export
    exportProxies: (type, format) => _req('GET', `/proxies/export?type=${type || 'public'}&format=${format || 'text'}`),

    // tokens
    listTokens: () => _req('GET', '/tokens'),
    createToken: (label) => _req('POST', '/tokens', { label }),
    deleteToken: (id) => _req('DELETE', `/tokens/${id}`),

    // SSE
    subscribeEvents: (onEvent) => {
      const es = new EventSource(BASE + '/events?token=' + encodeURIComponent(getToken()));
      es.addEventListener('hello', e => onEvent('hello', JSON.parse(e.data)));
      ['session.status', 'session.public_ip', 'session.rotate', 'proxy.cred_changed', 'proxy.started', 'proxy.stopped'].forEach(t => {
        es.addEventListener(t, e => onEvent(t, JSON.parse(e.data)));
      });
      return es;
    },
  };
})();

// ─── Dialog — Promise-based custom modal thay alert/confirm/prompt ──────────
// Mọi action quan trọng (xác nhận xóa, hiển thị giá trị quan trọng, nhập sửa)
// đều dùng Dialog thay vì alert/confirm/prompt của trình duyệt. Lý do:
//   1. Trải nghiệm thống nhất (style theo card/modal hiện có).
//   2. Hiển thị tốt đa dòng, mono, code block.
//   3. Không bị bật cảnh báo "trang này muốn hiển thị thông báo" trên 1 số browser.
//
// API:
//   await Dialog.alert(message, {title?, kind?: 'info'|'warn'|'danger'|'success'})
//   await Dialog.confirm(message, {title?, okText?, cancelText?, kind?, danger?: bool})
//   await Dialog.prompt(message, {title?, defaultValue?, password?, okText?, cancelText?})
//   await Dialog.show({title, bodyHTML, actions:[{label, kind?, value?}], dismissValue?})
//      → return value của action được chọn (hoặc dismissValue nếu đóng qua Escape/backdrop)
const Dialog = (() => {
  let zCounter = 1000;

  function escape(s) { return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
  // nl2br — message của Dialog được coi là HTML (caller có quyền dùng <b>, <i>,
  // span, v.v.). Caller TỰ chịu trách nhiệm escape dynamic content qua escapeHTML.
  // Chỉ replace \n → <br> để giữ wrap dòng tự nhiên.
  function nl2br(s) { return String(s ?? '').replace(/\n/g, '<br>'); }

  function show({ title, bodyHTML, actions, dismissValue, onMount, autofocusSel }) {
    return new Promise(resolve => {
      const z = ++zCounter;
      const bg = document.createElement('div');
      bg.className = 'dlg-bg';
      bg.style.zIndex = z;
      bg.innerHTML = `<div class="dlg" role="dialog" aria-modal="true">
        ${title ? `<div class="dlg-title">${escape(title)}</div>` : ''}
        <div class="dlg-body">${bodyHTML || ''}</div>
        <div class="dlg-actions"></div>
      </div>`;
      const act = bg.querySelector('.dlg-actions');
      (actions || [{ label: 'OK', value: true, kind: 'primary' }]).forEach((a, i) => {
        const btn = document.createElement('button');
        btn.textContent = a.label;
        if (a.kind === 'secondary') btn.className = 'secondary';
        else if (a.kind === 'danger') btn.className = 'danger';
        btn.addEventListener('click', () => done(a.value));
        act.appendChild(btn);
      });
      document.body.appendChild(bg);

      function done(v) {
        document.removeEventListener('keydown', onKey, true);
        bg.classList.add('dlg-closing');
        setTimeout(() => { bg.remove(); resolve(v); }, 120);
      }
      function onKey(e) {
        if (e.key === 'Escape') { e.preventDefault(); done(dismissValue); }
        else if (e.key === 'Enter' && (e.target.tagName !== 'TEXTAREA')) {
          // Enter trigger action đầu tiên (thường là OK)
          const firstBtn = act.querySelector('button');
          if (firstBtn) { e.preventDefault(); firstBtn.click(); }
        }
      }
      bg.addEventListener('click', (e) => { if (e.target === bg) done(dismissValue); });
      document.addEventListener('keydown', onKey, true);

      if (onMount) onMount(bg);
      const focusTarget = autofocusSel ? bg.querySelector(autofocusSel) : act.querySelector('button');
      if (focusTarget) focusTarget.focus();
    });
  }

  async function alert(message, opts) {
    opts = opts || {};
    const title = opts.title || 'Thông báo';
    return show({
      title,
      bodyHTML: `<div class="dlg-msg ${opts.kind ? 'dlg-' + opts.kind : ''}">${nl2br(message)}</div>`,
      actions: [{ label: opts.okText || 'OK', kind: opts.kind === 'danger' ? 'danger' : 'primary', value: true }],
      dismissValue: true,
    });
  }

  async function confirm(message, opts) {
    opts = opts || {};
    const title = opts.title || 'Xác nhận';
    return show({
      title,
      bodyHTML: `<div class="dlg-msg ${opts.danger ? 'dlg-danger' : (opts.kind ? 'dlg-' + opts.kind : '')}">${nl2br(message)}</div>`,
      actions: [
        { label: opts.cancelText || 'Hủy', kind: 'secondary', value: false },
        { label: opts.okText || 'Đồng ý', kind: opts.danger ? 'danger' : 'primary', value: true },
      ],
      dismissValue: false,
    });
  }

  async function prompt(message, opts) {
    opts = opts || {};
    const title = opts.title || 'Nhập giá trị';
    const inputType = opts.password ? 'password' : 'text';
    const defVal = escape(opts.defaultValue || '');
    const fieldHTML = `<input class="dlg-input" id="dlg-prompt-input" type="${inputType}" value="${defVal}" autocomplete="off">`;
    let resolver;
    const p = show({
      title,
      bodyHTML: `<div class="dlg-msg">${nl2br(message)}</div>${fieldHTML}`,
      actions: [
        { label: opts.cancelText || 'Hủy', kind: 'secondary', value: null },
        { label: opts.okText || 'OK', kind: 'primary', value: '__OK__' },
      ],
      dismissValue: null,
      autofocusSel: '#dlg-prompt-input',
      onMount: (bg) => {
        // Override: Enter trên input → confirm với giá trị hiện tại
        const inp = bg.querySelector('#dlg-prompt-input');
        if (inp) {
          inp.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              const okBtn = bg.querySelector('.dlg-actions button:last-child');
              if (okBtn) okBtn.click();
            }
          });
        }
      },
    });
    return p.then(v => {
      if (v !== '__OK__') return null;
      // Đọc giá trị input — input đã bị remove khỏi DOM khi resolve, nên ta đã đọc trước
      // Workaround: lưu giá trị trước khi resolve. Đơn giản: read tại moment OK click.
      // Lưu ý: show() đã .remove() trước khi resolve. Cần đọc giá trị ngay khi click.
      // → refactor: dùng custom handler. Code below cho đúng.
      return null;
    });
  }

  // Vì cách show() resolve sau khi remove DOM khiến không đọc được input.value,
  // ta override prompt() bằng phiên bản riêng.
  async function promptV2(message, opts) {
    opts = opts || {};
    const title = opts.title || 'Nhập giá trị';
    const inputType = opts.password ? 'password' : 'text';
    const defVal = escape(opts.defaultValue || '');
    return new Promise(resolve => {
      const z = ++zCounter;
      const bg = document.createElement('div');
      bg.className = 'dlg-bg';
      bg.style.zIndex = z;
      bg.innerHTML = `<div class="dlg" role="dialog" aria-modal="true">
        <div class="dlg-title">${escape(title)}</div>
        <div class="dlg-body">
          <div class="dlg-msg">${nl2br(message)}</div>
          <input class="dlg-input" id="dlg-prompt-input" type="${inputType}" value="${defVal}" autocomplete="off">
        </div>
        <div class="dlg-actions">
          <button class="secondary" data-act="cancel">${escape(opts.cancelText || 'Hủy')}</button>
          <button data-act="ok">${escape(opts.okText || 'OK')}</button>
        </div>
      </div>`;
      const inp = bg.querySelector('#dlg-prompt-input');
      function done(v) {
        document.removeEventListener('keydown', onKey, true);
        bg.classList.add('dlg-closing');
        setTimeout(() => { bg.remove(); resolve(v); }, 120);
      }
      function onKey(e) {
        if (e.key === 'Escape') { e.preventDefault(); done(null); }
      }
      bg.querySelector('[data-act=cancel]').addEventListener('click', () => done(null));
      bg.querySelector('[data-act=ok]').addEventListener('click', () => done(inp.value));
      inp.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') { e.preventDefault(); done(inp.value); }
      });
      bg.addEventListener('click', (e) => { if (e.target === bg) done(null); });
      document.addEventListener('keydown', onKey, true);
      document.body.appendChild(bg);
      inp.focus(); inp.select();
    });
  }

  return { show, alert, confirm, prompt: promptV2 };
})();

// --- UI helpers ---
const Toast = (() => {
  function show(kind, msg) {
    const area = document.querySelector('.toast-area') || (() => {
      const a = document.createElement('div');
      a.className = 'toast-area';
      document.body.appendChild(a);
      return a;
    })();
    const t = document.createElement('div');
    t.className = 'toast ' + kind;
    t.textContent = msg;
    area.appendChild(t);
    setTimeout(() => t.remove(), 4000);
  }
  return {
    success: (msg) => show('success', msg),
    error: (msg) => show('error', msg),
    info: (msg) => show('info', msg),
  };
})();

function ensureAuth() {
  if (!Api.getToken()) {
    if (location.pathname !== '/' && !location.pathname.endsWith('/index.html')) {
      location.href = '/';
    }
    return false;
  }
  return true;
}

async function logout() {
  // Cố gắng invalidate session phía server; nếu lỗi vẫn clear local
  try { await Api.logout(); } catch (_) {}
  Api.clearToken();
  location.href = '/';
}

function renderNav(active) {
  const html = `
    <header>
      <h1>bestphone-pppoe</h1>
      <nav>
        <a href="/lines.html" class="${active==='lines'?'active':''}">Tổng quan</a>
        <a href="/sessions.html" class="${active==='sessions'?'active':''}">Phiên</a>
        <a href="/rules.html" class="${active==='rules'?'active':''}">Rules</a>
        <a href="/export.html" class="${active==='export'?'active':''}">Xuất proxy</a>
        <a href="/logs.html" class="${active==='logs'?'active':''}">Logs</a>
        <a href="/api.html" class="${active==='api'?'active':''}">API</a>
        <a href="/settings.html" class="${active==='settings'?'active':''}">Cài đặt</a>
      </nav>
      <span class="spacer"></span>
      <span class="muted small" id="conn-user"></span>
      <span class="muted small" id="conn-status">●</span>
      <button class="secondary" onclick="logout()" title="Đăng xuất">⎋ Đăng xuất</button>
    </header>
  `;
  document.body.insertAdjacentHTML('afterbegin', html);
  // Lazy fetch user info
  Api.me().then(u => {
    const el = document.getElementById('conn-user');
    if (!el) return;
    if (u.is_api_token) el.textContent = '🔑 API token';
    else el.textContent = '👤 ' + u.username;
  }).catch(() => {});
}

function escapeHTML(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

// copyText — universal clipboard copy. navigator.clipboard chỉ work trên HTTPS/
// localhost. HTTP plain bị browser block → fallback textarea + execCommand.
async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try { await navigator.clipboard.writeText(text); return true; } catch (_) { /* fall through */ }
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.cssText = 'position:fixed;top:-9999px;left:-9999px;opacity:0';
  document.body.appendChild(ta);
  ta.select();
  ta.setSelectionRange(0, ta.value.length);
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (_) {}
  ta.remove();
  return ok;
}

// statusBadge — icon + nhãn Việt cho trạng thái phiên/proxy.
const STATUS_LABEL = {
  connected:    { icon: '✓',  text: 'Đã kết nối' },
  dialing:      { icon: '⟳',  text: 'Đang quay số' },
  error:        { icon: '⚠',  text: 'Lỗi' },
  disconnected: { icon: '○',  text: 'Đã ngắt' },
  running:      { icon: '▶',  text: 'Đang chạy' },
  stopped:      { icon: '■',  text: 'Đã dừng' },
};
function statusBadge(s) {
  const m = STATUS_LABEL[s] || { icon: '·', text: s };
  return `<span class="badge ${escapeHTML(s)}"><span class="badge-icon">${m.icon}</span>${escapeHTML(m.text)}</span>`;
}

// ─── Button UX: ripple + async disable ───
// 1. Global click listener: spawn ripple span tại vị trí click trên mọi <button>.
// 2. Intercept onclick handler: nếu trả Promise → add .is-loading + disable cho đến khi resolve.
(function setupButtonUX() {
  if (window.__btnUXReady) return;
  window.__btnUXReady = true;

  document.addEventListener('click', (e) => {
    const btn = e.target.closest('button, .btn');
    if (!btn) return;
    if (btn.disabled || btn.classList.contains('is-loading')) {
      e.preventDefault(); e.stopImmediatePropagation(); return;
    }
    // Ripple
    const rect = btn.getBoundingClientRect();
    const r = document.createElement('span');
    r.className = 'btn-ripple';
    r.style.left = (e.clientX - rect.left) + 'px';
    r.style.top = (e.clientY - rect.top) + 'px';
    btn.appendChild(r);
    setTimeout(() => r.remove(), 600);
  }, true);

  // Intercept inline onclick — wrap để detect Promise return.
  // Approach: override Element.prototype.onclick setter để wrap handler.
  // Áp dụng cho mọi button có onclick attribute.
  const origSetAttr = Element.prototype.setAttribute;
  // Quá invasive — simpler: scan buttons sau page load + wrap.
  function wrapOnclick(btn) {
    if (btn.__uxWrapped) return;
    const original = btn.onclick;
    if (!original) return;
    btn.__uxWrapped = true;
    btn.onclick = function(ev) {
      const ret = original.call(this, ev);
      if (ret && typeof ret.then === 'function') {
        const saved = this.textContent;
        this.classList.add('is-loading');
        ret.finally(() => {
          this.classList.remove('is-loading');
          this.textContent = saved;
        });
      }
      return ret;
    };
  }

  // Re-scan khi DOM thay đổi.
  function scanAll() {
    document.querySelectorAll('button[onclick], .btn[onclick]').forEach(wrapOnclick);
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', scanAll);
  } else {
    scanAll();
  }
  // Watch DOM thêm button mới (modal open hoặc render bảng).
  const obs = new MutationObserver(scanAll);
  obs.observe(document.body || document.documentElement, { childList: true, subtree: true });
})();

// withButton — explicit helper cho async action có btn reference rõ ràng.
// Dùng khi onclick handler được attach qua addEventListener (không qua attribute).
async function withButton(btn, asyncFn) {
  if (!btn || btn.disabled || btn.classList.contains('is-loading')) return;
  btn.classList.add('is-loading');
  try {
    return await asyncFn();
  } finally {
    btn.classList.remove('is-loading');
  }
}
