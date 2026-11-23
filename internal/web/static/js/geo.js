// Geodesy on a sphere, enough for measuring on a map and following a route.
// Distances use the haversine formula with the mean Earth radius (IUGG
// 6,371,008.8 m): within 0.5 % of the ellipsoid everywhere, which is well
// under what a click on a map can resolve.

export const EARTH_RADIUS = 6371008.8;
const rad = (d) => (d * Math.PI) / 180;
const deg = (r) => (r * 180) / Math.PI;

// distance returns the great-circle distance in metres between [lon, lat]
// points.
export function distance(a, b) {
  const dLat = rad(b[1] - a[1]);
  const dLon = rad(b[0] - a[0]);
  const s = Math.sin(dLat / 2) ** 2 + Math.cos(rad(a[1])) * Math.cos(rad(b[1])) * Math.sin(dLon / 2) ** 2;
  return 2 * EARTH_RADIUS * Math.asin(Math.min(1, Math.sqrt(s)));
}

export function pathLength(pts) {
  let d = 0;
  for (let i = 1; i < pts.length; i++) d += distance(pts[i - 1], pts[i]);
  return d;
}

// bearing returns the initial true bearing in degrees (0–360) from a to b.
export function bearing(a, b) {
  const φ1 = rad(a[1]);
  const φ2 = rad(b[1]);
  const Δλ = rad(b[0] - a[0]);
  const y = Math.sin(Δλ) * Math.cos(φ2);
  const x = Math.cos(φ1) * Math.sin(φ2) - Math.sin(φ1) * Math.cos(φ2) * Math.cos(Δλ);
  return (deg(Math.atan2(y, x)) + 360) % 360;
}

const POINTS = ['N', 'NNE', 'NE', 'ENE', 'E', 'ESE', 'SE', 'SSE', 'S', 'SSW', 'SW', 'WSW', 'W', 'WNW', 'NW', 'NNW'];
export function compassPoint(b) {
  return POINTS[Math.round((((b % 360) + 360) % 360) / 22.5) % 16];
}

// area returns the area in square metres enclosed by a ring of [lon, lat]
// points (closed or not), on the sphere. Spherical excess by the method of
// Chamberlain and Duquette (JPL, 2007).
export function area(ring) {
  const n = ring.length;
  if (n < 3) return 0;
  let total = 0;
  for (let i = 0; i < n; i++) {
    const p1 = ring[i];
    const p2 = ring[(i + 1) % n];
    const p3 = ring[(i + 2) % n];
    total += (rad(p3[0]) - rad(p1[0])) * Math.sin(rad(p2[1]));
  }
  return Math.abs((total * EARTH_RADIUS * EARTH_RADIUS) / 2);
}

// nearestOnPath finds the point of a polyline nearest to p, using a local
// equirectangular projection (accurate to a few centimetres over the short
// segments of a route). It returns the distance from p in metres, the
// segment index, and how far along the whole path the nearest point lies.
export function nearestOnPath(pts, p) {
  let best = { dist: Infinity, index: 0, along: 0, point: pts[0] };
  let along = 0;
  const kx = Math.cos(rad(p[1])) * rad(1) * EARTH_RADIUS;
  const ky = rad(1) * EARTH_RADIUS;
  for (let i = 1; i < pts.length; i++) {
    const a = pts[i - 1];
    const b = pts[i];
    const ax = (a[0] - p[0]) * kx;
    const ay = (a[1] - p[1]) * ky;
    const bx = (b[0] - p[0]) * kx;
    const by = (b[1] - p[1]) * ky;
    const dx = bx - ax;
    const dy = by - ay;
    const len2 = dx * dx + dy * dy;
    let t = len2 > 0 ? -(ax * dx + ay * dy) / len2 : 0;
    t = Math.max(0, Math.min(1, t));
    const x = ax + t * dx;
    const y = ay + t * dy;
    const d = Math.hypot(x, y);
    const segLen = Math.sqrt(len2);
    if (d < best.dist) {
      best = { dist: d, index: i - 1, along: along + t * segLen, point: [a[0] + t * (b[0] - a[0]), a[1] + t * (b[1] - a[1])] };
    }
    along += segLen;
  }
  return best;
}
