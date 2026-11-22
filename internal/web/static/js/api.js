// API client. State-changing requests carry the session token, which only
// pages served from Arcana's own origin can obtain.

let session = null;

export async function loadSession() {
  const r = await fetch('/api/session', { cache: 'no-store' });
  session = await r.json();
  return session;
}

export const getSession = () => session;

async function request(method, url, body) {
  const opts = { method, headers: {}, cache: 'no-store' };
  if (method !== 'GET') {
    opts.headers['X-Arcana-Token'] = session?.token || '';
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
  }
  let r;
  try {
    r = await fetch(url, opts);
  } catch (e) {
    throw new Error('Arcana is not responding. It may have been closed, or the drive was disconnected.');
  }
  let data = null;
  try { data = await r.json(); } catch (_) { /* empty body */ }
  if (!r.ok) throw new Error((data && data.error) || `${r.status} ${r.statusText}`);
  return data;
}

export const api = {
  get: (u) => request('GET', u),
  post: (u, b) => request('POST', u, b ?? {}),
  put: (u, b) => request('PUT', u, b),
  del: (u) => request('DELETE', u),
};

// contentURL builds an address on the content origin (same host, port+1).
export function contentURL(path) {
  const port = session?.contentPort;
  return `${location.protocol}//${location.hostname}:${port}${path}`;
}
