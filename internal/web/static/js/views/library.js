import { api, getSession } from '../api.js';
import { h, clear, fill, fmtBytes, fmtAgo, fmtDate, langName, licenseBadge, warningBadges, storageBar, toast } from '../util.js';
import { titleSearchBox, highlight, snippet, setMore } from '../search.js';

const CATEGORY_TITLES = {};

// Library: the dashboard (status readouts and attention list), full-text
// search across installed archives and documents, and the user's library:
// installed resources, bookmarks, history, notes and documents.
export async function libraryView(main, params, ctx) {
  const s = getSession();
  const canChange = s.admin && !s.readOnly;
  const [status, lib, facets, favs] = await Promise.all([
    ctx.refreshStatus(), api.get('/api/library'), api.get('/api/catalog/facets').catch(() => null),
    api.get('/api/user/favorites').catch(() => ({ favorites: [] })),
  ]);
  if (facets) for (const c of facets.categories) CATEGORY_TITLES[c.id] = c.title;
  const favorites = new Set(favs.favorites);
  const titles = Object.fromEntries(lib.resources.map((r) => [r.resourceId, r.title]));

  if (status?.library.readOnly) {
    main.appendChild(h('div', { class: 'banner warn' }, `Read-only: ${status.library.readOnlyReason}. You can browse and search; downloads and changes are disabled.`));
  }
  if (status?.welcome?.pending && canChange) {
    main.appendChild(h('div', { class: 'banner info' }, h('b', null, 'Getting started: '),
      'choose what this drive should hold, and Arcana suggests editions that fit. ',
      h('a', { class: 'button primary', href: '#/welcome' }, 'Start')));
  }
  main.appendChild(dashboard(status));
  const tiles = [...(status?.links || [])];
  if (status?.kolibri?.builtIn) {
    tiles.unshift({ title: 'Classroom (Kolibri)', url: status.kolibri.url,
      description: status.kolibri.running ? 'Khan Academy and other courses, with lessons and quizzes. Students sign in with their username; teachers with a password.' : 'Kolibri is not running yet; see the Classroom page.' });
  } else if (status?.kolibri?.configured) tiles.unshift({ title: 'Kolibri courses', url: status.kolibri.url, description: 'Khan Academy and other courses, with progress for each learner.' });
  if (tiles.length) {
    main.appendChild(h('section', { class: 'panel' }, h('header', null, h('h2', null, 'On this network')),
      h('div', { class: 'body link-tiles' }, tiles.map((l) => h('a', { class: 'link-tile', href: l.url, target: '_blank', rel: 'noopener noreferrer' },
        h('b', null, l.title), l.description ? h('span', { class: 'small muted' }, l.description) : null,
        h('span', { class: 'small faint mono' }, l.url.replace(/^https?:\/\//, '')))))));
  }

  // Search: titles that match, the best full-text matches from each
  // archive (with a count per source), and documents. Choosing a source
  // searches only that source, a page at a time.
  const results = h('div');
  const input = h('input', { type: 'search', placeholder: 'Search installed content and documents', 'aria-label': 'Search installed content', value: params.get('q') || '' });
  const archives = lib.resources.filter((r) => r.openPath && r.openPath.startsWith('/kiwix/'));
  const docSources = lib.resources.filter((r) => r.openPath && r.openPath.startsWith('/files/') && !['pmtiles', 'arcroute', 'video'].includes(r.format));
  const scope = h('select', { 'aria-label': 'Search in' },
    h('option', { value: '' }, 'Everything installed'),
    archives.length ? h('optgroup', { label: 'Archives' }, archives.map((r) => h('option', { value: r.resourceId }, r.title))) : null,
    docSources.length ? h('optgroup', { label: 'Books and documents' }, docSources.map((r) => h('option', { value: r.resourceId }, r.title))) : null,
    s.local ? h('option', { value: '@user' }, 'Your documents') : null);
  scope.value = params.get('in') || '';
  if (scope.selectedIndex < 0) scope.value = '';
  scope.addEventListener('change', () => { if (input.value.trim()) runSearch(); });
  const box = titleSearchBox(input, {
    showBook: true,
    fetchTitles: async (q) => {
      const r = await api.get(`/api/suggest?q=${encodeURIComponent(q)}&limit=8${scope.value && !scope.value.startsWith('@') ? `&resource=${encodeURIComponent(scope.value)}` : ''}`);
      return r.titles;
    },
    onPick: (t) => { location.hash = articleHref(t); },
    onSubmit: () => runSearch(),
  });
  // When question answering is available, a question typed here can be
  // sent to the assistant instead.
  const askBtn = status?.assistant?.canAsk ? h('button', { type: 'button', title: 'Ask the assistant: an answer written from passages of your library, with sources',
    // The question goes where the search does: "Search in" carries over.
    onclick: () => { location.hash = `#/ask?q=${encodeURIComponent(input.value.trim())}${scope.value ? `&in=${encodeURIComponent(scope.value)}` : ''}`; } }, 'Ask') : null;
  const form = h('form', { class: 'toolbar', role: 'search', onsubmit: (e) => { e.preventDefault(); runSearch(); } },
    h('div', { class: 'grow' }, box), scope, h('button', { type: 'submit', class: 'primary' }, 'Search'), askBtn);
  main.appendChild(h('section', { class: 'panel' }, form, results));

  const articleHref = (it) => `#/read/${encodeURIComponent(it.resourceId)}?path=${encodeURIComponent(it.path)}`;
  const n = (x) => Number(x || 0).toLocaleString();
  const resultItem = (it) => h('li', null,
    h('a', { href: articleHref(it) }, it.title), h('div', { class: 'src' }, it.book), snippet(it.snippet));
  const docItem = (d, q) => h('li', null,
    h('a', { href: `#/doc?key=${encodeURIComponent(d.key)}&find=${encodeURIComponent(q)}` }, d.title || d.name),
    h('div', { class: 'src' }, d.source === 'user' ? `Your document · ${d.name}` : `${titles[d.resourceId] || d.resourceId} · ${d.name}`),
    snippet(d.snippet));
  const titleList = (list, q, withBook) => h('ul', { class: 'title-hits' }, list.map((t) => h('li', null,
    h('a', { href: articleHref(t) }, highlight(t.title, q)), withBook ? h('span', { class: 'src' }, ` · ${t.book}`) : null)));

  // Courses from the Kolibri server, if one is set up, after the library's
  // own results. Kolibri can be slow or switched off; the section says so.
  async function kolibriResults(q) {
    const mine = searchSeq;
    const sec = h('section', { class: 'search-sec' }, h('div', { class: 'search-group-head' }, h('h3', null, 'Courses in Kolibri'),
      h('span', { class: 'small muted' }, 'Searching…')));
    results.appendChild(sec);
    try {
      const r = await api.get(`/api/kolibri/search?q=${encodeURIComponent(q)}`);
      if (mine !== searchSeq) return;
      const open = h('a', { href: r.url, target: '_blank', rel: 'noopener noreferrer', class: 'small' }, status?.kolibri?.builtIn ? 'Open the classroom' : 'Open Kolibri');
      fill(sec, h('div', { class: 'search-group-head' }, h('h3', null, status?.kolibri?.builtIn ? 'Courses in the classroom' : 'Courses in Kolibri'),
        h('span', { class: 'small muted' }, r.error ? r.error : `${r.items.length} match${r.items.length === 1 ? '' : 'es'}`), open),
        r.items.length ? h('ul', { class: 'results' }, r.items.map((it) => h('li', null,
          h('a', { href: it.url, target: '_blank', rel: 'noopener noreferrer' }, it.title),
          h('div', { class: 'src' }, it.kind === 'topic' ? 'Topic' : it.kind), it.description ? h('div', { class: 'snippet' }, it.description) : null))) : null);
    } catch (e) { if (mine === searchSeq) sec.remove(); }
  }

  let searchSeq = 0;
  async function runSearch() {
    const q = input.value.trim();
    const where = scope.value;
    const mine = ++searchSeq;
    clear(results);
    if (!q) return;
    history.replaceState(null, '', `#/library?q=${encodeURIComponent(q)}${where ? `&in=${encodeURIComponent(where)}` : ''}`);
    results.appendChild(h('p', { class: 'empty' }, 'Searching…'));
    try {
      const url = `/api/search?q=${encodeURIComponent(q)}${where ? `&resource=${encodeURIComponent(where)}` : ''}`;
      const r = await api.get(url);
      if (mine !== searchSeq) return;
      clear(results);
      if (r.error) results.appendChild(h('div', { class: 'banner warn' }, r.error));
      if (where) renderOne(r, q, where, url);
      else renderAll(r, q);
    } catch (e) {
      if (mine === searchSeq) fill(results, h('div', { class: 'banner err' }, e.message));
    }
  }

  function renderAll(r, q) {
    if (r.resources?.length) {
      results.appendChild(h('div', { class: 'toolbar small muted' }, 'Matching resources: ',
        r.resources.map((v) => h('a', { href: openHref(v), class: 'gap-right' }, v.title))));
    }
    const groups = r.groups || [];
    const docs = r.documents || [];
    const titleHits = r.titles || [];
    if (titleHits.length) {
      results.appendChild(h('section', { class: 'search-sec' }, h('h3', null, 'Articles with matching titles'), titleList(titleHits, q, true)));
    }
    for (const g of groups) {
      const head = h('div', { class: 'search-group-head' },
        h('h3', null, g.book || titles[g.resourceId] || g.resourceId),
        h('span', { class: 'small muted' }, g.error ? g.error : `${n(g.total)} match${g.total === 1 ? '' : 'es'}`),
        g.total > g.results.length ? h('button', { class: 'linkish small', onclick: () => { scope.value = g.resourceId; runSearch(); } }, `Show all in ${g.book || 'this source'}`) : null);
      results.appendChild(h('section', { class: 'search-sec' }, head, h('ul', { class: 'results' }, g.results.map(resultItem))));
    }
    if (status?.kolibri?.configured) kolibriResults(q);
    if (docs.length) {
      results.appendChild(h('section', { class: 'search-sec' },
        h('div', { class: 'search-group-head' }, h('h3', null, 'Documents'), h('span', { class: 'small muted' }, `${n(r.documentTotal)} match${r.documentTotal === 1 ? '' : 'es'}`)),
        h('ul', { class: 'results' }, docs.map((d) => docItem(d, q)))));
    }
    if ((r.titleOnly || []).length) {
      results.appendChild(h('p', { class: 'small muted search-note' },
        `Searched by title only, because they have no full-text index: ${r.titleOnly.map((id) => titles[id] || id).join(', ')}.`));
    }
    if (!groups.length && !docs.length && !titleHits.length && !r.resources?.length) {
      results.appendChild(h('p', { class: 'empty' }, `Nothing installed matches “${q}”. Try fewer or different words.`));
    }
  }

  function renderOne(r, q, where, url) {
    const name = where === '@user' ? 'your documents' : (titles[where] || where);
    const isArchive = archives.some((a) => a.resourceId === where);
    const list = h('ul', { class: 'results' });
    const more = h('div', { class: 'toolbar' });
    if ((r.titles || []).length) {
      results.appendChild(h('section', { class: 'search-sec' }, h('h3', null, 'Matching titles'), titleList(r.titles, q, false)));
    }
    if (isArchive) {
      if (r.notSearchable) {
        results.appendChild(h('p', { class: 'small muted search-note' }, `${name} has no full-text index, so only titles can be searched.`));
      } else {
        results.appendChild(h('div', { class: 'toolbar small muted' }, `${n(r.total)} page${r.total === 1 ? '' : 's'} in ${name} match “${q}”.`));
      }
      r.results.forEach((it) => list.appendChild(resultItem(it)));
      let shown = r.results.length;
      const next = () => setMore(more, r.total - shown, 'Show more results', async () => {
        const p = await api.get(`${url}&start=${shown}`);
        p.results.forEach((it) => list.appendChild(resultItem(it)));
        shown += p.results.length;
        if (!p.results.length) shown = r.total;
        next();
      });
      next();
    } else {
      const docs = r.documents || [];
      results.appendChild(h('div', { class: 'toolbar small muted' }, `${n(r.documentTotal)} match${r.documentTotal === 1 ? '' : 'es'} in ${name}.`));
      docs.forEach((d) => list.appendChild(docItem(d, q)));
      let shown = docs.length;
      const next = () => setMore(more, r.documentTotal - shown, 'Show more results', async () => {
        const p = await api.get(`${url}&start=${shown}`);
        (p.documents || []).forEach((d) => list.appendChild(docItem(d, q)));
        shown += (p.documents || []).length;
        if (!(p.documents || []).length) shown = r.documentTotal;
        next();
      });
      next();
    }
    results.appendChild(list);
    results.appendChild(more);
    results.appendChild(h('div', { class: 'toolbar small' }, h('button', { class: 'linkish', onclick: () => { scope.value = ''; runSearch(); } }, 'Search everything installed instead')));
  }
  if (input.value) runSearch();

  // Tabs
  const tabs = [['installed', 'Installed'], ['bookmarks', 'Bookmarks']];
  if (s.local) tabs.push(['recent', 'Recent'], ['notes', 'Notes'], ['documents', 'Your documents']);
  let active = params.get('tab') || 'installed';
  const tabBar = h('div', { class: 'subtabs', role: 'tablist' });
  const body = h('div');
  const panel = h('section', { class: 'panel' }, tabBar, body);
  main.appendChild(panel);

  function renderTabs() {
    fill(tabBar, tabs.map(([id, label]) => h('button', { role: 'tab', 'aria-selected': id === active ? 'true' : 'false',
      class: id === active ? 'active' : null, onclick: () => { active = id; renderTabs(); renderBody(); } }, label)));
  }

  async function renderBody() {
    clear(body).appendChild(h('p', { class: 'empty' }, 'Loading…'));
    try {
      if (active === 'installed') renderInstalled();
      else if (active === 'bookmarks') await renderBookmarks();
      else if (active === 'recent') await renderRecent();
      else if (active === 'notes') await renderNotes();
      else if (active === 'documents') await renderDocuments();
    } catch (e) {
      fill(body, h('div', { class: 'banner err' }, e.message));
    }
  }

  function renderInstalled() {
    const rows = [...lib.resources].sort((a, b) => (favorites.has(b.resourceId) - favorites.has(a.resourceId)) || a.title.localeCompare(b.title));
    if (!rows.length) {
      fill(body, h('div', { class: 'empty' }, 'Nothing is installed yet. Use Discover to choose resources.', ' ', h('a', { href: '#/discover' }, 'Add resources')));
      return;
    }
    const star = (r) => {
      const on = favorites.has(r.resourceId);
      return h('button', { class: `star${on ? ' on' : ''}`, 'aria-pressed': on ? 'true' : 'false', title: on ? 'Remove from favorites' : 'Add to favorites',
        disabled: !canChange, onclick: async () => {
          try {
            const res = await api.put('/api/user/favorites', { resourceId: r.resourceId, on: !on });
            favorites.clear(); res.favorites.forEach((f) => favorites.add(f));
            renderInstalled();
          } catch (e) { toast(e.message); }
        } }, on ? '★' : '☆');
    };
    fill(body,
      h('div', { class: 'toolbar small muted' }, `${rows.length} resources, ${fmtBytes(rows.reduce((n, r) => n + r.size, 0))}`,
        h('span', { style: 'flex:1' }), h('a', { class: 'button', href: '#/discover' }, 'Add resources')),
      h('div', { class: 'table-wrap' }, h('table', null,
        h('thead', null, h('tr', null, h('th', { class: 'check' }, h('span', { class: 'sr-only' }, 'Favorite')),
          h('th', null, 'Resource'), h('th', { class: 'hide-sm' }, 'Category'), h('th', { class: 'hide-sm' }, 'Language'),
          h('th', { class: 'hide-sm' }, 'Edition'), h('th', { class: 'right' }, 'Size'), h('th', { class: 'hide-sm' }, 'License'), h('th', null, ''))),
        h('tbody', null, rows.map((r) => h('tr', null,
          h('td', { class: 'check' }, star(r)),
          h('td', { class: 'title-cell' },
            r.openPath ? h('a', { href: openHref(r) }, h('b', null, r.title)) : h('b', null, r.title), ' ',
            warningBadges(r.warnings), r.unverified ? h('span', { class: 'badge warn', title: 'The source published no checksum.' }, 'Unverified') : null,
            h('span', { class: 'sub' }, r.description)),
          h('td', { class: 'hide-sm small' }, CATEGORY_TITLES[r.category] || r.category),
          h('td', { class: 'hide-sm small' }, (r.languages || []).map(langName).join(', ')),
          h('td', { class: 'num hide-sm' }, r.version, h('div', { class: 'faint small' }, `published ${fmtDate(r.published)}`)),
          h('td', { class: 'right num' }, fmtBytes(r.size)),
          h('td', { class: 'hide-sm' }, licenseBadge(r.license)),
          h('td', { class: 'right' }, h('div', { class: 'row actions' },
            r.format === 'gguf' || r.format === 'llamafile' ? h('a', { class: 'button', href: '#/ask', title: 'Used by the assistant to answer questions' }, 'Ask') :
            r.format === 'druglabels' ? h('a', { class: 'button', href: '#/medicines' }, 'Open') :
            r.openPath ? h('a', { class: 'button', href: openHref(r) }, r.format === 'video' ? 'Watch' : 'Open') :
              h('a', { class: 'small', href: `#/read/${encodeURIComponent(r.resourceId)}`, title: r.openError || 'The archive reader is not available.' }, 'Cannot open — why?'),
            canChange ? h('button', { class: 'danger', onclick: () => remove(r) }, 'Remove') : null)),
        ))))));
  }

  async function renderBookmarks() {
    const { bookmarks } = await api.get('/api/user/bookmarks');
    if (!bookmarks.length) {
      fill(body, h('p', { class: 'empty' }, 'No bookmarks yet. While reading, use “Bookmark” in the reader bar.'));
      return;
    }
    fill(body, h('ul', { class: 'results' }, bookmarks.map((b) => h('li', { class: 'row' },
      h('div', { style: 'flex:1;min-width:0' },
        h('a', { href: `#/read/${encodeURIComponent(b.resourceId)}?page=${encodeURIComponent(b.path)}` }, b.title || b.path),
        h('div', { class: 'src' }, `${titles[b.resourceId] || `${b.resourceId} (not installed)`} · ${fmtAgo(b.createdAt)}`)),
      canChange ? h('button', { onclick: async () => {
        try { await api.del(`/api/user/bookmarks/${b.id}`); renderBookmarks(); } catch (e) { toast(e.message); }
      } }, 'Remove') : null))));
  }

  async function renderRecent() {
    const { recent } = await api.get('/api/user/recent');
    if (!recent.length) {
      fill(body, h('p', { class: 'empty' }, 'Pages you read appear here. History is stored on the drive and is only shown on this computer.'));
      return;
    }
    fill(body,
      h('div', { class: 'toolbar small muted' }, 'Recently read on this library.', h('span', { style: 'flex:1' }),
        canChange ? h('button', { onclick: async () => { if (confirm('Clear reading history?')) { await api.del('/api/user/recent'); renderRecent(); } } }, 'Clear history') : null),
      h('ul', { class: 'results' }, recent.map((v) => h('li', null,
        h('a', { href: `#/read/${encodeURIComponent(v.resourceId)}?page=${encodeURIComponent(v.path)}` }, v.title || v.path),
        h('div', { class: 'src' }, `${titles[v.resourceId] || v.resourceId} · ${fmtAgo(v.at)}`)))));
  }

  async function renderNotes() {
    const { notes } = await api.get('/api/user/notes');
    const editor = noteEditor(null, async () => renderNotes());
    fill(body,
      canChange ? h('div', { class: 'body' }, h('h3', { style: 'margin-bottom:6px' }, 'New note'), editor) : null,
      notes.length ? h('ul', { class: 'results' }, notes.map((n) => h('li', null,
        h('div', { class: 'row' }, h('b', { style: 'flex:1' }, n.title || 'Untitled'),
          h('span', { class: 'small muted' }, fmtAgo(n.updatedAt)),
          canChange ? h('button', { onclick: (e) => {
            const li = e.target.closest('li');
            fill(li, noteEditor(n, async () => renderNotes()));
          } }, 'Edit') : null,
          canChange ? h('button', { class: 'danger', onclick: async () => {
            if (!confirm('Delete this note? It is moved to user/notes/deleted on the drive.')) return;
            await api.del(`/api/user/notes/${n.id}`); renderNotes();
          } }, 'Delete') : null),
        n.resourceId ? h('div', { class: 'src' }, 'About ',
          h('a', { href: `#/read/${encodeURIComponent(n.resourceId)}?page=${encodeURIComponent(n.path || '')}` }, n.path ? `${n.path} in ${titles[n.resourceId] || n.resourceId}` : (titles[n.resourceId] || n.resourceId))) : null,
        h('div', { class: 'note-text' }, n.text)))) : h('p', { class: 'empty' }, 'No notes yet. Notes are saved as plain text files in user/notes on the drive.'));
  }

  async function renderDocuments() {
    const { documents, indexed } = await api.get('/api/user/documents');
    const fileInput = h('input', { type: 'file', multiple: true, 'aria-label': 'Choose documents to add' });
    const upload = async () => {
      for (const f of fileInput.files) {
        try {
          const r = await fetch('/api/user/documents', { method: 'POST', body: f,
            headers: { 'X-Arcana-Token': s.token, 'X-Filename': encodeURIComponent(f.name) } });
          const data = await r.json();
          if (!r.ok) throw new Error(data.error);
          toast(`Added ${data.name}.`);
        } catch (e) { toast(`${f.name}: ${e.message}`); }
      }
      renderDocuments();
    };
    fill(body,
      canChange ? h('div', { class: 'toolbar' }, fileInput, h('button', { class: 'primary', onclick: upload }, 'Add documents'),
        h('span', { class: 'small muted' }, 'PDF, EPUB, text, Markdown and HTML are indexed for search. Files are stored in user/documents on the drive.')) : null,
      documents.length ? h('div', { class: 'table-wrap' }, h('table', null,
        h('thead', null, h('tr', null, h('th', null, 'Document'), h('th', { class: 'right' }, 'Size'), h('th', { class: 'hide-sm' }, 'Added'), h('th', null, ''))),
        h('tbody', null, documents.map((d) => h('tr', null,
          h('td', null, d.indexed ? h('a', { href: `#/doc?key=${encodeURIComponent('user:' + d.name)}` }, d.name) : d.name,
            !d.indexed ? h('div', { class: 'small muted' }, 'Not searchable (format not indexed)') : null),
          h('td', { class: 'right num' }, fmtBytes(d.size)),
          h('td', { class: 'hide-sm small' }, fmtDate(d.modified)),
          h('td', { class: 'right' }, canChange ? h('button', { class: 'danger', onclick: async () => {
            if (!confirm(`Remove ${d.name}? It is moved to user/documents/.deleted on the drive.`)) return;
            try { await api.del(`/api/user/documents/${encodeURIComponent(d.name)}`); renderDocuments(); } catch (e) { toast(e.message); }
          } }, 'Remove') : null))))),
        h('div', { class: 'toolbar small muted' }, `${indexed} documents in the search index.`))
        : h('p', { class: 'empty' }, 'No documents yet. Add your own plans, contact lists, manuals or maps.'));
  }

  async function remove(r) {
    if (!confirm(`Remove “${r.title}” (${fmtBytes(r.size)}) from this drive? Your notes, bookmarks and documents are not affected.`)) return;
    try {
      await api.del(`/api/library/${encodeURIComponent(r.resourceId)}`);
      toast(`Removed ${r.title}.`);
      lib.resources = lib.resources.filter((x) => x.resourceId !== r.resourceId);
      renderInstalled();
    } catch (e) { toast(e.message); }
  }

  renderTabs();
  renderBody();
  return null;
}

// noteEditor edits a note (or creates one when note is null).
export function noteEditor(note, onSaved, ref) {
  const title = h('input', { type: 'text', placeholder: 'Title', value: note?.title || ref?.title || '', style: 'width:100%' });
  const text = h('textarea', { rows: 5, placeholder: 'Write a note…', style: 'width:100%' });
  text.value = note?.text || '';
  const save = h('button', { class: 'primary', onclick: async () => {
    if (!text.value.trim()) { toast('The note is empty.'); return; }
    try {
      await api.post('/api/user/notes', { id: note?.id || '', resourceId: note?.resourceId || ref?.resourceId || '', path: note?.path || ref?.path || '',
        title: title.value, text: text.value });
      toast('Note saved to the drive.');
      onSaved?.();
    } catch (e) { toast(e.message); }
  } }, 'Save note');
  return h('div', { class: 'note-editor' }, title, text, h('div', { class: 'row' }, save));
}

// Maps and road networks open in the map view, films in the player;
// everything else in the reader.
function openHref(r) {
  if (r.format === 'pmtiles') return `#/maps?set=${encodeURIComponent(r.resourceId)}`;
  if (r.format === 'arcroute') return '#/maps';
  if (r.format === 'video') return `#/watch/${encodeURIComponent(r.resourceId)}`;
  return `#/read/${encodeURIComponent(r.resourceId)}`;
}

function dashboard(st) {
  if (!st) return h('div', { class: 'banner err' }, 'Status unavailable.');
  const plan = st.storage.plan;
  const vol = st.storage.volume;
  const readout = (label, value, sub, dot, href) => h('div', { class: 'readout' },
    h('div', { class: 'label' }, dot ? h('span', { class: `dot ${dot}` }) : null, label),
    href ? h('a', { class: 'value', href }, value) : h('div', { class: 'value' }, value),
    h('div', { class: 'sub', title: sub }, sub));
  const att = st.attention || [];
  const free = plan.afterQueue - plan.reserve;
  const gps = st.gps || {};
  return h('section', { class: 'panel' },
    h('div', { class: 'readouts' },
      readout('Installed', `${st.installed.count}`, `${fmtBytes(st.installed.bytes)} of reference content`, null, '#/library'),
      readout('Storage', fmtBytes(Math.max(free, 0)), `usable after queue and ${fmtBytes(plan.reserve)} reserve`,
        free < 0 ? 'err' : free < plan.reserve ? 'warn' : 'ok', '#/settings'),
      readout('Offline', st.offline.ready ? 'Ready' : 'Not ready',
        st.offline.ready ? 'everything installed works without internet' : (st.installed.count ? `reader ${st.offline.reader.state}` : 'nothing installed yet'),
        st.offline.ready ? 'ok' : 'warn'),
      readout('Updates', st.updates.available ? `${st.updates.available} available` : 'None known',
        `catalog checked ${fmtAgo(st.updates.lastCatalogRefresh)}`, st.updates.highPriority ? 'warn' : null, '#/updates'),
      readout('Sharing', st.sharing.active ? 'On network' : 'This computer',
        st.sharing.active ? (st.sharing.urls[0] || 'no network address') : 'local access only', st.sharing.active ? 'info' : null, '#/server'),
      readout('GPS', gps.label || 'Not connected', gps.sub || '', gps.dot || null, gps.href || null),
      readout('Downloads', st.downloads.active ? `${st.downloads.active} active` : 'Idle',
        st.downloads.failed ? `${st.downloads.failed} failed` : `${fmtBytes(plan.queued)} queued`, st.downloads.failed ? 'err' : null, '#/downloads'),
    ),
    h('div', { class: 'body' },
      h('div', { class: 'row small muted', style: 'margin-bottom:6px' },
        h('span', null, `Drive ${vol.mountPoint}`), h('span', null, st.storage.fs.name),
        h('span', { class: 'num' }, `${fmtBytes(plan.total)} formatted`),
        plan.nominal ? h('span', null, `(likely sold as ${fmtBytes(plan.nominal)})`) : null,
        vol.removable === true ? h('span', { class: 'badge' }, 'Removable') : null),
      storageBar(plan)),
    att.length ? h('ul', { class: 'attention', 'aria-label': 'Needs attention' }, att.map((a) => h('li', null,
      h('span', { class: `dot ${a.level === 'error' ? 'err' : a.level === 'warning' ? 'warn' : 'info'}` }),
      h('span', null, a.message, ' ', a.view ? h('a', { href: `#/${a.view}` }, 'Go') : null)))) : null);
}
