import { api, getSession } from '../api.js';
import { h, clear, fmtBytes, fmtAgo, fmtDate, toast } from '../util.js';

export async function updatesView(main) {
  const s = getSession();
  const canChange = s.admin && !s.readOnly;
  const r = await api.get('/api/updates');
  const remote = r.providers.filter((p) => p.id !== 'arcana');
  const last = remote.map((p) => p.fetchedAt).filter(Boolean).sort().pop();

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Knowledge updates')),
    h('div', { class: 'body' },
      h('p', null, 'Updates compare your installed editions with the most recently loaded catalog. This works offline; refresh the catalog in Discover while connected to learn about newer editions.'),
      h('p', { class: 'small muted' }, `Last successful catalog check: ${fmtAgo(last)}.`,
        ...remote.filter((p) => p.lastError).map((p) => h('span', { class: 'badge warn', title: p.lastError, style: 'margin-left:6px' }, `${p.name}: last attempt failed`))),
      h('p', { class: 'small muted' }, 'An update downloads the new edition next to the current one and verifies it. The current edition stays usable until the new one is verified; only then is it replaced. Your notes and documents are stored separately and are never touched.'))));

  const ups = r.updates;
  const checks = new Map();
  const total = h('span', { class: 'muted small' });
  const btn = h('button', { class: 'primary', disabled: true, onclick: apply }, 'Update selected');
  function sum() {
    let n = 0; let b = 0;
    for (const [u, box] of checks) if (box.checked) { n++; b += u.downloadSize; }
    total.textContent = n ? `${n} selected, ${fmtBytes(b)} to download` : '';
    btn.disabled = n === 0;
  }
  const table = h('table', null,
    h('thead', null, h('tr', null, h('th', { class: 'check' }), h('th', null, 'Resource'), h('th', null, 'Installed'), h('th', null, 'Available'),
      h('th', { class: 'right' }, 'Download'), h('th', null, 'Priority'))),
    h('tbody', null, ups.map((u) => {
      const box = h('input', { type: 'checkbox', disabled: !canChange || u.queued, checked: false, 'aria-label': `Update ${u.title}` });
      box.addEventListener('change', sum);
      checks.set(u, box);
      return h('tr', null, h('td', { class: 'check' }, box),
        h('td', null, h('b', null, u.title), u.queued ? h('span', { class: 'badge', style: 'margin-left:6px' }, 'Queued') : null),
        h('td', { class: 'num' }, u.installedVersion, h('div', { class: 'small faint' }, fmtDate(u.installedDate))),
        h('td', { class: 'num' }, u.latestVersion, h('div', { class: 'small faint' }, fmtDate(u.latestDate))),
        h('td', { class: 'right num' }, fmtBytes(u.downloadSize)),
        h('td', null, u.priority === 'high' ? h('span', { class: 'badge warn', title: 'Medical and regulatory references change over time.' }, 'High') : h('span', { class: 'small muted' }, 'Normal')));
    })));
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, `${ups.length} update${ups.length === 1 ? '' : 's'} available`), total,
      canChange && ups.length ? h('button', { onclick: () => { for (const [u, b] of checks) if (!u.queued) b.checked = true; sum(); } }, 'Select all') : null, canChange ? btn : null),
    ups.length ? h('div', { class: 'body flush table-wrap' }, table) : h('p', { class: 'empty' }, 'All installed resources match the latest catalog editions known to this library.')));

  async function apply() {
    const ids = [...checks].filter(([, b]) => b.checked).map(([u]) => u.resourceId);
    try {
      const res = await api.post('/api/downloads', { resourceIds: ids });
      const bad = Object.entries(res.results).filter(([, v]) => v !== 'queued');
      toast(bad.length ? `Some updates were not queued: ${bad.map(([k, v]) => `${k}: ${v}`).join('; ')}` : `Queued ${ids.length} update(s).`);
      location.hash = '#/downloads';
    } catch (e) { toast(e.message); }
  }
  return null;
}
