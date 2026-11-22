import { api, getSession } from '../api.js';
import { h, clear, fill, fmtBytes, fmtDate, toast } from '../util.js';
import { tasksPanel } from './tasks.js';

const GB = 1e9;

export async function settingsView(main) {
  const s = getSession();
  const [r, lib] = await Promise.all([api.get('/api/settings'), api.get('/api/library')]);
  const st = r.settings;
  const ro = r.readOnly;

  const reserve = h('input', { type: 'number', min: 0, step: 1, value: st.reserveBytes ? Math.round(st.reserveBytes / GB) : '', placeholder: String(Math.round(r.defaultReserve / GB)), style: 'width:110px', disabled: ro });
  const conc = h('select', { disabled: ro }, [1, 2, 3, 4, 6, 8].map((n) => h('option', { value: n, selected: n === st.maxConcurrentDownloads }, n)));
  const bw = h('input', { type: 'number', min: 0, step: 0.1, value: st.bandwidthLimit ? (st.bandwidthLimit / 1e6).toFixed(1) : '', placeholder: 'unlimited', style: 'width:110px', disabled: ro });
  const priv = h('input', { type: 'checkbox', checked: st.allowPrivateNetworkDownloads, disabled: ro });
  const lan = h('input', { type: 'checkbox', checked: st.server.shareOnLan, disabled: ro });
  const kiwix = h('input', { type: 'checkbox', checked: st.catalog.kiwixEnabled, disabled: ro });
  const gutenberg = h('input', { type: 'checkbox', checked: st.catalog.gutenbergEnabled, disabled: ro });
  const autoRefresh = h('select', { disabled: ro }, [[0, 'Off (refresh only when I ask)'], [24, 'Daily, while Arcana is running'], [168, 'Weekly'], [720, 'Monthly']]
    .map(([v, t]) => h('option', { value: v, selected: v === (st.catalog.autoRefreshHours || 0) }, t)));
  const updateURL = h('input', { type: 'url', value: st.updates?.url || '', style: 'width:min(460px,100%)', disabled: ro });
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
    field('Project Gutenberg', h('label', { class: 'inline' }, gutenberg, 'Include practical public-domain books (about 6,500)'),
      'Health, farming, cooking, engineering, crafts and science books from Project Gutenberg\u2019s mirror. Gutenberg publishes no checksums, so these downloads are marked unverified. Historical medical books are marked as outdated.'),
    field('Additional catalogs', h('div', null, feedList,
      h('div', { class: 'row' }, fid, fname, furl, h('button', { disabled: ro, onclick: () => {
        if (!fid.value || !furl.value) { toast('Enter an id and a URL.'); return; }
        feeds.push({ id: fid.value.trim().toLowerCase(), name: fname.value.trim() || fid.value.trim(), url: furl.value.trim(), enabled: true });
        fid.value = fname.value = furl.value = '';
        renderFeeds();
      } }, 'Add'))),
      'Catalogs in Arcana\u2019s JSON feed format (see docs/spec). Entries are validated and can only use their own identifier namespace.'),
    field('Automatic refresh', autoRefresh, 'Arcana makes no network requests unless you ask, or you turn this on. Failed attempts (no internet) are retried at the next interval.')));

  // Maps and position.
  const g = { source: '', baudRate: 4800, udpPort: 10110, simulatorSpeed: 1, ...(st.gps || {}) };
  const mapsOn = h('input', { type: 'checkbox', checked: st.maps ? st.maps.enabled : true, disabled: ro });
  const gpsSource = h('select', { disabled: ro }, [['', 'Off'], ['serial', 'USB or serial receiver (NMEA)'], ['gpsd', 'gpsd'],
    ['udp', 'NMEA over UDP (network multiplexer, phone app)'], ['tcp', 'NMEA over TCP'], ['simulator', 'Simulator (replays a GPX track)'],
    ['manual', 'Fixed position (entered by hand)']].map(([v, t]) => h('option', { value: v, selected: v === (g.source || '') }, t)));
  const serialPort = h('select', { disabled: ro, style: 'min-width:200px' });
  const fillPorts = async () => {
    let ports = [];
    try { ports = (await api.get('/api/gps/ports')).ports || []; } catch (_) { /* unavailable */ }
    if (g.serialPort && !ports.includes(g.serialPort)) ports.unshift(g.serialPort);
    fill(serialPort, ports.length ? ports.map((p) => h('option', { value: p, selected: p === g.serialPort }, p)) : h('option', { value: '' }, 'No serial ports found'));
  };
  fillPorts();
  const baud = h('select', { disabled: ro }, [4800, 9600, 19200, 38400, 57600, 115200].map((b) => h('option', { value: b, selected: b === (g.baudRate || 4800) }, b)));
  const udpPort = h('input', { type: 'number', min: 1, max: 65535, value: g.udpPort || 10110, style: 'width:110px', disabled: ro });
  const address = h('input', { type: 'text', value: g.address || '', placeholder: 'host:port, e.g. 192.168.1.20:10110 (gpsd: 127.0.0.1:2947)', style: 'width:min(360px,100%)', disabled: ro });
  const simTrack = h('input', { type: 'text', value: g.simulatorTrack || '', placeholder: 'empty: a 1 km loop on your map', style: 'width:min(300px,100%)', disabled: ro });
  const simSpeed = h('select', { disabled: ro }, [1, 2, 5, 10, 30].map((v) => h('option', { value: v, selected: v === (g.simulatorSpeed || 1) }, `${v}×`)));
  const mLat = h('input', { type: 'number', step: 'any', min: -90, max: 90, value: g.manualLat || '', placeholder: 'latitude', style: 'width:130px', disabled: ro });
  const mLon = h('input', { type: 'number', step: 'any', min: -180, max: 180, value: g.manualLon || '', placeholder: 'longitude', style: 'width:130px', disabled: ro });
  const record = h('input', { type: 'checkbox', checked: !!g.recordTrack, disabled: ro });
  const shareGPS = h('input', { type: 'checkbox', checked: !!g.shareWithClients, disabled: ro });
  const sourceFields = h('div');
  const renderSource = () => {
    const v = gpsSource.value;
    fill(sourceFields,
      v === 'serial' ? field('Port', h('span', { class: 'row' }, serialPort, baud, h('button', { onclick: fillPorts }, 'Rescan')),
        'Most USB receivers use 4800 or 9600 baud. On Linux, your user may need to be in the dialout group.') : null,
      v === 'udp' ? field('UDP port', udpPort, 'Arcana listens on this port on every network interface for NMEA sentences (10110 is the common default). It only reads; nothing is sent.') : null,
      v === 'tcp' || v === 'gpsd' ? field('Address', address, v === 'gpsd' ? 'Leave empty for gpsd on this computer (127.0.0.1:2947).' : 'Arcana connects to this address and reads NMEA sentences.') : null,
      v === 'simulator' ? field('Track', h('span', { class: 'row' }, simTrack, simSpeed),
        'Name of a GPX file in the user folder (tracks or documents). The simulator produces real NMEA sentences, so it tests the same path as a receiver. Positions are labelled as simulated.') : null,
      v === 'manual' ? field('Position', h('span', { class: 'row' }, mLat, mLon), 'Decimal degrees; south and west are negative.') : null);
  };
  gpsSource.addEventListener('change', renderSource);
  renderSource();
  const gpsBody = () => ({
    source: gpsSource.value, serialPort: (gpsSource.value === 'serial' && serialPort.value) || g.serialPort || '', baudRate: parseInt(baud.value, 10),
    udpPort: parseInt(udpPort.value, 10) || 10110, address: address.value.trim(), simulatorTrack: simTrack.value.trim(),
    simulatorSpeed: parseFloat(simSpeed.value), manualLat: parseFloat(mLat.value) || 0, manualLon: parseFloat(mLon.value) || 0,
    shareWithClients: shareGPS.checked, recordTrack: record.checked,
  });
  let gpsInitial = JSON.stringify(gpsBody());

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Maps and position')),
    field('Map regions', h('label', { class: 'inline' }, mapsOn, 'Offer OpenStreetMap regions in Discover'),
      'Region sizes are bundled with Arcana. Downloading a region extracts it from the newest Protomaps planet build.'),
    field('Position source', gpsSource, 'Arcana only reads positions. It never transmits them; network clients see them only if you allow it below.'),
    sourceFields,
    field('Track recording', h('label', { class: 'inline' }, record, 'Record a track while a position is available'),
      'Tracks are saved as GPX files in user/tracks on the drive.'),
    field('Share position', h('label', { class: 'inline' }, shareGPS, 'Show the position, waypoints and tracks to network clients'),
      'Off by default. Applies only when network sharing is on.')));

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
      gutenbergEnabled: gutenberg.checked,
      autoRefreshHours: parseInt(autoRefresh.value, 10),
      updateUrl: updateURL.value.trim(),
      feeds,
      mapsEnabled: mapsOn.checked,
    };
    const gb = gpsBody();
    if (JSON.stringify(gb) !== gpsInitial || !st.gps) body.gps = gb;
    try {
      const res = await api.put('/api/settings', body);
      toast(lan.checked !== st.server.shareOnLan ? 'Saved. Restart Arcana to change network sharing.' : 'Settings saved to the drive.');
      Object.assign(st, res.settings);
      gpsInitial = JSON.stringify(gpsBody());
    } catch (e) { toast(e.message); }
  } }, 'Save settings');
  main.appendChild(h('div', { class: 'row', style: 'margin-bottom:14px' }, save,
    h('span', { class: 'small muted' }, ro ? 'The library is read-only.' : 'Settings are stored on the drive (user/settings.json), so they travel with the library.')));

  // Background tasks (transfers, full checks, updates)
  const tp = tasksPanel();
  main.appendChild(tp.el);

  // Transfers and program updates touch the host's files: host only.
  if (s.local) main.appendChild(transferPanel(lib.resources, ro, tp));
  main.appendChild(integrityPanel(tp));
  if (s.local) main.appendChild(await updatesPanel(s, ro, updateURL, tp));

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'About this library')),
    h('div', { class: 'body' }, h('dl', { class: 'meta' },
      h('dt', null, 'Location'), h('dd', { class: 'mono small' }, r.libraryRoot),
      h('dt', null, 'Library ID'), h('dd', { class: 'mono small' }, r.libraryId),
      h('dt', null, 'Arcana version'), h('dd', { class: 'mono small' }, `${s.version} (${s.platform})`)))));
  return () => tp.stop();
}

function transferPanel(resources, ro, tp) {
  // Export
  const checks = new Map();
  const list = h('div', { class: 'checklist' }, resources.map((x) => {
    const box = h('input', { type: 'checkbox' });
    checks.set(x.resourceId, box);
    return h('label', null, box, h('span', { style: 'flex:1' }, x.title, ' ', h('span', { class: 'small muted' }, x.version)), h('span', { class: 'num small' }, fmtBytes(x.size)));
  }));
  const dest = h('input', { type: 'text', placeholder: 'Destination folder, e.g. F:\\ArcanaExport or /media/usb/ArcanaExport', style: 'width:min(520px,100%)' });
  const withDocs = h('input', { type: 'checkbox' });
  const exportBtn = h('button', { class: 'primary', onclick: async () => {
    const ids = [...checks].filter(([, b]) => b.checked).map(([id]) => id);
    try {
      await api.post('/api/transfer/export', { resourceIds: ids, destination: dest.value.trim(), userDocuments: withDocs.checked });
      toast('Export started. Progress is shown under Background tasks.');
      tp.refresh();
    } catch (e) { toast(e.message); }
  } }, 'Export');

  // Import
  const src = h('input', { type: 'text', placeholder: 'Folder with an Arcana export or another library', style: 'width:min(520px,100%)' });
  const planBox = h('div');
  const scan = h('button', { disabled: ro, onclick: async () => {
    try {
      const res = await api.get(`/api/transfer/scan?source=${encodeURIComponent(src.value.trim())}`);
      const boxes = new Map();
      const docsBox = h('input', { type: 'checkbox', checked: res.userDocuments > 0, disabled: res.userDocuments === 0 });
      fill(planBox,
        h('p', { class: 'small muted' }, `From “${res.source}”: ${res.plans.length} resources.`),
        h('table', null, h('thead', null, h('tr', null, h('th', { class: 'check' }), h('th', null, 'Resource'), h('th', null, 'Edition'), h('th', { class: 'right' }, 'Size'), h('th', null, 'Action'))),
          h('tbody', null, res.plans.map((p) => {
            const b = h('input', { type: 'checkbox', checked: p.action === 'install' || p.action === 'update', disabled: p.action === 'identical' || p.action === 'older' });
            boxes.set(p.resourceId, b);
            return h('tr', null, h('td', { class: 'check' }, b), h('td', null, p.title),
              h('td', { class: 'num small' }, p.version, p.installedVersion ? h('div', { class: 'faint' }, `installed: ${p.installedVersion}`) : null),
              h('td', { class: 'right num' }, fmtBytes(p.size)),
              h('td', { class: 'small' }, { install: 'Install', update: 'Update', identical: 'Already installed', older: 'Older than installed' }[p.action]));
          }))),
        h('div', { class: 'row', style: 'margin-top:8px' },
          h('label', { class: 'inline' }, docsBox, `Merge ${res.userDocuments} user documents (never overwrites yours)`),
          h('button', { class: 'primary', onclick: async () => {
            const ids = [...boxes].filter(([, b]) => b.checked).map(([id]) => id);
            if (!ids.length && !docsBox.checked) { toast('Nothing selected.'); return; }
            try {
              await api.post('/api/transfer/import', { source: src.value.trim(), resourceIds: ids, userDocuments: docsBox.checked });
              toast('Import started. Progress is shown under Background tasks.');
              fill(planBox);
              tp.refresh();
            } catch (e) { toast(e.message); }
          } }, 'Import selected')));
    } catch (e) { toast(e.message); }
  } }, 'Scan');

  const manifestBtn = h('button', { disabled: ro, onclick: async () => {
    try { const m = await api.post('/api/manifest'); toast(`Wrote ${m.path}`); } catch (e) { toast(e.message); }
  } }, 'Write library manifest');

  return h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Transfer between drives'), manifestBtn),
    h('div', { class: 'body' },
      h('p', { class: 'small muted' }, 'Copy resources to another drive or folder, or bring them in from an export or another Arcana library, with no internet. Every file is checked against the SHA-256 recorded at installation; damaged copies are refused and identical content is not copied twice.'),
      h('h3', { style: 'margin:10px 0 6px' }, 'Export'),
      resources.length ? list : h('p', { class: 'small muted' }, 'Nothing installed to export.'),
      h('div', { class: 'row', style: 'margin-top:6px' }, dest, h('label', { class: 'inline' }, withDocs, 'Include my documents'), exportBtn),
      h('h3', { style: 'margin:14px 0 6px' }, 'Import'),
      h('div', { class: 'row' }, src, scan),
      planBox));
}

function integrityPanel(tp) {
  const out = h('div', { class: 'body' });
  const quickBtn = h('button', { onclick: async (e) => {
    e.target.disabled = true;
    fill(out, h('p', { class: 'muted' }, 'Checking…'));
    try {
      const rep = await api.get('/api/check');
      fill(out, h('p', null, `Quick check of ${rep.resources} resources (${rep.files} files, ${fmtBytes(rep.bytes)}): `,
        rep.problems.length ? h('b', { style: 'color:var(--err)' }, `${rep.problems.length} problem(s)`) : h('b', { style: 'color:var(--ok)' }, 'no problems found.')),
      rep.problems.length ? h('table', null, h('tbody', null, rep.problems.map((p) => h('tr', null,
        h('td', { class: 'mono small' }, p.kind), h('td', { class: 'mono small' }, p.resourceId || ''), h('td', { class: 'small' }, p.file || '', ' ', p.detail))))) : null);
    } catch (err) { fill(out, h('div', { class: 'banner err' }, err.message)); }
    e.target.disabled = false;
  } }, 'Quick check');
  const fullBtn = h('button', { onclick: async () => {
    try { await api.post('/api/check/full'); toast('Full check started. Results appear under Background tasks.'); tp.refresh(); } catch (e) { toast(e.message); }
  } }, 'Full check (re-hash)');
  return h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Library integrity'), quickBtn, fullBtn),
    h('div', { class: 'body small muted' }, 'A quick check confirms every installed file is present with the recorded size. A full check re-reads every byte and compares it with the SHA-256 recorded at installation, detecting silent corruption. It runs in the background.'),
    out);
}

async function updatesPanel(s, ro, updateURL, tp) {
  let info;
  try { info = await api.get('/api/app/update'); } catch (_) { return h('div'); }
  const out = h('div', { class: 'small', style: 'margin-top:8px' });
  const folder = h('input', { type: 'text', placeholder: 'Folder containing release.json (e.g. an unzipped release on another drive)', style: 'width:min(520px,100%)' });
  const st = info.status || {};
  const render = (msg, available) => fill(out, msg ? h('p', null, msg) : null,
    available ? h('div', null, h('p', null, h('b', null, `Version ${available.version}`), ` · released ${fmtDate(available.released)}`),
      available.notes ? h('p', { class: 'note-text' }, available.notes) : null,
      h('button', { class: 'primary', disabled: ro, onclick: async () => {
        try { await api.post('/api/app/update/download'); toast('Downloading the update; see Background tasks.'); tp.refresh(); } catch (e) { toast(e.message); }
      } }, 'Download and verify')) : null);
  return h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Application updates')),
    h('div', { class: 'body' },
      h('dl', { class: 'meta' },
        h('dt', null, 'This version'), h('dd', { class: 'mono small' }, `${info.version} (${info.platform})`),
        h('dt', null, 'Signature keys'), h('dd', null, info.keysTrusted ? `${info.keysTrusted} trusted key(s): updates are verified before installation` : 'None in this build: signed updates cannot be verified, so this build installs no updates. Download new releases manually.'),
        st.staged ? [h('dt', null, 'Ready to install'), h('dd', null, `Version ${st.staged.version}`)] : null,
        st.appliedVersion ? [h('dt', null, 'Last installed'), h('dd', null, `${st.appliedVersion} on ${fmtDate(st.appliedAt)}${st.rolledBack ? ' (rolled back)' : ''}`)] : null),
      h('p', { class: 'small muted' }, 'Arcana never updates itself automatically and never requires updates. Content updates are separate (see Updates). The previous version is kept so an update can be rolled back.'),
      h('div', { class: 'field', style: 'padding-left:0;padding-right:0' }, h('label', null, 'Update address'), h('div', null, updateURL, h('div', { class: 'help' }, 'Where release.json and its signature are published. Saved with the settings above.'))),
      h('div', { class: 'row' },
        h('button', { disabled: !info.keysTrusted, onclick: async () => {
          try { const r = await api.post('/api/app/update/check'); render(r.checkMessage, r.available); } catch (e) { render(e.message); }
        } }, 'Check online'),
        folder,
        h('button', { disabled: ro || !info.keysTrusted, onclick: async () => {
          try { const m = await api.post('/api/app/update/stage', { source: folder.value.trim() }); render(`Verified the signature and files of version ${m.version}. Install it below.`); } catch (e) { render(e.message); }
        } }, 'Verify folder'),
        h('button', { class: 'primary', disabled: ro || !st.staged, onclick: async () => {
          if (!confirm('Install the staged version? Close and restart Arcana afterwards.')) return;
          try { const r = await api.post('/api/app/update/apply'); render(r.message); } catch (e) { render(e.message); }
        } }, 'Install staged update'),
        h('button', { disabled: ro || !st.appliedVersion || st.rolledBack, onclick: async () => {
          if (!confirm('Restore the previous version of the program files?')) return;
          try { const r = await api.post('/api/app/update/rollback'); render(r.message); } catch (e) { render(e.message); }
        } }, 'Roll back')),
      out));
}
