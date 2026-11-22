import { api, contentURL, getSession } from '../api.js';
import { h, clear, fill, fmtDate, fmtAgo, licenseBadge, toast } from '../util.js';
import { noteEditor } from './library.js';

// Reader: an Arcana header with provenance and warnings above the archive,
// which is loaded from the separate content origin. Pages inside the archive
// report their location (see /_arcana/frame.js), which enables bookmarks,
// notes about the current page, and reading history.
export async function readerView(main, id, params) {
  const s = getSession();
  const canChange = s.admin && !s.readOnly;
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
  // Documents (PDF and others) open in the document view.
  if (r.openPath.startsWith('/files/')) {
    location.replace(`#/doc?key=${encodeURIComponent(`res:${r.resourceId}/${r.openPath.split('/').pop()}`)}&resource=${encodeURIComponent(id)}`);
    return null;
  }
  let path = r.openPath;
  const p = params.get('path');
  const page = params.get('page');
  if (p && p.startsWith('/kiwix/content/')) path = p;
  else if (page) path = r.openPath + page.replace(/^\/+/, '');
  const src = contentURL(path);
  const contentOrigin = new URL(src).origin;
  document.title = `${r.title} — Arcana`;

  let current = { path: '', title: r.title };
  const bookmarkBtn = h('button', { disabled: true, onclick: bookmark }, 'Bookmark');
  const notesBtn = h('button', { onclick: toggleNotes, disabled: !s.local }, 'Notes');
  const pageTitle = h('span', { class: 'small muted' });
  const iframe = h('iframe', { src, title: r.title, referrerpolicy: 'no-referrer' });
  const notesPanel = h('aside', { class: 'notes-panel', hidden: true });
  const reader = h('div', { class: 'reader' },
    h('header', null,
      h('a', { class: 'button', href: '#/library' }, '← Library'),
      h('div', { class: 'title' }, h('b', null, r.title),
        h('span', { class: 'small muted' }, `${r.version} · published ${fmtDate(r.published)} · `, licenseBadge(r.license),
          r.publisher ? ` · ${r.publisher}` : ''), h('div', null, pageTitle)),
      canChange ? bookmarkBtn : null, s.local ? notesBtn : null,
      h('a', { class: 'button', href: src, target: '_blank', rel: 'noopener' }, 'Open in new tab')),
    (r.warnings || []).map((w) => h('div', { class: `notice ${w.kind}`, role: 'note' }, w.text)),
    h('div', { class: 'reader-body' }, iframe, notesPanel));
  document.body.appendChild(reader);

  const prefix = r.openPath; // e.g. /kiwix/content/<book>/
  function relPath(p) {
    if (!p.startsWith(prefix)) {
      // The book name can carry a different edition date; keep the part
      // after the book segment.
      const m = p.match(/^\/kiwix\/content\/[^/]+\/(.*)$/);
      return m ? m[1] : '';
    }
    return p.slice(prefix.length);
  }

  let lastRecorded = '';
  const onMessage = (e) => {
    if (e.origin !== contentOrigin || e.source !== iframe.contentWindow) return;
    const d = e.data;
    if (!d || d.type !== 'arcana:location' || typeof d.path !== 'string' || d.path.length > 2000) return;
    const rel = relPath(d.path);
    current = { path: rel, title: typeof d.title === 'string' ? d.title.slice(0, 300) : '' };
    pageTitle.textContent = current.title && current.title !== r.title ? current.title : '';
    bookmarkBtn.disabled = !rel;
    if (canChange && rel && rel !== lastRecorded) {
      lastRecorded = rel;
      api.post('/api/user/recent', { resourceId: id, path: rel, title: current.title }).catch(() => {});
    }
    if (!notesPanel.hidden) renderNotes();
  };
  window.addEventListener('message', onMessage);

  async function bookmark() {
    try {
      await api.post('/api/user/bookmarks', { resourceId: id, path: current.path, title: current.title || r.title });
      toast('Bookmarked. Find it under Library → Bookmarks.');
    } catch (e) { toast(e.message); }
  }

  function toggleNotes() {
    notesPanel.hidden = !notesPanel.hidden;
    if (!notesPanel.hidden) renderNotes();
  }

  async function renderNotes() {
    let notes = [];
    try { ({ notes } = await api.get(`/api/user/notes?resource=${encodeURIComponent(id)}`)); } catch (_) { /* host only */ }
    fill(notesPanel,
      h('div', { class: 'row' }, h('h2', { style: 'flex:1' }, 'Notes'), h('button', { onclick: toggleNotes }, 'Close')),
      canChange ? h('div', null, h('p', { class: 'small muted' }, current.path ? `New note about “${current.title || current.path}”` : 'New note about this resource'),
        noteEditor(null, renderNotes, { resourceId: id, path: current.path, title: current.title })) : null,
      notes.length ? h('ul', { class: 'results' }, notes.map((n) => h('li', null,
        h('b', null, n.title || 'Untitled'), h('div', { class: 'src' }, `${n.path || 'whole resource'} · ${fmtAgo(n.updatedAt)}`),
        n.path ? h('a', { href: '#', onclick: (e) => { e.preventDefault(); iframe.src = contentURL(prefix + n.path); } }, 'Go to page') : null,
        h('div', { class: 'note-text' }, n.text)))) : h('p', { class: 'small muted' }, 'No notes about this resource yet.'));
  }

  const onKey = (e) => { if (e.key === 'Escape' && document.activeElement === document.body) location.hash = '#/library'; };
  document.addEventListener('keydown', onKey);
  return () => {
    reader.remove();
    document.removeEventListener('keydown', onKey);
    window.removeEventListener('message', onMessage);
  };
}

// documentView shows the extracted text of a PDF, EPUB or other document,
// with a link to the original file on the content origin.
export async function documentView(main, params) {
  const key = params.get('key') || '';
  let d;
  try {
    d = await api.get(`/api/documents/text?key=${encodeURIComponent(key)}`);
  } catch (e) {
    main.appendChild(h('div', { class: 'banner warn' }, 'This document has not been indexed yet. It is indexed in the background shortly after installation; try again in a moment.'));
    return null;
  }
  let resource = null;
  if (key.startsWith('res:')) {
    const lib = await api.get('/api/library');
    resource = lib.resources.find((x) => key.startsWith(`res:${x.resourceId}/`));
  }
  document.title = `${d.title} — Arcana`;
  const textEl = h('div', { class: 'doc-text' });
  // Plain text only: paragraphs are rebuilt from blank lines.
  for (const para of d.text.split(/\n{2,}|\n(?=[A-Z0-9•\-–])/)) {
    if (para.trim()) textEl.appendChild(h('p', null, para.trim()));
  }
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('a', { class: 'button', href: '#/library' }, '← Library'), h('h2', null, d.title),
      h('a', { class: 'button', href: contentURL(d.url), target: '_blank', rel: 'noopener' }, 'Open original')),
    resource ? h('div', { class: 'toolbar small muted' }, resource.title, ` · ${resource.version} · published ${fmtDate(resource.published)} · `, licenseBadge(resource.license)) : null,
    resource ? (resource.warnings || []).map((w) => h('div', { class: `banner ${w.kind === 'medical' ? 'err' : 'warn'}`, style: 'margin:8px 12px' }, w.text)) : null,
    h('div', { class: 'body' },
      h('p', { class: 'small muted' }, 'Text extracted for reading and search. Layout, images and tables appear only in the original.'),
      textEl,
      d.truncated ? h('p', { class: 'small muted' }, 'Preview shortened. Open the original for the full document.') : null)));
  return null;
}
