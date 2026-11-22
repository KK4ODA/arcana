// Small DOM and formatting helpers. All text is inserted with textContent;
// nothing from the server or a catalog is interpreted as HTML.

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === null || v === undefined || v === false) continue;
      if (k === 'class') el.className = v;
      // CSSOM assignment is permitted by the strict CSP; style attributes are not.
      else if (k === 'style') el.style.cssText = v;
      else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
      else if (k === 'dataset') Object.assign(el.dataset, v);
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, String(v));
    }
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    if (Array.isArray(c)) append(el, c);
    else if (c instanceof Node) el.appendChild(c);
    else el.appendChild(document.createTextNode(String(c)));
  }
}

export function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); return el; }

// fill replaces an element's children, skipping null/false like h() does
// (DOM append() would render them as the text "null").
export function fill(el, ...children) { clear(el); append(el, children); return el; }

export function fmtBytes(n) {
  if (n === null || n === undefined || isNaN(n)) return '—';
  const neg = n < 0; n = Math.abs(n);
  const u = ['B', 'kB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  while (n >= 1000 && i < u.length - 1) { n /= 1000; i++; }
  const s = i === 0 ? `${n} B` : `${n >= 100 ? Math.round(n) : n.toFixed(1)} ${u[i]}`;
  return neg ? '−' + s : s;
}

export function fmtBinary(n) {
  const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return i === 0 ? `${n} B` : `${n.toFixed(1)} ${u[i]}`;
}

export function fmtDuration(s) {
  if (!s || s < 0) return '—';
  if (s < 60) return `${Math.round(s)} s`;
  if (s < 3600) return `${Math.floor(s / 60)} min`;
  const hrs = Math.floor(s / 3600);
  return hrs < 48 ? `${hrs} h ${Math.floor((s % 3600) / 60)} min` : `${Math.round(hrs / 24)} days`;
}

export function fmtDate(s) {
  if (!s) return '—';
  return String(s).slice(0, 10);
}

export function fmtAgo(s) {
  if (!s) return 'never';
  const t = Date.parse(s);
  if (isNaN(t)) return s;
  const d = (Date.now() - t) / 1000;
  if (d < 90) return 'just now';
  if (d < 5400) return `${Math.round(d / 60)} minutes ago`;
  if (d < 172800) return `${Math.round(d / 3600)} hours ago`;
  return `${Math.round(d / 86400)} days ago (${fmtDate(s)})`;
}

export const LANGS = {
  en: 'English', es: 'Spanish', fr: 'French', de: 'German', pt: 'Portuguese', it: 'Italian', nl: 'Dutch',
  ru: 'Russian', uk: 'Ukrainian', pl: 'Polish', ar: 'Arabic', fa: 'Persian', he: 'Hebrew', hi: 'Hindi',
  bn: 'Bengali', ur: 'Urdu', zh: 'Chinese', ja: 'Japanese', ko: 'Korean', vi: 'Vietnamese', th: 'Thai',
  id: 'Indonesian', tr: 'Turkish', sw: 'Swahili', am: 'Amharic', ha: 'Hausa', yo: 'Yoruba', ht: 'Haitian Creole',
  tl: 'Tagalog', el: 'Greek', cs: 'Czech', ro: 'Romanian', hu: 'Hungarian', sv: 'Swedish', fi: 'Finnish',
  no: 'Norwegian', da: 'Danish', ca: 'Catalan', eo: 'Esperanto', la: 'Latin', mul: 'Multiple languages',
};
export const langName = (c) => LANGS[c] || c;

export function licenseBadge(lic) {
  if (!lic) return null;
  const r = lic.redistribution;
  const cls = r === 'permitted' ? 'ok' : r === 'noncommercial' ? 'info' : r === 'restricted' ? 'err' : 'warn';
  const label = r === 'permitted' ? lic.id : r === 'noncommercial' ? 'Non-commercial' : r === 'restricted' ? 'Restricted' : 'License unverified';
  return h('span', { class: `badge ${cls}`, title: [lic.name || lic.id, lic.notes].filter(Boolean).join('. ') }, label);
}

export function warningBadges(warnings) {
  if (!warnings || !warnings.length) return null;
  const kinds = [...new Set(warnings.map((w) => w.kind))];
  return kinds.map((k) => h('span', {
    class: `badge ${k === 'medical' ? 'err' : 'warn'}`,
    title: warnings.filter((w) => w.kind === k).map((w) => w.text).join(' '),
  }, { medical: 'Medical', regulatory: 'Regulatory', outdated: 'Dated', license: 'License', content: 'Note' }[k] || k));
}

export function warningList(warnings) {
  if (!warnings || !warnings.length) return null;
  return h('ul', { class: 'warnings' }, warnings.map((w) => h('li', { class: w.kind }, w.text)));
}

let toastTimer;
export function toast(msg) {
  let el = document.querySelector('.toast');
  if (!el) { el = h('div', { class: 'toast', role: 'status' }); document.body.appendChild(el); }
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 5000);
}

export function debounce(fn, ms) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

// storageBar renders the segmented capacity bar and legend from a plan.
export function storageBar(plan) {
  const total = Math.max(plan.total, 1);
  const pct = (n) => `${Math.max(0, (n / total) * 100)}%`;
  const free = Math.max(plan.afterSelection - plan.reserve, 0);
  const over = plan.headroom < 0 ? -plan.headroom : 0;
  const bar = h('div', { class: 'storagebar', role: 'img',
    'aria-label': `Installed ${fmtBytes(plan.installed)}, queued ${fmtBytes(plan.queued)}, selected ${fmtBytes(plan.selected)}, reserve ${fmtBytes(plan.reserve)}, free ${fmtBytes(free)}` },
    h('span', { class: 'installed', style: `width:${pct(plan.installed + plan.staged)}` }),
    h('span', { class: 'other', style: `width:${pct(plan.other)}` }),
    h('span', { class: 'queued', style: `width:${pct(Math.max(plan.queued - plan.staged, 0))}` }),
    h('span', { class: 'selected', style: `width:${pct(Math.max(plan.selected - over, 0))}` }),
    over > 0 ? h('span', { class: 'over', style: `width:${pct(over)}` }) : null,
    h('span', { class: 'reserve', style: `width:${pct(Math.min(plan.reserve, Math.max(plan.afterSelection, 0)))}` }),
  );
  const legend = h('div', { class: 'legend' },
    h('span', { class: 'installed' }, h('i'), `Installed ${fmtBytes(plan.installed)}`),
    plan.other > 0 ? h('span', { class: 'other' }, h('i'), `Other files ${fmtBytes(plan.other)}`) : null,
    plan.queued > 0 ? h('span', { class: 'queued' }, h('i'), `Queued ${fmtBytes(plan.queued)}`) : null,
    plan.selected > 0 ? h('span', { class: 'selected' }, h('i'), `Selected ${fmtBytes(plan.selected)}`) : null,
    h('span', { class: 'reserve' }, h('i'), `Reserve ${fmtBytes(plan.reserve)}`),
    h('span', { class: 'free' }, h('i'), `Free after plan ${fmtBytes(free)}`),
  );
  return h('div', null, bar, legend);
}
