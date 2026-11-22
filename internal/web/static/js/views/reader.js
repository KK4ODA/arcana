import { api, contentURL } from '../api.js';
import { h, fmtDate, licenseBadge } from '../util.js';

// Reader: an Arcana header with provenance and warnings above the archive,
// which is loaded from the separate content origin.
export async function readerView(main, id, params) {
  const lib = await api.get('/api/library');
  const r = lib.resources.find((x) => x.resourceId === id);
  if (!r) {
    main.appendChild(h('div', { class: 'banner err' }, 'This resource is not installed.'));
    return null;
  }
  if (!r.openPath) {
    main.appendChild(h('div', { class: 'banner err' }, 'This resource cannot be opened: the archive reader is not available on this computer.'));
    return null;
  }
  let path = r.openPath;
  const p = params.get('path');
  if (p && p.startsWith('/kiwix/content/')) path = p;
  const src = contentURL(path);
  document.title = `${r.title} — Arcana`;
  const reader = h('div', { class: 'reader' },
    h('header', null,
      h('a', { class: 'button', href: '#/library' }, '← Library'),
      h('div', { class: 'title' }, h('b', null, r.title),
        h('span', { class: 'small muted' }, `${r.version} · published ${fmtDate(r.published)} · `, licenseBadge(r.license),
          r.publisher ? ` · ${r.publisher}` : '')),
      h('a', { class: 'button', href: src, target: '_blank', rel: 'noopener' }, 'Open in new tab')),
    (r.warnings || []).map((w) => h('div', { class: `notice ${w.kind}`, role: 'note' }, w.text)),
    h('iframe', { src, title: r.title, referrerpolicy: 'no-referrer' }));
  document.body.appendChild(reader);
  const onKey = (e) => { if (e.key === 'Escape' && document.activeElement === document.body) location.hash = '#/library'; };
  document.addEventListener('keydown', onKey);
  return () => { reader.remove(); document.removeEventListener('keydown', onKey); };
}
