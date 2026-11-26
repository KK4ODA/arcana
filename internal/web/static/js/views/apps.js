import { api, getSession } from '../api.js';
import { h, fill, fmtBytes, licenseBadge, toast } from '../util.js';

// Apps: web applications installed from the catalog (ADR-0030). Each opens
// in a tab of its own, on an address of its own.

const PLATFORM_NAMES = {
  'windows-amd64': 'Windows', 'darwin-arm64': 'macOS (Apple silicon)', 'darwin-amd64': 'macOS (Intel)',
  'linux-amd64': 'Linux x86-64', 'linux-arm64': 'Linux ARM64 (Raspberry Pi)',
};

export function platformList(list) {
  if (!list || !list.length || (list.length === 1 && list[0] === 'any')) return 'Any computer (runs in the browser)';
  return list.map((p) => PLATFORM_NAMES[p] || p).join(', ');
}

const STATE_TEXT = { preparing: 'Preparing', ready: 'Ready', unavailable: 'Not available here', failed: 'Failed' };

export async function appsView(main) {
  const s = getSession();
  const canChange = s.admin && !s.readOnly;
  const yours = h('div', { class: 'body' });
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Apps'),
      h('span', { class: 'small muted' }, 'Each app opens in a new tab and works without the internet.')),
    yours));
  const addBody = h('div');
  if (canChange) main.appendChild(addBody);

  let timer = null;
  async function load() {
    const [{ apps }, catalog] = await Promise.all([
      api.get('/api/apps'),
      canChange ? api.get('/api/catalog?format=app&limit=100').catch(() => null) : null,
    ]);
    fill(yours, apps.length ? h('div', { class: 'app-list' }, apps.map(appCard))
      : h('p', { class: 'empty' }, canChange ? 'No apps on this drive yet. Choose some below; they download in the background.'
        : 'No apps on this drive yet. Apps are added on the computer running Arcana.'));
    if (catalog) renderCatalog(catalog.resources.filter((r) => r.status !== 'installed'));
    clearTimeout(timer);
    if (apps.some((a) => a.state === 'preparing')) timer = setTimeout(() => load().catch(() => {}), 2000);
  }

  function renderCatalog(available) {
    if (!available.length) { fill(addBody); return; }
    fill(addBody, h('section', { class: 'panel' },
      h('header', null, h('h2', null, 'Apps you can add')),
      h('p', { class: 'small muted body' }, 'Arcana downloads each app from its publisher, checks it against the SHA-256 recorded when it was reviewed, and keeps the files for every computer the app supports, so the drive works wherever you take it.'),
      h('div', { class: 'table-wrap' }, h('table', null,
        h('thead', null, h('tr', null, h('th', null, 'App'), h('th', { class: 'hide-sm' }, 'Runs on'), h('th', { class: 'right' }, 'Size'),
          h('th', { class: 'hide-sm' }, 'License'), h('th', null, ''))),
        h('tbody', null, available.map((r) => h('tr', null,
          h('td', { class: 'title-cell' }, h('b', null, r.title), h('span', { class: 'sub' }, r.description)),
          h('td', { class: 'hide-sm small' }, platformList((r.providerRef?.platforms || 'any').split(' '))),
          h('td', { class: 'right num' }, fmtBytes(r.downloadSize)),
          h('td', { class: 'hide-sm' }, licenseBadge(r.license)),
          h('td', { class: 'right' }, r.status === 'queued' ? h('span', { class: 'small muted' }, 'Queued')
            : !r.fits ? h('span', { class: 'badge err', title: r.fitReason }, 'Does not fit')
              : h('button', { onclick: (e) => add(r, e.target) }, 'Add')))))))));
  }

  async function add(r, btn) {
    btn.disabled = true;
    try {
      const res = await api.post('/api/downloads', { resourceIds: [r.id] });
      if (res.results[r.id] !== 'queued') throw new Error(res.results[r.id]);
      btn.replaceWith(h('span', { class: 'small muted' }, 'Queued'));
      toast(`Queued ${r.title}. It downloads in the background; see Downloads.`);
    } catch (e) { btn.disabled = false; toast(e.message); }
  }

  await load();
  return () => clearTimeout(timer);
}

function appCard(a) {
  const ready = a.state === 'ready' && !a.portProblem;
  const badgeClass = { ready: 'ok', preparing: 'info', unavailable: 'warn', failed: 'err' }[a.state] || '';
  const notes = [];
  if (a.portProblem) notes.push(h('p', { class: 'small err-text' }, a.portProblem));
  else if (a.problem) notes.push(h('p', { class: 'small err-text' }, a.problem));
  if (a.secureContext && !a.secure) notes.push(h('p', { class: 'small warn-text' }, a.secureContext));
  return h('div', { class: 'app-card' },
    h('div', { class: 'app-head' },
      h('b', null, a.title), ' ',
      h('span', { class: `badge ${badgeClass}` }, STATE_TEXT[a.state] || a.state),
      h('span', { class: 'small muted' }, ` ${a.version}`)),
    h('span', { class: 'small' }, a.summary),
    ...notes,
    h('div', { class: 'row' },
      ready ? h('a', { class: 'button primary', href: a.url, target: '_blank', rel: 'noopener noreferrer' }, `Open ${a.title}`)
        : h('span', { class: 'small muted' }, a.state === 'preparing' ? 'Being unpacked on this computer…' : ''),
      h('span', { class: 'small muted' }, a.url)));
}
