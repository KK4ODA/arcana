import { api, getSession } from '../api.js';
import { h, clear, fmtBytes, fmtDuration, fmtAgo, toast } from '../util.js';

const STATE_TEXT = {
  queued: 'Queued', resolving: 'Locating', downloading: 'Downloading', waiting: 'Waiting to retry', paused: 'Paused',
  verifying: 'Verifying', installing: 'Installing', completed: 'Installed', failed: 'Failed', cancelled: 'Cancelled',
};

export async function downloadsView(main) {
  const s = getSession();
  const canChange = s.admin && !s.readOnly;
  const body = h('div', { class: 'body flush table-wrap' });
  const summary = h('span', { class: 'muted small' });
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Downloads'), summary),
    h('div', { class: 'toolbar small muted' },
      'Downloads resume automatically after interruptions, restarts and network outages. Each file is verified against the publisher\'s checksum before it replaces anything in the library.'),
    body));

  async function load() {
    let r;
    try { r = await api.get('/api/downloads'); } catch (e) { clear(body).appendChild(h('div', { class: 'banner err' }, e.message)); return; }
    const jobs = r.jobs;
    const active = jobs.filter((j) => !['completed', 'failed', 'cancelled'].includes(j.state));
    const remaining = active.reduce((n, j) => n + Math.max(j.bytesTotal - j.bytesDone, 0), 0);
    const speed = active.reduce((n, j) => n + (j.speed || 0), 0);
    summary.textContent = active.length ? `${active.length} active · ${fmtBytes(remaining)} remaining${speed ? ` · ${fmtBytes(speed)}/s` : ''}` : 'Nothing in progress';
    clear(body);
    if (!jobs.length) { body.appendChild(h('p', { class: 'empty' }, 'No downloads yet. Choose resources in Discover.')); return; }
    body.appendChild(h('table', null,
      h('thead', null, h('tr', null, h('th', null, 'Resource'), h('th', null, 'State'), h('th', null, 'Progress'),
        h('th', { class: 'right hide-sm' }, 'Speed'), h('th', { class: 'right hide-sm' }, 'Remaining'), h('th', null, ''))),
      h('tbody', null, jobs.map((j) => {
        const pct = j.bytesTotal ? Math.min(100, (100 * j.bytesDone) / j.bytesTotal) : 0;
        const act = [];
        if (canChange) {
          if (['queued', 'resolving', 'downloading', 'waiting'].includes(j.state)) act.push(h('button', { onclick: () => action(j, 'pause') }, 'Pause'));
          if (['paused', 'waiting', 'failed'].includes(j.state)) act.push(h('button', { onclick: () => action(j, 'resume') }, j.state === 'failed' ? 'Retry' : 'Resume'));
          if (!['completed', 'cancelled', 'installing'].includes(j.state)) act.push(h('button', { class: 'danger', onclick: () => cancel(j) }, 'Cancel'));
          if (['completed', 'cancelled', 'failed'].includes(j.state)) act.push(h('button', { onclick: () => removeJob(j) }, 'Clear'));
        }
        return h('tr', null,
          h('td', { class: 'title-cell' }, h('b', null, j.title), h('span', { class: 'sub' },
            `${j.version}${j.replaces ? ` (replaces ${j.replaces})` : ''}`, j.mirror ? ` · from ${j.mirror}` : '')),
          h('td', null, h('span', { class: `state ${j.state}` }, STATE_TEXT[j.state] || j.state),
            j.state === 'waiting' && j.nextAttempt ? h('div', { class: 'small muted' }, `next try ${new Date(j.nextAttempt).toLocaleTimeString()}`) : null,
            j.error ? h('div', { class: 'small', style: 'color:var(--err);max-width:420px' }, j.error) : null),
          h('td', null, h('div', { class: 'progress', role: 'progressbar', 'aria-valuenow': Math.round(pct), 'aria-valuemin': 0, 'aria-valuemax': 100 },
            h('span', { style: `width:${pct}%` })),
            h('div', { class: 'small muted num' }, `${fmtBytes(j.bytesDone)} of ${fmtBytes(j.bytesTotal)}`)),
          h('td', { class: 'right num hide-sm' }, j.state === 'downloading' && j.speed ? `${fmtBytes(j.speed)}/s` : '—'),
          h('td', { class: 'right num hide-sm' }, j.state === 'downloading' ? fmtDuration(j.etaSeconds) : j.state === 'completed' ? fmtAgo(j.updatedAt) : '—'),
          h('td', { class: 'right' }, h('div', { class: 'row actions' }, act)));
      }))));
  }
  async function action(j, a) {
    try { await api.post(`/api/downloads/${encodeURIComponent(j.id)}/${a}`); load(); } catch (e) { toast(e.message); }
  }
  async function cancel(j) {
    if (!confirm(`Cancel downloading “${j.title}”? Downloaded data for it will be deleted. Installed content is not affected.`)) return;
    action(j, 'cancel');
  }
  async function removeJob(j) {
    try { await api.del(`/api/downloads/${encodeURIComponent(j.id)}`); load(); } catch (e) { toast(e.message); }
  }
  await load();
  const timer = setInterval(() => { if (!document.hidden) load(); }, 1500);
  return () => clearInterval(timer);
}
