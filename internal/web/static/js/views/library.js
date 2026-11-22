import { api, getSession } from '../api.js';
import { h, clear, fmtBytes, fmtAgo, fmtDate, langName, licenseBadge, warningBadges, storageBar, toast } from '../util.js';

const CATEGORY_TITLES = {};

// Library: the dashboard (status readouts and attention list), full-text
// search across installed archives, and the installed-resources table.
export async function libraryView(main, params, ctx) {
  const s = getSession();
  const [status, lib, facets] = await Promise.all([
    ctx.refreshStatus(), api.get('/api/library'), api.get('/api/catalog/facets').catch(() => null),
  ]);
  if (facets) for (const c of facets.categories) CATEGORY_TITLES[c.id] = c.title;

  if (status?.library.readOnly) {
    main.appendChild(h('div', { class: 'banner warn' }, `Read-only: ${status.library.readOnlyReason}. You can browse and search; downloads and changes are disabled.`));
  }
  main.appendChild(dashboard(status));

  // Search
  const results = h('div');
  const input = h('input', { type: 'search', placeholder: 'Search installed content (full text)', 'aria-label': 'Search installed content', value: params.get('q') || '' });
  const form = h('form', { class: 'toolbar', role: 'search', onsubmit: (e) => { e.preventDefault(); runSearch(); } },
    h('div', { class: 'grow' }, input), h('button', { type: 'submit', class: 'primary' }, 'Search'));
  main.appendChild(h('section', { class: 'panel' }, form, results));

  async function runSearch() {
    const q = input.value.trim();
    clear(results);
    if (!q) return;
    history.replaceState(null, '', `#/library?q=${encodeURIComponent(q)}`);
    results.appendChild(h('p', { class: 'empty' }, 'Searching…'));
    try {
      const r = await api.get(`/api/search?q=${encodeURIComponent(q)}`);
      clear(results);
      if (r.error) results.appendChild(h('div', { class: 'banner warn' }, r.error));
      if (r.resources?.length) {
        results.appendChild(h('div', { class: 'toolbar small muted' }, 'Matching resources: ',
          r.resources.map((v) => h('a', { href: `#/read/${encodeURIComponent(v.resourceId)}` }, v.title, ' '))));
      }
      if (!r.results.length) {
        results.appendChild(h('p', { class: 'empty' }, 'No matching articles. Archives without a full-text index can only be searched by title from inside the reader.'));
        return;
      }
      results.appendChild(h('div', { class: 'toolbar small muted' }, `${r.total} matching articles; showing ${r.results.length}.`));
      results.appendChild(h('ul', { class: 'results' }, r.results.map((it) => {
        const snippet = h('div', { class: 'snippet' });
        // Snippets are escaped on the server; only <b> highlighting remains.
        snippet.innerHTML = it.snippet;
        return h('li', null,
          h('a', { href: `#/read/${encodeURIComponent(it.resourceId)}?path=${encodeURIComponent(it.path)}` }, it.title),
          h('div', { class: 'src' }, it.book), snippet);
      })));
    } catch (e) {
      clear(results).appendChild(h('div', { class: 'banner err' }, e.message));
    }
  }
  if (input.value) runSearch();

  // Installed resources
  const rows = lib.resources.sort((a, b) => a.title.localeCompare(b.title));
  const table = h('table', null,
    h('thead', null, h('tr', null,
      h('th', null, 'Resource'), h('th', { class: 'hide-sm' }, 'Category'), h('th', { class: 'hide-sm' }, 'Language'),
      h('th', { class: 'hide-sm' }, 'Edition'), h('th', { class: 'right' }, 'Size'), h('th', { class: 'hide-sm' }, 'License'), h('th', null, ''))),
    h('tbody', null, rows.map((r) => h('tr', null,
      h('td', { class: 'title-cell' },
        r.openPath ? h('a', { href: `#/read/${encodeURIComponent(r.resourceId)}` }, h('b', null, r.title)) : h('b', null, r.title), ' ',
        warningBadges(r.warnings), r.unverified ? h('span', { class: 'badge warn', title: 'The source published no checksum.' }, 'Unverified') : null,
        h('span', { class: 'sub' }, r.description)),
      h('td', { class: 'hide-sm small' }, CATEGORY_TITLES[r.category] || r.category),
      h('td', { class: 'hide-sm small' }, (r.languages || []).map(langName).join(', ')),
      h('td', { class: 'num hide-sm' }, r.version, h('div', { class: 'faint small' }, `published ${fmtDate(r.published)}`)),
      h('td', { class: 'right num' }, fmtBytes(r.size)),
      h('td', { class: 'hide-sm' }, licenseBadge(r.license)),
      h('td', { class: 'right' }, h('div', { class: 'row actions' },
        r.openPath ? h('a', { class: 'button', href: `#/read/${encodeURIComponent(r.resourceId)}` }, 'Open') :
          h('span', { class: 'small muted', title: 'The archive reader is not available.' }, 'Cannot open'),
        s.admin && !s.readOnly ? h('button', { class: 'danger', onclick: () => remove(r) }, 'Remove') : null)),
    ))));
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Installed resources'), h('span', { class: 'muted small' }, `${rows.length} resources, ${fmtBytes(rows.reduce((n, r) => n + r.size, 0))}`),
      h('a', { class: 'button', href: '#/discover' }, 'Add resources')),
    rows.length ? h('div', { class: 'body flush table-wrap' }, table) :
      h('div', { class: 'empty' }, 'Nothing is installed yet. Use Discover to choose resources, or start with the Arcana sample library.')));

  async function remove(r) {
    if (!confirm(`Remove “${r.title}” (${fmtBytes(r.size)}) from this drive? Your notes and documents are not affected.`)) return;
    try {
      await api.del(`/api/library/${encodeURIComponent(r.resourceId)}`);
      toast(`Removed ${r.title}.`);
      location.hash = '#/library';
      window.dispatchEvent(new HashChangeEvent('hashchange'));
    } catch (e) { toast(e.message); }
  }
  return null;
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
      readout('GPS', 'Not in this build', 'planned for Phase 3', null),
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
