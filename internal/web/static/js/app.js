import { loadSession, getSession, api } from './api.js';
import { h, clear } from './util.js';
import { libraryView } from './views/library.js';
import { discoverView } from './views/discover.js';
import { downloadsView } from './views/downloads.js';
import { updatesView } from './views/updates.js';
import { serverView } from './views/server.js';
import { settingsView } from './views/settings.js';
import { setupView } from './views/setup.js';
import { readerView } from './views/reader.js';

const views = {
  library: { title: 'Library', render: libraryView },
  discover: { title: 'Discover', render: discoverView },
  downloads: { title: 'Downloads', render: downloadsView },
  updates: { title: 'Updates', render: updatesView },
  server: { title: 'Server', render: serverView },
  settings: { title: 'Settings', render: settingsView, admin: true },
};

let cleanup = null;
let status = null;

function applyTheme() {
  let t = 'system';
  try { t = localStorage.getItem('arcana-theme') || 'system'; } catch (_) { /* storage unavailable */ }
  if (t === 'system') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.dataset.theme = t;
}

function parseRoute() {
  const raw = location.hash.replace(/^#\/?/, '');
  const [path, query] = raw.split('?');
  const parts = path.split('/').filter(Boolean).map(decodeURIComponent);
  return { name: parts[0] || 'library', args: parts.slice(1), params: new URLSearchParams(query || '') };
}

function renderTabs(active) {
  const nav = clear(document.getElementById('tabs'));
  const s = getSession();
  for (const [name, v] of Object.entries(views)) {
    if (v.admin && !s.admin) continue;
    let count = null;
    if (status && name === 'downloads' && status.downloads.active) count = status.downloads.active;
    if (status && name === 'updates' && status.updates.available) count = status.updates.available;
    nav.appendChild(h('a', { href: `#/${name}`, 'aria-current': active === name ? 'page' : null },
      v.title, count ? h('span', { class: 'count' }, count) : null));
  }
}

function renderPills() {
  const el = clear(document.getElementById('pills'));
  const s = getSession();
  if (!status) return;
  const pill = (cls, text, title) => h('span', { class: 'pill', title }, h('span', { class: `dot ${cls}` }), text);
  el.appendChild(status.offline.ready
    ? pill('ok', 'Offline ready', 'Installed content is read from the drive; no internet connection is needed.')
    : pill('warn', 'Not ready offline', 'No browsable content installed yet, or the archive reader is not running.'));
  if (status.sharing.active) el.appendChild(pill('info', 'Sharing on network', status.sharing.urls.join(' ')));
  if (s.readOnly) el.appendChild(pill('warn', 'Read-only', s.readOnlyReason));
  if (!s.local) el.appendChild(pill('info', 'Network client (read-only)', 'Changes can only be made on the computer running Arcana.'));
}

async function refreshStatus() {
  try {
    status = await api.get('/api/status');
  } catch (_) {
    status = null;
  }
  const r = parseRoute();
  renderTabs(r.name);
  renderPills();
  return status;
}

export function currentStatus() { return status; }
export { refreshStatus };

async function route() {
  if (cleanup) { try { cleanup(); } catch (_) { /* ignore */ } cleanup = null; }
  const s = getSession();
  const main = clear(document.getElementById('main'));
  const r = parseRoute();
  if (s.setup) {
    document.getElementById('tabs').hidden = true;
    cleanup = await setupView(main);
    return;
  }
  if (r.name === 'read') {
    cleanup = await readerView(main, r.args[0], r.params);
    return;
  }
  const v = views[r.name] || views.library;
  renderTabs(r.name in views ? r.name : 'library');
  document.title = `${v.title} — Arcana`;
  try {
    cleanup = await v.render(main, r.params, { refreshStatus });
  } catch (e) {
    main.appendChild(h('div', { class: 'banner err' }, e.message));
  }
}

async function start() {
  applyTheme();
  window.addEventListener('storage', applyTheme);
  window.addEventListener('arcana-theme', applyTheme);
  try {
    await loadSession();
  } catch (e) {
    clear(document.getElementById('main')).appendChild(
      h('div', { class: 'banner err' }, 'Cannot reach Arcana. Make sure it is still running.'));
    return;
  }
  const s = getSession();
  document.getElementById('library-label').textContent = s.libraryLabel || '';
  if (!s.setup) await refreshStatus();
  window.addEventListener('hashchange', route);
  await route();
  setInterval(() => { if (!getSession().setup && !document.hidden) refreshStatus(); }, 10000);
}

start();
