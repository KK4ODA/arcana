// Medicines: the FDA drug label reference built from openFDA downloads, and
// the built-in home care and herbs (ADR-0028). Search by name, look up by
// situation, read a label or a remedy, compare warnings.
import { api } from '../api.js';
import { h, clear, fill, fmtDate, debounce } from '../util.js';

const ACK_KEY = 'arcana-medicines-ack';
const KIND_LABEL = { otc: 'Non-prescription', rx: 'Prescription', homeopathic: 'Homeopathic', other: 'Other' };
const REMEDY_KIND = { home: 'Home care', herbal: 'Herb or supplement' };
const WARNING_KEYS = ['boxed_warning', 'contraindications', 'do_not_use', 'ask_doctor', 'ask_doctor_or_pharmacist', 'when_using',
  'stop_use', 'warnings', 'warnings_and_cautions', 'precautions', 'drug_interactions', 'pregnancy_or_breast_feeding', 'pregnancy',
  'keep_out_of_reach_of_children', 'overdosage'];

function acknowledged() {
  try { return localStorage.getItem(ACK_KEY) === '1'; } catch (_) { return false; }
}

function kindBadge(kind) {
  const cls = kind === 'rx' ? 'warn' : kind === 'homeopathic' ? 'info' : 'ok';
  return h('span', { class: `badge ${cls}`, title: kind === 'homeopathic' ? 'Homeopathic products are not evaluated by the FDA for effectiveness.' : null }, KIND_LABEL[kind] || kind);
}

const notice = () => h('div', { class: 'banner err' },
  'Manufacturers’ label text, not medical advice. Follow the label of the product you actually have. ',
  'In an emergency, contact emergency services or a poison control center; otherwise ask a pharmacist, doctor or nurse when you can.');

const remedyNotice = () => h('div', { class: 'banner err' },
  'Summaries of US government health pages (CDC, MedlinePlus, FDA and NCCIH), not medical advice. Read the cautions before trying one. ',
  'In an emergency, contact emergency services or a poison control center; otherwise ask a pharmacist, doctor or nurse when you can.');

function remedyBadge(kind) {
  return h('span', { class: `badge ${kind === 'herbal' ? 'info' : 'ok'}`,
    title: kind === 'herbal' ? 'Herbs and supplements are not approved as medicines by the FDA. The page says what the evidence shows.' : null }, REMEDY_KIND[kind] || kind);
}

function remedyItem(r, names) {
  return h('li', null,
    h('a', { href: `#/medicines/remedy/${encodeURIComponent(r.slug)}` }, h('b', null, r.name)), ' ', remedyBadge(r.kind),
    // The whole text: a remedy for several problems covers each in its own
    // sentence, so a first sentence can be about a different one.
    h('div', { class: 'src' }, r.uses),
    names ? h('div', { class: 'src' }, `For: ${r.situations.map((s) => names[s] || s).join(', ')}`) : null);
}

function gate(main, onAccept) {
  const btn = h('button', { class: 'primary', onclick: () => {
    try { localStorage.setItem(ACK_KEY, '1'); } catch (_) { /* storage unavailable: ask again next time */ }
    onAccept();
  } }, 'I understand');
  main.appendChild(h('section', { class: 'panel' }, h('header', null, h('h2', null, 'Before you use the medicines reference')),
    h('div', { class: 'body' },
      h('p', null, 'This reference shows the labels manufacturers submitted to the U.S. Food and Drug Administration: the Drug Facts panel of non-prescription medicines and the main sections of prescription labels.'),
      h('ul', null,
        h('li', null, 'It is health information, not medical advice, and it cannot replace a pharmacist, doctor or nurse.'),
        h('li', null, 'Labels can be out of date, and products differ between countries. Always follow the label of the product you have.'),
        h('li', null, '"By situation" lists medicines whose labels mention the problem. It does not say which one is right or safe for a particular person.'),
        h('li', null, 'Comparing two labels shows their warnings side by side; it does not check whether the medicines can be taken together.'),
        h('li', null, 'Home care and herbs summarize US government health pages. Herbs and supplements are not approved as medicines, the evidence for most is weak, and some interact with medicines; each page says what is known.')),
      h('p', null, 'In an emergency, contact emergency services or a poison control center whenever possible.'),
      h('p', { class: 'small muted' }, 'Your answer is remembered in this browser only.'),
      btn)));
  btn.focus();
}

function sectionBody(text) {
  return h('div', { class: 'label-text' }, String(text).split('\n').map((l) => h('p', null, l)));
}

function groupCard(g, kindParam) {
  const href = `#/medicines/group?key=${encodeURIComponent(g.key)}${kindParam ? `&kind=${kindParam}` : ''}`;
  return h('li', null,
    h('a', { href }, h('b', null, g.title)), ' ',
    g.kinds.map(kindBadge),
    h('div', { class: 'src' }, `${g.products} product${g.products === 1 ? '' : 's'}${g.example?.name && g.example.name !== g.title ? ` · e.g. ${g.example.name}` : ''}`));
}

async function searchPanel(params) {
  const input = h('input', { type: 'search', placeholder: 'Medicine, brand or ingredient, e.g. ibuprofen or Pepto', 'aria-label': 'Medicine name', value: params.get('q') || '' });
  const kind = h('select', { 'aria-label': 'Kind' }, [['', 'All medicines'], ['otc', 'Non-prescription'], ['rx', 'Prescription']]
    .map(([v, t]) => h('option', { value: v, selected: v === (params.get('kind') || '') }, t)));
  const results = h('div');
  let seq = 0;
  async function run() {
    const q = input.value.trim();
    const mine = ++seq;
    history.replaceState(null, '', `#/medicines?q=${encodeURIComponent(q)}${kind.value ? `&kind=${kind.value}` : ''}`);
    if (!q) { clear(results); return; }
    try {
      const r = await api.get(`/api/drugs/search?q=${encodeURIComponent(q)}&limit=30${kind.value ? `&kind=${kind.value}` : ''}`);
      if (mine !== seq) return;
      fill(results,
        h('p', { class: 'small muted' }, `${r.total} group${r.total === 1 ? '' : 's'} of products sharing the same active ingredients.`),
        r.groups.length ? h('ul', { class: 'results' }, r.groups.map((g) => groupCard(g, kind.value))) : h('p', { class: 'empty' }, 'No label matches. Try the ingredient name, or fewer letters.'));
    } catch (e) { if (mine === seq) fill(results, h('div', { class: 'banner err' }, e.message)); }
  }
  input.addEventListener('input', debounce(run, 250));
  kind.addEventListener('change', run);
  if (input.value) run();
  return h('div', null,
    h('form', { class: 'toolbar', role: 'search', onsubmit: (e) => { e.preventDefault(); run(); } }, h('div', { class: 'grow' }, input), kind),
    results);
}

function situationsPanel(info, params) {
  const installed = info.installed;
  const chosen = new Set((params.get('s') || '').split(',').filter(Boolean));
  const rx = h('input', { type: 'checkbox', checked: params.get('kind') === 'all' });
  const results = h('div');
  const byCat = {};
  for (const s of info.situations) (byCat[s.category] = byCat[s.category] || []).push(s);
  const boxes = Object.entries(byCat).map(([cat, list]) => h('fieldset', { class: 'situations' },
    h('legend', null, cat),
    list.map((s) => {
      const cb = h('input', { type: 'checkbox', value: s.slug, checked: chosen.has(s.slug) });
      cb.addEventListener('change', () => { if (cb.checked) chosen.add(s.slug); else chosen.delete(s.slug); });
      return h('label', { class: 'inline' }, cb, s.label);
    })));
  const names = Object.fromEntries(info.situations.map((s) => [s.slug, s.label]));
  async function run() {
    if (!chosen.size) { fill(results, h('p', { class: 'empty' }, 'Choose one or more situations.')); return; }
    const s = [...chosen].slice(0, 5).join(',');
    history.replaceState(null, '', `#/medicines?tab=situations&s=${s}${rx.checked ? '&kind=all' : ''}`);
    fill(results, h('p', { class: 'empty' }, 'Looking…'));
    try {
      const [r, rem] = await Promise.all([
        installed ? api.get(`/api/drugs/situations?s=${encodeURIComponent(s)}${rx.checked ? '&kind=all' : ''}`) : null,
        api.get(`/api/drugs/remedies?s=${encodeURIComponent(s)}`),
      ]);
      // Without the label reference, each situation gets a section of its own.
      const sections = r ? r.results : s.split(',').map((x) => ({ situations: [x], groups: [] }));
      fill(results, sections.map((res, i) => {
        const single = res.situations.length === 1;
        const rems = single ? rem.remedies.filter((x) => x.situations.includes(res.situations[0])) : [];
        return h('section', { class: 'search-sec' },
          h('h3', null, single ? names[res.situations[0]] : `Covering all of: ${res.situations.map((x) => names[x]).join(', ')}`),
          !installed ? null : res.groups.length ? h('ul', { class: 'results' }, res.groups.map((g) => groupCard(g, rx.checked ? '' : 'otc')))
            : h('p', { class: 'small muted' }, i === 0 && !single ? 'No single medicine’s label mentions all of these. See each situation below.' : 'No label mentions this.'),
          rems.length ? [h('h4', null, 'Home care and herbs'), h('ul', { class: 'results' }, rems.map((x) => remedyItem(x)))]
            : single && !installed ? h('p', { class: 'small muted' }, 'No home care or herb is listed for this.') : null);
      }));
    } catch (e) { fill(results, h('div', { class: 'banner err' }, e.message)); }
  }
  if (chosen.size) run();
  return h('div', null,
    installed
      ? h('p', { class: 'small muted' }, 'Medicines whose labels say they treat the problem, grouped by active ingredient, most products first, then home care and herbs. Several choices list medicines covering all of them first. Homeopathic products are left out.')
      : h('p', { class: 'small muted' }, 'Home care and herbs for each problem. Medicines appear here too once the FDA drug label reference is installed.'),
    boxes,
    h('div', { class: 'row' },
      installed ? h('label', { class: 'inline' }, rx, 'Include prescription medicines') : null,
      h('button', { class: 'primary', onclick: run }, installed ? 'Show medicines' : 'Show home care and herbs')),
    results);
}

function remediesPanel(info) {
  const names = Object.fromEntries(info.situations.map((s) => [s.slug, s.label]));
  const input = h('input', { type: 'search', placeholder: 'Remedy or problem, e.g. honey or sore throat', 'aria-label': 'Filter remedies' });
  const list = h('div', null, h('p', { class: 'empty' }, 'Loading…'));
  let all = [];
  function render() {
    const q = input.value.trim().toLowerCase();
    const match = (r) => !q || [r.name, ...r.commonNames, ...r.situations.map((s) => names[s] || s)].join(' ').toLowerCase().includes(q);
    fill(list, [['home', 'Home care'], ['herbal', 'Herbs and supplements']].map(([kind, title]) => {
      const rs = all.filter((r) => r.kind === kind && match(r));
      return h('section', { class: 'search-sec' }, h('h3', null, title),
        rs.length ? h('ul', { class: 'results' }, rs.map((r) => remedyItem(r, names))) : h('p', { class: 'small muted' }, 'None match.'));
    }));
  }
  api.get('/api/drugs/remedies').then((r) => { all = r.remedies; render(); })
    .catch((e) => fill(list, h('div', { class: 'banner err' }, e.message)));
  input.addEventListener('input', debounce(render, 150));
  return h('div', null,
    h('p', { class: 'small muted' }, 'Home care from CDC, MedlinePlus and FDA pages, and herbs from the NCCIH “Herbs at a Glance” pages, with the evidence and cautions for each. Built into Arcana: nothing to download.'),
    h('form', { class: 'toolbar', role: 'search', onsubmit: (e) => e.preventDefault() }, h('div', { class: 'grow' }, input)),
    list);
}

async function remedyView(main, slug) {
  const [r, info] = await Promise.all([api.get(`/api/drugs/remedies/${encodeURIComponent(slug)}`), api.get('/api/drugs')]);
  const m = r.remedy;
  const names = Object.fromEntries(info.situations.map((s) => [s.slug, s.label]));
  const sec = (title, text, cls = '') => (text ? h('section', { class: `label-sec ${cls}` }, h('h3', null, title), sectionBody(text)) : null);
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('a', { href: '#/medicines?tab=remedies', class: 'small', onclick: (e) => { if (history.length > 1) { e.preventDefault(); history.back(); } } }, '← Back'),
      h('h2', null, m.name), remedyBadge(m.kind)),
    h('div', { class: 'body' },
      m.commonNames.length ? h('div', { class: 'src' }, `Also called ${m.commonNames.join(', ')}`) : null,
      h('div', { class: 'src' }, 'For: ', m.situations.map((s, i) => [i ? ', ' : '', h('a', { href: `#/medicines?tab=situations&s=${encodeURIComponent(s)}` }, names[s] || s)])),
      remedyNotice(),
      sec('Cautions', m.cautions, 'boxed'),
      sec('What it is used for', m.uses),
      sec('How it is used', m.how),
      sec('What the evidence shows', m.evidence),
      h('p', { class: 'small muted' }, 'Source: ', h('span', { class: 'mono' }, m.sourceUrl)),
      info.remedies ? h('p', { class: 'small muted' }, `Summary of that page compiled by Project NOMAD (${info.remedies.license}), version ${info.remedies.version}.`) : null)));
}

function remediesAbout(info) {
  const rm = info.remedies;
  if (!rm) return null;
  return [h('h3', null, 'Home care and herbs'),
    h('dl', { class: 'meta' },
      h('dt', null, 'Sources'), h('dd', null, rm.sources.map((s) => s.name).join('; ')),
      h('dt', null, 'Remedies'), h('dd', null, String(rm.count)),
      h('dt', null, 'Version'), h('dd', null, rm.version),
      h('dt', null, 'Compiled by'), h('dd', null, `Project NOMAD, ${rm.license}. Each entry names the government page it summarizes.`)),
    h('p', { class: 'small muted' }, 'Built into Arcana, so it is there before the label reference is installed. It changes only with Arcana updates.')];
}

function aboutPanel(info) {
  if (!info.installed) return h('div', null, remediesAbout(info));
  const st = info.stats || {};
  const m = st.meta || {};
  const c = info.citation;
  return h('div', null,
    h('h3', null, 'FDA drug labels'),
    h('dl', { class: 'meta' },
      h('dt', null, 'Source'), h('dd', null, 'U.S. Food and Drug Administration, openFDA drug label downloads'),
      h('dt', null, 'FDA export date'), h('dd', null, fmtDate(m.export)),
      h('dt', null, 'Built on this computer'), h('dd', null, fmtDate(m.built)),
      h('dt', null, 'Labels'), h('dd', null, `${(st.labels || 0).toLocaleString()} (${(st.distinct || 0).toLocaleString()} distinct texts; ${Object.entries(st.byKind || {}).map(([k, n]) => `${KIND_LABEL[k] || k} ${n.toLocaleString()}`).join(', ')})`),
      h('dt', null, 'License'), h('dd', null, 'Public domain (CC0 1.0); label text is the manufacturers’')),
    c ? h('p', { class: 'small muted' }, `Installed as ${c.title}, edition ${c.edition}. Update it from the Updates view when the FDA publishes a newer export.`) : null,
    h('p', { class: 'small muted' }, 'Arcana keeps the parts of each label a reader needs (uses, directions, warnings, interactions, overdose, side effects); clinical studies, pharmacology and package text are left out to keep the reference small.'),
    remediesAbout(info));
}

async function groupView(main, params) {
  const key = params.get('key') || '';
  const kind = params.get('kind') || '';
  const r = await api.get(`/api/drugs/group?key=${encodeURIComponent(key)}&limit=100${kind ? `&kind=${kind}` : ''}`);
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('a', { href: '#/medicines', class: 'small' }, '← Medicines'), h('h2', null, `${r.total} product${r.total === 1 ? '' : 's'}: ${key.replace(/^name:/, '')}`)),
    h('div', { class: 'body' }, notice(),
      h('table', { class: 'grid' }, h('thead', null, h('tr', null, h('th', null, 'Product'), h('th', null, 'Manufacturer'), h('th', null, 'Kind'), h('th', { class: 'hide-sm' }, 'Label date'))),
        h('tbody', null, r.products.map((p) => h('tr', null,
          h('td', null, h('a', { href: `#/medicines/label/${p.id}` }, p.name)),
          h('td', null, p.manufacturer || '—'), h('td', null, kindBadge(p.kind)), h('td', { class: 'hide-sm' }, fmtDate(p.effective)))))),
      r.total > r.products.length ? h('p', { class: 'small muted' }, `The first ${r.products.length} are shown.`) : null)));
}

async function labelView(main, id) {
  const r = await api.get(`/api/drugs/label/${encodeURIComponent(id)}`);
  const p = r.product;
  const picker = h('div', { class: 'compare-picker', hidden: true });
  const pickInput = h('input', { type: 'search', placeholder: 'Second medicine', 'aria-label': 'Second medicine' });
  const pickList = h('ul', { class: 'results' });
  pickInput.addEventListener('input', debounce(async () => {
    const q = pickInput.value.trim();
    if (!q) { clear(pickList); return; }
    const sr = await api.get(`/api/drugs/search?q=${encodeURIComponent(q)}&limit=8`);
    fill(pickList, sr.groups.map((g) => h('li', null, h('a', { href: `#/medicines/compare/${p.id}/${g.example.id}` }, g.example.name), ' ', kindBadge(g.example.kind),
      h('div', { class: 'src' }, g.title))));
  }, 250));
  picker.append(pickInput, pickList);
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('a', { href: '#/medicines', class: 'small', onclick: (e) => { if (history.length > 1) { e.preventDefault(); history.back(); } } }, '← Back'),
      h('h2', null, p.name), kindBadge(p.kind),
      h('button', { onclick: () => { picker.hidden = !picker.hidden; if (!picker.hidden) pickInput.focus(); } }, 'Compare warnings…')),
    h('div', { class: 'body' },
      h('div', { class: 'src' }, [p.generic, p.manufacturer, p.route, p.effective ? `label of ${fmtDate(p.effective)}` : null].filter(Boolean).join(' · ')),
      picker,
      notice(),
      r.sections.map((s) => h('section', { class: `label-sec ${s.key === 'boxed_warning' ? 'boxed' : ''}` }, h('h3', null, s.title), sectionBody(s.text))),
      h('p', { class: 'small muted' }, `FDA set ID ${p.setId}. Text as submitted by the manufacturer.`))));
}

async function compareView(main, a, b) {
  const [ra, rb] = await Promise.all([api.get(`/api/drugs/label/${encodeURIComponent(a)}`), api.get(`/api/drugs/label/${encodeURIComponent(b)}`)]);
  const keys = WARNING_KEYS.filter((k) => ra.sections.some((s) => s.key === k) || rb.sections.some((s) => s.key === k));
  const get = (r, k) => r.sections.find((s) => s.key === k);
  const title = (k) => (get(ra, k) || get(rb, k)).title;
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('a', { href: `#/medicines/label/${a}`, class: 'small' }, '← Back'), h('h2', null, 'Label warnings side by side')),
    h('div', { class: 'body' },
      h('div', { class: 'banner warn' }, 'This puts two manufacturers’ warnings next to each other. It does not check whether the two medicines can be taken together, and a missing section does not mean there is no risk. Ask a pharmacist or doctor.'),
      h('div', { class: 'compare' },
        h('div', { class: 'compare-head' }, h('a', { href: `#/medicines/label/${a}` }, h('b', null, ra.product.name)), ' ', kindBadge(ra.product.kind)),
        h('div', { class: 'compare-head' }, h('a', { href: `#/medicines/label/${b}` }, h('b', null, rb.product.name)), ' ', kindBadge(rb.product.kind)),
        keys.map((k) => [
          h('h3', { class: 'compare-title' }, title(k)),
          h('div', null, get(ra, k) ? sectionBody(get(ra, k).text) : h('p', { class: 'muted small' }, 'Not on this label.')),
          h('div', null, get(rb, k) ? sectionBody(get(rb, k).text) : h('p', { class: 'muted small' }, 'Not on this label.')),
        ])))));
}

export async function medicinesView(main, params, ctx, args = []) {
  if (!acknowledged()) {
    gate(main, () => { clear(main); medicinesView(main, params, ctx, args); });
    return null;
  }
  if (args[0] === 'label' && args[1]) { await labelView(main, args[1]); return null; }
  if (args[0] === 'compare' && args[1] && args[2]) { await compareView(main, args[1], args[2]); return null; }
  if (args[0] === 'group') { await groupView(main, params); return null; }
  if (args[0] === 'remedy' && args[1]) { await remedyView(main, args[1]); return null; }

  const info = await api.get('/api/drugs');
  const tabs = [...(info.installed ? [['search', 'By name']] : []), ['situations', 'By situation'], ['remedies', 'Home care and herbs'], ['about', 'About the data']];
  const tab = tabs.some(([id]) => id === params.get('tab')) ? params.get('tab') : tabs[0][0];
  const body = h('div', { class: 'body' });
  const nav = h('div', { class: 'subtabs', role: 'tablist' }, tabs.map(([id, t]) => h('a', { href: `#/medicines?tab=${id}`, role: 'tab', 'aria-selected': id === tab ? 'true' : 'false' }, t)));
  main.appendChild(h('section', { class: 'panel' }, h('header', null, h('h2', null, 'Medicines'), nav), body));
  if (!info.installed) {
    body.appendChild(h('p', { class: 'small' }, 'The FDA drug label reference is not installed, so only the built-in home care and herbs are shown. ',
      h('a', { href: '#/discover?category=medicine&q=FDA%20drug%20labels' }, 'Find it in Discover'),
      ' (refresh the catalog first). Arcana downloads about 1.8 GB from openFDA and builds a reference of about 1 GB on this computer.'));
  }
  body.appendChild(tab === 'remedies' || !info.installed ? remedyNotice() : notice());
  if (tab === 'situations') body.appendChild(situationsPanel(info, params));
  else if (tab === 'remedies') body.appendChild(remediesPanel(info));
  else if (tab === 'about') body.appendChild(aboutPanel(info));
  else body.appendChild(await searchPanel(params));
  return null;
}
