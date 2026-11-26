// Getting started: a short guided setup after a library is created.
// Topics → language and area → how much space → review and download.
import { api, getSession } from '../api.js';
import { h, clear, fill, fmtBytes, storageBar, toast, debounce, LANGS } from '../util.js';

const TOPICS = [
  { id: 'essential', title: 'Everyday reference', text: 'Wikipedia, a dictionary, repair guides and travel information.' },
  { id: 'family-emergency', title: 'Emergency preparedness', text: 'First aid, water, food, shelter and repairs for a household.' },
  { id: 'medical', title: 'Medical reference', text: 'Medical encyclopedias, field medicine and public health guidance.' },
  { id: 'amateur-radio', title: 'Amateur radio', text: 'Regulations, radio and electronics questions and answers.' },
  { id: 'field-technician', title: 'Repair and technical work', text: 'Repair manuals, electronics, mechanics and energy.' },
  { id: 'remote-expedition', title: 'Travel and the outdoors', text: 'Travel guides, outdoor skills, knots and the World Factbook.' },
  { id: 'education', title: 'Education', text: 'Textbooks, courses and simulations for learners.' },
  { id: 'food-homestead', title: 'Food and homesteading', text: 'Cooking, preserving food, gardening and self-reliant living.' },
  { id: 'computing', title: 'Computing and programming', text: 'Programming lessons and documentation for common languages and tools.' },
  { id: 'entertainment', title: 'Films and games', text: 'Classic public-domain films to watch offline, and rules questions for board games. The board games are built in.' },
];

const EXTRAS = [
  { id: 'maps', title: 'Maps of my area', text: 'Offline maps with place search, for the region you choose next.', on: true },
  { id: 'roads', title: 'Directions', text: 'Routes and turn-by-turn guidance in the same region (built on this computer).' },
  { id: 'drugs', title: 'Medicines reference', text: 'FDA labels of US medicines, by name or by situation (about 1.8 GB to download, 0.9 GB installed).' },
  { id: 'assistant', title: 'Ask questions', text: 'A small language model answers from your library with sources (about 1.9 GB; slower on older computers).' },
  { id: 'translation', title: 'Translation', text: 'Translate pages between English and another language on the device reading them (about 50 MB).' },
];

const TIERS = [
  { id: 'essential', title: 'Essential', text: 'Up to about 8 GB: compact editions, mostly text. Good for small drives and quick downloads.' },
  { id: 'standard', title: 'Standard', text: 'Up to about 40 GB: fuller editions with pictures where they fit.' },
  { id: 'comprehensive', title: 'Comprehensive', text: 'As much as fits above your reserved free space: the most detailed editions.' },
];

const MAP_VARIANT = { essential: 'z10', standard: 'z13', comprehensive: 'z15' };

export async function welcomeView(main) {
  const s = getSession();
  if (!s.admin || s.readOnly) {
    main.appendChild(h('div', { class: 'banner info' }, 'Getting started is available on the computer running Arcana.'));
    return null;
  }
  const [status, cat] = await Promise.all([api.get('/api/status'), api.get('/api/updates').catch(() => null)]);
  const kiwixKnown = (cat?.providers || []).some((p) => p.id === 'kiwix' && p.fetchedAt);
  const browserLang = (navigator.language || 'en').split('-')[0];

  const state = {
    step: 0, topics: new Set(['essential', 'family-emergency']), extras: new Set(['maps']),
    lang: LANGS[browserLang] ? browserLang : 'en', region: null, tier: 'standard', otherLang: '', plan: null,
  };

  const body = h('div', { class: 'body' });
  const steps = ['What do you need?', 'Language and area', 'How much space?', 'Review'];
  const stepNav = h('ol', { class: 'wizard-steps' });
  const back = h('button', { onclick: () => go(state.step - 1) }, 'Back');
  // The handler is set per step in render().
  const next = h('button', { class: 'primary' }, 'Next');
  const skip = h('button', { class: 'link small', onclick: finishLater }, 'Skip for now');
  main.appendChild(h('section', { class: 'panel wizard' },
    h('header', null, h('h2', null, 'Getting started'), skip),
    stepNav, body,
    h('div', { class: 'row wizard-buttons' }, back, next)));

  async function finishLater() {
    try { await api.put('/api/settings', { welcomeDone: true }); } catch (_) { /* ignore */ }
    location.hash = '#/library';
  }

  function go(n) {
    if (n < 0 || n > 3) return;
    state.step = n;
    render();
  }

  const choice = (type, name, checked, title, text, onchange) => {
    const input = h('input', { type, name, checked });
    input.addEventListener('change', () => onchange(input.checked));
    return h('label', { class: 'choice' }, input, h('span', null, h('b', null, title), h('span', { class: 'small muted' }, text)));
  };

  async function refreshCatalog(btn, note) {
    btn.disabled = true;
    btn.textContent = 'Refreshing… (this can take a minute)';
    try {
      const r = await api.post('/api/catalog/refresh');
      const failed = (r.results || []).filter((x) => x.error);
      note.textContent = failed.length ? `Some catalogs could not be reached: ${failed.map((x) => x.name).join(', ')}.` : 'Catalogs refreshed.';
      btn.textContent = 'Refreshed';
    } catch (e) { note.textContent = e.message; btn.disabled = false; btn.textContent = 'Try again'; }
  }

  function renderStep0() {
    const note = h('span', { class: 'small muted' });
    const refreshBtn = h('button', { onclick: () => refreshCatalog(refreshBtn, note) }, 'Refresh the catalogs now');
    body.append(
      h('p', null, 'Choose what this drive should hold. Arcana suggests editions that fit the space you have, and you can change anything later in Discover.'),
      kiwixKnown ? null : h('div', { class: 'banner info' },
        'Most reference material comes from online catalogs. Connect to the internet and refresh them first; without internet you can still import archives from another drive.', ' ', refreshBtn, ' ', note),
      h('h3', null, 'Topics'),
      h('div', { class: 'choices' }, TOPICS.map((t) => choice('checkbox', 'topic', state.topics.has(t.id), t.title, t.text,
        (on) => { if (on) state.topics.add(t.id); else state.topics.delete(t.id); }))),
      h('h3', null, 'Tools'),
      h('div', { class: 'choices' }, EXTRAS.map((t) => choice('checkbox', 'extra', state.extras.has(t.id), t.title, t.text,
        (on) => { if (on) state.extras.add(t.id); else state.extras.delete(t.id); }))));
  }

  function renderStep1() {
    const langSel = h('select', null, Object.entries(LANGS).filter(([c]) => c !== 'mul').map(([c, n]) => h('option', { value: c, selected: c === state.lang }, n)));
    langSel.addEventListener('change', () => { state.lang = langSel.value; });
    body.append(h('h3', null, 'Language of the reference material'), langSel,
      h('p', { class: 'small muted' }, 'Collections use this language where an edition exists, and English otherwise.'));
    if (state.extras.has('maps') || state.extras.has('roads')) {
      const q = h('input', { type: 'search', placeholder: 'Country, state or province, e.g. Virginia', style: 'width:min(420px,100%)', 'aria-label': 'Region' });
      const list = h('ul', { class: 'results region-list' });
      const chosen = h('p', { class: 'small' }, state.region ? `Chosen: ${state.region.title}` : 'No region chosen yet.');
      const search = debounce(async () => {
        const text = q.value.trim();
        if (!text) { clear(list); return; }
        const r = await api.get(`/api/catalog?category=maps-navigation&q=${encodeURIComponent(text)}&limit=60`);
        const fams = new Map();
        for (const x of r.resources) if (x.format === 'pmtiles' && x.family && !fams.has(x.family)) fams.set(x.family, x);
        fill(list, [...fams.values()].slice(0, 12).map((x) => h('li', null, h('button', { class: 'linkish', onclick: () => {
          state.region = { family: x.family, title: x.title.replace(/ — map$/, '') };
          chosen.textContent = `Chosen: ${state.region.title}`;
        } }, x.title.replace(/ — map$/, '')))));
        if (!fams.size) list.appendChild(h('li', { class: 'small muted' }, 'No map region matches. Refresh the catalogs, or try the country name.'));
      }, 250);
      q.addEventListener('input', search);
      body.append(h('h3', null, 'Your area (for maps and directions)'), q, chosen, list);
    }
    if (state.extras.has('translation')) {
      const other = h('select', null, h('option', { value: '' }, 'Choose a language'),
        Object.entries(LANGS).filter(([c]) => c !== 'en' && c !== 'mul').map(([c, n]) => h('option', { value: c, selected: c === state.otherLang }, n)));
      other.addEventListener('change', () => { state.otherLang = other.value; });
      if (!state.otherLang && state.lang !== 'en') state.otherLang = state.lang;
      other.value = state.otherLang;
      body.append(h('h3', null, 'Translate between English and'), other);
    }
  }

  function renderStep2() {
    const st = status.storage;
    body.append(h('p', null, `This drive has ${fmtBytes(st.plan.available)} free; Arcana keeps ${fmtBytes(st.plan.reserve)} of it in reserve, so about ${fmtBytes(Math.max(st.plan.headroom, 0))} can be used.`),
      h('div', { class: 'choices' }, TIERS.map((t) => choice('radio', 'tier', state.tier === t.id, t.title, t.text, (on) => { if (on) state.tier = t.id; }))));
  }

  function extrasIds() {
    const ids = [];
    const region = state.region?.family?.replace(/^maps\./, '');
    if (state.extras.has('maps') && region) ids.push(`maps.${region}-${MAP_VARIANT[state.tier]}`);
    if (state.extras.has('roads') && region) ids.push(`roads.${region}`);
    if (state.extras.has('drugs')) ids.push('openfda.drug-labels');
    if (state.extras.has('assistant')) ids.push(state.tier === 'essential' ? 'arcana.assistant-qwen2.5-0.5b' : 'arcana.assistant-granite-3.3-2b');
    if (state.extras.has('translation') && state.otherLang) ids.push(`translations.${state.otherLang}`);
    return ids;
  }

  async function renderStep3() {
    body.appendChild(h('p', { class: 'empty' }, 'Working out what fits…'));
    try {
      state.plan = await api.post('/api/welcome/plan', { presets: [...state.topics], language: state.lang, tier: state.tier, extras: extrasIds() });
    } catch (e) { fill(body, h('div', { class: 'banner err' }, e.message)); return; }
    const p = state.plan;
    const groups = new Map();
    for (const it of p.items) (groups.get(it.group) || groups.set(it.group, []).get(it.group)).push(it);
    fill(body,
      h('p', null, p.items.length ? `${p.items.length} item${p.items.length === 1 ? '' : 's'}, ${fmtBytes(p.total)} to download.` : 'Nothing to download with these choices.'),
      storageBar(p.storage.plan),
      [...groups].map(([g, items]) => h('section', { class: 'search-sec' }, h('h3', null, g),
        h('ul', { class: 'results' }, items.map((it) => h('li', null, h('b', null, it.title), ' ', h('span', { class: 'small muted' }, fmtBytes(it.size)),
          it.reason ? h('div', { class: 'src' }, it.reason) : null))))),
      p.skipped.length ? h('details', null, h('summary', { class: 'small' }, `${p.skipped.length} left out (do not fit, or not in the catalog yet)`),
        h('ul', { class: 'small muted' }, p.skipped.map((it) => h('li', null, `${it.title}${it.reason && it.group === 'Your choices' ? ` — ${it.reason}` : ''}`)))) : null,
      state.extras.has('assistant') ? h('p', { class: 'small muted' }, 'Question answering will be turned on for this computer. Network devices cannot ask until you allow it in Settings.') : null);
  }

  async function download() {
    const ids = state.plan.items.map((i) => i.resourceId);
    next.disabled = true;
    try {
      if (ids.length) {
        const r = await api.post('/api/downloads', { resourceIds: ids });
        const bad = Object.entries(r.results).filter(([, v]) => v !== 'queued');
        if (bad.length) toast(`Some items were not queued: ${bad.map(([k, v]) => `${k}: ${v}`).join('; ')}`);
      }
      const settings = { welcomeDone: true };
      if (state.extras.has('assistant')) {
        const cur = (await api.get('/api/settings')).settings.assistant;
        settings.assistant = { ...cur, enabled: true, backend: 'local' };
      }
      await api.put('/api/settings', settings);
      window.dispatchEvent(new Event('arcana-status'));
      location.hash = ids.length ? '#/downloads' : '#/library';
    } catch (e) { toast(e.message); next.disabled = false; }
  }

  function render() {
    clear(body);
    fill(stepNav, steps.map((t, i) => h('li', { class: i === state.step ? 'current' : i < state.step ? 'done' : null }, t)));
    back.hidden = state.step === 0;
    next.textContent = state.step === 3 ? 'Download' : 'Next';
    next.onclick = state.step === 3 ? download : () => go(state.step + 1);
    next.disabled = false;
    [renderStep0, renderStep1, renderStep2, renderStep3][state.step]();
  }
  render();
  return null;
}
