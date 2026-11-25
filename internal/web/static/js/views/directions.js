// Directions and turn-by-turn guidance in the map view. Routes come from
// the installed road networks (GET /api/route); guidance runs here, from
// the position (a GPS receiver, a Starlink dish, this browser's location or
// any other source in Settings), and asks for a new route when the
// traveller leaves this one. Routes can be saved in the user folder and
// followed later. Spoken instructions use the browser's own voices, when it
// has any.
import { api } from '../api.js';
import { h, clear, fill, toast, debounce } from '../util.js';
import { getUnits, fmtDistance, fmtStepDistance, fmtTravelTime } from '../units.js';
import { distance, nearestOnPath } from '../geo.js';

const MODES = [['car', 'Car'], ['bike', 'Bicycle'], ['foot', 'Walk']];
// How far from the route (metres) counts as having left it.
const OFF_ROUTE = { car: 50, bike: 35, foot: 30 };
// When to announce a maneuver (metres before it): far, near, now.
const ANNOUNCE = { car: [1000, 250, 40], bike: [200, 60, 15], foot: [100, 30, 10] };

const ARROWS = {
  depart: '↑', arrive: '◎', waypoint: '◉', roundabout: '⟳', uturn: '↶', merge: '↗', ramp: '↗',
  straight: '↑', 'slight left': '↖', left: '←', 'sharp left': '↙', 'slight right': '↗', right: '→', 'sharp right': '↘',
};

function arrow(step) {
  if (step.type === 'turn' || step.type === 'fork' || step.type === 'continue' || step.type === 'ramp' || step.type === 'merge') {
    return ARROWS[step.modifier] || ARROWS[step.type] || '↑';
  }
  return ARROWS[step.type] || '↑';
}

// spokenDistance says a distance the way a voice should: "300 metres",
// "half a mile".
function spokenDistance(m) {
  const s = fmtStepDistance(m);
  return s.replace(/ ft$/, ' feet').replace(/^0\.5 mi$/, 'half a mile').replace(/ mi$/, ' miles')
    .replace(/ km$/, ' kilometres').replace(/ m$/, ' metres');
}

function lowerFirst(s) { return s ? s[0].toLowerCase() + s.slice(1) : s; }

function gpxFor(route, name) {
  const esc = (s) => String(s).replace(/[<>&"]/g, (c) => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;' }[c]));
  const pts = route.geometry.map(([lon, lat]) => `<trkpt lat="${lat}" lon="${lon}"></trkpt>`).join('');
  return `<?xml version="1.0" encoding="UTF-8"?>\n<gpx version="1.1" creator="Arcana" xmlns="http://www.topografix.com/GPX/1/1">` +
    `<metadata><name>${esc(name)}</name><copyright author="OpenStreetMap contributors"><license>https://www.openstreetmap.org/copyright</license></copyright></metadata>` +
    `<trk><name>${esc(name)}</name><trkseg>${pts}</trkseg></trk></gpx>\n`;
}

// liveFix is the position to start from or follow: current, fixed, or at
// most a minute old.
function liveFix(g) {
  return g && g.fix && (g.state === 'current' || g.state === 'manual' || g.state === 'stale') ? g.fix : null;
}

// createDirections builds the directions panel. ctx gives access to the
// map view: map(), setData(id, data), gps() (the last GPS message),
// flyTo(lon, lat, zoom), fitBounds(bounds), cssVar(name), follow(on),
// canChange (may save routes).
export function createDirections(ctx) {
  let mode = 'car';
  try { mode = localStorage.getItem('arcana-route-mode') || 'car'; } catch (_) { /* ignore */ }
  if (!MODES.some(([m]) => m === mode)) mode = 'car';
  const ends = { from: null, to: null }; // { lat, lon, label }
  let picking = null; // 'from' | 'to'
  let route = null;
  let network = null;
  let networks = [];
  let nav = null; // navigation state
  let busy = false;

  const el = h('div', { class: 'directions' });
  const out = h('div', { class: 'route-out' });
  const savedBox = h('div', { class: 'saved-routes' });
  let saved = null; // null: not shared by the host
  const navEl = h('div', { class: 'nav-banner', hidden: true, role: 'status', 'aria-live': 'polite' });

  // ------------------------------------------------------------ inputs
  function endInput(which, placeholder) {
    const input = h('input', { type: 'search', placeholder, 'aria-label': placeholder, autocomplete: 'off' });
    const list = h('ul', { class: 'place-results end-results' });
    const search = debounce(async () => {
      const q = input.value.trim();
      if (!q || (ends[which] && q === ends[which].label)) { clear(list); return; }
      try {
        const r = await api.get(`/api/maps/search?q=${encodeURIComponent(q)}&limit=6`);
        fill(list, r.places.map((p) => h('li', null,
          h('button', { class: 'linkish', onclick: () => { setEnd(which, { lat: p.lat, lon: p.lon, label: p.name }); clear(list); } }, p.name),
          h('span', { class: 'sub' }, p.kind || 'place'))));
      } catch (_) { clear(list); }
    }, 250);
    input.addEventListener('input', () => { ends[which] = null; search(); });
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); const b = list.querySelector('button'); if (b) b.click(); }
    });
    const pick = h('button', { class: 'icon', title: 'Choose on the map', 'aria-label': `Choose the ${which === 'from' ? 'start' : 'destination'} on the map` }, '⌖');
    pick.addEventListener('click', () => startPicking(which));
    const here = which === 'from' ? h('button', { class: 'icon', title: 'Use my position', 'aria-label': 'Use my position as the start' }, '◎') : null;
    if (here) here.addEventListener('click', () => useMyPosition());
    return { input, list, pick, here, row: h('div', { class: 'end-row' }, h('span', { class: 'end-badge' }, which === 'from' ? 'A' : 'B'), input, here, pick) };
  }
  const fromI = endInput('from', 'Start: search, or choose on the map');
  const toI = endInput('to', 'Destination: search, or choose on the map');
  const inputs = { from: fromI, to: toI };

  const modeBar = h('div', { class: 'seg', role: 'radiogroup', 'aria-label': 'Travel by' });
  function renderModes() {
    fill(modeBar, MODES.map(([m, label]) => h('button', {
      role: 'radio', 'aria-checked': m === mode ? 'true' : 'false', class: m === mode ? 'on' : '',
      onclick: () => { mode = m; try { localStorage.setItem('arcana-route-mode', m); } catch (_) { /* ignore */ } renderModes(); if (ends.from && ends.to) compute(); },
    }, label)));
  }
  renderModes();

  const goBtn = h('button', { class: 'primary', onclick: () => compute() }, 'Get directions');
  const swapBtn = h('button', { title: 'Swap start and destination', onclick: () => {
    const f = ends.from; ends.from = ends.to; ends.to = f;
    fromI.input.value = ends.from ? ends.from.label : '';
    toI.input.value = ends.to ? ends.to.label : '';
    drawEnds();
    if (ends.from && ends.to) compute();
  } }, '⇅ Swap');
  const clearBtn = h('button', { onclick: () => reset() }, 'Clear');
  const hint = h('p', { class: 'small muted' });

  function fmtLabel(p) { return `${p.lat.toFixed(5)}, ${p.lon.toFixed(5)}`; }

  function setEnd(which, p) {
    ends[which] = p;
    inputs[which].input.value = p.label || fmtLabel(p);
    clear(inputs[which].list);
    drawEnds();
    if (ends.from && ends.to) compute();
    else if (which === 'from' && !ends.to) toI.input.focus();
  }

  function myPosition() {
    const f = liveFix(ctx.gps());
    return f ? { lat: f.lat, lon: f.lon, label: 'My position', mine: true } : null;
  }

  function useMyPosition() {
    const p = myPosition();
    if (!p) { toast('No position yet. Choose a position source in Settings: a GPS receiver, a Starlink dish or this browser’s location.'); return false; }
    const f = ctx.gps().fix;
    if (f.accuracy > 500) toast(`Your position is only good to about ${fmtDistance(f.accuracy)}; check the start on the map.`);
    setEnd('from', p);
    return true;
  }

  function startPicking(which) {
    picking = which;
    hint.textContent = `Click the map to choose the ${which === 'from' ? 'start' : 'destination'}.`;
    const m = ctx.map();
    if (m) m.getCanvas().classList.add('measuring');
  }

  function stopPicking() {
    picking = null;
    hint.textContent = '';
    const m = ctx.map();
    if (m) m.getCanvas().classList.remove('measuring');
  }

  // onMapClick lets the panel take a click while choosing a point.
  function onMapClick(ll) {
    if (!picking) return false;
    const which = picking;
    stopPicking();
    setEnd(which, { lat: ll.lat, lon: ((ll.lng + 540) % 360) - 180, label: '' });
    return true;
  }

  function reset() {
    stopNav();
    ends.from = ends.to = null;
    route = network = null;
    fromI.input.value = toI.input.value = '';
    clear(out);
    stopPicking();
    drawEnds();
    drawRoute();
  }

  // ------------------------------------------------------------ map layers
  function addLayers(map) {
    const surface = ctx.cssVar('--surface') || '#fff';
    const empty = { type: 'FeatureCollection', features: [] };
    for (const id of ['arcana-route', 'arcana-route-ends', 'arcana-route-next']) {
      if (!map.getSource(id)) map.addSource(id, { type: 'geojson', data: empty });
    }
    map.addLayer({ id: 'arcana-route-casing', type: 'line', source: 'arcana-route', layout: { 'line-cap': 'round', 'line-join': 'round' },
      paint: { 'line-color': '#0b3d66', 'line-width': 8, 'line-opacity': 0.55 } });
    map.addLayer({ id: 'arcana-route-line', type: 'line', source: 'arcana-route', layout: { 'line-cap': 'round', 'line-join': 'round' },
      paint: { 'line-color': '#2f80c9', 'line-width': 5 } });
    map.addLayer({ id: 'arcana-route-next', type: 'circle', source: 'arcana-route-next',
      paint: { 'circle-radius': 7, 'circle-color': surface, 'circle-stroke-width': 3, 'circle-stroke-color': '#2f80c9' } });
    map.addLayer({ id: 'arcana-route-ends', type: 'circle', source: 'arcana-route-ends',
      paint: { 'circle-radius': 9, 'circle-color': ['get', 'color'], 'circle-stroke-width': 2, 'circle-stroke-color': surface } });
    map.addLayer({ id: 'arcana-route-ends-label', type: 'symbol', source: 'arcana-route-ends',
      layout: { 'text-field': ['get', 'label'], 'text-font': ['Noto Sans Medium'], 'text-size': 11, 'text-allow-overlap': true },
      paint: { 'text-color': '#fff' } });
    drawEnds();
    drawRoute();
  }

  function drawEnds() {
    const f = [];
    if (ends.from) f.push({ type: 'Feature', properties: { label: 'A', color: '#1b7f3b' }, geometry: { type: 'Point', coordinates: [ends.from.lon, ends.from.lat] } });
    if (ends.to) f.push({ type: 'Feature', properties: { label: 'B', color: '#b3261e' }, geometry: { type: 'Point', coordinates: [ends.to.lon, ends.to.lat] } });
    ctx.setData('arcana-route-ends', { type: 'FeatureCollection', features: f });
  }

  function drawRoute() {
    ctx.setData('arcana-route', route ? { type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: route.geometry } } : { type: 'FeatureCollection', features: [] });
    if (!route) ctx.setData('arcana-route-next', { type: 'FeatureCollection', features: [] });
  }

  function showNext(step) {
    ctx.setData('arcana-route-next', step ? { type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: step.location } } : { type: 'FeatureCollection', features: [] });
  }

  function fitRoute() {
    if (!route) return;
    let w = Infinity; let s = Infinity; let e = -Infinity; let n = -Infinity;
    for (const [lon, lat] of route.geometry) { w = Math.min(w, lon); e = Math.max(e, lon); s = Math.min(s, lat); n = Math.max(n, lat); }
    ctx.fitBounds([[w, s], [e, n]]);
  }

  // ------------------------------------------------------------ routing
  async function fetchRoute(points) {
    const q = points.map((p) => `${p.lat.toFixed(6)},${p.lon.toFixed(6)}`).join(';');
    return api.get(`/api/route?mode=${mode}&points=${encodeURIComponent(q)}`);
  }

  async function compute() {
    if (!ends.from || !ends.to) { toast('Choose a start and a destination.'); return; }
    if (busy) return;
    busy = true;
    goBtn.disabled = true;
    fill(out, h('p', { class: 'small muted' }, 'Finding the route… (the first route in a region also loads its road network)'));
    try {
      const r = await fetchRoute([ends.from, ends.to]);
      route = r.route;
      network = r.network;
      drawRoute();
      fitRoute();
      renderRoute();
    } catch (e) {
      route = null;
      drawRoute();
      fill(out, h('div', { class: 'banner warn' }, e.message), noNetworkHelp(e.message));
    } finally {
      busy = false;
      goBtn.disabled = false;
    }
  }

  function noNetworkHelp(msg) {
    if (!/road network/.test(msg)) return null;
    return h('p', { class: 'small' }, h('a', { href: '#/discover?category=maps-navigation&lang=&q=road%20network' }, 'Get a road network for your area'));
  }

  function stepItem(s, i) {
    const li = h('li', { class: 'step' },
      h('span', { class: 'step-arrow', 'aria-hidden': 'true' }, arrow(s)),
      h('span', { class: 'step-text' }, s.text),
      s.distance > 0 ? h('span', { class: 'step-dist num' }, fmtDistance(s.distance)) : null);
    li.addEventListener('click', () => { showNext(s); ctx.flyTo(s.location[0], s.location[1], 17); });
    li.dataset.index = String(i);
    return li;
  }

  function renderRoute() {
    if (!route) { clear(out); return; }
    const gpsFix = liveFix(ctx.gps());
    const navBtn = h('button', { class: 'primary', disabled: !gpsFix, title: gpsFix ? '' : 'Turn-by-turn guidance needs a live position', onclick: () => startNav() }, 'Start navigation');
    const saveBtn = ctx.canChange && saved !== null ? h('button', { onclick: () => saveRoute() }, 'Save route') : null;
    const gpx = h('button', { onclick: () => {
      const name = `Route to ${ends.to.label || fmtLabel(ends.to)}`;
      const blob = new Blob([gpxFor(route, name)], { type: 'application/gpx+xml' });
      const a = h('a', { href: URL.createObjectURL(blob), download: 'arcana-route.gpx' });
      document.body.appendChild(a);
      a.click();
      setTimeout(() => { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
    } }, 'Save as GPX');
    fill(out,
      h('div', { class: 'route-summary' },
        h('span', { class: 'route-time' }, fmtTravelTime(route.duration)),
        h('span', { class: 'num' }, fmtDistance(route.distance))),
      h('div', { class: 'small muted' }, `${MODES.find(([m]) => m === route.mode)[1]} · ${network ? network.title.replace(/ — road network$/, '') : ''} · © OpenStreetMap contributors`),
      route.warnings.map((w) => h('div', { class: 'small route-warn' }, w)),
      h('div', { class: 'row' }, navBtn, saveBtn, gpx),
      gpsFix ? null : h('p', { class: 'small muted' }, 'Turn-by-turn guidance follows your position: choose a GPS receiver, a Starlink dish or this browser’s location in Settings. You can save the route and follow it later.'),
      h('ol', { class: 'steps' }, route.steps.map(stepItem)),
      h('p', { class: 'small muted' }, 'Directions come from OpenStreetMap data and may be wrong or out of date. Follow road signs and traffic rules.'));
  }

  // ------------------------------------------------------------ navigation
  let voice = false;
  try { voice = localStorage.getItem('arcana-route-voice') === '1'; } catch (_) { /* ignore */ }
  const canSpeak = 'speechSynthesis' in window;

  function say(text) {
    if (!voice || !canSpeak) return;
    try {
      speechSynthesis.cancel();
      const u = new SpeechSynthesisUtterance(text);
      speechSynthesis.speak(u);
    } catch (_) { /* no voice available */ }
  }

  function prepare(r) {
    // Cumulative distance along the line, and where each step happens.
    const cum = [0];
    for (let i = 1; i < r.geometry.length; i++) cum.push(cum[i - 1] + distance(r.geometry[i - 1], r.geometry[i]));
    return { route: r, cum, along: 0, seg: 0, off: 0, announced: new Set(), lastReroute: 0, arrived: false };
  }

  function startNav() {
    if (!route) return;
    nav = prepare(route);
    navEl.hidden = false;
    ctx.follow(true);
    // On a phone the panel sits below the map: bring the map into view.
    if (matchMedia('(max-width: 820px)').matches && ctx.showMap) ctx.showMap();
    renderNav();
    const g = ctx.gps();
    if (g && g.fix) onGPS(g);
    say(route.steps[0].text);
  }

  function stopNav() {
    if (!nav) return;
    nav = null;
    navEl.hidden = true;
    clear(navEl);
    showNext(null);
    if (canSpeak) try { speechSynthesis.cancel(); } catch (_) { /* ignore */ }
  }

  // Steps from the current one on; steps[i].index is where maneuver i is.
  function upcoming() {
    const r = nav.route;
    for (let i = 1; i < r.steps.length; i++) {
      const at = nav.cum[r.steps[i].index] || 0;
      if (at > nav.along + 3) return { i, dist: at - nav.along };
    }
    return { i: r.steps.length - 1, dist: 0 };
  }

  function renderNav() {
    if (!nav) return;
    const r = nav.route;
    if (nav.arrived) {
      fill(navEl, h('div', { class: 'nav-main' }, h('span', { class: 'nav-arrow' }, '◎'), h('div', null, h('div', { class: 'nav-text' }, 'You have arrived'))),
        h('div', { class: 'nav-foot' }, h('button', { onclick: () => stopNav() }, 'End')));
      return;
    }
    const { i, dist } = upcoming();
    const step = r.steps[i];
    const next = r.steps[i + 1];
    showNext(step);
    const total = nav.cum[nav.cum.length - 1];
    const left = Math.max(0, total - nav.along);
    const timeLeft = r.distance > 0 ? r.duration * (left / r.distance) : 0;
    const eta = new Date(Date.now() + timeLeft * 1000);
    const voiceBtn = canSpeak ? h('button', { 'aria-pressed': voice ? 'true' : 'false', onclick: () => {
      voice = !voice;
      try { localStorage.setItem('arcana-route-voice', voice ? '1' : '0'); } catch (_) { /* ignore */ }
      renderNav();
    } }, voice ? 'Voice on' : 'Voice off') : null;
    fill(navEl,
      h('div', { class: 'nav-main' },
        h('span', { class: 'nav-arrow', 'aria-hidden': 'true' }, arrow(step)),
        h('div', null,
          h('div', { class: 'nav-dist num' }, fmtStepDistance(dist)),
          h('div', { class: 'nav-text' }, step.text))),
      next && next.type !== 'arrive' && step.distance < 300 ? h('div', { class: 'nav-then small' }, `Then ${lowerFirst(next.text)}`) : null,
      nav.off > 0 ? h('div', { class: 'nav-then small' }, 'Off the route — finding a new one…') : null,
      h('div', { class: 'nav-foot' },
        h('span', { class: 'num' }, `${fmtDistance(left)} · ${fmtTravelTime(timeLeft)} · arrive ${eta.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`),
        voiceBtn, h('button', { onclick: () => stopNav() }, 'End')));
  }

  async function reroute(fix) {
    if (Date.now() - nav.lastReroute < 10000 || busy) return;
    nav.lastReroute = Date.now();
    busy = true;
    try {
      const r = await fetchRoute([{ lat: fix.lat, lon: fix.lon }, ends.to]);
      if (!nav) return;
      route = r.route;
      network = r.network;
      nav = { ...prepare(route), lastReroute: Date.now() };
      drawRoute();
      renderRoute();
      say(`New route. ${route.steps[0].text}`);
    } catch (e) {
      toast(`Could not find a new route: ${e.message}`);
    } finally {
      busy = false;
    }
  }

  // onGPS follows the traveller along the route.
  function onGPS(g) {
    if (!nav || !g || !g.fix || (g.state !== 'current' && g.state !== 'manual')) return;
    const p = [g.fix.lon, g.fix.lat];
    const r = nav.route;
    // Search near the last known position first, so a route that passes
    // the same place twice is followed in order.
    const from = Math.max(0, nav.seg - 2);
    const windowPts = r.geometry.slice(from, Math.min(r.geometry.length, nav.seg + 400));
    let near = windowPts.length >= 2 ? nearestOnPath(windowPts, p) : null;
    if (near) near = { ...near, index: near.index + from, along: nav.cum[from] + near.along };
    // A coarse position (Wi-Fi, a dish on a roof) must not count as
    // leaving the route while it is still within its own error.
    const offRoute = Math.max(OFF_ROUTE[r.mode], Math.min(g.fix.accuracy || 0, 300));
    if (!near || near.dist > offRoute) {
      const all = nearestOnPath(r.geometry, p);
      if (!near || all.dist < near.dist) near = all;
    }
    if (near.dist > offRoute) {
      nav.off++;
      if (nav.off >= 2) reroute(g.fix);
      renderNav();
      return;
    }
    nav.off = 0;
    nav.seg = near.index;
    nav.along = Math.max(nav.along - 20, near.along); // GPS jitter must not walk backwards much
    const total = nav.cum[nav.cum.length - 1];
    const dest = r.geometry[r.geometry.length - 1];
    if (!nav.arrived && (total - nav.along < 15 || distance(p, dest) < 20)) {
      nav.arrived = true;
      say('You have arrived at your destination.');
      renderNav();
      return;
    }
    const { i, dist } = upcoming();
    const step = r.steps[i];
    const [far, close, now] = ANNOUNCE[r.mode];
    const key = (k) => `${i}:${k}`;
    if (dist <= now && !nav.announced.has(key('now'))) {
      nav.announced.add(key('now')); nav.announced.add(key('near')); nav.announced.add(key('far'));
      say(step.text);
    } else if (dist <= close && dist > now && !nav.announced.has(key('near'))) {
      nav.announced.add(key('near')); nav.announced.add(key('far'));
      say(`In ${spokenDistance(dist)}, ${lowerFirst(step.text)}`);
    } else if (dist <= far && dist > close * 1.5 && !nav.announced.has(key('far'))) {
      nav.announced.add(key('far'));
      say(`In ${spokenDistance(dist)}, ${lowerFirst(step.text)}`);
    }
    renderNav();
  }

  // ------------------------------------------------------------ panel
  async function refreshNetworks() {
    try { networks = (await api.get('/api/roads')).networks; } catch (_) { networks = []; }
    render();
  }

  function render() {
    if (!networks.length) {
      fill(el, h('p', { class: 'small muted' }, 'Directions need a road network for the area, built from OpenStreetMap. ',
        h('a', { href: '#/discover?category=maps-navigation&lang=&q=road%20network' }, 'Get a road network'), '.'), out, savedBox);
      return;
    }
    fill(el, modeBar, fromI.row, fromI.list, toI.row, toI.list, hint,
      h('div', { class: 'row' }, goBtn, swapBtn, clearBtn), out, savedBox);
  }

  // ------------------------------------------------------------ saved routes
  async function loadSaved() {
    try { saved = (await api.get('/api/gps/routes')).routes; } catch (_) { saved = null; }
    renderSaved();
  }

  function renderSaved() {
    if (!saved || !saved.length) {
      fill(savedBox, saved && ctx.canChange ? h('p', { class: 'small muted' }, 'Saved routes appear here. They can be followed later, even without the road network.') : null);
      return;
    }
    fill(savedBox, h('h4', null, 'Saved routes'), h('ul', { class: 'map-list' }, saved.map((r) => h('li', null,
      h('button', { class: 'linkish', onclick: () => openSaved(r.id) }, r.name),
      h('span', { class: 'sub' }, `${MODES.find(([m]) => m === r.mode)?.[1] || r.mode} · ${fmtDistance(r.distance)} · ${fmtTravelTime(r.duration)}`),
      h('span', { class: 'row' },
        h('a', { class: 'small', href: `/api/gps/routes/${encodeURIComponent(r.id)}/gpx`, download: `${r.name}.gpx` }, 'GPX'),
        ctx.canChange ? h('button', { class: 'icon danger', title: `Delete ${r.name}`, 'aria-label': `Delete ${r.name}`, onclick: () => deleteSaved(r) }, '×') : null)))));
  }

  async function saveRoute() {
    if (!route) return;
    const name = prompt('Route name', `Route to ${ends.to.label || fmtLabel(ends.to)}`);
    if (name === null) return;
    const end = (p) => ({ lat: p.lat, lon: p.lon, label: p.label || '' });
    try {
      await api.post('/api/gps/routes', {
        name, mode: route.mode, from: end(ends.from), to: end(ends.to), distance: route.distance, duration: route.duration,
        network: network ? network.title : '', warnings: route.warnings, geometry: route.geometry, steps: route.steps,
      });
      toast('Route saved. Open it under Saved routes to follow it later.');
      loadSaved();
    } catch (e) { toast(e.message); }
  }

  async function openSaved(id) {
    try {
      const r = await api.get(`/api/gps/routes/${encodeURIComponent(id)}`);
      stopNav();
      stopPicking();
      ends.from = { ...r.from };
      ends.to = { ...r.to };
      fromI.input.value = r.from.label || fmtLabel(r.from);
      toI.input.value = r.to.label || fmtLabel(r.to);
      if (MODES.some(([m]) => m === r.mode)) { mode = r.mode; renderModes(); }
      route = { mode: r.mode, distance: r.distance, duration: r.duration, geometry: r.geometry, steps: r.steps, warnings: r.warnings || [] };
      network = r.network ? { title: r.network } : null;
      drawEnds();
      drawRoute();
      fitRoute();
      renderRoute();
    } catch (e) { toast(e.message); }
  }

  async function deleteSaved(r) {
    if (!confirm(`Delete saved route “${r.name}”?`)) return;
    try { await api.del(`/api/gps/routes/${encodeURIComponent(r.id)}`); loadSaved(); } catch (e) { toast(e.message); }
  }

  const onUnits = () => { renderRoute(); renderNav(); };
  window.addEventListener('arcana-units', onUnits);
  refreshNetworks();
  loadSaved();

  return {
    el,
    navEl,
    addLayers,
    onMapClick,
    onGPS,
    // directionsFrom/To are offered in the map's point popup.
    setFrom: (lat, lon) => setEnd('from', { lat, lon, label: '' }),
    // Directions to a point start from the current position when no start
    // was chosen.
    setTo: (lat, lon) => {
      if (!ends.from) { const p = myPosition(); if (p) { ends.from = p; fromI.input.value = p.label; } }
      setEnd('to', { lat, lon, label: '' });
    },
    fromMyPosition: () => { if (useMyPosition() && !ends.to) toI.input.focus(); },
    hasNetworks: () => networks.length > 0,
    gpsChanged: () => { if (route && !nav) renderRoute(); },
    dispose: () => { stopNav(); window.removeEventListener('arcana-units', onUnits); },
    units: getUnits,
  };
}
