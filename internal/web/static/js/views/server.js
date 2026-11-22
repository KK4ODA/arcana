// Server: how other devices reach this library, and the optional network
// features (name on the network, HTTPS, remote administration, hotspot).
import { api, getSession } from '../api.js';
import { h, clear, fill, fmtDate, toast } from '../util.js';

const qrURL = (u) => `/api/server/qr.svg?url=${encodeURIComponent(u)}`;

function addressList(info) {
  const all = [...info.urls, ...(info.secureUrls || [])];
  if (!info.active) return h('p', { class: 'small muted' }, 'Network sharing is off: only this computer can open Arcana.');
  if (!all.length) return h('p', { class: 'small muted' }, 'Sharing is on, but this computer has no network connection right now.');
  return h('div', { class: 'addr-grid' }, all.map((u) => h('figure', { class: 'addr' },
    h('img', { src: qrURL(u), alt: `QR code for ${u}`, width: 132, height: 132 }),
    h('figcaption', { class: 'mono' }, u))));
}

export async function serverView(main) {
  const s = getSession();
  const info = await api.get('/api/server');
  const host = s.local;

  const rows = [
    ['On this computer', h('a', { href: info.localUrl }, info.localUrl)],
    ['Network sharing', info.active ? 'On' : 'Off'],
    ['Ports', h('span', { class: 'num' }, `${info.appPort} (interface), ${info.contentPort} (reference content)`)],
  ];
  if (info.active) {
    rows.push(['Name on the network', info.mdns.running ? h('span', { class: 'mono' }, `${info.mdns.host}`)
      : (info.mdnsEnabled ? h('span', { class: 'muted' }, info.mdns.error ? `not available: ${info.mdns.error}` : 'starting…') : 'off')]);
  }
  if (info.tls.enabled) {
    rows.push(['HTTPS', h('span', null, info.tls.certificate.mode === 'self-signed' ? 'Self-signed certificate' : 'Your certificate',
      h('div', { class: 'mono small muted' }, `SHA-256 ${info.tls.certificate.fingerprint}`))]);
  }
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Local server'),
      info.active ? h('a', { class: 'button', href: '#/connect' }, 'Connection card') : null),
    h('div', { class: 'body' },
      h('dl', { class: 'meta' }, rows.map(([k, v]) => [h('dt', null, k), h('dd', null, v)])),
      h('h3', null, 'Open from another device'),
      addressList(info),
      !info.active && s.admin ? h('p', { class: 'small muted' },
        'To share with phones and other computers, turn on network sharing in Settings and restart Arcana (or start it with --lan). Other devices then scan a code or open one of the addresses shown here. Your operating system may ask whether to allow incoming connections.') : null,
      h('p', { class: 'small muted' }, info.remoteAdmin
        ? 'Devices on the network can read and search. Someone who knows the administrator password can also sign in to manage downloads and settings.'
        : 'Devices on the network can read and search the library but cannot download, remove or change anything.'),
      h('p', { class: 'small muted' }, 'Arcana never contacts cloud services and is not reachable from the internet unless you configure your network to expose it.'))));

  if (!host) return null;

  // ---------------------------------------------------------- name
  const nameIn = h('input', { type: 'text', value: info.mdnsName || 'arcana', maxlength: 63, style: 'width:180px', 'aria-label': 'Network name' });
  const mdnsOn = h('input', { type: 'checkbox', checked: !!info.mdnsEnabled });
  const saveName = h('button', { onclick: async () => {
    try {
      await api.put('/api/settings', { mdns: mdnsOn.checked, mdnsName: nameIn.value.trim().toLowerCase() });
      toast('Saved.');
      location.reload();
    } catch (e) { toast(e.message); }
  } }, 'Save');

  // ---------------------------------------------------------- HTTPS
  const tlsSel = h('select', { 'aria-label': 'HTTPS' }, [['', 'Off (HTTP only)'], ['self-signed', 'Self-signed certificate'], ['custom', 'My certificate (user/tls/cert.pem and key.pem)']]
    .map(([v, t]) => h('option', { value: v, selected: v === (info.tlsMode || '') }, t)));
  const saveTLS = h('button', { onclick: async () => {
    try { await api.put('/api/server/tls', { mode: tlsSel.value }); toast('Saved.'); location.reload(); } catch (e) { toast(e.message); }
  } }, 'Apply');

  // ---------------------------------------------------------- remote admin
  const auth = await api.get('/api/auth');
  const pw1 = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'New password (10+ characters)', style: 'width:240px' });
  const pw2 = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'Repeat', style: 'width:160px' });
  const setPw = h('button', { onclick: async () => {
    if (pw1.value !== pw2.value) { toast('The passwords differ.'); return; }
    try { await api.put('/api/auth/password', { password: pw1.value }); toast('Password set. Network clients can now sign in.'); location.reload(); } catch (e) { toast(e.message); }
  } }, auth.enabled ? 'Change password' : 'Turn on');
  const clearPw = auth.enabled ? h('button', { class: 'danger', onclick: async () => {
    if (!confirm('Turn off remote administration? Signed-in devices are signed out.')) return;
    try { await api.del('/api/auth/password'); location.reload(); } catch (e) { toast(e.message); }
  } }, 'Turn off') : null;

  const field = (label, control, help) => h('div', { class: 'field' }, h('label', null, label), h('div', null, control, help ? h('div', { class: 'help' }, help) : null));

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Network options')),
    field('Name on the network', h('span', { class: 'row' }, h('label', { class: 'inline' }, mdnsOn, 'Advertise'), nameIn, h('span', { class: 'mono small' }, '.local'), saveName),
      'Other devices can type this name instead of an address (works on most phones and computers; some Android versions need the address or QR code).'),
    field('HTTPS', h('span', { class: 'row' }, tlsSel, saveTLS, info.tls.error ? h('span', { class: 'small', style: 'color:var(--err)' }, info.tls.error) : null),
      'Offered on the same port as HTTP. Browsers warn about a self-signed certificate; compare the fingerprint shown above before accepting it. HTTPS lets phones share their own location with the map and keeps the administrator password from crossing the network in clear text.'),
    field('Remote administration', h('div', null,
      h('div', { class: 'small', style: 'margin-bottom:4px' }, auth.enabled
        ? `On since ${fmtDate(auth.setAt)} · ${auth.sessions} device(s) signed in` : 'Off: only this computer can make changes.'),
      h('div', { class: 'row' }, pw1, pw2, setPw, clearPw)),
      `Lets someone on the network sign in to manage downloads and settings. File transfers, program updates, this password and your private notes stay on this computer. ${info.tls.enabled ? '' : 'Without HTTPS the password crosses the network unencrypted.'}`)));

  // ---------------------------------------------------------- hotspot
  const hsBody = h('div', { class: 'body' }, h('p', { class: 'small muted' }, 'Checking…'));
  main.appendChild(h('section', { class: 'panel' }, h('header', null, h('h2', null, 'Wi-Fi hotspot')), hsBody));
  async function renderHotspot() {
    let r;
    try { r = await api.get('/api/server/hotspot'); } catch (e) { fill(hsBody, h('p', { class: 'small', style: 'color:var(--err)' }, e.message)); return; }
    const st = r.status;
    if (!st.supported) { fill(hsBody, h('p', { class: 'small muted' }, st.reason)); return; }
    const ssid = h('input', { type: 'text', value: r.ssid || st.ssid || 'Arcana', maxlength: 32, style: 'width:180px', 'aria-label': 'Network name (SSID)' });
    const pw = h('input', { type: 'password', placeholder: r.hasPassword ? 'unchanged' : '8–63 characters', style: 'width:180px', 'aria-label': 'Wi-Fi password' });
    const dev = h('select', { 'aria-label': 'Wi-Fi adapter' }, st.devices.map((d) => h('option', { value: d, selected: d === st.device }, d)));
    const act = async (action) => {
      try {
        await api.post('/api/server/hotspot', { action, ssid: ssid.value.trim(), password: pw.value, device: dev.value });
        toast(action === 'start' ? 'Hotspot on.' : 'Hotspot off.');
        renderHotspot();
      } catch (e) { toast(e.message); }
    };
    fill(hsBody,
      h('p', { class: 'small' }, st.active ? `On: “${st.ssid}” on ${st.device}.` : 'Off.'),
      h('div', { class: 'row' }, ssid, pw, dev,
        st.active ? h('button', { onclick: () => act('stop') }, 'Stop') : h('button', { class: 'primary', onclick: () => act('start') }, 'Start hotspot')),
      h('p', { class: 'small muted' }, 'Creates a Wi-Fi network from this computer (NetworkManager) so phones can join where there is no router. It disconnects this Wi-Fi adapter from any other network while on. Turn on network sharing as well.'),
      (r.ssid && r.hasPassword) ? h('figure', { class: 'addr' }, h('img', { src: '/api/server/wifi-qr.svg', alt: 'QR code to join the Wi-Fi network', width: 132, height: 132 }),
        h('figcaption', { class: 'small' }, 'Scan to join the Wi-Fi network')) : null);
  }
  renderHotspot();
  return null;
}

// connectView is a printable card: join the Wi-Fi (if a hotspot is
// configured), then open the library.
export async function connectView(main) {
  const s = getSession();
  const info = await api.get('/api/server');
  // A numeric address scans reliably on every phone; names (.local) and
  // self-signed HTTPS are listed below it.
  const ipURL = info.urls.find((u) => /^http:\/\/\d+\.\d+\.\d+\.\d+:/.test(u));
  const preferred = ipURL || info.urls[0];
  const others = [...info.urls, ...(info.secureUrls || [])].filter((u) => u !== preferred);
  let wifi = null;
  if (s.admin) {
    try { const r = await api.get('/api/server/hotspot'); if (r.ssid && r.hasPassword) wifi = r.ssid; } catch (_) { /* not host */ }
  }
  main.appendChild(h('section', { class: 'connect-card' },
    h('h1', null, s.libraryLabel ? `Arcana — ${s.libraryLabel}` : 'Arcana offline library'),
    h('p', null, 'A library of reference material that works without the internet.'),
    h('div', { class: 'connect-steps' },
      wifi ? h('figure', null, h('div', { class: 'step' }, '1. Join the Wi-Fi network'),
        h('img', { src: '/api/server/wifi-qr.svg', alt: 'Wi-Fi QR code', width: 220, height: 220 }),
        h('figcaption', null, h('b', null, wifi))) : null,
      preferred ? h('figure', null, h('div', { class: 'step' }, wifi ? '2. Open the library' : 'Open the library'),
        h('img', { src: qrURL(preferred), alt: `QR code for ${preferred}`, width: 220, height: 220 }),
        h('figcaption', { class: 'mono' }, preferred)) : h('p', null, 'Turn on network sharing to show an address here.')),
    others.length ? h('p', { class: 'small' }, 'Other addresses: ', others.join('  ·  ')) : null,
    h('p', { class: 'small muted' }, 'Read-only for visitors. Nothing you do here is sent to the internet.'),
    h('div', { class: 'row no-print' }, h('button', { class: 'primary', onclick: () => window.print() }, 'Print'), h('a', { class: 'button', href: '#/server' }, 'Back'))));
  return null;
}
