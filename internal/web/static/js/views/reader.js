import { api, contentURL, getSession } from '../api.js';
import { h, clear, fill, fmtDate, fmtAgo, licenseBadge, toast, debounce } from '../util.js';
import { noteEditor } from './library.js';
import { titleSearchBox, highlight, snippet, setMore } from '../search.js';

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
  const searchPanel = h('aside', { class: 'notes-panel search-panel', hidden: true, 'aria-label': `Search results in ${r.title}` });
  const searchInput = h('input', { type: 'search', placeholder: `Search in ${r.title}`, 'aria-label': `Search in ${r.title}` });
  const searchBox = titleSearchBox(searchInput, {
    fetchTitles: async (q) => (await api.get(`/api/suggest?resource=${encodeURIComponent(id)}&q=${encodeURIComponent(q)}&limit=8`)).titles,
    onPick: (t) => goTo(t.path),
    onSubmit: (q) => runSearch(q),
  });
  searchBox.classList.add('reader-search');
  const reader = h('div', { class: 'reader' },
    h('header', null,
      h('a', { class: 'button', href: '#/library' }, '← Library'),
      h('div', { class: 'title' }, h('b', null, r.title),
        h('span', { class: 'small muted' }, `${r.version} · published ${fmtDate(r.published)} · `, licenseBadge(r.license),
          r.publisher ? ` · ${r.publisher}` : ''), h('div', null, pageTitle)),
      searchBox,
      canChange ? bookmarkBtn : null, s.local ? notesBtn : null,
      h('a', { class: 'button', href: src, target: '_blank', rel: 'noopener' }, 'Open in new tab')),
    (r.warnings || []).map((w) => h('div', { class: `notice ${w.kind}`, role: 'note' }, w.text)),
    h('div', { class: 'reader-body' }, iframe, searchPanel, notesPanel));
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
    if (!notesPanel.hidden) { searchPanel.hidden = true; renderNotes(); }
  }

  // ------------------------------------------------------------ search
  // Titles complete as you type; Enter lists full-text matches in this
  // archive beside the page, a page of results at a time.
  const narrow = matchMedia('(max-width: 820px)');
  function goTo(p) {
    iframe.src = contentURL(p);
    if (narrow.matches) searchPanel.hidden = true;
    iframe.focus();
  }

  let searchSeq = 0;
  async function runSearch(q) {
    const mine = ++searchSeq;
    notesPanel.hidden = true;
    searchPanel.hidden = false;
    const close = h('button', { onclick: () => { searchPanel.hidden = true; searchInput.focus(); } }, 'Close');
    const head = h('div', { class: 'row' }, h('h2', { class: 'grow' }, `“${q}”`), close);
    fill(searchPanel, head, h('p', { class: 'small muted' }, 'Searching…'));
    const base = `/api/search?resource=${encodeURIComponent(id)}&q=${encodeURIComponent(q)}&limit=20`;
    let res;
    try { res = await api.get(base); } catch (e) {
      if (mine === searchSeq) fill(searchPanel, head, h('div', { class: 'banner err' }, e.message));
      return;
    }
    if (mine !== searchSeq) return;
    const titleHits = res.titles || [];
    const item = (it) => h('li', null,
      h('a', { href: '#', onclick: (e) => { e.preventDefault(); goTo(it.path); } }, it.title), snippet(it.snippet));
    const list = h('ul', { class: 'results' }, res.results.map(item));
    const more = h('div');
    fill(searchPanel, head,
      titleHits.length ? h('div', null, h('h3', null, 'Matching titles'),
        h('ul', { class: 'title-hits' }, titleHits.map((t) => h('li', null,
          h('a', { href: '#', onclick: (e) => { e.preventDefault(); goTo(t.path); } }, highlight(t.title, q)))))) : null,
      res.error ? h('div', { class: 'banner warn' }, res.error) : null,
      res.notSearchable
        ? h('p', { class: 'small muted' }, titleHits.length
          ? 'This archive has no full-text index, so only titles are searched.'
          : 'No titles match, and this archive has no full-text index to search inside pages. Try another word.')
        : h('div', null,
          h('h3', null, res.total ? `${res.total.toLocaleString()} page${res.total === 1 ? '' : 's'} mention it` : 'No page mentions it'),
          list, more));
    let shown = res.results.length;
    const next = () => setMore(more, res.total - shown, 'Show more results', async () => {
      const p = await api.get(`${base}&start=${shown}`);
      p.results.forEach((it) => list.appendChild(item(it)));
      shown = p.results.length ? shown + p.results.length : res.total;
      next();
    });
    next();
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

  const onKey = (e) => {
    if (document.activeElement !== document.body) return;
    if (e.key === '/') { e.preventDefault(); searchInput.focus(); return; }
    if (e.key !== 'Escape') return;
    if (!searchPanel.hidden) searchPanel.hidden = true;
    else location.hash = '#/library';
  };
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
  const paras = [];
  for (const para of d.text.split(/\n{2,}|\n(?=[A-Z0-9•\-–])/)) {
    const t = para.trim();
    if (!t) continue;
    const el = h('p', null, t);
    paras.push({ el, text: t, lower: t.toLowerCase() });
    textEl.appendChild(el);
  }

  // Find in document: every match is marked; Enter and the arrows step
  // through them.
  const findInput = h('input', { type: 'search', placeholder: 'Find in this document', 'aria-label': 'Find in this document', value: params.get('find') || '' });
  const findCount = h('span', { class: 'small muted', 'aria-live': 'polite' });
  const prevBtn = h('button', { 'aria-label': 'Previous match', title: 'Previous match (Shift+Enter)', disabled: true }, '↑');
  const nextBtn = h('button', { 'aria-label': 'Next match', title: 'Next match (Enter)', disabled: true }, '↓');
  let marks = [];
  let cur = -1;
  let marked = new Set();
  function find() {
    const q = findInput.value.trim().toLowerCase();
    for (const p of marked) fill(p.el, p.text);
    marked = new Set();
    marks = [];
    cur = -1;
    if (q.length >= 2) {
      for (const p of paras) {
        let i = p.lower.indexOf(q);
        if (i < 0) continue;
        const parts = [];
        let pos = 0;
        while (i >= 0) {
          if (i > pos) parts.push(p.text.slice(pos, i));
          const m = h('mark', null, p.text.slice(i, i + q.length));
          marks.push(m);
          parts.push(m);
          pos = i + q.length;
          i = p.lower.indexOf(q, pos);
        }
        if (pos < p.text.length) parts.push(p.text.slice(pos));
        fill(p.el, parts);
        marked.add(p);
      }
    }
    findCount.textContent = q.length < 2 ? '' : marks.length ? `${marks.length.toLocaleString()} match${marks.length === 1 ? '' : 'es'}` : 'No matches';
    prevBtn.disabled = nextBtn.disabled = !marks.length;
    if (marks.length) step(1);
  }
  function step(dir) {
    if (!marks.length) return;
    if (cur >= 0) marks[cur].classList.remove('current');
    cur = (cur + dir + marks.length) % marks.length;
    marks[cur].classList.add('current');
    marks[cur].scrollIntoView({ block: 'center' });
    findCount.textContent = `${cur + 1} of ${marks.length.toLocaleString()}`;
  }
  const findLater = debounce(find, 200);
  findInput.addEventListener('input', findLater);
  findInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); if (cur < 0) find(); else step(e.shiftKey ? -1 : 1); }
  });
  prevBtn.addEventListener('click', () => step(-1));
  nextBtn.addEventListener('click', () => step(1));
  const findBar = h('div', { class: 'toolbar find-bar', role: 'search' }, h('div', { class: 'grow' }, findInput), findCount, prevBtn, nextBtn);
  if (findInput.value) setTimeout(find, 0);

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('a', { class: 'button', href: '#/library' }, '← Library'), h('h2', null, d.title),
      h('a', { class: 'button', href: contentURL(d.url), target: '_blank', rel: 'noopener' }, 'Open original')),
    findBar,
    resource ? h('div', { class: 'toolbar small muted' }, resource.title, ` · ${resource.version} · published ${fmtDate(resource.published)} · `, licenseBadge(resource.license)) : null,
    resource ? (resource.warnings || []).map((w) => h('div', { class: `banner ${w.kind === 'medical' ? 'err' : 'warn'}`, style: 'margin:8px 12px' }, w.text)) : null,
    h('div', { class: 'body' },
      h('p', { class: 'small muted' }, 'Text extracted for reading and search. Layout, images and tables appear only in the original.'),
      textEl,
      d.truncated ? h('p', { class: 'small muted' }, 'Preview shortened. Open the original for the full document.') : null)));
  return null;
}
