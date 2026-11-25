// Sends this browser's position to Arcana when the owner chose "This
// browser's location services" as a position source in Settings
// (ADR-0026). The position comes from the Geolocation API, which uses the
// device's own GPS, Wi-Fi or mobile network as the operating system sees
// fit. Browsers only offer it to secure pages (HTTPS, or localhost).
// Arcana's server feeds what it receives through the same path as a GPS
// receiver, so the map, tracks and guidance work unchanged.
import { api, getSession } from './api.js';

const KEY = 'arcana-browser-id';
let id = null;

// browserID is a random identifier kept by this browser, so Settings can
// say which browser's location to use.
export function browserID() {
  if (id) return id;
  try { id = localStorage.getItem(KEY); } catch (_) { /* storage unavailable */ }
  if (!id || !/^[A-Za-z0-9-]{8,64}$/.test(id)) {
    const b = new Uint8Array(12);
    crypto.getRandomValues(b);
    id = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
    try { localStorage.setItem(KEY, id); } catch (_) { /* this tab only */ }
  }
  return id;
}

// geoState says whether this browser is sending its position, and why not
// when it should but cannot.
export const geoState = { active: false, problem: '', accuracy: null };

let watch = null;
let timer = null;
let latest = null;
let sentAt = 0;

function changed() { window.dispatchEvent(new Event('arcana-geo')); }

function setProblem(p) {
  if (geoState.problem === p) return;
  geoState.problem = p;
  changed();
}

// insecureReason explains why a browser will not share its location here.
export function insecureReason() {
  if (!('geolocation' in navigator)) return 'This browser has no location services.';
  if (!window.isSecureContext) {
    return 'Browsers share their location only with secure pages. Open Arcana with its https address (turn on HTTPS on the Server page), or use the browser on the computer running Arcana (localhost).';
  }
  return '';
}

async function send() {
  if (!latest) return;
  const c = latest.coords;
  const num = (v) => (typeof v === 'number' && Number.isFinite(v) ? v : null);
  try {
    await api.post('/api/gps/browser', {
      lat: c.latitude, lon: c.longitude, accuracy: num(c.accuracy) || 0,
      altitude: num(c.altitude), speed: num(c.speed), heading: num(c.heading), clientId: browserID(),
    });
    sentAt = Date.now();
    geoState.accuracy = num(c.accuracy);
    setProblem('');
  } catch (e) {
    // The owner chose another source or another browser.
    if (/another browser|not a position source/.test(e.message)) stop();
    else setProblem(e.message);
  }
}

function start() {
  if (watch !== null) return;
  const why = insecureReason();
  if (why) { setProblem(why); return; }
  geoState.active = true;
  changed();
  watch = navigator.geolocation.watchPosition((p) => {
    latest = p;
    if (Date.now() - sentAt > 1000) send();
  }, (err) => {
    latest = null;
    setProblem(err.code === 1
      ? 'Location permission was refused. Allow location for this site in the browser’s site settings, then reload.'
      : `The browser could not find its location: ${err.message || 'unknown error'}.`);
  }, { enableHighAccuracy: true, maximumAge: 10000, timeout: 60000 });
  // A device that is not moving gets no new positions; repeat the latest
  // so it stays current. If this page closes, the host sees the position
  // go stale and then lost, as with a receiver that is unplugged.
  timer = setInterval(() => { if (Date.now() - sentAt >= 2500) send(); }, 3000);
}

function stop() {
  if (watch !== null) navigator.geolocation.clearWatch(watch);
  if (timer) clearInterval(timer);
  watch = timer = latest = null;
  const was = geoState.active || geoState.problem;
  geoState.active = false;
  geoState.problem = '';
  geoState.accuracy = null;
  if (was) changed();
}

// syncBrowserPosition starts or stops sending, as Settings say.
export async function syncBrowserPosition() {
  const s = getSession();
  if (!s || !s.admin || s.setup) { stop(); return; }
  let wanted = false;
  try { wanted = (await api.get(`/api/gps/browser?id=${encodeURIComponent(browserID())}`)).wanted; } catch (_) { /* no library yet */ }
  if (wanted) start(); else stop();
}
