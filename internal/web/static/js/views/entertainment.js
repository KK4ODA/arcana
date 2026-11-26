import { api, getSession } from '../api.js';
import { h, fill, fmtBytes, licenseBadge, warningBadges, toast } from '../util.js';
import { GAMES } from '../games/index.js';
import { savedPosition, fmtClock } from './watch.js';

// Entertainment: the board games built into Arcana, the films on this
// drive (and, for the administrator, films in the catalog to add), and
// installed references about games and films.
export async function entertainmentView(main) {
  const s = getSession();
  const canChange = s.admin && !s.readOnly;
  const [lib, catalog] = await Promise.all([
    api.get('/api/library'),
    canChange ? api.get('/api/catalog?category=entertainment&format=video&limit=100').catch(() => null) : null,
  ]);
  const films = lib.resources.filter((r) => r.format === 'video');
  const reading = lib.resources.filter((r) => r.category === 'entertainment' && r.format !== 'video' && r.openPath);

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Board games'), h('span', { class: 'small muted' }, 'Play on this device: two players taking turns, or against the computer.')),
    h('div', { class: 'body game-tiles' }, GAMES.map((g) => h('a', { class: 'game-tile', href: `#/play/${g.id}` },
      h('span', { class: `game-icon ${g.id}`, 'aria-hidden': 'true' }),
      h('b', null, g.title), h('span', { class: 'small' }, g.blurb), h('span', { class: 'small muted' }, g.players))))));

  const filmBody = h('div', { class: 'body' });
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Films'),
      films.length ? h('span', { class: 'small muted' }, `${films.length} on this drive`) : null),
    filmBody));
  fill(filmBody,
    films.length ? h('div', { class: 'film-list' }, films.map(filmCard))
      : h('p', { class: 'empty' }, canChange ? 'No films on this drive yet. Choose some below; they download in the background.'
        : 'No films on this drive yet. Films are added on the computer running Arcana.'));

  if (canChange && catalog) {
    const available = catalog.resources.filter((r) => r.status !== 'installed');
    if (available.length) {
      const list = h('div', { class: 'table-wrap' }, h('table', null,
        h('thead', null, h('tr', null, h('th', null, 'Film'), h('th', { class: 'right' }, 'Size'), h('th', { class: 'hide-sm' }, 'License'), h('th', null, ''))),
        h('tbody', null, available.map((r) => h('tr', null,
          h('td', { class: 'title-cell' }, h('b', null, r.title), ' ', warningBadges(r.warnings), h('span', { class: 'sub' }, r.description)),
          h('td', { class: 'right num' }, fmtBytes(r.downloadSize)),
          h('td', { class: 'hide-sm' }, licenseBadge(r.license)),
          h('td', { class: 'right' }, r.status === 'queued' ? h('span', { class: 'small muted' }, 'Queued')
            : !r.fits ? h('span', { class: 'badge err', title: r.fitReason }, 'Does not fit')
              : h('button', { onclick: (e) => download(r, e.target) }, 'Download')))))));
      main.appendChild(h('section', { class: 'panel' },
        h('header', null, h('h2', null, 'Films you can add'),
          h('a', { class: 'small', href: '#/discover?category=entertainment' }, 'More in Discover')),
        h('p', { class: 'small muted body' }, 'Feature films that are in the public domain in the United States, from the Internet Archive. Each is checked against the SHA-256 recorded when it was reviewed. Copyright differs in other countries.'),
        list));
    }
  }

  if (reading.length) {
    main.appendChild(h('section', { class: 'panel' },
      h('header', null, h('h2', null, 'About games and films')),
      h('ul', { class: 'results body' }, reading.map((r) => h('li', null,
        h('a', { href: `#/read/${encodeURIComponent(r.resourceId)}` }, r.title), h('div', { class: 'src' }, r.description))))));
  }

  async function download(r, btn) {
    btn.disabled = true;
    try {
      const res = await api.post('/api/downloads', { resourceIds: [r.id] });
      if (res.results[r.id] !== 'queued') throw new Error(res.results[r.id]);
      btn.replaceWith(h('span', { class: 'small muted' }, 'Queued'));
      toast(`Queued ${r.title}. It downloads in the background; see Downloads.`);
    } catch (e) { btn.disabled = false; toast(e.message); }
  }
  return null;
}

function filmCard(r) {
  const at = savedPosition(r.resourceId);
  return h('a', { class: 'film-card', href: `#/watch/${encodeURIComponent(r.resourceId)}` },
    h('span', { class: 'film-poster', 'aria-hidden': 'true' }, '▶︎'),
    h('span', { class: 'film-text' },
      h('b', null, r.title),
      h('span', { class: 'small muted' }, r.description),
      h('span', { class: 'small' }, at ? `Continue from ${fmtClock(at)}` : fmtBytes(r.size), ' ', warningBadges(r.warnings))));
}
