import { loadSession, getSession, api } from './api.js';
import { h, clear } from './util.js';
import { renderUpdateBanner } from './updatebanner.js';
import { libraryView } from './views/library.js';
import { askView } from './views/ask.js';
import { medicinesView } from './views/medicines.js';
import { welcomeView } from './views/welcome.js';
import { discoverView } from './views/discover.js';
import { mapsView } from './views/maps.js';
import { downloadsView } from './views/downloads.js';
import { updatesView } from './views/updates.js';
import { serverView, connectView } from './views/server.js';
import { settingsView } from './views/settings.js';
import { setupView } from './views/setup.js';
import { readerView, documentView } from './views/reader.js';

const views = {
  library: { title: 'Library', render: libraryView },
  ask: { title: 'Ask', render: askView, when: (st) => st?.assistant?.canAsk },
  medicines: { title: 'Medicines', render: medicinesView, when: (st) => st?.drugs?.installed },
  discover: { title: 'Discover', render: discoverView },
  maps: { title: 'Maps', render: mapsView },
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
    if (v.when && !v.when(status) && active !== name) continue;
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
  if (s.remoteAdmin) {
    el.appendChild(pill('ok', 'Signed in as administrator', 'You can manage this library from this device.'));
    el.appendChild(h('button', { class: 'link small', onclick: signOut }, 'Sign out'));
  } else if (!s.local) {
    el.appendChild(pill('info', 'Network client (read-only)', 'Changes can only be made on the computer running Arcana.'));
    if (s.authEnabled) el.appendChild(h('button', { class: 'link small', onclick: signIn }, 'Sign in'));
  }
}

async function signOut() {
  try { await api.post('/api/auth/logout'); } catch (_) { /* ignore */ }
  location.reload();
}

function signIn() {
  const pw = h('input', { type: 'password', autocomplete: 'current-password', required: true, 'aria-label': 'Administrator password', style: 'width:100%' });
  const msg = h('p', { class: 'small', role: 'alert' });
  const dlg = h('dialog', { class: 'signin', 'aria-label': 'Sign in' });
  const submit = async (ev) => {
    ev.preventDefault();
    try {
      const r = await fetch('/api/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: pw.value }), cache: 'no-store' });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(d.error || r.statusText);
      dlg.close();
      location.reload();
    } catch (e) { msg.textContent = e.message; pw.select(); }
  };
  dlg.append(
    h('header', null, h('h2', null, 'Sign in to manage this library')),
    h('form', { class: 'body', onsubmit: submit },
      h('p', { class: 'small muted' }, location.protocol === 'https:' ? 'The connection is encrypted.'
        : 'This connection is not encrypted; others on the network could see the password. Use the https:// address if the host offers one.'),
      pw, msg,
      h('div', { class: 'row', style: 'justify-content:flex-end' },
        h('button', { type: 'button', onclick: () => dlg.close() }, 'Cancel'), h('button', { class: 'primary', type: 'submit' }, 'Sign in'))));
  dlg.addEventListener('close', () => dlg.remove());
  document.body.appendChild(dlg);
  dlg.showModal();
  pw.focus();
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
  renderUpdateBanner(status);
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
  if (r.name === 'welcome') {
    renderTabs('library');
    document.title = 'Getting started — Arcana';
    cleanup = await welcomeView(main);
    return;
  }
  if (r.name === 'connect') {
    renderTabs('server');
    cleanup = await connectView(main);
    return;
  }
  if (r.name === 'doc') {
    renderTabs('library');
    cleanup = await documentView(main, r.params);
    return;
  }
  const v = views[r.name] || views.library;
  renderTabs(r.name in views ? r.name : 'library');
  document.title = `${v.title} — Arcana`;
  try {
    cleanup = await v.render(main, r.params, { refreshStatus }, r.args);
  } catch (e) {
    main.appendChild(h('div', { class: 'banner err' }, e.message));
  }
}

async function start() {
  applyTheme();
  window.addEventListener('storage', applyTheme);
  // Units chosen in another tab apply here too.
  window.addEventListener('storage', (e) => { if (e.key === 'arcana-units') window.dispatchEvent(new Event('arcana-units')); });
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
  window.addEventListener('arcana-status', () => refreshStatus());
  await route();
  setInterval(() => { if (!getSession().setup && !document.hidden) refreshStatus(); }, 10000);
}

start();
