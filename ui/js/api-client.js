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

    // lines
    listLines: () => _req('GET', '/lines'),
    createLine: (data) => _req('POST', '/lines', data),
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
        <a href="/export.html" class="${active==='export'?'active':''}">Export</a>
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
