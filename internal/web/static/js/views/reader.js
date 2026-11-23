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
    main.appendChild(h('div', { class: 'banner err' }, `This resource cannot be opened. ${r.openError || 'The archive reader is not available on this computer.'}`));
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
  const wantPage = Number(params.get('page')) || 0;
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
  // Browsers' PDF viewers open a PDF at #page=N.
  const original = (n) => contentURL(d.url) + (d.paged && n > 0 ? `#page=${n}` : '');
  const unit = d.paged ? 'page' : 'section';

  // Plain text only: each page (or section) gets a marker, and paragraphs
  // are rebuilt from blank lines.
  const textEl = h('div', { class: 'doc-text' });
  const paras = [];
  const markers = new Map();
  let lastHeading = '';
  for (const sec of d.sections) {
    let marker;
    if (d.paged) {
      marker = h('div', { class: 'page-mark', id: `page-${sec.n}` },
        h('span', { class: 'page-no' }, `Page ${sec.n}`),
        sec.heading ? h('span', { class: 'page-heading' }, sec.heading) : null,
        sec.text.trim() ? null : h('span', { class: 'muted' }, 'no text'),
        h('a', { href: original(sec.n), target: '_blank', rel: 'noopener', title: `Open page ${sec.n} of the original` }, 'Original'));
    } else if (sec.heading && sec.heading !== lastHeading) {
      marker = h('h3', { class: 'section-mark', id: `section-${sec.n}` }, sec.heading);
    } else {
      marker = h('span', { id: `section-${sec.n}` });
    }
    lastHeading = sec.heading || '';
    markers.set(sec.n, marker);
    textEl.appendChild(marker);
    for (const para of sec.text.split(/\n{2,}|\n(?=[A-Z0-9•\-–])/)) {
      const t = para.trim();
      if (!t) continue;
      const el = h('p', null, t);
      paras.push({ el, text: t, n: sec.n });
      textEl.appendChild(el);
    }
  }

  // Find in document: every match is marked; Enter and the arrows step
  // through them. When the words never occur together, each word is
  // marked on its own, as search matches words anywhere on a page.
  const findInput = h('input', { type: 'search', placeholder: 'Find in this document', 'aria-label': 'Find in this document', value: params.get('find') || '' });
  const findCount = h('span', { class: 'small muted', 'aria-live': 'polite' });
  const prevBtn = h('button', { 'aria-label': 'Previous match', title: 'Previous match (Shift+Enter)', disabled: true }, '↑');
  const nextBtn = h('button', { 'aria-label': 'Next match', title: 'Next match (Enter)', disabled: true }, '↓');
  const escapeRe = (x) => x.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  let marks = [];
  let cur = -1;
  let marked = new Set();
  let anyWord = false;
  function markAll(re) {
    for (const p of paras) {
      re.lastIndex = 0;
      if (!re.test(p.text)) continue;
      re.lastIndex = 0;
      const parts = [];
      let pos = 0;
      for (const m of p.text.matchAll(re)) {
        if (m.index > pos) parts.push(p.text.slice(pos, m.index));
        const el = h('mark', null, m[0]);
        marks.push({ el, n: p.n });
        parts.push(el);
        pos = m.index + m[0].length;
      }
      if (pos < p.text.length) parts.push(p.text.slice(pos));
      fill(p.el, parts);
      marked.add(p);
    }
  }
  function find(jump = true) {
    const q = findInput.value.trim();
    for (const p of marked) fill(p.el, p.text);
    marked = new Set();
    marks = [];
    cur = -1;
    anyWord = false;
    if (q.length >= 2) {
      markAll(new RegExp(escapeRe(q), 'gi'));
      const words = q.split(/\s+/).filter((w) => w.length >= 2);
      if (!marks.length && words.length > 1) {
        anyWord = true;
        words.sort((a, b) => b.length - a.length);
        markAll(new RegExp(words.map(escapeRe).join('|'), 'gi'));
      }
    }
    const what = anyWord ? ' of these words' : '';
    findCount.textContent = q.length < 2 ? '' : marks.length ? `${marks.length.toLocaleString()} match${marks.length === 1 ? '' : 'es'}${what}` : 'No matches';
    prevBtn.disabled = nextBtn.disabled = !marks.length;
    if (marks.length && jump) step(1);
  }
  function step(dir) {
    if (!marks.length) return;
    if (cur >= 0) marks[cur].el.classList.remove('current');
    cur = (cur + dir + marks.length) % marks.length;
    marks[cur].el.classList.add('current');
    marks[cur].el.scrollIntoView({ block: 'center' });
    findCount.textContent = `${cur + 1} of ${marks.length.toLocaleString()}${anyWord ? ' (any of the words)' : ''}`;
  }
  // goTo shows a page: its first match when there is one, else its top.
  function goTo(n) {
    const i = marks.findIndex((m) => m.n === n);
    if (i >= 0) {
      cur = i - 1;
      step(1);
      return;
    }
    const m = markers.get(n);
    if (m) m.scrollIntoView({ block: 'start' });
  }
  const findLater = debounce(() => find(), 200);
  findInput.addEventListener('input', findLater);
  findInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); if (cur < 0) find(); else step(e.shiftKey ? -1 : 1); }
  });
  prevBtn.addEventListener('click', () => step(-1));
  nextBtn.addEventListener('click', () => step(1));
  const count = d.sections.length;
  const pageInput = d.paged && count > 1 ? h('input', { type: 'number', min: 1, max: count, class: 'page-input', 'aria-label': 'Go to page',
    value: wantPage || '', placeholder: 'Page' }) : null;
  if (pageInput) {
    pageInput.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      const n = Math.min(Math.max(Math.round(Number(pageInput.value)), 1), count);
      if (n) goTo(n);
    });
  }
  const findBar = h('div', { class: 'toolbar find-bar', role: 'search' }, h('div', { class: 'grow' }, findInput), findCount, prevBtn, nextBtn,
    pageInput ? h('label', { class: 'small muted page-go' }, pageInput, ` of ${count.toLocaleString()}`) : null);

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('a', { class: 'button', href: '#/library' }, '← Library'), h('h2', null, d.title),
      h('a', { class: 'button', href: original(wantPage), target: '_blank', rel: 'noopener' }, wantPage && d.paged ? `Open original at page ${wantPage}` : 'Open original')),
    findBar,
    resource ? h('div', { class: 'toolbar small muted' }, resource.title, ` · ${resource.version} · published ${fmtDate(resource.published)} · `, licenseBadge(resource.license)) : null,
    resource ? (resource.warnings || []).map((w) => h('div', { class: `banner ${w.kind === 'medical' ? 'err' : 'warn'}`, style: 'margin:8px 12px' }, w.text)) : null,
    d.problem ? h('div', { class: 'banner warn', style: 'margin:8px 12px' }, d.problem) : null,
    h('div', { class: 'body' },
      h('p', { class: 'small muted' }, `Text extracted for reading and search, ${unit} by ${unit}. Layout, images and tables appear only in the original.`),
      textEl,
      d.truncated ? h('p', { class: 'small muted' }, 'Preview shortened. Open the original for the full document.') : null)));
  // Arriving from a search result: the matches are marked, and the view
  // goes to the page the result named.
  setTimeout(() => {
    if (findInput.value) find(!wantPage);
    if (wantPage) goTo(wantPage);
  }, 0);
  return null;
}
