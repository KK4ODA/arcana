import { api, getSession } from '../api.js';
import { h } from '../util.js';

export async function serverView(main) {
  const s = getSession();
  const info = await api.get('/api/server');
  const rows = [
    ['On this computer', h('a', { href: info.localUrl }, info.localUrl)],
    ['Network sharing', info.active ? 'On: devices on the same network can browse and search (read-only).' : 'Off: only this computer can open Arcana.'],
    ['Network addresses', info.active ? (info.urls.length ? h('div', null, info.urls.map((u) => h('div', { class: 'mono' }, u))) : 'No network connection detected.') : '—'],
    ['Ports', h('span', { class: 'num' }, `${info.appPort} (interface), ${info.contentPort} (reference content)`)],
  ];
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Local server')),
    h('div', { class: 'body' },
      h('dl', { class: 'meta' }, rows.map(([k, v]) => [h('dt', null, k), h('dd', null, v)])),
      !info.active && s.admin ? h('p', { class: 'small muted' },
        'To share with phones and other computers, turn on network sharing in Settings and restart Arcana (or start it with --lan). Other devices then open one of the addresses shown here. Your operating system may ask whether to allow incoming connections.') : null,
      h('p', { class: 'small muted' }, 'Devices on the network can read and search the library but cannot download, remove or change anything. Arcana never contacts cloud services and is not reachable from the internet unless you configure your network to expose it.'),
      h('p', { class: 'small muted' }, info.note))));
  return null;
}
