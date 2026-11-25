// Classroom: Kolibri run by Arcana (ADR-0024). Khan Academy and other
// channels (ADR-0025) with classes, lessons, quizzes and progress for each
// student. The owner sets it up and chooses grades and subjects here;
// teachers and students use Kolibri's own pages on the classroom's address.
import { api, getSession } from '../api.js';
import { h, clear, fill, fmtBytes, toast } from '../util.js';

const STATE_TEXT = {
  download: 'Downloading Kolibri',
  preparing: 'Preparing Kolibri on this computer',
  starting: 'Starting',
  running: 'Running',
  failed: 'Not running',
  stopped: 'Stopped',
  'read-only': 'Read-only library',
};

const IMPORT_TEXT = {
  PENDING: 'Waiting', SCHEDULED: 'Waiting', QUEUED: 'Waiting', SELECTED: 'Starting', RUNNING: 'Downloading',
  CANCELING: 'Stopping', CANCELED: 'Stopped', FAILED: 'Failed', COMPLETED: 'Done',
};

// Kolibri's interface languages offered at setup.
const LANG = { en: 'English', es: 'Español' };

// A channel's language by name ("pt-BR" → "Brazilian Portuguese"), from the
// browser's own data, which lacks some languages Kolibri Studio has.
const LANG_NAMES = { fuv: 'Fulfulde' };
let langNames = null;
try { langNames = new Intl.DisplayNames(['en'], { type: 'language', fallback: 'none' }); } catch (_) { /* older browser */ }
function langName(code) {
  if (LANG_NAMES[code]) return LANG_NAMES[code];
  try { return (langNames && langNames.of(code)) || code; } catch (_) { return code; }
}

// The channels by group (kolibri.Group*). Folded groups start closed.
const GROUPS = [
  { id: 'khan', title: 'Khan Academy',
    help: 'Choose grades and subjects to download into the classroom. Kolibri downloads them from Kolibri Studio, resumes after interruptions, and keeps them in the library\'s kolibri folder.' },
  { id: 'more', title: 'More courses for high school, college and adults',
    help: 'Other publishers\' channels from Kolibri Studio\'s public library, in English and Spanish: textbooks, simulations, lesson plans and videos. Each shows its licence: many are for non-commercial use only.' },
  { id: 'khan-world', title: 'Khan Academy in other languages', folded: true,
    help: 'Khan Academy as translated for other countries. Several hold only early math; the description of each says what it covers.' },
];

export async function classroomView(main) {
  const s = getSession();
  const admin = s.admin;
  const head = h('span', { class: 'muted small' });
  const body = h('div', { class: 'body' });
  main.appendChild(h('section', { class: 'panel' }, h('header', null, h('h2', null, 'Classroom'), head), body));
  const content = h('div');
  main.appendChild(content);

  let timer = null;
  let picker = null; // the channel whose picker is open, kept across refreshes
  const unfolded = {}; // folded groups the owner opened or closed, by group
  let last = null;

  async function load() {
    clearTimeout(timer);
    let r;
    try { r = await api.get('/api/classroom'); } catch (e) { fill(body, h('div', { class: 'banner err' }, e.message)); return; }
    last = r;
    render(r.classroom, r.url);
    const busy = ['download', 'preparing', 'starting'].includes(r.classroom.state) ||
      (r.classroom.imports || []).some((t) => !['COMPLETED', 'FAILED', 'CANCELED'].includes(t.status));
    timer = setTimeout(load, busy ? 2000 : 10000);
  }

  function render(c, url) {
    head.textContent = c.state === 'off' ? '' : (STATE_TEXT[c.state] || c.state) + (c.version ? ` · Kolibri ${c.version}` : '');
    if (c.state === 'off') {
      fill(body, admin && !s.readOnly ? setupForm(c) : h('p', { class: 'empty' }, 'No classroom is set up on this Arcana.'));
      clear(content);
      return;
    }
    const parts = [];
    if (c.state === 'read-only') parts.push(h('div', { class: 'banner warn' }, c.message));
    if (c.state === 'download') parts.push(downloadPart(c));
    if (c.state === 'preparing') parts.push(h('p', null, 'Unpacking Kolibri and Python onto this computer. This takes a few seconds, once per computer.'));
    if (c.state === 'starting') parts.push(startingPart(c));
    if (c.state === 'failed' || c.state === 'stopped') {
      parts.push(h('div', { class: 'banner err' }, c.message || 'Kolibri is not running.'));
      if (admin) parts.push(h('div', { class: 'row' }, h('button', { onclick: () => setUp(c.language) }, 'Try again')));
    }
    if (c.state === 'running') parts.push(...runningPart(c, url));
    if (admin && !s.readOnly) {
      parts.push(h('div', { class: 'row', style: 'margin-top:12px' },
        h('button', { class: 'danger', onclick: turnOff }, 'Turn off the classroom'),
        h('span', { class: 'small muted' }, 'Kolibri stops. Accounts, progress and downloaded courses stay on the drive.')));
    }
    fill(body, ...parts);
    if (c.state === 'running' && admin) renderContent(c);
    else clear(content);
  }

  function setupForm(c) {
    const lang = h('select', { 'aria-label': 'Kolibri language' },
      Object.entries(LANG).map(([v, t]) => h('option', { value: v }, t)));
    return h('div', null,
      h('p', null, 'Set up a classroom on this library with Kolibri, Learning Equality\'s offline learning platform. ',
        'Students watch Khan Academy videos, read and practise exercises; teachers make classes, lessons and quizzes and follow each student\'s progress.'),
      h('ul', null,
        h('li', null, `Arcana first downloads Kolibri with Python for Windows, macOS and Linux (${fmtBytes(c.runtimeSize || 244721734)}), so this drive's classroom runs on any of them.`),
        h('li', null, 'Then you choose what to download, with the size of each: Khan Academy\'s grades and subjects in English (US, Philippine or Indian curriculum), Spanish and 25 other languages, its test preparation, and other publishers\' courses for high school, college and adults, such as CK-12, OpenStax and PhET.'),
        h('li', null, 'Students sign in with their username alone; teachers with a password. Anyone can also browse without an account, without progress.')),
      h('p', { class: 'small muted' }, 'Khan Academy material is licensed CC BY-NC-SA: non-commercial use only. Each other channel shows its licence. Kolibri is MIT-licensed.'),
      h('div', { class: 'row' },
        h('label', null, 'Kolibri\'s language ', lang),
        h('button', { class: 'primary', onclick: () => setUp(lang.value) }, 'Set up the classroom')));
  }

  // While Kolibri starts: how long for, and its latest message, so a long
  // first start (a slow drive, a Raspberry Pi) visibly makes progress.
  function startingPart(c) {
    const secs = c.stateSeconds || 0;
    const took = secs < 60 ? `${secs} s` : `${Math.floor(secs / 60)} min ${secs % 60} s`;
    return h('div', null,
      h('p', null, 'Kolibri is starting. Its first start prepares its database, which takes about a minute (longer on a Raspberry Pi or a slow drive).'),
      // The page is rebuilt every two seconds: the animation's phase follows
      // the clock so the bar slides on instead of starting over.
      h('div', { class: 'progress indeterminate', role: 'progressbar', 'aria-label': 'Kolibri is starting' },
        h('span', { style: `animation-delay:-${(Date.now() % 1400) / 1000}s` })),
      h('div', { class: 'small muted activity', style: 'margin-top:6px' }, `Starting for ${took}`,
        c.activity ? ' · Kolibri: ' : '', c.activity ? h('code', null, c.activity) : null));
  }

  function downloadPart(c) {
    const d = c.download;
    if (!d) {
      return h('div', null, h('p', null, 'Kolibri is not downloaded yet.'),
        admin ? h('button', { class: 'primary', onclick: () => setUp(c.language) }, 'Download Kolibri') : null);
    }
    const pct = d.bytesTotal ? Math.min(100, (d.bytesDone / d.bytesTotal) * 100) : 0;
    return h('div', null,
      h('p', null, 'Downloading Kolibri and Python. ', h('a', { href: '#/downloads' }, 'See Downloads'), '.'),
      h('div', { class: 'progress', role: 'progressbar', 'aria-valuenow': Math.round(pct), 'aria-valuemin': 0, 'aria-valuemax': 100 },
        h('span', { style: `width:${pct}%` })),
      h('div', { class: 'small muted num' }, `${fmtBytes(d.bytesDone)} of ${fmtBytes(d.bytesTotal)}`),
      d.error ? h('div', { class: 'small', style: 'color:var(--err)' }, d.error) : null);
  }

  function runningPart(c, url) {
    const out = [
      h('div', { class: 'row' },
        h('a', { class: 'button primary', href: url, target: '_blank', rel: 'noopener' }, 'Open the classroom'),
        h('code', { class: 'small' }, url)),
      h('dl', { class: 'meta' },
        h('dt', null, 'Students'), h('dd', null, 'Open the classroom and sign in with the username their teacher gave them (no password), or choose to explore without an account. Kolibri\'s Learn page has their lessons and quizzes.'),
        h('dt', null, 'Teachers'), h('dd', null, 'Sign in with their username and password, then open Coach to make classes, lessons and quizzes and follow progress.'),
        h('dt', null, 'Other devices'), h('dd', null, 'Phones and laptops on this network open the same address, once Arcana shares on the network (Server).')),
    ];
    if (admin) out.push(adminPart(c));
    return out;
  }

  function adminPart(c) {
    const pw = h('code', null, '••••-••••-••••-••••');
    let shown = false;
    const show = h('button', { class: 'link small', onclick: () => {
      shown = !shown;
      pw.textContent = shown ? c.password : '••••-••••-••••-••••';
      show.textContent = shown ? 'Hide' : 'Show';
    } }, 'Show');
    const copy = h('button', { class: 'link small', onclick: async () => {
      try { await navigator.clipboard.writeText(c.password); toast('Password copied'); } catch (_) { toast('Copying is not allowed here; use Show.'); }
    } }, 'Copy');
    const newPw = h('input', { type: 'password', autocomplete: 'off', 'aria-label': 'New administrator password', style: 'width:min(240px,100%)' });
    const change = h('details', null, h('summary', { class: 'small' }, 'Changed this password in Kolibri?'),
      h('form', { class: 'row', onsubmit: async (e) => {
        e.preventDefault();
        try { await api.post('/api/classroom/password', { password: newPw.value }); toast('Password saved'); newPw.value = ''; load(); } catch (err) { toast(err.message); }
      } }, h('span', { class: 'small' }, 'Enter the new one, so Arcana can still download courses: '), newPw, h('button', { type: 'submit' }, 'Save')));
    return h('div', { style: 'margin-top:12px' },
      h('h3', null, 'Kolibri administrator'),
      h('p', { class: 'small' }, 'Sign in to the classroom with this account to add teachers and students: Facility → Users. Teachers then make their own classes.'),
      h('dl', { class: 'meta' },
        h('dt', null, 'School'), h('dd', null, c.facility || '—'),
        h('dt', null, 'Username'), h('dd', null, h('code', null, c.username)),
        h('dt', null, 'Password'), h('dd', null, pw, ' ', show, ' ', copy)),
      c.adminError ? h('div', { class: 'banner err' }, c.adminError) : null,
      change);
  }

  function renderContent(c) {
    const onDevice = new Map((c.onDevice || []).map((d) => [d.id, d]));
    const imports = c.imports || [];
    clear(content);
    if (imports.length) {
      content.appendChild(h('section', { class: 'panel' },
        h('header', null, h('h2', null, 'Downloading courses')),
        h('div', { class: 'body' }, imports.map(importRow))));
    }
    for (const g of GROUPS) {
      const list = c.channels.filter((ch) => ch.group === g.id);
      if (list.length) content.appendChild(groupPanel(g, list, onDevice));
    }
  }

  function groupPanel(g, list, onDevice) {
    const cards = list.map((ch) => channelCard(ch, onDevice.get(ch.id)));
    let shown = cards;
    if (g.folded) {
      // Open when the owner opened it, or else when one of its channels is
      // on this drive or being chosen; refreshes keep the owner's choice.
      const open = unfolded[g.id] ?? list.some((ch) => ch.id === picker || onDevice.has(ch.id));
      const d = h('details', { class: 'channel-group', open },
        h('summary', null, `${list.length} channels: `, list.map((ch) => langName(ch.language)).join(', ')), cards);
      d.addEventListener('toggle', () => { unfolded[g.id] = d.open; });
      shown = d;
    }
    return h('section', { class: 'panel' },
      h('header', null, h('h2', null, g.title), h('span', { class: 'muted small' }, `${list.length} channels`)),
      h('div', { class: 'body' }, h('p', { class: 'small muted' }, g.help), shown));
  }

  function importRow(t) {
    const pct = Math.round((t.percentage || 0) * 100);
    const done = ['COMPLETED', 'FAILED', 'CANCELED'].includes(t.status);
    return h('div', { class: 'row', style: 'align-items:center;margin:6px 0' },
      h('div', { class: 'grow' },
        h('b', null, t.channelName || t.channelId), ' ',
        h('span', { class: `state ${t.status === 'FAILED' ? 'failed' : t.status === 'COMPLETED' ? 'completed' : ''}` }, IMPORT_TEXT[t.status] || t.status),
        done ? null : h('div', { class: 'progress', role: 'progressbar', 'aria-valuenow': pct, 'aria-valuemin': 0, 'aria-valuemax': 100 }, h('span', { style: `width:${pct}%` })),
        t.fileSize ? h('div', { class: 'small muted num' }, `${fmtBytes(t.transferred || 0)} of ${fmtBytes(t.fileSize)}`) : null,
        t.exception ? h('div', { class: 'small', style: 'color:var(--err)' }, t.exception) : null),
      !done && t.cancellable ? h('button', { onclick: () => taskAction(t, 'cancel') }, 'Stop') : null,
      done ? h('button', { class: 'link small', onclick: () => taskAction(t, 'clear') }, 'Remove from list') : null);
  }

  function channelCard(ch, dev) {
    const box = h('div', { class: 'channel' });
    const open = picker === ch.id;
    box.append(
      h('div', { class: 'row', style: 'align-items:baseline' },
        h('div', { class: 'grow' }, h('b', null, ch.title), ' ', h('span', { class: 'badge' }, langName(ch.language)),
          ch.levels ? h('span', { class: 'small muted' }, ' · ', ch.levels) : null,
          ch.about ? h('div', { class: 'small' }, ch.about) : null,
          h('div', { class: 'small muted' }, `Whole channel ${fmtBytes(ch.size)} · published ${ch.published} · ${ch.license}`,
            dev ? ` · on this drive: ${dev.resources} resources, ${fmtBytes(dev.size)}` : '')),
        h('button', { onclick: () => { picker = open ? null : ch.id; load(); } }, open ? 'Close' : ch.group === 'more' ? 'Choose topics' : 'Choose grades and subjects')));
    if (open) box.appendChild(pickerFor(ch));
    return box;
  }

  function pickerFor(ch) {
    const wrap = h('div', { class: 'small muted' }, 'Reading the channel\'s list of topics… The first time, this downloads it from Kolibri Studio (up to 44 MB, for the largest channel).');
    const holder = h('div', null, wrap);
    if (pickers[ch.id]) {
      fill(holder, pickers[ch.id]);
      return holder;
    }
    api.get(`/api/classroom/channels/${ch.id}/tree`).then((tree) => {
      pickers[ch.id] = buildPicker(ch, tree);
      fill(holder, pickers[ch.id]);
    }).catch((e) => fill(holder, h('div', { class: 'banner err' }, e.message)));
    return holder;
  }

  const pickers = {};

  function buildPicker(ch, tree) {
    const chosen = new Set();
    const total = h('span', { class: 'num' }, 'Nothing chosen');
    const go = h('button', { class: 'primary', disabled: true }, 'Download chosen');
    let seq = 0;
    const update = async () => {
      go.disabled = chosen.size === 0;
      if (!chosen.size) { total.textContent = 'Nothing chosen'; return; }
      const mine = ++seq;
      total.textContent = 'Measuring…';
      try {
        const r = await api.post(`/api/classroom/channels/${ch.id}/size`, { nodeIds: [...chosen] });
        if (mine === seq) total.textContent = `${fmtBytes(r.size)} · ${r.resources} resources (videos, exercises, readings and activities)`;
      } catch (e) { if (mine === seq) total.textContent = e.message; }
    };
    const boxes = [];
    const topic = (n, parentBox) => {
      const cb = h('input', { type: 'checkbox', value: n.id });
      boxes.push({ cb, n, parentBox });
      cb.addEventListener('change', () => {
        if (cb.checked) chosen.add(n.id); else chosen.delete(n.id);
        // A subject covers its grades: choosing it clears and locks them.
        for (const b of boxes) {
          if (b.parentBox === cb) {
            b.cb.disabled = cb.checked;
            if (cb.checked) { b.cb.checked = false; chosen.delete(b.n.id); }
          }
        }
        update();
      });
      return h('label', null, cb, h('span', { class: 'grow' }, n.title), h('span', { class: 'small muted num' }, fmtBytes(n.size)));
    };
    // The channel's root topic: Kolibri imports everything under a topic, so
    // this takes the whole channel, including resources kept outside its
    // topics (some channels have only those).
    const all = h('input', { type: 'checkbox', value: tree.id });
    all.addEventListener('change', () => {
      chosen.clear();
      for (const b of boxes) { b.cb.checked = false; b.cb.disabled = all.checked; }
      if (all.checked) chosen.add(tree.id);
      update();
    });
    const everything = h('div', { class: 'topic topic-all' }, h('span', { class: 'toggle' }),
      h('label', null, all, h('span', { class: 'grow' }, h('b', null, 'Everything in this channel')), h('span', { class: 'small muted num' }, fmtBytes(tree.size))));
    const list = h('div', { class: 'topic-tree' }, everything, (tree.children || []).map((n) => {
      const head = topic(n, null);
      const headBox = boxes[boxes.length - 1].cb;
      if (!n.children?.length) return h('div', { class: 'topic' }, h('span', { class: 'toggle' }), head);
      const kids = h('div', { class: 'topic-kids', hidden: true }, n.children.map((k) => topic(k, headBox)));
      const toggle = h('button', { type: 'button', class: 'toggle', 'aria-expanded': 'false', 'aria-label': `Show ${n.title}'s parts`,
        onclick: () => {
          kids.hidden = !kids.hidden;
          toggle.setAttribute('aria-expanded', String(!kids.hidden));
          toggle.textContent = kids.hidden ? '▸' : '▾';
        } }, '▸');
      return h('div', null, h('div', { class: 'topic' }, toggle, head), kids);
    }));
    go.addEventListener('click', async () => {
      go.disabled = true;
      try {
        await api.post('/api/classroom/import', { channel: ch.id, nodeIds: [...chosen] });
        toast('Kolibri is downloading the chosen courses');
        picker = null;
        delete pickers[ch.id];
        load();
      } catch (e) { toast(e.message); go.disabled = false; }
    });
    return h('div', { style: 'margin-top:8px' },
      h('p', { class: 'small muted' }, 'Tick the whole channel or a subject, or open a subject (▸) to choose grades and courses. Sizes count each file once, even when it appears under several topics.'),
      h('div', { class: 'checklist', style: 'max-height:420px' }, list),
      h('div', { class: 'row', style: 'margin-top:8px;align-items:center' }, h('span', { class: 'grow small' }, total), go));
  }

  async function setUp(language) {
    try { await api.post('/api/classroom/setup', { language }); load(); } catch (e) { toast(e.message); }
  }

  async function turnOff() {
    if (!confirm('Turn off the classroom? Kolibri stops; accounts, progress and downloaded courses stay on the drive, and turning it on again picks them up.')) return;
    try { await api.post('/api/classroom/stop', {}); load(); } catch (e) { toast(e.message); }
  }

  async function taskAction(t, action) {
    try { await api.post(`/api/classroom/imports/${encodeURIComponent(t.id)}/${action}`, {}); load(); } catch (e) { toast(e.message); }
  }

  await load();
  return () => clearTimeout(timer);
}
