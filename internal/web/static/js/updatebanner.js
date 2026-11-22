// The "new version available" banner: one button downloads, verifies and
// installs the update, then waits for Arcana to restart and reloads.
import { api } from './api.js';
import { h, fill } from './util.js';

let busy = false;

function dismissedKey(v) { return `arcana-update-later-${v}`; }

function isDismissed(v) {
  try { return sessionStorage.getItem(dismissedKey(v)) === '1'; } catch (_) { return false; }
}

async function waitForRestart(el, version) {
  fill(el, h('span', null, `Arcana is restarting into version ${version}…`));
  const deadline = Date.now() + 120000;
  // Give the old process time to stop before polling.
  await new Promise((r) => setTimeout(r, 2500));
  while (Date.now() < deadline) {
    try {
      const r = await fetch('/api/session', { cache: 'no-store' });
      if (r.ok) {
        const s = await r.json();
        if (s.version === version) { location.reload(); return; }
      }
    } catch (_) { /* still restarting */ }
    await new Promise((r) => setTimeout(r, 1000));
  }
  fill(el, h('span', null, 'Arcana did not come back by itself. Close it and start it again.'));
}

async function install(el, n) {
  busy = true;
  const msg = h('span', null, 'Starting the update…');
  fill(el, msg);
  let task;
  try {
    task = await api.post('/api/app/update/install');
  } catch (e) {
    busy = false;
    fill(el, h('span', null, e.message));
    return;
  }
  for (;;) {
    await new Promise((r) => setTimeout(r, 800));
    let t;
    try {
      t = (await api.get('/api/tasks')).tasks.find((x) => x.id === task.id);
    } catch (_) { continue; }
    if (!t) continue;
    if (t.state === 'running') {
      const pct = t.total ? ` ${Math.floor((100 * t.done) / t.total)}%` : '';
      msg.textContent = `${t.message || 'Updating'}${pct}…`;
      continue;
    }
    busy = false;
    if (t.state === 'succeeded' && t.result && t.result.restarting) {
      await waitForRestart(el, t.result.version || n.available);
    } else if (t.state === 'succeeded') {
      fill(el, h('span', null, (t.result && t.result.message) || 'Installed. Restart Arcana to use it.'));
    } else {
      fill(el, h('span', null, `The update was not installed: ${t.error || t.state}. Arcana is unchanged.`),
        h('button', { onclick: () => install(el, n) }, 'Try again'));
    }
    return;
  }
}

// renderUpdateBanner shows or hides the banner for the dashboard status.
export function renderUpdateBanner(status) {
  let el = document.getElementById('update-banner');
  if (!el) {
    el = h('div', { id: 'update-banner', class: 'update-banner', role: 'status', 'aria-live': 'polite', hidden: true });
    document.querySelector('.topbar').after(el);
  }
  if (busy) return;
  const n = status && status.appUpdate;
  const later = (v) => h('button', { class: 'link', onclick: () => {
    try { sessionStorage.setItem(dismissedKey(v), '1'); } catch (_) { /* ignore */ }
    el.hidden = true;
  } }, 'Later');
  if (n && !n.available && n.installed && !isDismissed(n.installed)) {
    // Installed on the drive, but this copy could not restart itself.
    el.hidden = false;
    fill(el, h('span', null, h('b', null, `Arcana ${n.installed} is installed.`), ' Close Arcana and start it again to use it.'),
      later(n.installed));
    return;
  }
  if (!n || !n.available || isDismissed(n.available)) { el.hidden = true; return; }
  el.hidden = false;
  if (n.installing) { fill(el, h('span', null, `Installing Arcana ${n.available}…`)); return; }
  const notes = n.notes ? h('details', null, h('summary', null, 'What’s new'), h('p', { class: 'note-text' }, n.notes)) : null;
  fill(el,
    h('span', null, h('b', null, `Arcana ${n.available} is available`), ` (you have ${n.current}).`),
    n.selfUpdate.possible
      ? h('button', { class: 'primary', onclick: () => install(el, n) }, 'Update now')
      : h('span', { class: 'small' }, n.selfUpdate.reason),
    later(n.available),
    notes);
}
