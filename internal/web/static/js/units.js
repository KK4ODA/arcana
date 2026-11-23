// Units for distances, speeds, altitudes and areas. The choice is a
// per-browser preference, like the theme, so every person reading the same
// library (including network clients) sees their own units. Changing it
// dispatches an "arcana-units" event on window.

const KEY = 'arcana-units';
export const UNIT_SYSTEMS = [['metric', 'Kilometres and metres'], ['imperial', 'Miles and feet']];

const M_PER_MI = 1609.344;
const M_PER_FT = 0.3048;

// Countries whose road signs use miles. Only the default depends on this;
// the person can always choose.
const IMPERIAL_REGIONS = new Set(['US', 'GB', 'LR', 'MM']);

function defaultUnits() {
  const langs = (navigator.languages && navigator.languages.length) ? navigator.languages : [navigator.language || ''];
  const m = String(langs[0] || '').match(/[-_]([A-Za-z]{2})\b/);
  return m && IMPERIAL_REGIONS.has(m[1].toUpperCase()) ? 'imperial' : 'metric';
}

export function getUnits() {
  let u = null;
  try { u = localStorage.getItem(KEY); } catch (_) { /* storage unavailable */ }
  return u === 'metric' || u === 'imperial' ? u : defaultUnits();
}

export function setUnits(u) {
  if (u !== 'metric' && u !== 'imperial') return;
  try { localStorage.setItem(KEY, u); } catch (_) { /* storage unavailable */ }
  window.dispatchEvent(new Event('arcana-units'));
}

function trim(n, digits) {
  return n.toFixed(digits).replace(/\.0+$/, '').replace(/(\.\d*?)0+$/, '$1');
}

// fmtDistance formats a distance in metres for reading at a glance: short
// distances in metres or feet, longer ones in kilometres or miles.
export function fmtDistance(m, units = getUnits()) {
  if (m == null || !isFinite(m)) return '—';
  if (units === 'imperial') {
    const mi = m / M_PER_MI;
    if (mi < 0.1) return `${Math.round(m / M_PER_FT)} ft`;
    return `${trim(mi, mi < 10 ? 2 : mi < 100 ? 1 : 0)} mi`;
  }
  if (m < 1000) return `${Math.round(m)} m`;
  const km = m / 1000;
  return `${trim(km, km < 10 ? 2 : km < 100 ? 1 : 0)} km`;
}

// fmtStepDistance rounds the way a navigation instruction is read out:
// "in 300 m", "in 0.5 mi", "in 1,000 ft".
export function fmtStepDistance(m, units = getUnits()) {
  if (m == null || !isFinite(m)) return '';
  if (units === 'imperial') {
    const ft = m / M_PER_FT;
    if (ft < 1000) return `${Math.max(50, Math.round(ft / 50) * 50)} ft`;
    const mi = m / M_PER_MI;
    return `${trim(mi, mi < 10 ? 1 : 0)} mi`;
  }
  if (m < 1000) return `${Math.max(10, Math.round(m / (m < 200 ? 10 : 50)) * (m < 200 ? 10 : 50))} m`;
  const km = m / 1000;
  return `${trim(km, km < 10 ? 1 : 0)} km`;
}

export function fmtSpeed(mps, units = getUnits()) {
  if (mps == null || !isFinite(mps)) return '—';
  return units === 'imperial' ? `${(mps * 3600 / M_PER_MI).toFixed(1)} mph` : `${(mps * 3.6).toFixed(1)} km/h`;
}

export function fmtAltitude(m, units = getUnits()) {
  if (m == null || !isFinite(m)) return '—';
  return units === 'imperial' ? `${Math.round(m / M_PER_FT).toLocaleString()} ft` : `${Math.round(m).toLocaleString()} m`;
}

export function fmtArea(m2, units = getUnits()) {
  if (m2 == null || !isFinite(m2)) return '—';
  if (units === 'imperial') {
    const acres = m2 / 4046.8564224;
    if (acres < 640) return `${trim(acres, acres < 10 ? 2 : acres < 100 ? 1 : 0)} acres`;
    const sqmi = m2 / (M_PER_MI * M_PER_MI);
    return `${trim(sqmi, sqmi < 10 ? 2 : sqmi < 100 ? 1 : 0)} sq mi`;
  }
  if (m2 < 10000) return `${Math.round(m2)} m²`;
  if (m2 < 1e6) return `${trim(m2 / 10000, 2)} ha`;
  const km2 = m2 / 1e6;
  return `${trim(km2, km2 < 10 ? 2 : km2 < 100 ? 1 : 0)} km²`;
}

// fmtTravelTime formats a travel time in seconds: "4 min", "1 h 05 min".
export function fmtTravelTime(s) {
  if (s == null || !isFinite(s)) return '—';
  const min = Math.max(1, Math.round(s / 60));
  if (min < 60) return `${min} min`;
  const hr = Math.floor(min / 60);
  return `${hr} h ${String(min % 60).padStart(2, '0')} min`;
}
