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

  if (canChange) main.appendChild(await schedulePanel());

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

// schedulePanel sets up automatic content updates: a time window, an
// interval, which updates to take and a bandwidth cap.
async function schedulePanel() {
  let st;
  try { st = await api.get('/api/updates/schedule'); } catch (_) { return h('div'); }
  const c = st.settings;
  const hours = (sel) => h('select', null, Array.from({ length: 24 }, (_, i) => h('option', { value: i, selected: i === sel }, `${String(i).padStart(2, '0')}:00`)));
  const on = h('input', { type: 'checkbox', checked: c.enabled });
  const from = hours(c.windowStart);
  const to = hours(c.windowEnd);
  const every = h('select', null, [[1, 'every day'], [7, 'every week'], [30, 'every month']].map(([v, t]) => h('option', { value: v, selected: v === c.everyDays }, t)));
  const only = h('select', null, [['all', 'all updates that fit'], ['important', 'only medical and regulatory references']].map(([v, t]) => h('option', { value: v, selected: v === c.only }, t)));
  const cap = h('input', { type: 'number', min: 0, step: 0.1, value: c.bandwidthLimit ? (c.bandwidthLimit / 1e6).toFixed(1) : '', placeholder: 'no extra limit', style: 'width:120px' });
  const statusEl = h('div', { class: 'small muted' });
  const logEl = h('ul', { class: 'small update-log' });
  const render = (x) => {
    const s = x.state;
    statusEl.textContent = !x.settings.enabled ? 'Off. Updates are only downloaded when you choose them below.'
      : `${x.inWindow ? 'The update window is open now.' : `Next window opens ${new Date(x.nextWindow).toLocaleString()}.`} `
        + `Last check: ${fmtAgo(s.lastCheck && !s.lastCheck.startsWith('0001') ? s.lastCheck : '')}. Next check: ${new Date(x.nextCheck).toLocaleString()}.`
        + (s.jobs.length ? ` ${s.jobs.length} scheduled download${s.jobs.length === 1 ? '' : 's'} in progress${x.inWindow ? '' : ' (paused until the window opens)'}.` : '');
    clear(logEl);
    for (const ev of s.log.slice(0, 5)) {
      logEl.appendChild(h('li', null, `${new Date(ev.time).toLocaleString()}: ${ev.message}`,
        ev.queued?.length ? ` (${ev.queued.join(', ')})` : '', ev.skipped?.length ? h('span', { class: 'faint' }, ` Skipped: ${ev.skipped.join(', ')}.`) : null));
    }
  };
  render(st);
  const body = () => ({ enabled: on.checked, windowStart: parseInt(from.value, 10), windowEnd: parseInt(to.value, 10), everyDays: parseInt(every.value, 10),
    only: only.value, bandwidthLimit: cap.value === '' ? 0 : Math.round(parseFloat(cap.value) * 1e6) });
  const save = h('button', { class: 'primary', onclick: async () => {
    try {
      await api.put('/api/settings', { contentUpdates: body() });
      toast('Saved.');
      setTimeout(async () => { try { render(await api.get('/api/updates/schedule')); } catch (_) { /* ignore */ } }, 800);
    } catch (e) { toast(e.message); }
  } }, 'Save');
  const now = h('button', { onclick: async () => {
    now.disabled = true;
    now.textContent = 'Checking…';
    try {
      const res = await api.post('/api/updates/schedule/run');
      render(res.status);
      toast(res.result.message || 'Done.');
    } catch (e) { toast(e.message); }
    now.disabled = false;
    now.textContent = 'Check and queue now';
  } }, 'Check and queue now');
  return h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Automatic updates')),
    h('div', { class: 'body' },
      h('p', null, h('label', { class: 'inline' }, on, 'Keep installed content up to date automatically')),
      h('p', { class: 'row' }, 'Between ', from, ' and ', to, ', check the catalogs ', every, ' and download ', only, '.'),
      h('p', { class: 'row' }, 'While scheduled updates download, limit all downloads to ', cap, ' MB/s.'),
      h('p', { class: 'small muted' }, 'Off by default: Arcana makes no network requests unless you ask or turn this on. Outside the hours you choose, scheduled downloads pause and continue in the next window. Without internet, Arcana simply tries again later. Updates that would eat into your reserved free space are skipped, and medical and regulatory references go first.'),
      h('div', { class: 'row' }, save, now),
      statusEl, logEl));
}
