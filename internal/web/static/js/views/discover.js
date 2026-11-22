import { api, getSession } from '../api.js';
import { h, clear, fill, fmtBytes, fmtDate, fmtAgo, langName, licenseBadge, warningBadges, warningList, storageBar, toast, debounce } from '../util.js';

// The selection survives navigation within the session.
const selection = new Map(); // id -> {title, size}
let saved = loadSaved();

function loadSaved() {
  try { return JSON.parse(localStorage.getItem('arcana-selection') || '[]'); } catch (_) { return []; }
}
function persist() {
  try { localStorage.setItem('arcana-selection', JSON.stringify([...selection.entries()])); } catch (_) { /* ignore */ }
}
for (const [id, v] of saved) selection.set(id, v);

// preferredLanguage picks the browser's language; the catalog spans hundreds
// of languages, so an unfiltered list is rarely what people want first.
function preferredLanguage() {
  try {
    const saved = localStorage.getItem('arcana-lang');
    if (saved !== null) return saved;
  } catch (_) { /* ignore */ }
  return (navigator.language || 'en').split('-')[0].toLowerCase();
}

const STATUS_LABEL = { installed: 'Installed', queued: 'Queued', update: 'Update available', available: '' };

export async function discoverView(main, params) {
  const s = getSession();
  const canChange = s.admin && !s.readOnly;
  const state = {
    q: params.get('q') || '', category: params.get('category') || '',
    lang: params.has('lang') ? params.get('lang') : preferredLanguage(),
    sort: params.get('sort') || 'relevance', offset: 0, active: null,
  };
  const facets = await api.get(`/api/catalog/facets${state.lang ? `?lang=${state.lang}` : ''}`);

  const kiwixProvider = facets.providers.find((p) => p.id === 'kiwix');
  const header = h('div', { class: 'toolbar' },
    h('div', { class: 'grow small muted' },
      kiwixProvider?.fetchedAt ? `Catalog: ${facets.providers.reduce((n, p) => n + p.resourceCount, 0)} resources from ${facets.providers.length} sources, refreshed ${fmtAgo(kiwixProvider.fetchedAt)}.`
        : 'Only the built-in catalog is loaded. Refresh while connected to the internet to load the Kiwix library and other sources.',
      kiwixProvider?.lastError ? h('span', { class: 'badge warn', title: kiwixProvider.lastError }, 'Last refresh failed') : null),
    canChange ? h('button', { onclick: refresh }, 'Refresh catalog') : null,
    h('button', { onclick: () => openPresets() }, 'Collections and recommendations'));

  // Sidebar: categories
  const side = h('ul', { class: 'sidelist' });
  function renderSide() {
    clear(side);
    side.appendChild(h('li', null, h('a', { href: '#', 'aria-current': state.category === '' ? 'true' : null,
      onclick: (e) => { e.preventDefault(); setCategory(''); } }, h('span', null, 'All categories'))));
    for (const c of facets.categories) {
      side.appendChild(h('li', null, h('a', { href: '#', 'aria-current': state.category === c.id ? 'true' : null,
        onclick: (e) => { e.preventDefault(); setCategory(c.id); } }, h('span', null, c.title), h('span', { class: 'num' }, c.count))));
    }
  }
  function setCategory(c) { state.category = c; state.offset = 0; renderSide(); load(); }

  // Filters
  const search = h('input', { type: 'search', placeholder: 'Search the catalog', 'aria-label': 'Search the catalog', value: state.q });
  search.addEventListener('input', debounce(() => { state.q = search.value; state.offset = 0; load(); }, 250));
  const langSel = h('select', { 'aria-label': 'Language' }, h('option', { value: '' }, 'All languages'),
    facets.languages.filter((l) => l.key).map((l) => h('option', { value: l.key, selected: l.key === state.lang }, `${langName(l.key)} (${l.count})`)));
  langSel.addEventListener('change', () => {
    state.lang = langSel.value; state.offset = 0;
    try { localStorage.setItem('arcana-lang', state.lang); } catch (_) { /* ignore */ }
    load();
  });
  const sortSel = h('select', { 'aria-label': 'Sort' }, [['relevance', 'Relevance'], ['title', 'Title'], ['size', 'Smallest first'],
    ['size-desc', 'Largest first'], ['date', 'Newest first']].map(([v, t]) => h('option', { value: v, selected: v === state.sort }, t)));
  sortSel.addEventListener('change', () => { state.sort = sortSel.value; load(); });

  const list = h('div', { class: 'body flush table-wrap' });
  const pager = h('div', { class: 'toolbar small muted' });
  const detail = h('div');
  const selBar = h('div', { class: 'selectionbar', hidden: true });

  renderSide();
  main.appendChild(h('section', { class: 'panel' }, header));
  main.appendChild(h('div', { class: 'discover' },
    h('aside', { class: 'panel' }, h('header', null, h('h2', null, 'Categories')), side),
    h('div', null,
      h('section', { class: 'panel' },
        h('div', { class: 'toolbar' }, h('div', { class: 'grow' }, search), langSel, sortSel),
        list, pager),
      detail, selBar)));

  async function load() {
    const q = new URLSearchParams({ q: state.q, category: state.category, lang: state.lang, sort: state.sort, offset: state.offset, limit: 100 });
    history.replaceState(null, '', `#/discover?${new URLSearchParams({ q: state.q, category: state.category, lang: state.lang, sort: state.sort })}`);
    let r;
    try { r = await api.get(`/api/catalog?${q}`); } catch (e) { clear(list).appendChild(h('div', { class: 'banner err' }, e.message)); return; }
    clear(list);
    if (!r.resources.length) {
      list.appendChild(h('p', { class: 'empty' }, 'No resources match. Try another category or language, or refresh the catalog.'));
    } else {
      list.appendChild(h('table', null,
        h('thead', null, h('tr', null, h('th', { class: 'check' }, h('span', { class: 'sr-only' }, 'Select')), h('th', null, 'Resource'),
          h('th', { class: 'hide-sm' }, 'Language'), h('th', null, 'Date'), h('th', { class: 'right' }, 'Size'),
          h('th', { class: 'hide-sm' }, 'License'), h('th', { class: 'hide-sm' }, 'Status'))),
        h('tbody', null, r.resources.map(row))));
    }
    fill(pager,
      `${r.total} resources`, ' ',
      state.offset > 0 ? h('button', { class: 'link', onclick: () => { state.offset = Math.max(0, state.offset - 100); load(); } }, 'Previous') : null, ' ',
      state.offset + 100 < r.total ? h('button', { class: 'link', onclick: () => { state.offset += 100; load(); } }, 'Next 100') : null);
  }

  function row(res) {
    const checked = selection.has(res.id);
    const box = h('input', { type: 'checkbox', checked, 'aria-label': `Select ${res.title}`,
      disabled: !canChange || res.status === 'installed' || res.status === 'queued' || !res.fits });
    box.addEventListener('click', (e) => e.stopPropagation());
    box.addEventListener('change', () => toggle(res, box.checked));
    const tr = h('tr', { class: `clickable${state.active === res.id ? ' active' : ''}`, onclick: () => showDetail(res.id, tr) },
      h('td', { class: 'check' }, box),
      h('td', { class: 'title-cell' }, h('b', null, res.title), ' ', res.variantLabel ? h('span', { class: 'muted small' }, `· ${res.variantLabel}`) : null, ' ',
        warningBadges(res.warnings),
        !res.fits ? h('span', { class: 'badge err', title: res.fitReason }, 'Too large for this drive') : null,
        h('span', { class: 'sub' }, res.description)),
      h('td', { class: 'hide-sm small' }, (res.languages || []).slice(0, 3).map(langName).join(', ') + ((res.languages || []).length > 3 ? ' …' : '')),
      h('td', { class: 'num small' }, fmtDate(res.published || res.version)),
      h('td', { class: 'right num' }, (res.sizeEstimated ? '≈ ' : '') + fmtBytes(res.downloadSize)),
      h('td', { class: 'hide-sm' }, licenseBadge(res.license)),
      h('td', { class: 'hide-sm small' }, STATUS_LABEL[res.status]));
    return tr;
  }

  function toggle(res, on) {
    if (on) selection.set(res.id, { title: res.title, size: res.downloadSize });
    else selection.delete(res.id);
    persist();
    updateSelectionBar();
  }

  async function showDetail(id, tr) {
    state.active = id;
    list.querySelectorAll('tr.active').forEach((x) => x.classList.remove('active'));
    tr?.classList.add('active');
    let d;
    try { d = await api.get(`/api/catalog/item/${encodeURIComponent(id)}`); } catch (e) { toast(e.message); return; }
    const r = d.resource;
    const f = r.files[0] || {};
    const meta = [
      ['Publisher', r.publisher], ['Source', r.sourceUrl ? h('span', { class: 'mono small' }, r.sourceUrl) : null],
      ['Provider', r.provider], ['Identifier', h('span', { class: 'mono small' }, r.id)],
      ['Version', r.version], ['Published', fmtDate(r.published)],
      ['Catalog verified', r.lastVerified ? fmtDate(r.lastVerified) : null],
      ['Languages', (r.languages || []).map(langName).join(', ')],
      ['Coverage', r.coverage?.scope === 'regional' ? (r.coverage.regions || []).join(', ') : r.coverage?.scope === 'global' ? 'Worldwide' : null],
      ['Format', r.format.toUpperCase()],
      ['Download size', `${(r.sizeEstimated ? '≈ ' : '') + fmtBytes(r.downloadSize)}${f.name ? ` (${f.name})` : ''}`],
      ['Content', [r.media?.pictures ? 'images' : 'no images', r.media?.videos ? 'video' : null, r.media?.detail].filter(Boolean).join(', ')],
      ['Search', r.capabilities?.fullTextSearch ? 'Full-text index included' : 'Title only'],
      ['Integrity', f.hashes?.sha256 ? 'SHA-256 published in catalog' : f.metalinkUrl ? 'SHA-256 obtained from the publisher\'s metalink at download' : 'No checksum published'],
      ['License', h('span', null, licenseBadge(r.license), ' ', r.license.name || r.license.id, r.license.notes ? h('div', { class: 'small muted' }, r.license.notes) : null)],
      ['Articles', r.stats?.articles ? r.stats.articles.toLocaleString() : null],
    ].filter(([, v]) => v);

    closeDetail();
    const dlg = h('dialog', { class: 'detail', 'aria-label': r.title },
      h('header', null, h('h2', null, r.title), STATUS_LABEL[r.status] ? h('span', { class: 'badge' }, STATUS_LABEL[r.status]) : null,
        h('button', { onclick: () => dlg.close() }, 'Close')),
      h('div', { class: 'body' },
        h('p', null, r.description),
        warningList(r.warnings),
        h('dl', { class: 'meta' }, meta.map(([k, v]) => [h('dt', null, k), h('dd', null, v)])),
        d.alternatives && d.alternatives.length > 1 ? h('div', null,
          h('h3', { style: 'margin:12px 0 4px' }, 'Editions'),
          h('table', null, h('thead', null, h('tr', null, h('th', null, 'Edition'), h('th', null, 'Content'), h('th', { class: 'right' }, 'Size'), h('th', null, 'Date'), h('th', null, ''))),
            h('tbody', null, d.alternatives.map((a) => h('tr', { class: a.id === r.id ? 'active' : null },
              h('td', null, a.variantLabel || a.variant),
              h('td', { class: 'small muted' }, [a.media?.pictures ? 'images' : 'no images', a.media?.detail].filter(Boolean).join(', ')),
              h('td', { class: 'right num' }, fmtBytes(a.downloadSize)), h('td', { class: 'num small' }, fmtDate(a.published)),
              h('td', { class: 'right' }, canChange && a.status === 'available' && a.fits
                ? h('button', { onclick: () => { selectOnly(a, d.alternatives); } }, selection.has(a.id) ? 'Selected' : 'Select')
                : h('span', { class: 'small muted' }, STATUS_LABEL[a.status]))))))) : null,
        canChange && r.status !== 'installed' && r.status !== 'queued' && r.fits ? h('div', { class: 'row', style: 'margin-top:10px' },
          h('button', { class: 'primary', onclick: () => { selectOnly(r, d.alternatives || []); } }, selection.has(r.id) ? 'Selected' : 'Add to selection')) : null,
      ));
    detail.appendChild(dlg);
    dlg.addEventListener('close', () => { dlg.remove(); state.active = null; });
    dlg.showModal();
  }

  function closeDetail() {
    const open = detail.querySelector('dialog');
    if (open) open.close();
  }

  function selectOnly(res, alternatives) {
    // Choosing an edition replaces other editions of the same resource.
    for (const a of alternatives) selection.delete(a.id);
    selection.set(res.id, { title: res.title + (res.variantLabel ? ` (${res.variantLabel})` : ''), size: res.downloadSize });
    persist();
    updateSelectionBar();
    load();
    closeDetail();
    toast(`Added ${res.title} to the selection.`);
  }

  const updateSelectionBar = debounce(async () => {
    if (!selection.size || !canChange) { selBar.hidden = true; return; }
    let rep;
    try { rep = await api.post('/api/storage/plan', { resourceIds: [...selection.keys()] }); } catch (e) { return; }
    const p = rep.plan;
    selBar.hidden = false;
    fill(selBar,
      h('div', { class: 'summary' },
        h('b', null, `${selection.size} selected`),
        h('span', { class: 'num' }, `${fmtBytes(p.selected)} to download`),
        h('span', { class: 'num' }, `${fmtBytes(Math.max(p.headroom, 0))} free after (keeping ${fmtBytes(p.reserve)} reserve)`),
        p.status === 'over' ? h('span', { class: 'status-over' }, 'Does not fit') : p.status === 'tight' ? h('span', { class: 'status-tight' }, 'Tight: sizes are estimates') : null,
        h('span', { style: 'flex:1' }),
        h('button', { onclick: () => showSelection() }, 'Review'),
        h('button', { onclick: () => { selection.clear(); persist(); updateSelectionBar(); load(); } }, 'Clear'),
        h('button', { class: 'primary', disabled: p.status === 'over', onclick: enqueue }, 'Download selected')),
      storageBar(p),
      p.messages?.length ? h('div', { class: 'small muted', style: 'margin-top:4px' }, p.messages.join(' ')) : null);
  }, 150);

  function showSelection() {
    const dlg = h('dialog', null,
      h('header', null, h('h2', null, 'Selected resources'), h('button', { onclick: () => dlg.close() }, 'Close')),
      h('div', { class: 'body' }, h('table', null, h('tbody', null, [...selection.entries()].map(([id, v]) => h('tr', null,
        h('td', null, v.title, h('div', { class: 'mono faint small' }, id)), h('td', { class: 'right num' }, fmtBytes(v.size)),
        h('td', { class: 'right' }, h('button', { onclick: (e) => { selection.delete(id); persist(); e.target.closest('tr').remove(); updateSelectionBar(); load(); } }, 'Remove'))))))));
    document.body.appendChild(dlg);
    dlg.addEventListener('close', () => dlg.remove());
    dlg.showModal();
  }

  async function enqueue() {
    const ids = [...selection.keys()];
    try {
      const r = await api.post('/api/downloads', { resourceIds: ids });
      const failed = Object.entries(r.results).filter(([, v]) => v !== 'queued');
      for (const id of ids) if (r.results[id] === 'queued') selection.delete(id);
      persist();
      toast(failed.length ? `Queued ${ids.length - failed.length}; not queued: ${failed.map(([k, v]) => `${k} (${v})`).join('; ')}`
        : `Queued ${ids.length} resource(s). Downloads continue in the background.`);
      updateSelectionBar();
      load();
    } catch (e) { toast(e.message); }
  }

  async function refresh(e) {
    const btn = e.target;
    btn.disabled = true;
    btn.textContent = 'Refreshing…';
    try {
      const r = await api.post('/api/catalog/refresh');
      toast(r.results.map((x) => x.error ? `${x.name}: failed (${x.error})` : `${x.name}: ${x.resources} resources`).join(' · '));
      location.reload();
    } catch (err) {
      toast(err.message);
      btn.disabled = false;
      btn.textContent = 'Refresh catalog';
    }
  }

  async function openPresets() {
    const { presets } = await api.get('/api/presets');
    const langSel2 = h('select', { 'aria-label': 'Language' }, facets.languages.filter((l) => l.key && l.key.length <= 3).slice(0, 40)
      .map((l) => h('option', { value: l.key, selected: l.key === (state.lang || 'en') }, langName(l.key))));
    const presetSel = h('select', { 'aria-label': 'Collection' }, presets.map((p) => h('option', { value: p.id }, p.title)));
    const out = h('div');
    const desc = h('p', { class: 'muted' });
    const footerInfo = h('span', { class: 'small muted', style: 'flex:1' });
    const addBtn = h('button', { class: 'primary', disabled: true }, 'Add suggestions to selection');
    let rec = null;
    const dlg = h('dialog', null,
      h('header', null, h('h2', null, 'Collections and recommendations'), h('button', { onclick: () => dlg.close() }, 'Close')),
      h('div', { class: 'body' },
        h('p', { class: 'small muted' }, 'Collections are starting points, not fixed packages. Arcana suggests editions that fit the space left on this drive (after downloads already queued and your reserve) and explains each choice. Nothing is downloaded until you confirm.'),
        h('div', { class: 'row' }, h('label', { class: 'inline' }, 'Collection ', presetSel), h('label', { class: 'inline' }, 'Language ', langSel2)),
        desc, out),
      h('footer', null, footerInfo, h('button', { onclick: () => dlg.close() }, 'Cancel'), addBtn));
    document.body.appendChild(dlg);
    dlg.addEventListener('close', () => dlg.remove());

    async function recommend() {
      const p = presets.find((x) => x.id === presetSel.value);
      desc.textContent = p.description;
      clear(out).appendChild(h('p', { class: 'empty' }, 'Calculating…'));
      try { rec = await api.post('/api/recommend', { preset: presetSel.value, language: langSel2.value }); } catch (e) { clear(out).appendChild(h('div', { class: 'banner err' }, e.message)); return; }
      const checks = new Map();
      clear(out).appendChild(h('table', null,
        h('thead', null, h('tr', null, h('th', { class: 'check' }), h('th', null, 'Item'), h('th', null, 'Priority'), h('th', { class: 'right' }, 'Size'), h('th', null, 'Why'))),
        h('tbody', null, rec.items.map((it) => {
          const box = h('input', { type: 'checkbox', checked: it.status === 'selected', disabled: it.status !== 'selected' && it.status !== 'skipped' || !it.resourceId && it.status !== 'skipped', 'aria-label': it.title });
          if (it.status === 'skipped' && it.alternatives?.length) {
            const smallest = [...it.alternatives].sort((a, b) => a.size - b.size)[0];
            it.resourceId = it.resourceId || smallest.resourceId;
            it.size = it.size || smallest.size;
            box.disabled = false;
          }
          checks.set(it, box);
          box.addEventListener('change', sum);
          return h('tr', null, h('td', { class: 'check' }, box),
            h('td', null, h('b', null, it.title), it.variant ? h('div', { class: 'small muted' }, it.variant) : null),
            h('td', { class: 'small' }, ['', 'Essential', 'Recommended', 'Optional'][it.priority]),
            h('td', { class: 'right num' }, it.size ? fmtBytes(it.size) : '—'),
            h('td', { class: 'small' }, it.reason, h('div', { class: 'muted' }, it.decision)));
        }))));
      function sum() {
        let total = 0; let n = 0;
        for (const [it, box] of checks) if (box.checked && it.resourceId) { total += it.size; n++; }
        footerInfo.textContent = `${n} items, ${fmtBytes(total)}; ${fmtBytes(rec.budget)} available for new downloads.`;
        addBtn.disabled = n === 0 || !canChange;
        addBtn.onclick = () => {
          for (const [it, box] of checks) if (box.checked && it.resourceId) selection.set(it.resourceId, { title: it.title, size: it.size });
          persist(); dlg.close(); updateSelectionBar(); load();
        };
      }
      sum();
    }
    presetSel.addEventListener('change', recommend);
    langSel2.addEventListener('change', recommend);
    dlg.showModal();
    recommend();
  }

  await load();
  updateSelectionBar();
  return null;
}
