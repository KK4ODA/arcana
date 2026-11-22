// Maps: offline vector maps (MapLibre GL, vendored), place search from the
// local gazetteer, the live position from the GPS receiver, waypoints,
// tracks and custom-area downloads. Nothing here contacts the internet
// except "Measure" and "Download" for a custom area, which the host asks for
// explicitly.
import { api, getSession } from '../api.js';
import { h, clear, fill, fmtBytes, fmtAgo, toast, debounce } from '../util.js';

let libPromise = null;

function loadMapLibre() {
  if (window.maplibregl) return Promise.resolve(window.maplibregl);
  if (libPromise) return libPromise;
  libPromise = new Promise((resolve, reject) => {
    document.head.appendChild(h('link', { rel: 'stylesheet', href: 'vendor/maplibre-gl/maplibre-gl.css' }));
    const s = h('script', { src: 'vendor/maplibre-gl/maplibre-gl-csp.js' });
    s.addEventListener('load', () => {
      window.maplibregl.setWorkerUrl(new URL('vendor/maplibre-gl/maplibre-gl-csp-worker.js', location.href).href);
      resolve(window.maplibregl);
    });
    s.addEventListener('error', () => { libPromise = null; reject(new Error('The map viewer could not be loaded.')); });
    document.head.appendChild(s);
  });
  return libPromise;
}

function flavor() {
  const t = document.documentElement.dataset.theme;
  if (t === 'dark' || t === 'light') return t;
  return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

const layerCache = {};
async function buildStyle(fl, maxZoom) {
  if (!layerCache[fl]) {
    const r = await fetch(`maps/layers-${fl}.json`);
    if (!r.ok) throw new Error('Map style missing from this build.');
    layerCache[fl] = await r.json();
  }
  const o = location.origin;
  return {
    version: 8,
    glyphs: `${o}/maps/fonts/{fontstack}/{range}.pbf`,
    sprite: `${o}/maps/sprites/${fl}`,
    sources: {
      protomaps: {
        type: 'vector', tiles: [`${o}/maps/tiles/{z}/{x}/{y}.mvt`], minzoom: 0, maxzoom: Math.max(1, maxZoom || 15),
        attribution: '© <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">OpenStreetMap contributors</a> · <a href="https://protomaps.com" target="_blank" rel="noopener">Protomaps</a>',
      },
    },
    layers: structuredClone(layerCache[fl]),
  };
}

function cssVar(name) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim(); }

// ---------------------------------------------------------------- coordinates

function dms(v, pos, neg) {
  const hemi = v < 0 ? neg : pos;
  v = Math.abs(v);
  const d = Math.floor(v);
  const mf = (v - d) * 60;
  const m = Math.floor(mf);
  const s = ((mf - m) * 60).toFixed(1);
  return `${d}°${String(m).padStart(2, '0')}′${s.padStart(4, '0')}″${hemi}`;
}

// Maidenhead locator (6 characters), used by radio amateurs.
export function maidenhead(lat, lon) {
  const A = 'ABCDEFGHIJKLMNOPQRSTUVWX';
  let x = lon + 180;
  let y = lat + 90;
  if (x >= 360) x = 359.9999;
  if (y >= 180) y = 179.9999;
  const f1 = A[Math.floor(x / 20)] + A[Math.floor(y / 10)];
  x %= 20; y %= 10;
  const f2 = `${Math.floor(x / 2)}${Math.floor(y)}`;
  x %= 2; y %= 1;
  const f3 = A[Math.floor(x * 12)].toLowerCase() + A[Math.floor(y * 24)].toLowerCase();
  return f1 + f2 + f3;
}

function coordLines(lat, lon) {
  return [
    `${lat.toFixed(5)}, ${lon.toFixed(5)}`,
    `${dms(lat, 'N', 'S')} ${dms(lon, 'E', 'W')}`,
    `Grid ${maidenhead(lat, lon)}`,
  ];
}

const wrapLon = (lon) => ((((lon + 180) % 360) + 360) % 360) - 180;

const STATE_DOT = { current: 'ok', stale: 'warn', lost: 'err', error: 'err', searching: 'info', waiting: 'info', connecting: 'info', manual: 'info' };
const STATE_TEXT = {
  off: 'No receiver', connecting: 'Connecting', waiting: 'Waiting for data', searching: 'Searching for satellites',
  current: 'Position current', stale: 'Position stale', lost: 'Signal lost', manual: 'Fixed position', error: 'Receiver problem',
};
const ZOOM_FOR_KIND = { country: 5, region: 7, macroregion: 7, county: 9, locality: 12, city: 11, town: 13, village: 14, neighbourhood: 15, suburb: 14 };

// ---------------------------------------------------------------- view

export async function mapsView(main) {
  const s = getSession();
  const canChange = s.admin && !s.readOnly;
  let map = null;
  let es = null;
  let lastGPS = null;
  let follow = false;
  let waypoints = [];
  const shownTracks = new Map(); // name -> GeoJSON feature
  let areaBox = null;
  const disposers = [];

  main.classList.add('main-map');
  disposers.push(() => main.classList.remove('main-map'));

  const mapEl = h('div', { class: 'map-canvas', role: 'region', 'aria-label': 'Map' });
  const side = h('aside', { class: 'map-side' });
  const layout = h('div', { class: 'map-layout' }, mapEl, side);
  main.appendChild(layout);

  // Side panel sections.
  const searchInput = h('input', { type: 'search', placeholder: 'Search places', 'aria-label': 'Search places', autocomplete: 'off' });
  const searchResults = h('ul', { class: 'place-results' });
  const posBox = h('div', { class: 'pos-box' });
  const setsBox = h('div');
  const wptBox = h('div');
  const trkBox = h('div');
  const areaBoxEl = h('div');

  side.append(
    h('section', { class: 'map-sec' }, searchInput, searchResults),
    h('section', { class: 'map-sec' }, h('h3', null, 'Position'), posBox),
    h('section', { class: 'map-sec' }, h('h3', null, 'Installed maps'), setsBox),
    h('section', { class: 'map-sec' }, h('h3', null, 'Waypoints'), wptBox),
    h('section', { class: 'map-sec' }, h('h3', null, 'Tracks'), trkBox),
    canChange ? h('section', { class: 'map-sec' }, h('h3', null, 'Download this area'), areaBoxEl) : null,
  );

  // ------------------------------------------------------------ map status
  let ms;
  try { ms = await api.get('/api/maps'); } catch (e) { ms = { sets: [], maxZoom: 0 }; }
  const usable = ms.sets.filter((x) => !x.error);

  function renderSets() {
    if (!ms.sets.length) {
      fill(setsBox, h('p', { class: 'small muted' }, 'No maps installed. ',
        h('a', { href: '#/discover?category=maps&lang=' }, 'Browse map regions'),
        ' — the world overview is about 46 MB.'));
      return;
    }
    fill(setsBox,
      h('ul', { class: 'map-list' }, ms.sets.map((x) => h('li', null,
        h('button', { class: 'linkish', disabled: !!x.error || !map, onclick: () => fitSet(x) }, x.title),
        h('span', { class: 'sub' }, x.error ? `cannot open: ${x.error}` : `detail to zoom ${x.maxZoom} · ${x.version}`)))),
      ms.indexing ? h('p', { class: 'small muted' }, `Indexing place names in ${ms.indexing} (${Math.round((ms.progress || 0) * 100)}%)…`) : null,
      ms.error ? h('p', { class: 'small', style: 'color:var(--err)' }, `Place index: ${ms.error}`) : null,
      h('p', { class: 'small muted' }, h('a', { href: '#/discover?category=maps&lang=' }, 'Get more map regions')));
  }

  function fitSet(x) {
    const [w, so, e, n] = x.bounds;
    map.fitBounds([[w, so], [e, n]], { padding: 20, duration: 600 });
  }

  // ------------------------------------------------------------ map
  if (usable.length) {
    let ml;
    try { ml = await loadMapLibre(); } catch (e) { fill(mapEl, h('div', { class: 'banner err' }, e.message)); }
    if (ml) {
      const fl = flavor();
      const start = usable.reduce((a, b) => ((b.bounds[2] - b.bounds[0]) * (b.bounds[3] - b.bounds[1]) <
        (a.bounds[2] - a.bounds[0]) * (a.bounds[3] - a.bounds[1]) ? b : a));
      let saved = null;
      try { saved = JSON.parse(sessionStorage.getItem('arcana-map-view') || 'null'); } catch (_) { /* unavailable */ }
      map = new ml.Map({
        container: mapEl,
        style: await buildStyle(fl, ms.maxZoom),
        bounds: saved ? undefined : [[start.bounds[0], start.bounds[1]], [start.bounds[2], start.bounds[3]]],
        center: saved ? saved.center : undefined,
        zoom: saved ? saved.zoom : undefined,
        maxZoom: Math.min(20, (ms.maxZoom || 15) + 4),
        attributionControl: { compact: true },
        hash: false,
      });
      map.addControl(new ml.NavigationControl({ visualizePitch: false }), 'top-right');
      map.addControl(new ml.ScaleControl({ unit: 'metric' }), 'bottom-left');
      map.on('style.load', addOverlays);
      map.on('moveend', () => {
        try { sessionStorage.setItem('arcana-map-view', JSON.stringify({ center: map.getCenter().toArray(), zoom: map.getZoom() })); } catch (_) { /* unavailable */ }
      });
      map.on('dragstart', () => { if (follow) { follow = false; renderPosition(); } });
      map.on('click', (ev) => showPointPopup(ml, ev.lngLat));
      const onTheme = async () => {
        const next = flavor();
        map.setStyle(await buildStyle(next, ms.maxZoom));
      };
      const mq = matchMedia('(prefers-color-scheme: dark)');
      mq.addEventListener('change', onTheme);
      window.addEventListener('arcana-theme', onTheme);
      disposers.push(() => { mq.removeEventListener('change', onTheme); window.removeEventListener('arcana-theme', onTheme); });
      disposers.push(() => map.remove());
    }
  } else {
    fill(mapEl, h('div', { class: 'map-empty' },
      h('h2', null, 'No maps installed yet'),
      h('p', null, 'Maps are downloaded once and then work without internet. Start with the world overview, then add the countries or states you need in more detail.'),
      h('p', null, h('a', { class: 'button primary', href: '#/discover?category=maps&lang=' }, 'Browse map regions')),
      h('p', { class: 'small muted' }, 'Map data © OpenStreetMap contributors, available under the Open Database License.')));
  }
  renderSets();

  function addOverlays() {
    if (!map) return;
    const surface = cssVar('--surface') || '#fff';
    const text = cssVar('--text') || '#111';
    const empty = { type: 'FeatureCollection', features: [] };
    for (const id of ['arcana-area', 'arcana-tracks', 'arcana-waypoints', 'arcana-position']) {
      if (!map.getSource(id)) map.addSource(id, { type: 'geojson', data: empty });
    }
    map.addLayer({ id: 'arcana-area-line', type: 'line', source: 'arcana-area',
      paint: { 'line-color': cssVar('--warn') || '#9a6200', 'line-width': 2, 'line-dasharray': [3, 2] } });
    map.addLayer({ id: 'arcana-tracks-line', type: 'line', source: 'arcana-tracks',
      layout: { 'line-cap': 'round', 'line-join': 'round' },
      paint: { 'line-color': '#d9480f', 'line-width': 3, 'line-opacity': 0.85 } });
    map.addLayer({ id: 'arcana-wpt-dot', type: 'circle', source: 'arcana-waypoints',
      paint: { 'circle-radius': 5, 'circle-color': '#b02a6b', 'circle-stroke-width': 2, 'circle-stroke-color': surface } });
    map.addLayer({ id: 'arcana-wpt-label', type: 'symbol', source: 'arcana-waypoints',
      layout: { 'text-field': ['get', 'name'], 'text-font': ['Noto Sans Medium'], 'text-size': 12, 'text-offset': [0, 1.1], 'text-anchor': 'top', 'text-optional': true },
      paint: { 'text-color': text, 'text-halo-color': surface, 'text-halo-width': 1.5 } });
    map.addLayer({ id: 'arcana-pos-halo', type: 'circle', source: 'arcana-position',
      paint: { 'circle-radius': 15, 'circle-color': ['get', 'color'], 'circle-opacity': 0.18 } });
    map.addLayer({ id: 'arcana-pos-dot', type: 'circle', source: 'arcana-position',
      paint: { 'circle-radius': 6, 'circle-color': ['get', 'color'], 'circle-stroke-width': 2, 'circle-stroke-color': surface } });
    map.getSource('arcana-position').setData(positionData());
    map.getSource('arcana-waypoints').setData(waypointData());
    map.getSource('arcana-tracks').setData({ type: 'FeatureCollection', features: [...shownTracks.values()] });
    if (areaBox) drawArea(areaBox);
  }

  function setData(id, data) {
    const src = map && map.getSource(id);
    if (src) src.setData(data);
  }

  function showPointPopup(ml, ll) {
    const lat = ll.lat;
    const lon = wrapLon(ll.lng);
    const lines = coordLines(lat, lon);
    const content = h('div', { class: 'map-popup' },
      lines.map((l) => h('div', { class: 'num' }, l)),
      h('div', { class: 'row' },
        h('button', { onclick: () => { navigator.clipboard?.writeText(lines[0]).then(() => toast('Copied'), () => toast(lines[0])); } }, 'Copy'),
        canChange ? h('button', { onclick: () => { popup.remove(); addWaypointAt(lat, lon); } }, 'Save as waypoint') : null));
    const popup = new ml.Popup({ closeButton: true, maxWidth: '280px' }).setLngLat(ll).setDOMContent(content).addTo(map);
  }

  // ------------------------------------------------------------ search
  const doSearch = debounce(async () => {
    const q = searchInput.value.trim();
    if (!q) { clear(searchResults); return; }
    try {
      const r = await api.get(`/api/maps/search?q=${encodeURIComponent(q)}&limit=8`);
      fill(searchResults, r.places.length ? r.places.map((p) => h('li', null,
        h('button', { class: 'linkish', onclick: () => flyTo(p.lon, p.lat, ZOOM_FOR_KIND[p.kind] || 12) }, p.name),
        h('span', { class: 'sub' }, `${p.kind || 'place'} · ${p.lat.toFixed(3)}, ${p.lon.toFixed(3)}`)))
        : h('li', { class: 'small muted' }, ms.sets.length ? 'No matching place in the installed maps.' : 'Install a map to search places.'));
    } catch (e) { fill(searchResults, h('li', { class: 'small', style: 'color:var(--err)' }, e.message)); }
  }, 250);
  searchInput.addEventListener('input', doSearch);

  function flyTo(lon, lat, zoom) {
    if (!map) return;
    follow = false;
    map.flyTo({ center: [lon, lat], zoom, duration: 800 });
  }

  // ------------------------------------------------------------ position
  function positionData() {
    const f = lastGPS && lastGPS.fix;
    if (!f) return { type: 'FeatureCollection', features: [] };
    const color = lastGPS.state === 'current' || lastGPS.state === 'manual' ? (cssVar('--accent') || '#1f5a85')
      : lastGPS.state === 'stale' ? (cssVar('--warn') || '#9a6200') : (cssVar('--faint') || '#888');
    return { type: 'FeatureCollection', features: [{ type: 'Feature', properties: { color }, geometry: { type: 'Point', coordinates: [f.lon, f.lat] } }] };
  }

  function renderPosition() {
    const g = lastGPS;
    if (!g) { fill(posBox, h('p', { class: 'small muted' }, 'Loading…')); return; }
    if (g.private) {
      fill(posBox, h('p', { class: 'small muted' }, 'The host has not shared its position with network clients.'));
      return;
    }
    const f = g.fix;
    const rows = [];
    rows.push(h('div', { class: 'pos-state' }, h('span', { class: `dot ${STATE_DOT[g.state] || ''}` }),
      h('b', null, STATE_TEXT[g.state] || g.state),
      f && f.mode && g.state !== 'manual' ? h('span', { class: 'muted' }, ` · ${f.mode.toUpperCase()}`) : null,
      f && f.satellites ? h('span', { class: 'muted' }, ` · ${f.satellites} satellites`) : null));
    if (f) {
      const [a, b, c] = coordLines(f.lat, f.lon);
      rows.push(h('div', { class: 'pos-coords num' }, h('div', null, a), h('div', { class: 'muted small' }, b), h('div', { class: 'muted small' }, c)));
      const facts = [];
      if (f.alt != null) facts.push(`Altitude ${Math.round(f.alt)} m`);
      if (f.speed != null) facts.push(`Speed ${(f.speed * 3.6).toFixed(1)} km/h`);
      if (f.course != null) facts.push(`Course ${Math.round(f.course)}°`);
      if (f.hdop) facts.push(`HDOP ${f.hdop.toFixed(1)}`);
      if (g.state !== 'manual' && g.age != null) facts.push(g.age < 2 ? 'just now' : `${Math.round(g.age)} s ago`);
      if (facts.length) rows.push(h('div', { class: 'small muted' }, facts.join(' · ')));
    } else if (g.state === 'off') {
      rows.push(h('p', { class: 'small muted' }, 'No position source is selected. ',
        s.admin ? h('a', { href: '#/settings' }, 'Choose a GPS receiver or the simulator in Settings.') : null));
    }
    if (g.detail && g.state !== 'off') rows.push(h('div', { class: 'small muted' }, g.detail));
    if (g.error) rows.push(h('div', { class: 'small', style: 'color:var(--err)' }, g.error));
    if (g.recording) rows.push(h('div', { class: 'small muted' }, `Recording ${g.track || 'track'} · ${g.points || 0} points`));
    if (g.state !== 'off' && g.sentences + g.rejected > 0 && g.rejected > g.sentences) {
      rows.push(h('div', { class: 'small', style: 'color:var(--warn)' }, `Most data from the receiver is unreadable (${g.rejected} rejected). Check the baud rate.`));
    }
    rows.push(h('div', { class: 'row' },
      h('button', { disabled: !f || !map, onclick: () => { follow = false; flyTo(f.lon, f.lat, Math.max(map.getZoom(), 14)); } }, 'Center'),
      h('button', { disabled: !f || !map, 'aria-pressed': follow ? 'true' : 'false', onclick: () => { follow = !follow; renderPosition(); if (follow && f) map.easeTo({ center: [f.lon, f.lat] }); } }, follow ? 'Following' : 'Follow'),
      canChange ? h('button', { disabled: !f, onclick: () => addWaypointAt(f.lat, f.lon) }, 'Save as waypoint') : null));
    fill(posBox, rows);
  }

  function onGPS(g) {
    lastGPS = g;
    renderPosition();
    setData('arcana-position', positionData());
    if (follow && g.fix && map) map.easeTo({ center: [g.fix.lon, g.fix.lat], duration: 400 });
  }

  try {
    onGPS(await api.get('/api/gps'));
    es = new EventSource('/api/gps/stream');
    es.onmessage = (ev) => { try { onGPS(JSON.parse(ev.data)); } catch (_) { /* ignore */ } };
    disposers.push(() => es.close());
  } catch (e) {
    onGPS({ private: true });
  }

  // ------------------------------------------------------------ waypoints
  function waypointData() {
    return { type: 'FeatureCollection', features: waypoints.map((w) => ({ type: 'Feature', properties: { name: w.name, id: w.id }, geometry: { type: 'Point', coordinates: [w.lon, w.lat] } })) };
  }

  async function loadWaypoints() {
    try { waypoints = (await api.get('/api/gps/waypoints')).waypoints; } catch (_) { waypoints = []; }
    setData('arcana-waypoints', waypointData());
    const list = waypoints.length ? h('ul', { class: 'map-list' }, waypoints.map((w) => h('li', null,
      h('button', { class: 'linkish', onclick: () => flyTo(w.lon, w.lat, Math.max(map ? map.getZoom() : 14, 14)) }, w.name),
      h('span', { class: 'sub' }, `${w.lat.toFixed(5)}, ${w.lon.toFixed(5)}${w.note ? ` · ${w.note}` : ''}`),
      canChange ? h('button', { class: 'icon danger', title: `Delete ${w.name}`, 'aria-label': `Delete ${w.name}`, onclick: () => deleteWaypoint(w) }, '×') : null)))
      : h('p', { class: 'small muted' }, canChange ? 'Click the map or use your position to save a waypoint.' : 'No waypoints.');
    fill(wptBox, list, h('div', { class: 'row' },
      waypoints.length ? h('a', { class: 'button', href: '/api/gps/export', download: 'arcana-waypoints.gpx' }, 'Export GPX') : null,
      canChange ? importButton() : null));
  }

  async function addWaypointAt(lat, lon) {
    const name = prompt('Waypoint name', `Waypoint ${waypoints.length + 1}`);
    if (!name) return;
    try { await api.post('/api/gps/waypoints', { name, lat, lon }); toast('Waypoint saved'); loadWaypoints(); } catch (e) { toast(e.message); }
  }

  async function deleteWaypoint(w) {
    if (!confirm(`Delete waypoint “${w.name}”?`)) return;
    try { await api.del(`/api/gps/waypoints/${encodeURIComponent(w.id)}`); loadWaypoints(); } catch (e) { toast(e.message); }
  }

  function importButton() {
    const input = h('input', { type: 'file', accept: '.gpx,application/gpx+xml', hidden: true });
    input.addEventListener('change', async () => {
      const file = input.files[0];
      if (!file) return;
      try {
        const r = await fetch(`/api/gps/import?name=${encodeURIComponent(file.name)}`, {
          method: 'POST', headers: { 'X-Arcana-Token': getSession().token || '', 'Content-Type': 'application/gpx+xml' }, body: file,
        });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || r.statusText);
        toast(`Imported ${d.waypoints} waypoint(s) and ${d.tracks ? d.tracks.length : 0} track(s)${d.skipped ? `; ${d.skipped} skipped` : ''}`);
        loadWaypoints();
        loadTracks();
      } catch (e) { toast(e.message); }
      input.value = '';
    });
    return h('span', null, input, h('button', { onclick: () => input.click() }, 'Import GPX'));
  }

  // ------------------------------------------------------------ tracks
  async function loadTracks() {
    let tracks = [];
    try { tracks = (await api.get('/api/gps/tracks')).tracks; } catch (_) { /* private or unavailable */ }
    if (!tracks.length) {
      fill(trkBox, h('p', { class: 'small muted' }, 'No tracks. Turn on track recording in Settings, or import a GPX file.'));
      return;
    }
    fill(trkBox, h('ul', { class: 'map-list' }, tracks.map((t) => {
      const cb = h('input', { type: 'checkbox', checked: shownTracks.has(t.name), 'aria-label': `Show ${t.name} on the map` });
      cb.addEventListener('change', () => toggleTrack(t, cb.checked));
      return h('li', null,
        h('label', { class: 'track-row' }, cb, h('span', null, t.name.replace(/\.gpx$/, ''))),
        h('span', { class: 'sub' }, `${(t.length / 1000).toFixed(2)} km · ${t.points} points${t.start ? ` · ${fmtAgo(t.start)}` : ''}`),
        h('span', { class: 'row' },
          h('a', { class: 'small', href: `/api/gps/export?tracks=${encodeURIComponent(t.name)}`, download: t.name }, 'GPX'),
          canChange ? h('button', { class: 'icon danger', title: `Delete ${t.name}`, 'aria-label': `Delete ${t.name}`, onclick: () => deleteTrack(t) }, '×') : null));
    })));
  }

  async function toggleTrack(t, on) {
    if (on) {
      try {
        const f = await api.get(`/api/gps/tracks/${encodeURIComponent(t.name)}`);
        shownTracks.set(t.name, f);
        const coords = f.geometry.coordinates.flat();
        if (map && coords.length) {
          const lons = coords.map((c) => c[0]);
          const lats = coords.map((c) => c[1]);
          map.fitBounds([[Math.min(...lons), Math.min(...lats)], [Math.max(...lons), Math.max(...lats)]], { padding: 40, maxZoom: 16, duration: 600 });
        }
      } catch (e) { toast(e.message); }
    } else {
      shownTracks.delete(t.name);
    }
    setData('arcana-tracks', { type: 'FeatureCollection', features: [...shownTracks.values()] });
  }

  async function deleteTrack(t) {
    if (!confirm(`Delete track “${t.name}”?`)) return;
    try {
      await api.del(`/api/gps/tracks/${encodeURIComponent(t.name)}`);
      shownTracks.delete(t.name);
      setData('arcana-tracks', { type: 'FeatureCollection', features: [...shownTracks.values()] });
      loadTracks();
    } catch (e) { toast(e.message); }
  }

  // ------------------------------------------------------------ custom area
  function drawArea(b) {
    const [w, so, e, n] = b;
    const ring = w <= e ? [[w, so], [e, so], [e, n], [w, n], [w, so]] : [[w, so], [e + 360, so], [e + 360, n], [w, n], [w, so]];
    setData('arcana-area', { type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: ring } });
  }

  function currentBBox() {
    const bb = map.getBounds();
    const clampLat = (v) => Math.max(-85.05, Math.min(85.05, v));
    let w = bb.getWest();
    let e = bb.getEast();
    if (e - w >= 360) { w = -180; e = 180; } else { w = wrapLon(w); e = wrapLon(e); }
    const r = (v) => Math.round(v * 1e5) / 1e5;
    return [r(w), r(clampLat(bb.getSouth())), r(e), r(clampLat(bb.getNorth()))];
  }

  function renderArea() {
    if (!canChange) return;
    if (!map) { fill(areaBoxEl, h('p', { class: 'small muted' }, 'Install a map first (the world overview is enough) to choose an area.')); return; }
    const level = h('select', { 'aria-label': 'Detail level' },
      h('option', { value: '10' }, 'Overview (zoom 10): roads and towns'),
      h('option', { value: '13', selected: true }, 'Standard (zoom 13): streets and places'),
      h('option', { value: '15' }, 'Detailed (zoom 15): buildings and paths'));
    const name = h('input', { type: 'text', placeholder: 'Name, e.g. Home county', maxlength: 80, 'aria-label': 'Area name' });
    const out = h('div', { class: 'small' });
    const measure = h('button', null, 'Measure visible area');
    measure.addEventListener('click', async () => {
      areaBox = currentBBox();
      drawArea(areaBox);
      measure.disabled = true;
      fill(out, h('span', { class: 'muted' }, 'Measuring against the newest map build (downloads only the map index)…'));
      const req = { name: name.value.trim() || `Area ${areaBox.map((v) => v.toFixed(2)).join(', ')}`, bbox: areaBox, maxZoom: Number(level.value) };
      try {
        const p = await api.post('/api/maps/plan', req);
        const dl = h('button', { class: 'primary' }, `Download ${fmtBytes(p.bytes)}`);
        dl.addEventListener('click', async () => {
          dl.disabled = true;
          try { await api.post('/api/maps/areas', req); toast('Queued. Progress is shown in Downloads.'); } catch (e) { toast(e.message); dl.disabled = false; }
        });
        fill(out, h('div', null, `${p.tiles.toLocaleString()} tiles · ${fmtBytes(p.bytes)} · planet build ${p.build.slice(0, 4)}-${p.build.slice(4, 6)}-${p.build.slice(6)}`),
          h('div', { class: 'row' }, dl));
      } catch (e) {
        fill(out, h('span', { style: 'color:var(--err)' }, e.message));
      }
      measure.disabled = false;
    });
    fill(areaBoxEl,
      h('p', { class: 'small muted' }, 'Zoom the map to the area you need, then measure it. Requires an internet connection; only the area’s tiles are downloaded.'),
      name, level, h('div', { class: 'row' }, measure), out);
  }

  renderPosition();
  renderArea();
  loadWaypoints();
  loadTracks();

  // Keep the installed-map list current while place names are indexed.
  const poll = setInterval(async () => {
    try {
      const next = await api.get('/api/maps');
      if (JSON.stringify(next) !== JSON.stringify(ms)) {
        const reload = next.sets.length !== ms.sets.length;
        ms = next;
        renderSets();
        if (reload && map) map.setStyle(await buildStyle(flavor(), ms.maxZoom));
      }
    } catch (_) { /* ignore */ }
  }, 5000);
  disposers.push(() => clearInterval(poll));

  return () => { for (const d of disposers.reverse()) { try { d(); } catch (_) { /* ignore */ } } };
}
