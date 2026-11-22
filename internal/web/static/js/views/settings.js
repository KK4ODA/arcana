import { api, getSession } from '../api.js';
import { h, clear, fmtBytes, toast } from '../util.js';

const GB = 1e9;

export async function settingsView(main) {
  const s = getSession();
  const r = await api.get('/api/settings');
  const st = r.settings;
  const ro = r.readOnly;

  const reserve = h('input', { type: 'number', min: 0, step: 1, value: st.reserveBytes ? Math.round(st.reserveBytes / GB) : '', placeholder: String(Math.round(r.defaultReserve / GB)), style: 'width:110px', disabled: ro });
  const conc = h('select', { disabled: ro }, [1, 2, 3, 4, 6, 8].map((n) => h('option', { value: n, selected: n === st.maxConcurrentDownloads }, n)));
  const bw = h('input', { type: 'number', min: 0, step: 0.1, value: st.bandwidthLimit ? (st.bandwidthLimit / 1e6).toFixed(1) : '', placeholder: 'unlimited', style: 'width:110px', disabled: ro });
  const priv = h('input', { type: 'checkbox', checked: st.allowPrivateNetworkDownloads, disabled: ro });
  const lan = h('input', { type: 'checkbox', checked: st.server.shareOnLan, disabled: ro });
  const kiwix = h('input', { type: 'checkbox', checked: st.catalog.kiwixEnabled, disabled: ro });
  let theme = 'system';
  try { theme = localStorage.getItem('arcana-theme') || 'system'; } catch (_) { /* ignore */ }
  const themeSel = h('select', null, [['system', 'Follow system'], ['light', 'Light'], ['dark', 'Dark']].map(([v, t]) => h('option', { value: v, selected: v === theme }, t)));
  themeSel.addEventListener('change', () => {
    try { localStorage.setItem('arcana-theme', themeSel.value); } catch (_) { /* ignore */ }
    window.dispatchEvent(new Event('arcana-theme'));
  });

  const feeds = [...(st.catalog.feeds || [])];
  const feedList = h('div');
  function renderFeeds() {
    clear(feedList);
    for (const f of feeds) {
      feedList.appendChild(h('div', { class: 'row', style: 'margin-bottom:4px' },
        h('span', { class: 'mono small' }, f.id), h('span', null, f.name), h('span', { class: 'mono small muted' }, f.url),
        h('button', { disabled: ro, onclick: () => { feeds.splice(feeds.indexOf(f), 1); renderFeeds(); } }, 'Remove')));
    }
  }
  renderFeeds();
  const fid = h('input', { type: 'text', placeholder: 'id (e.g. community)', style: 'width:150px', disabled: ro });
  const fname = h('input', { type: 'text', placeholder: 'Name', style: 'width:160px', disabled: ro });
  const furl = h('input', { type: 'url', placeholder: 'https://example.org/arcana-catalog.json', style: 'width:320px', disabled: ro });

  const field = (label, control, help) => h('div', { class: 'field' }, h('label', null, label), h('div', null, control, help ? h('div', { class: 'help' }, help) : null));

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Storage and downloads')),
    field('Reserved free space', h('span', null, reserve, ' GB'),
      `Downloads never use this space. Leave empty for the default (${fmtBytes(r.defaultReserve)}, 5% of the drive, between 1 and 50 GB).`),
    field('Simultaneous downloads', conc, 'More parallel downloads can be faster on fast connections; one is gentlest on slow or shared links.'),
    field('Bandwidth limit', h('span', null, bw, ' MB/s'), 'Leave empty for no limit. Applies to all downloads together.'),
    field('Local network sources', h('label', { class: 'inline' }, priv, 'Allow downloads from addresses on the local network'),
      'Off by default so a catalog cannot make Arcana send requests to devices on your network. Turn on to use a mirror or another Arcana installation on your LAN.')));

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Sharing')),
    field('Network sharing', h('label', { class: 'inline' }, lan, 'Let devices on this network browse the library (read-only)'),
      'Takes effect the next time Arcana starts.')));

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Catalog sources')),
    field('Kiwix library', h('label', { class: 'inline' }, kiwix, 'Use the Kiwix catalog (library.kiwix.org)'),
      'More than 3,000 offline archives: Wikipedia and other Wikimedia projects, Stack Exchange, iFixit, Project Gutenberg and more.'),
    field('Additional catalogs', h('div', null, feedList,
      h('div', { class: 'row' }, fid, fname, furl, h('button', { disabled: ro, onclick: () => {
        if (!fid.value || !furl.value) { toast('Enter an id and a URL.'); return; }
        feeds.push({ id: fid.value.trim().toLowerCase(), name: fname.value.trim() || fid.value.trim(), url: furl.value.trim(), enabled: true });
        fid.value = fname.value = furl.value = '';
        renderFeeds();
      } }, 'Add'))),
      'Catalogs in Arcana\'s JSON feed format (see docs/spec). Entries are validated and can only use their own identifier namespace. Refresh the catalog in Discover after adding.')));

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Appearance')),
    field('Theme', themeSel, 'Stored in this browser only.')));

  const save = h('button', { class: 'primary', disabled: ro, onclick: async () => {
    const body = {
      reserveBytes: reserve.value === '' ? 0 : Math.round(parseFloat(reserve.value) * GB),
      maxConcurrentDownloads: parseInt(conc.value, 10),
      bandwidthLimit: bw.value === '' ? 0 : Math.round(parseFloat(bw.value) * 1e6),
      allowPrivateNetworkDownloads: priv.checked,
      shareOnLan: lan.checked,
      kiwixEnabled: kiwix.checked,
      feeds,
    };
    try {
      const res = await api.put('/api/settings', body);
      toast(lan.checked !== st.server.shareOnLan ? 'Saved. Restart Arcana to change network sharing.' : 'Settings saved to the drive.');
      Object.assign(st, res.settings);
    } catch (e) { toast(e.message); }
  } }, 'Save settings');
  main.appendChild(h('div', { class: 'row', style: 'margin-bottom:14px' }, save,
    h('span', { class: 'small muted' }, ro ? 'The library is read-only.' : 'Settings are stored on the drive (user/settings.json), so they travel with the library.')));

  // Integrity
  const out = h('div', { class: 'body' });
  const run = async (full, btn) => {
    btn.disabled = true;
    clear(out).appendChild(h('p', { class: 'muted' }, full ? 'Reading and hashing every file. This can take a long time for large libraries…' : 'Checking…'));
    try {
      const rep = await api.get(`/api/check${full ? '?full=1' : ''}`);
      clear(out).appendChild(h('p', null, `${full ? 'Full' : 'Quick'} check of ${rep.resources} resources (${rep.files} files, ${fmtBytes(rep.bytes)}): `,
        rep.problems.length ? h('b', { style: 'color:var(--err)' }, `${rep.problems.length} problem(s)`) : h('b', { style: 'color:var(--ok)' }, 'no problems found.')));
      if (rep.problems.length) {
        out.appendChild(h('table', null, h('tbody', null, rep.problems.map((p) => h('tr', null,
          h('td', { class: 'mono small' }, p.kind), h('td', { class: 'mono small' }, p.resourceId || ''), h('td', { class: 'small' }, p.file || '', ' ', p.detail))))));
        out.appendChild(h('p', { class: 'small muted', style: 'margin-top:6px' }, 'Damaged or missing resources can be removed in Library and downloaded again. Orphaned folders are left over from interrupted installations.'));
      }
    } catch (e) { clear(out).appendChild(h('div', { class: 'banner err' }, e.message)); }
    btn.disabled = false;
  };
  const quickBtn = h('button', { onclick: (e) => run(false, e.target) }, 'Quick check');
  const fullBtn = h('button', { onclick: (e) => run(true, e.target) }, 'Full check (re-hash)');
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Library integrity'), quickBtn, fullBtn),
    h('div', { class: 'body small muted' }, 'A quick check confirms every installed file is present with the recorded size. A full check re-reads every byte and compares it with the SHA-256 recorded at installation, detecting silent corruption.'),
    out));

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'About this library')),
    h('div', { class: 'body' }, h('dl', { class: 'meta' },
      h('dt', null, 'Location'), h('dd', { class: 'mono small' }, r.libraryRoot),
      h('dt', null, 'Library ID'), h('dd', { class: 'mono small' }, r.libraryId),
      h('dt', null, 'Arcana version'), h('dd', { class: 'mono small' }, `${s.version} (${s.platform})`)))));
  return null;
}
