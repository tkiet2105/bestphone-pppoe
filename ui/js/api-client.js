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

    // auth
    login: (token) => fetch(BASE + '/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token }),
    }).then(r => r.json()),

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

function logout() {
  Api.clearToken();
  location.href = '/';
}

function renderNav(active) {
  const html = `
    <header>
      <h1>bestphone-pppoe</h1>
      <nav>
        <a href="/lines.html" class="${active==='lines'?'active':''}">Lines</a>
        <a href="/sessions.html" class="${active==='sessions'?'active':''}">Sessions</a>
        <a href="/rules.html" class="${active==='rules'?'active':''}">Rules</a>
        <a href="/export.html" class="${active==='export'?'active':''}">Export</a>
        <a href="/logs.html" class="${active==='logs'?'active':''}">Logs</a>
        <a href="/api.html" class="${active==='api'?'active':''}">API</a>
      </nav>
      <span class="spacer"></span>
      <span class="muted small" id="conn-status">●</span>
      <button onclick="logout()">Logout</button>
    </header>
  `;
  document.body.insertAdjacentHTML('afterbegin', html);
}

function escapeHTML(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

function statusBadge(s) {
  return `<span class="badge ${escapeHTML(s)}">${escapeHTML(s)}</span>`;
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
