import { api, getSession } from '../api.js';
import { h, clear, fill, fmtBytes, fmtDate, toast } from '../util.js';
import { tasksPanel } from './tasks.js';
import { integrationsPanel, peersPanel } from './integrations.js';
import { getUnits, setUnits, UNIT_SYSTEMS } from '../units.js';

const GB = 1e9;

export async function settingsView(main) {
  const s = getSession();
  const [r, lib, asst] = await Promise.all([api.get('/api/settings'), api.get('/api/library'), api.get('/api/assistant').catch(() => null)]);
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

  const unitsSel = h('select', null, UNIT_SYSTEMS.map(([v, t]) => h('option', { value: v, selected: v === getUnits() }, t)));
  unitsSel.addEventListener('change', () => setUnits(unitsSel.value));

  const feeds = [...(st.catalog.feeds || [])];
  const feedList = h('div');
  function renderFeeds() {
    clear(feedList);
    for (const f of feeds) {
      feedList.appendChild(h('div', { class: 'row', style: 'margin-bottom:4px' },
        h('span', { class: 'mono small' }, f.id), h('span', null, f.name), h('span', { class: 'mono small muted' }, f.url),
        f.publicKey ? h('span', { class: 'badge', title: `Signed by ${f.publicKey}` }, 'Signed') : null,
        h('button', { disabled: ro, onclick: () => { feeds.splice(feeds.indexOf(f), 1); renderFeeds(); } }, 'Remove')));
    }
  }
  renderFeeds();
  const fid = h('input', { type: 'text', placeholder: 'id (e.g. community)', style: 'width:150px', disabled: ro });
  const fname = h('input', { type: 'text', placeholder: 'Name', style: 'width:160px', disabled: ro });
  const furl = h('input', { type: 'url', placeholder: 'https://example.org/arcana-catalog.json', style: 'width:320px', disabled: ro });
  const fkey = h('input', { type: 'text', placeholder: 'publisher key (optional, base64 Ed25519)', class: 'mono', style: 'width:300px', disabled: ro });

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
      h('div', { class: 'row' }, fid, fname, furl, fkey, h('button', { disabled: ro, onclick: () => {
        if (!fid.value || !furl.value) { toast('Enter an id and a URL.'); return; }
        feeds.push({ id: fid.value.trim().toLowerCase(), name: fname.value.trim() || fid.value.trim(), url: furl.value.trim(), enabled: true, publicKey: fkey.value.trim() || undefined });
        fid.value = fname.value = furl.value = fkey.value = '';
        renderFeeds();
      } }, 'Add'))),
      'Catalogs in Arcana\u2019s JSON feed format (see docs/spec). Entries are validated and can only use their own identifier namespace. With a publisher key, the catalog is accepted only if its .sig file matches.'),
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

  // Meshtastic radio (listen only).
  const mc = { source: '', address: '', serialPort: '', baudRate: 115200, shareWithClients: false, ...(st.mesh || {}) };
  const meshSource = h('select', { disabled: ro }, [['', 'Off'], ['tcp', 'Radio on the network (Wi-Fi or Ethernet)'], ['serial', 'Radio on USB']]
    .map(([v, t]) => h('option', { value: v, selected: v === (mc.source || '') }, t)));
  const meshAddr = h('input', { type: 'text', value: mc.address || '', placeholder: 'radio address, e.g. 192.168.1.40 or meshtastic.local', style: 'width:min(320px,100%)', disabled: ro });
  const meshPort = h('select', { disabled: ro, style: 'min-width:200px' });
  const fillMeshPorts = async () => {
    let ports = [];
    try { ports = (await api.get('/api/gps/ports')).ports || []; } catch (_) { /* unavailable */ }
    if (mc.serialPort && !ports.includes(mc.serialPort)) ports.unshift(mc.serialPort);
    fill(meshPort, ports.length ? ports.map((p) => h('option', { value: p, selected: p === mc.serialPort }, p)) : h('option', { value: '' }, 'No serial ports found'));
  };
  const meshShare = h('input', { type: 'checkbox', checked: !!mc.shareWithClients, disabled: ro });
  const meshFields = h('div');
  const renderMesh = () => {
    fill(meshFields,
      meshSource.value === 'tcp' ? field('Radio address', meshAddr, 'The radio must have Wi-Fi or Ethernet turned on in its network settings. Arcana connects to port 4403.') : null,
      meshSource.value === 'serial' ? field('Port', h('span', { class: 'row' }, meshPort, h('button', { onclick: fillMeshPorts }, 'Rescan')),
        'Close the Meshtastic app or web client first: only one program can use the radio\u2019s USB port at a time.') : null);
    if (meshSource.value === 'serial') fillMeshPorts();
  };
  meshSource.addEventListener('change', renderMesh);
  renderMesh();
  const meshBody = () => ({ source: meshSource.value, address: meshAddr.value.trim(),
    serialPort: (meshSource.value === 'serial' && meshPort.value) || mc.serialPort || '', baudRate: mc.baudRate || 115200, shareWithClients: meshShare.checked });
  let meshInitial = JSON.stringify(meshBody());

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Maps and position')),
    field('Map regions', h('label', { class: 'inline' }, mapsOn, 'Offer OpenStreetMap regions in Discover'),
      'Region sizes are bundled with Arcana. Downloading a region extracts it from the newest Protomaps planet build.'),
    field('Position source', gpsSource, 'Arcana only reads positions. It never transmits them; network clients see them only if you allow it below.'),
    sourceFields,
    field('Track recording', h('label', { class: 'inline' }, record, 'Record a track while a position is available'),
      'Tracks are saved as GPX files in user/tracks on the drive.'),
    field('Share position', h('label', { class: 'inline' }, shareGPS, 'Show the position, waypoints and tracks to network clients'),
      'Off by default. Applies only when network sharing is on.'),
    field('Meshtastic radio', meshSource, 'Show the nodes a Meshtastic radio hears on the map, with their positions and the text messages on its channels. Arcana only listens; it never transmits.'),
    meshFields,
    field('Share the mesh', h('label', { class: 'inline' }, meshShare, 'Show mesh nodes and messages to network clients'),
      'Off by default: the positions of people on the mesh are personal.')));

  // Links on the Library page.
  const links = [...(st.links || [])];
  const linksInitial = JSON.stringify(links);
  const linkList = h('div');
  const renderLinks = () => fill(linkList, links.map((l) => h('div', { class: 'row', style: 'margin-bottom:4px' },
    h('b', null, l.title), h('span', { class: 'mono small muted' }, l.url), l.description ? h('span', { class: 'small' }, l.description) : null,
    h('button', { disabled: ro, onclick: () => { links.splice(links.indexOf(l), 1); renderLinks(); } }, 'Remove'))));
  renderLinks();
  const lTitle = h('input', { type: 'text', placeholder: 'Title, e.g. Kolibri courses', style: 'width:200px', maxlength: 60, disabled: ro });
  const lURL = h('input', { type: 'url', placeholder: 'http://raspberrypi.local:8080', style: 'width:260px', disabled: ro });
  const lDesc = h('input', { type: 'text', placeholder: 'Short description (optional)', style: 'width:260px', maxlength: 200, disabled: ro });
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Links on the Library page')),
    field('Other services', h('div', null, linkList, h('div', { class: 'row' }, lTitle, lURL, lDesc, h('button', { disabled: ro, onclick: () => {
      if (!lTitle.value.trim() || !/^https?:\/\//.test(lURL.value.trim())) { toast('Enter a title and an http:// or https:// address.'); return; }
      links.push({ title: lTitle.value.trim(), url: lURL.value.trim(), description: lDesc.value.trim() });
      lTitle.value = lURL.value = lDesc.value = '';
      renderLinks();
    } }, 'Add'))), 'Shortcuts shown to everyone on the Library page, for other things on this network: a Kolibri server, a radio\u2019s web page, a file share. Arcana does not check or manage them.')));

  const ap = assistantPanel(st, asst, ro, field);
  main.appendChild(ap.el);

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Appearance')),
    field('Theme', themeSel, 'Stored in this browser only.'),
    field('Units', unitsSel, 'For distances, speeds, altitudes and areas on the map. Stored in this browser only.')));

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
    const mb = meshBody();
    if (JSON.stringify(mb) !== meshInitial) body.mesh = mb;
    if (JSON.stringify(links) !== linksInitial) body.links = links;
    const ab = ap.body();
    if (JSON.stringify(ab) !== ap.initial) body.assistant = ab;
    try {
      const res = await api.put('/api/settings', body);
      toast(lan.checked !== st.server.shareOnLan ? 'Saved. Restart Arcana to change network sharing.' : 'Settings saved to the drive.');
      Object.assign(st, res.settings);
      gpsInitial = JSON.stringify(gpsBody());
      meshInitial = JSON.stringify(meshBody());
      ap.initial = JSON.stringify(ap.body());
      window.dispatchEvent(new Event('arcana-status'));
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
  let ip = null;
  if (s.local && !ro) {
    ip = integrationsPanel();
    main.appendChild(ip.el);
    main.appendChild(peersPanel().el);
  }

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'About this library')),
    h('div', { class: 'body' }, h('dl', { class: 'meta' },
      h('dt', null, 'Location'), h('dd', { class: 'mono small' }, r.libraryRoot),
      h('dt', null, 'Library ID'), h('dd', { class: 'mono small' }, r.libraryId),
      h('dt', null, 'Arcana version'), h('dd', { class: 'mono small' }, `${s.version} (${s.platform})`)))));
  return () => { tp.stop(); if (ip) ip.stop(); };
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
  let status;
  try { [info, status] = await Promise.all([api.get('/api/app/update'), api.get('/api/status')]); } catch (_) { return h('div'); }
  const n = status.appUpdate || {};
  const out = h('div', { class: 'small', style: 'margin-top:8px' });
  const st = info.status || {};
  const auto = h('input', { type: 'checkbox', checked: !!n.autoCheck, disabled: ro || !info.keysTrusted });
  auto.addEventListener('change', async () => {
    try { await api.put('/api/settings', { autoUpdateCheck: auto.checked }); toast(auto.checked ? 'Arcana will look for new versions once a day.' : 'Automatic checks are off.'); } catch (e) { toast(e.message); auto.checked = !auto.checked; }
  });
  const available = (v) => fill(out,
    h('p', null, h('b', null, `Version ${v.version}`), v.released ? ` · released ${fmtDate(v.released)}` : ''),
    v.notes ? h('p', { class: 'note-text' }, v.notes) : null,
    n.selfUpdate && n.selfUpdate.possible
      ? h('p', null, 'Use ', h('b', null, 'Update now'), ' in the bar at the top of the page; Arcana restarts by itself.')
      : h('p', null, (n.selfUpdate && n.selfUpdate.reason) || ''));
  const folder = h('input', { type: 'text', placeholder: 'Folder containing release.json (e.g. an unzipped release on another drive)', style: 'width:min(520px,100%)' });
  if (n.available) available({ version: n.available, released: n.released, notes: n.notes });
  else if (n.installed) fill(out, h('p', null, h('b', null, `Version ${n.installed} is installed.`), ' Close Arcana and start it again to use it.'));
  return h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Application updates')),
    h('div', { class: 'body' },
      h('dl', { class: 'meta' },
        h('dt', null, 'This version'), h('dd', { class: 'mono small' }, `${info.version} (${info.platform})`),
        h('dt', null, 'Last check'), h('dd', null, n.checkedAt ? fmtDate(n.checkedAt) : 'never'),
        st.appliedVersion ? [h('dt', null, 'Last installed'), h('dd', null, `${st.appliedVersion} on ${fmtDate(st.appliedAt)}${st.rolledBack ? ' (rolled back)' : ''}`)] : null),
      info.keysTrusted ? null : h('p', { class: 'small' }, 'This build has no release key, so it cannot verify updates. Download new versions manually.'),
      h('label', { class: 'inline' }, auto, 'Look for new versions automatically (once a day, only while online)'),
      h('p', { class: 'small muted' }, 'Only the update address below is contacted. Every update is checked against Arcana’s release signature before anything is installed; the previous version is kept so it can be rolled back. Content updates are separate (see Updates).'),
      h('div', { class: 'row' },
        h('button', { disabled: !info.keysTrusted, onclick: async () => {
          try {
            const r = await api.post('/api/app/update/check');
            if (r.available) { available(r.available); window.dispatchEvent(new Event('arcana-status')); } else fill(out, h('p', null, r.checkMessage || 'You have the latest version.'));
          } catch (e) { fill(out, h('p', null, e.message)); }
        } }, 'Check now'),
        h('button', { disabled: ro || !st.appliedVersion || st.rolledBack, onclick: async () => {
          if (!confirm('Restore the previous version of the program files? Restart Arcana afterwards.')) return;
          try { const r = await api.post('/api/app/update/rollback'); fill(out, h('p', null, r.message)); } catch (e) { fill(out, h('p', null, e.message)); }
        } }, 'Roll back')),
      out,
      h('details', { style: 'margin-top:8px' }, h('summary', { class: 'small' }, 'Advanced: install from a folder, update address'),
        h('div', { class: 'field', style: 'padding-left:0;padding-right:0' }, h('label', null, 'Update address'), h('div', null, updateURL, h('div', { class: 'help' }, 'Where release.json and its signature are published. Saved with the settings above.'))),
        h('div', { class: 'row' }, folder,
          h('button', { disabled: ro || !info.keysTrusted, onclick: async () => {
            try { const m = await api.post('/api/app/update/stage', { source: folder.value.trim() }); fill(out, h('p', null, `Verified the signature and files of version ${m.version}.`)); } catch (e) { fill(out, h('p', null, e.message)); }
          } }, 'Verify folder'),
          h('button', { disabled: ro || !st.staged, onclick: async () => {
            if (!confirm('Install the verified version? Restart Arcana afterwards.')) return;
            try { const r = await api.post('/api/app/update/apply'); fill(out, h('p', null, r.message)); } catch (e) { fill(out, h('p', null, e.message)); }
          } }, 'Install verified version')))));
}

// assistantPanel configures question answering. Its values are saved with
// the other settings.
export function assistantPanel(st, info, ro, field) {
  const a = { backend: 'local', relevanceFloor: 0.5, passages: 5, contextSize: 4096, url: 'http://127.0.0.1:11434/v1', ...(st.assistant || {}) };
  const status = info?.status || {};
  const enabled = h('input', { type: 'checkbox', checked: !!a.enabled, disabled: ro });
  const backend = h('select', { disabled: ro }, [['local', 'This computer: a model downloaded into the library'],
    ['external', 'A model server: Ollama, LM Studio or another OpenAI-compatible program']]
    .map(([v, t]) => h('option', { value: v, selected: v === a.backend }, t)));
  const models = status.models || [];
  const model = h('select', { disabled: ro }, h('option', { value: '' }, 'Largest installed model'),
    models.map((m) => h('option', { value: m.resourceId, selected: m.resourceId === a.model }, `${m.title} (${fmtBytes(m.size)})`)));
  const ctx = h('select', { disabled: ro }, [[2048, '2,048 tokens (least memory)'], [4096, '4,096 tokens (default)'], [8192, '8,192 tokens'], [16384, '16,384 tokens']]
    .map(([v, t]) => h('option', { value: v, selected: v === a.contextSize }, t)));
  const threads = h('input', { type: 'number', min: 0, max: 256, value: a.threads || '', placeholder: 'automatic', style: 'width:110px', disabled: ro });
  const idle = h('select', { disabled: ro }, [[5, '5 minutes'], [10, '10 minutes'], [30, '30 minutes'], [120, '2 hours']]
    .map(([v, t]) => h('option', { value: v, selected: v === (a.idleMinutes || 10) }, t)));
  const url = h('input', { type: 'url', value: a.url || '', placeholder: 'http://127.0.0.1:11434/v1', style: 'width:min(360px,100%)', disabled: ro });
  const key = h('input', { type: 'password', value: a.apiKey || '', placeholder: 'not needed for Ollama or LM Studio', autocomplete: 'off', style: 'width:min(300px,100%)', disabled: ro });
  const extModel = h('input', { type: 'text', value: a.externalModel || '', placeholder: 'e.g. llama3.2:3b', list: 'assistant-models', style: 'width:min(260px,100%)', disabled: ro });
  const modelList = h('datalist', { id: 'assistant-models' });
  const testMsg = h('span', { class: 'small muted' });
  const testBtn = h('button', { type: 'button', disabled: ro, onclick: async () => {
    testMsg.textContent = 'Connecting…';
    try {
      const r = await api.post('/api/assistant/test', { url: url.value.trim(), apiKey: key.value === '(saved)' ? '' : key.value });
      fill(modelList, r.models.map((m) => h('option', { value: m })));
      if (!extModel.value && r.models.length) extModel.value = r.models[0];
      testMsg.textContent = r.models.length ? `Connected. ${r.models.length} model${r.models.length === 1 ? '' : 's'}: ${r.models.slice(0, 6).join(', ')}` : 'Connected, but the server lists no models.';
    } catch (e) { testMsg.textContent = e.message; }
  } }, 'Test connection');
  const floor = h('select', { disabled: ro }, [[0.35, 'Lenient: answer from loosely related passages'], [0.5, 'Normal'], [0.7, 'Strict: only from closely matching passages']]
    .map(([v, t]) => h('option', { value: v, selected: Math.abs(v - a.relevanceFloor) < 0.01 }, t)));
  const passages = h('select', { disabled: ro }, [3, 5, 8].map((n) => h('option', { value: n, selected: n === a.passages }, `${n} passages`)));
  const instr = h('textarea', { rows: 3, maxlength: 4000, style: 'width:min(560px,100%)', placeholder: 'Optional, for example: "Use metric units." or "Answer in Spanish."', disabled: ro });
  instr.value = a.instructions || '';
  const net = h('input', { type: 'checkbox', checked: !!a.networkClients, disabled: ro });

  const engineState = { stopped: 'stopped (starts with the next question)', starting: 'starting', ready: 'running', failed: 'failed' }[status.engine?.state] || 'stopped';
  const stopBtn = h('button', { type: 'button', disabled: ro || status.engine?.state !== 'ready', onclick: async () => {
    try { await api.post('/api/assistant/stop'); stopBtn.disabled = true; toast('The model engine was stopped; its memory is free.'); } catch (e) { toast(e.message); }
  } }, 'Stop now');

  const localFields = h('div', null,
    field('Model', models.length ? model : h('span', null, 'No model installed. ', h('a', { href: '#/discover?category=assistant' }, 'Download one in Discover'), '.'),
      'Larger models answer better but need more memory and time. The tiny model suits a Raspberry Pi; the balanced model most laptops.'),
    field('Engine', h('span', null, status.engineInstalled ? `llamafile, ${engineState}. ` : 'Not installed (downloaded with the first model). ', status.engineInstalled ? stopBtn : null,
      status.engine?.error ? h('div', { class: 'banner err' }, status.engine.error) : null),
      'Runs only on this computer, only while answering, and stops after the idle time below.'),
    field('Context window', ctx, 'How much text the model reads at once. Larger windows need more memory.'),
    field('Processor threads', threads, 'Leave empty to let the engine choose.'),
    field('Stop when idle after', idle, 'Frees the model’s memory when no questions are asked.'));
  const extFields = h('div', null,
    field('Server address', h('span', { class: 'row' }, url, testBtn), h('span', null, testMsg, h('br'),
      'Ollama listens on port 11434 (http://127.0.0.1:11434/v1 — start it with OLLAMA_HOST=0.0.0.0 to reach it from another computer), LM Studio on port 1234 (http://127.0.0.1:1234/v1). ',
      'Questions and passages from your library are sent to this server.')),
    field('Model name', h('span', null, extModel, modelList), 'As the server names it. Test the connection to list its models.'),
    field('API key', key, 'Only if the server requires one. Stored on the drive in clear text, like other connection settings.'));
  const showBackend = () => { localFields.hidden = backend.value !== 'local'; extFields.hidden = backend.value !== 'external'; };
  backend.addEventListener('change', showBackend);
  showBackend();

  const body = () => ({
    enabled: enabled.checked, backend: backend.value, model: model.value, url: url.value.trim(), externalModel: extModel.value.trim(),
    apiKey: key.value, instructions: instr.value, relevanceFloor: parseFloat(floor.value), passages: parseInt(passages.value, 10),
    contextSize: parseInt(ctx.value, 10), threads: parseInt(threads.value, 10) || 0, idleMinutes: parseInt(idle.value, 10),
    networkClients: net.checked,
  });
  const el = h('section', { class: 'panel', id: 'assistant' },
    h('header', null, h('h2', null, 'Assistant (question answering)')),
    field('Question answering', h('label', { class: 'inline' }, enabled, 'Answer questions from the library'),
      'Answers are written by a language model from passages Arcana finds in your library, and list those passages. Nothing is downloaded until you choose a model.'),
    field('Where the model runs', backend), localFields, extFields,
    field('Strictness', floor, 'How closely a passage must match the question before the model is asked. When nothing matches, Arcana says so instead of guessing.'),
    field('Passages per answer', passages, 'More passages give the model more to work with but take longer to read.'),
    field('Instructions', instr, 'Added to the model’s instructions for every answer. The rule to answer only from the library always applies.'),
    field('Network devices', h('label', { class: 'inline' }, net, 'Let devices on the network ask questions'),
      'Off by default: answers use this computer’s processor and memory. Devices never see your own documents.'));
  return { el, body, initial: JSON.stringify(body()) };
}
