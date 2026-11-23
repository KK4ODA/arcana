// Ask: questions answered from the installed library, with the passages
// each answer relies on. Answers stream as the model writes them.
import { api, getSession } from '../api.js';
import { h, clear, fill, fmtDate, warningBadges, warningList } from '../util.js';

const HISTORY_KEY = 'arcana-ask-history';

function loadHistory() {
  try { return JSON.parse(sessionStorage.getItem(HISTORY_KEY) || '[]'); } catch (_) { return []; }
}
function saveHistory(list) {
  try { sessionStorage.setItem(HISTORY_KEY, JSON.stringify(list.slice(0, 20))); } catch (_) { /* storage unavailable */ }
}

export function passageHref(p) {
  if (p.kind === 'user') return `#/doc?key=${encodeURIComponent(p.path)}`;
  if (p.kind === 'document') return `#/doc?key=${encodeURIComponent(`res:${p.resourceId}/${p.path}`)}`;
  return `#/read/${encodeURIComponent(p.resourceId)}?path=${encodeURIComponent(p.path)}`;
}

// inline renders **bold** and [n] citations as elements; everything else
// stays text.
function inline(text, passages, idPrefix) {
  const out = [];
  const re = /\*\*([^*]+)\*\*|\[(\d{1,2})\]/g;
  let last = 0;
  let m;
  while ((m = re.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index));
    if (m[1] !== undefined) out.push(h('b', null, m[1]));
    else {
      const n = parseInt(m[2], 10);
      const p = passages.find((x) => x.n === n);
      out.push(p ? h('a', { class: 'cite', href: `#${idPrefix}-${n}`, title: p.title,
        onclick: (e) => { e.preventDefault(); document.getElementById(`${idPrefix}-${n}`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' }); } }, `[${n}]`) : `[${n}]`);
    }
    last = re.lastIndex;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

// renderAnswer turns the model's plain text (paragraphs, "-" and "1."
// lists) into elements without interpreting any HTML.
export function renderAnswer(text, passages, idPrefix) {
  const el = h('div', { class: 'answer-text' });
  let list = null;
  for (const raw of String(text).split('\n')) {
    const line = raw.trim();
    if (!line) { list = null; continue; }
    const bullet = /^[-*•]\s+(.*)$/.exec(line);
    const num = /^(\d+)[.)]\s+(.*)$/.exec(line);
    if (bullet || num) {
      const kind = bullet ? 'ul' : 'ol';
      if (!list || list.tagName.toLowerCase() !== kind) {
        list = h(kind, null);
        if (num && num[1] !== '1') list.setAttribute('start', num[1]);
        el.appendChild(list);
      }
      list.appendChild(h('li', null, inline(bullet ? bullet[1] : num[2], passages, idPrefix)));
      continue;
    }
    list = null;
    const heading = /^#{1,4}\s+(.*)$/.exec(line);
    el.appendChild(heading ? h('h4', null, inline(heading[1], passages, idPrefix)) : h('p', null, inline(line, passages, idPrefix)));
  }
  return el;
}

// passageText shows the start of a long passage with a button for the
// rest.
function passageText(text) {
  const lines = text.split('\n');
  const short = lines.length > 8 || text.length > 520;
  if (!short) return h('blockquote', { class: 'passage' }, text);
  let cut = lines.slice(0, 8).join('\n');
  if (cut.length > 520) cut = cut.slice(0, 520);
  const q = h('blockquote', { class: 'passage' }, cut + ' …');
  const btn = h('button', { type: 'button', class: 'linkish small passage-more', onclick: () => {
    const open = btn.dataset.open === '1';
    q.textContent = open ? cut + ' …' : text;
    btn.dataset.open = open ? '' : '1';
    btn.textContent = open ? 'Show the whole passage' : 'Show less';
  } }, 'Show the whole passage');
  return h('div', null, q, btn);
}

function passageCard(p, idPrefix, cited) {
  const src = p.source || {};
  const meta = [src.resourceTitle, src.edition ? `edition ${src.edition}` : null, src.published ? fmtDate(src.published) : null, src.license]
    .filter(Boolean).join(' · ');
  return h('li', { id: `${idPrefix}-${p.n}`, class: cited ? 'cited' : null },
    h('div', { class: 'row' },
      h('span', { class: 'cite-num' }, `[${p.n}]`),
      h('a', { href: passageHref(p) }, h('b', null, p.title || 'Untitled')),
      warningBadges(src.warnings)),
    h('div', { class: 'src' }, meta),
    passageText(p.text),
    warningList(src.warnings));
}

function resultBlock(res, idPrefix) {
  const cited = new Set(res.check?.cited || []);
  const flags = [];
  if (!res.fromLibrary) flags.push(h('div', { class: 'banner warn' }, 'General answer: your library was not searched, so nothing here is checked against a source. The model may be wrong.'));
  else if (res.skipped) flags.push(h('div', { class: 'banner info' }, (res.passages || []).length
    ? 'Nothing in your library covers this question closely enough, so no answer was written. The closest passages are below; try other words or search the library.'
    : 'Nothing in your library matches this question, so no answer was written. Try other words, or search the library.'));
  else if (res.check?.notFound) flags.push(h('div', { class: 'banner info' }, 'The model found no answer in the passages from your library.'));
  else if (res.check?.unsupported) flags.push(h('div', { class: 'banner warn' }, 'This answer cites no passage. Treat it with caution and read the sources.'));
  else if (res.check?.uncited) flags.push(h('div', { class: 'small muted' }, `${res.check.uncited} sentence${res.check.uncited === 1 ? '' : 's'} without a citation.`));
  if (res.truncated) flags.push(h('div', { class: 'small muted' }, 'The answer was cut off at its length limit.'));
  const medical = (res.passages || []).some((p) => (p.source?.warnings || []).some((w) => w.kind === 'medical'));
  if (medical) flags.push(h('div', { class: 'banner err' }, 'Medical sources: this is dated reference material, not current medical advice. In an emergency, contact emergency services or a qualified clinician whenever possible.'));
  return h('div', null,
    flags,
    res.skipped ? null : renderAnswer(res.answer, res.passages || [], idPrefix),
    (res.passages || []).length ? h('div', { class: 'passages' },
      h('h3', null, res.skipped ? 'Closest passages' : 'Sources'),
      h('ol', { class: 'passage-list' }, res.passages.map((p) => passageCard(p, idPrefix, cited.has(p.n))))) : null,
    h('div', { class: 'small muted' }, res.elapsedMs ? `${(res.elapsedMs / 1000).toFixed(1)} s` : ''));
}

// streamAsk reads the server-sent events of /api/assistant/ask.
async function streamAsk(url, signal, on) {
  const r = await fetch(url, { cache: 'no-store', signal });
  if (!r.ok) {
    let msg = `${r.status} ${r.statusText}`;
    try { msg = (await r.json()).error || msg; } catch (_) { /* not JSON */ }
    throw new Error(msg);
  }
  const reader = r.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let ev = 'message';
      let data = '';
      for (const line of block.split('\n')) {
        if (line.startsWith('event: ')) ev = line.slice(7);
        else if (line.startsWith('data: ')) data += line.slice(6);
      }
      if (data) on(ev, JSON.parse(data));
    }
  }
}

export async function askView(main, params) {
  const s = getSession();
  let info;
  try { info = await api.get('/api/assistant'); } catch (e) { info = { canAsk: false, reason: e.message, status: {} }; }

  const intro = h('p', { class: 'small muted' },
    'Answers are written by a language model from passages of your library, and list those passages so you can check them. ',
    'The model can misread or leave things out: read the sources before acting, especially on medical, legal or safety matters.');
  if (!info.canAsk) {
    main.appendChild(h('section', { class: 'panel' }, h('header', null, h('h2', null, 'Ask your library')),
      h('div', { class: 'body' }, intro, h('div', { class: 'banner info' }, info.reason || 'Question answering is not available.'),
        s.admin ? h('p', null, h('a', { href: '#/settings' }, 'Open Settings → Assistant'), ' · ',
          h('a', { href: '#/discover?category=assistant' }, 'Download a model')) : null)));
    return null;
  }

  const q = h('textarea', { rows: 2, maxlength: 1000, placeholder: 'Ask a question, e.g. "How do I purify water without a filter?"', 'aria-label': 'Question', style: 'width:100%' });
  q.value = params.get('q') || '';
  const useLib = h('input', { type: 'checkbox', checked: true });
  const askBtn = h('button', { class: 'primary', type: 'submit' }, 'Ask');
  const stopBtn = h('button', { type: 'button', hidden: true }, 'Stop');
  const statusLine = h('div', { class: 'small muted', role: 'status', 'aria-live': 'polite' });
  const current = h('div', { class: 'answer' });
  const historyEl = h('div');
  const st = info.status || {};
  const modelNote = st.model ? h('span', { class: 'small muted' }, `Model: ${st.model}${st.backend === 'external' && st.server ? ` on ${st.server}` : ''}`) : null;
  const remoteNote = st.remote ? h('div', { class: 'banner warn' }, `Questions and passages are sent to ${st.server}, which is not on this network.`) : null;

  let controller = null;
  let seq = 0;
  const history = loadHistory();

  function renderHistory() {
    clear(historyEl);
    if (!history.length) return;
    historyEl.appendChild(h('h3', null, 'Earlier in this session'));
    history.forEach((res, i) => {
      historyEl.appendChild(h('details', { class: 'ask-history' },
        h('summary', null, res.question),
        resultBlock(res, `h${i}`)));
    });
  }

  async function ask(ev) {
    ev?.preventDefault();
    const question = q.value.trim();
    if (!question) { q.focus(); return; }
    if (controller) controller.abort();
    controller = new AbortController();
    const mine = ++seq;
    askBtn.disabled = true;
    stopBtn.hidden = false;
    let text = '';
    let passages = [];
    const live = h('div', { class: 'answer-text streaming' });
    fill(current, h('h3', null, question), live);
    statusLine.textContent = 'Starting…';
    const url = `/api/assistant/ask?q=${encodeURIComponent(question)}&retrieval=${useLib.checked ? 1 : 0}`;
    try {
      await streamAsk(url, controller.signal, (type, e) => {
        if (mine !== seq) return;
        if (type === 'status') statusLine.textContent = e.message || e.status;
        else if (type === 'passages') {
          passages = e.passages || [];
          statusLine.textContent = `Found ${passages.length} passage${passages.length === 1 ? '' : 's'}. Waiting for the model…`;
        } else if (type === 'token') {
          text += e.token;
          fill(live, renderAnswer(text, passages, 'live'));
        } else if (type === 'done') {
          const res = e.result;
          statusLine.textContent = '';
          fill(current, h('h3', null, res.question), resultBlock(res, 'cur'));
          history.unshift(res);
          saveHistory(history);
        } else if (type === 'error') {
          statusLine.textContent = '';
          current.appendChild(h('div', { class: 'banner err' }, e.message));
        }
      });
    } catch (err) {
      if (err.name !== 'AbortError') {
        statusLine.textContent = '';
        current.appendChild(h('div', { class: 'banner err' }, err.message));
      } else statusLine.textContent = 'Stopped.';
    } finally {
      if (mine === seq) {
        askBtn.disabled = false;
        stopBtn.hidden = true;
        controller = null;
        renderHistory();
      }
    }
  }

  stopBtn.addEventListener('click', () => controller?.abort());
  q.addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); ask(); } });

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Ask your library'), modelNote),
    h('div', { class: 'body' }, intro, remoteNote,
      h('form', { onsubmit: ask },
        q,
        h('div', { class: 'row', style: 'justify-content:space-between' },
          h('label', { class: 'inline', title: 'Turn off to get a general answer that is not checked against your library.' }, useLib, 'Answer from my library (recommended)'),
          h('span', { class: 'row' }, stopBtn, askBtn))),
      statusLine, current, historyEl)));
  renderHistory();
  if (q.value) ask();
  else q.focus();
  return () => { if (controller) controller.abort(); };
}
