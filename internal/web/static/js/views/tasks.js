import { api, getSession } from '../api.js';
import { h, fill, fmtBytes, fmtAgo, toast } from '../util.js';

const STATE = { running: 'Running', succeeded: 'Done', failed: 'Failed', cancelled: 'Cancelled' };

// tasksPanel lists background operations (transfers, full checks, map
// extraction, application updates) and refreshes while visible.
export function tasksPanel() {
  const s = getSession();
  const body = h('div');
  const panel = h('section', { class: 'panel', hidden: true },
    h('header', null, h('h2', null, 'Background tasks')), body);
  async function load() {
    let tasks = [];
    try { ({ tasks } = await api.get('/api/tasks')); } catch (_) { return; }
    panel.hidden = tasks.length === 0;
    fill(body, tasks.map((t) => {
      const pct = t.total > 0 ? Math.min(100, (100 * t.done) / t.total) : null;
      return h('div', { class: 'task' },
        h('div', { class: 'row' }, h('b', { style: 'flex:1' }, t.title),
          h('span', { class: `state ${t.state === 'failed' ? 'failed' : t.state === 'succeeded' ? 'completed' : ''}` }, STATE[t.state] || t.state),
          t.state === 'running' && s.admin ? h('button', { onclick: async () => { try { await api.post(`/api/tasks/${t.id}/cancel`); } catch (e) { toast(e.message); } } }, 'Cancel') : null),
        t.state === 'running' && pct !== null ? h('div', { class: 'progress', style: 'margin:4px 0' }, h('span', { style: `width:${pct}%` })) : null,
        h('div', { class: 'small muted' }, [
          t.message,
          t.total > 0 ? `${fmtBytes(t.done)} of ${fmtBytes(t.total)}` : null,
          t.finished && t.state !== 'running' ? `finished ${fmtAgo(t.finished)}` : `started ${fmtAgo(t.started)}`,
        ].filter(Boolean).join(' · ')),
        t.error ? h('div', { class: 'small', style: 'color:var(--err)' }, t.error) : null,
        t.state === 'succeeded' && t.result ? resultLine(t.result) : null);
    }));
  }
  load();
  const timer = setInterval(() => { if (!document.hidden) load(); }, 1500);
  return { el: panel, stop: () => clearInterval(timer), refresh: load };
}

function resultLine(r) {
  if (typeof r === 'string') return h('div', { class: 'small' }, r);
  const parts = [];
  if (r.resources !== undefined && r.destination) parts.push(`Exported ${r.resources} resources (${fmtBytes(r.bytes)}) to ${r.destination}${r.skipped ? `, ${r.skipped} already up to date` : ''}.`);
  if (r.installed) parts.push(`Installed ${r.installed.length}; skipped ${(r.skipped || []).length} identical or older${r.userDocuments ? `; ${r.userDocuments} documents added` : ''}.`);
  if (r.problems) parts.push(r.problems.length ? `${r.problems.length} problem(s) found: ${r.problems.slice(0, 3).map((p) => `${p.resourceId} ${p.kind}`).join(', ')}` : `No problems in ${r.files} files.`);
  if (r.warnings?.length) parts.push(...r.warnings);
  return parts.length ? h('div', { class: 'small' }, parts.join(' ')) : null;
}
