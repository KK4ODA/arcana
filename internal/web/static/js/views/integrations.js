// Settings panels for programs that use Arcana's integration API and for
// other Arcana libraries to copy from. Host only.
import { api } from '../api.js';
import { h, fill, fmtBytes, fmtAgo, toast } from '../util.js';

export function integrationsPanel() {
  const body = h('div', { class: 'body' }, h('p', { class: 'small muted' }, 'Loading…'));
  const el = h('section', { class: 'panel' }, h('header', null, h('h2', null, 'Integrations')), body);
  let timer = null;

  async function render() {
    let d;
    try { d = await api.get('/api/integrations'); } catch (e) { fill(body, h('div', { class: 'banner err' }, e.message)); return; }
    const pending = d.pending.length ? h('div', { class: 'banner warn' },
      h('b', null, 'Programs asking for access'),
      d.pending.map((p) => h('div', { class: 'row', style: 'margin-top:6px' },
        h('span', null, h('b', null, p.name), ` from ${p.addr} · code `, h('span', { class: 'mono' }, p.code), ` · ${p.scopes.join(', ')}`),
        h('button', { class: 'primary', onclick: () => decide(p, 'approve') }, 'Approve'),
        h('button', { onclick: () => decide(p, 'deny') }, 'Deny'))),
      h('p', { class: 'small' }, 'Approve only if the program shows the same code.')) : null;

    const clients = d.clients.length ? h('table', null,
      h('thead', null, h('tr', null, h('th', null, 'Program'), h('th', null, 'Permissions'), h('th', { class: 'hide-sm' }, 'Last used'), h('th', null, ''))),
      h('tbody', null, d.clients.map((c) => h('tr', null,
        h('td', { class: 'title-cell' }, h('b', null, c.name), h('span', { class: 'sub mono' }, `${c.prefix}…`)),
        h('td', { class: 'small' }, c.scopes.join(', ')),
        h('td', { class: 'small muted hide-sm' }, c.lastUsed ? fmtAgo(c.lastUsed) : 'never'),
        h('td', { class: 'right' }, h('button', { class: 'danger', onclick: () => revoke(c) }, 'Revoke'))))))
      : h('p', { class: 'small muted' }, 'No programs have access.');

    const name = h('input', { type: 'text', placeholder: 'Program name, e.g. RadioPost', maxlength: 80, style: 'width:220px' });
    const boxes = d.scopes.map((s) => ({ s, cb: h('input', { type: 'checkbox', checked: s.id === 'search:read' || s.id === 'documents:read' }) }));
    const out = h('div');
    const create = h('button', { onclick: async () => {
      const scopes = boxes.filter((b) => b.cb.checked).map((b) => b.s.id);
      try {
        const r = await api.post('/api/integrations', { name: name.value.trim(), scopes });
        const tokenEl = h('input', { type: 'text', readonly: true, value: r.token, class: 'mono', style: 'width:min(560px,100%)' });
        fill(out, h('div', { class: 'banner info' },
          h('div', null, 'Access token for ', h('b', null, r.client.name), '. Copy it now; it is not shown again.'),
          tokenEl,
          h('div', { class: 'row' }, h('button', { onclick: () => { tokenEl.select(); navigator.clipboard?.writeText(r.token).then(() => toast('Copied')); } }, 'Copy'))));
        name.value = '';
        refreshList();
      } catch (e) { toast(e.message); }
    } }, 'Create token');

    fill(body,
      h('p', { class: 'small muted' }, `Other programs, such as RadioPost or Aether HF, can search the library, read articles with their source and license, look up coordinates and read the GPS position through a separate, versioned API (${d.endpoint}). They can never change the library. A program either asks for access and you approve it here, or you create a token and paste it into the program.`),
      pending, clients,
      h('h3', { style: 'margin-top:12px' }, 'New token'),
      h('div', { class: 'row' }, name),
      h('div', { class: 'scope-list' }, boxes.map(({ s, cb }) => h('label', { class: 'inline' }, cb, h('span', null, h('span', { class: 'mono small' }, s.id), ' — ', s.description)))),
      h('div', { class: 'row' }, create), out);
  }
  // The list part refreshes without discarding a freshly shown token.
  async function refreshList() {
    const keep = body.querySelector('.banner.info');
    await render();
    if (keep) body.appendChild(keep);
  }
  async function decide(p, d) {
    try { await api.post(`/api/integrations/pair/${encodeURIComponent(p.id)}/${d}`); toast(d === 'approve' ? `${p.name} can now use Arcana.` : 'Request denied.'); render(); } catch (e) { toast(e.message); render(); }
  }
  async function revoke(c) {
    if (!confirm(`Revoke access for “${c.name}”?`)) return;
    try { await api.del(`/api/integrations/${encodeURIComponent(c.id)}`); render(); } catch (e) { toast(e.message); }
  }
  render();
  // Pairing requests arrive while the page is open.
  timer = setInterval(async () => {
    try {
      const d = await api.get('/api/integrations');
      if (d.pending.length && !body.querySelector('.banner.warn')) render();
    } catch (_) { /* ignore */ }
  }, 4000);
  return { el, stop: () => clearInterval(timer) };
}

export function peersPanel() {
  const body = h('div', { class: 'body' });
  const el = h('section', { class: 'panel' }, h('header', null, h('h2', null, 'Other Arcana libraries')), body);

  async function render() {
    let d;
    try { d = await api.get('/api/peers'); } catch (e) { fill(body, h('div', { class: 'banner err' }, e.message)); return; }
    const name = h('input', { type: 'text', placeholder: 'Name', maxlength: 80, style: 'width:150px' });
    const url = h('input', { type: 'url', placeholder: 'http://192.168.1.20:8470', style: 'width:230px' });
    const token = h('input', { type: 'text', placeholder: 'arc1_… token with library:read', class: 'mono', style: 'width:260px' });
    const add = h('button', { onclick: async () => {
      try {
        const r = await api.post('/api/peers', { name: name.value.trim(), url: url.value.trim(), token: token.value.trim() });
        toast(r.warning ? `Added, but: ${r.warning}` : `Added: ${r.resources} resource(s) available.`);
        render();
      } catch (e) { toast(e.message); }
    } }, 'Add');
    fill(body,
      h('p', { class: 'small muted' }, 'Copy archives and maps from another Arcana on the same network, without internet. On the other library, create a token with the permission library:read (Settings → Integrations) and enter it here. Copies are verified against that library’s checksums, and against the publisher’s when this library’s catalog has them.'),
      d.peers.length ? h('ul', { class: 'map-list' }, d.peers.map((p) => h('li', null,
        h('button', { class: 'linkish', onclick: () => browse(p) }, p.name),
        h('span', { class: 'sub mono' }, p.url),
        h('span', { class: 'row' }, h('button', { onclick: () => browse(p) }, 'Browse'),
          h('button', { class: 'icon danger', title: `Remove ${p.name}`, 'aria-label': `Remove ${p.name}`, onclick: () => remove(p) }, '×'))))) : null,
      h('div', { class: 'row' }, name, url, token, add));
  }

  async function remove(p) {
    if (!confirm(`Forget “${p.name}”? Resources already copied stay.`)) return;
    try { await api.del(`/api/peers/${encodeURIComponent(p.id)}`); render(); } catch (e) { toast(e.message); }
  }

  async function browse(p) {
    const dlg = h('dialog', { class: 'detail', 'aria-label': `Resources on ${p.name}` });
    const content = h('div', { class: 'body' }, h('p', { class: 'small muted' }, `Asking ${p.name}…`));
    const copy = h('button', { class: 'primary', disabled: true }, 'Copy selected');
    const summary = h('span', { class: 'small muted' });
    dlg.append(h('header', null, h('h2', null, `Copy from ${p.name}`), h('button', { onclick: () => dlg.close() }, 'Close')), content,
      h('footer', null, summary, copy));
    dlg.addEventListener('close', () => dlg.remove());
    document.body.appendChild(dlg);
    dlg.showModal();
    let lib;
    try { lib = await api.get(`/api/peers/${encodeURIComponent(p.id)}/library`); } catch (e) { fill(content, h('div', { class: 'banner err' }, e.message)); return; }
    const label = { new: 'Not here', update: 'Newer edition', installed: 'Same edition here', older: 'Older than yours' };
    const rows = lib.resources.map((r) => {
      const size = r.files.reduce((n, f) => n + f.size, 0);
      const cb = h('input', { type: 'checkbox', checked: r.status === 'new' || r.status === 'update', disabled: r.status === 'installed' || r.status === 'older', 'aria-label': `Copy ${r.resource.title}` });
      cb.addEventListener('change', update);
      return { r, cb, size, tr: h('tr', null,
        h('td', null, cb),
        h('td', { class: 'title-cell' }, h('b', null, r.resource.title), h('span', { class: 'sub' }, `${r.version}${r.localVersion ? ` (here: ${r.localVersion})` : ''}${r.unverified ? ' · unverified source' : ''}`)),
        h('td', { class: 'small' }, label[r.status] || r.status),
        h('td', { class: 'right num' }, fmtBytes(size))) };
    });
    function update() {
      const sel = rows.filter((x) => x.cb.checked);
      summary.textContent = sel.length ? `${sel.length} selected · ${fmtBytes(sel.reduce((n, x) => n + x.size, 0))}` : 'Nothing selected';
      copy.disabled = !sel.length;
    }
    fill(content, rows.length ? h('table', null,
      h('thead', null, h('tr', null, h('th', null, ''), h('th', null, 'Resource'), h('th', null, 'Status'), h('th', { class: 'right' }, 'Size'))),
      h('tbody', null, rows.map((x) => x.tr))) : h('p', { class: 'empty' }, `${p.name} has nothing installed.`));
    update();
    copy.addEventListener('click', async () => {
      const ids = rows.filter((x) => x.cb.checked).map((x) => x.r.resource.id);
      copy.disabled = true;
      try {
        const r = await api.post(`/api/peers/${encodeURIComponent(p.id)}/pull`, { ids });
        const failed = Object.entries(r.results).filter(([, v]) => v !== 'queued');
        toast(failed.length ? `Some could not be queued: ${failed.map(([k, v]) => `${k}: ${v}`).join('; ')}` : 'Queued. Progress is shown in Downloads.');
        dlg.close();
      } catch (e) { toast(e.message); copy.disabled = false; }
    });
  }
  render();
  return { el };
}
