// Search helpers shared by the library and the reader: a search box that
// completes article titles as you type, and safe rendering of results.
import { h, clear, fill, debounce } from './util.js';

let boxSeq = 0;

// titleSearchBox wraps an <input type="search"> with a list of title
// matches that appears while typing.
//   fetchTitles(q) -> Promise<[{ title, book, ... }]>
//   onPick(item)   -> called when a title is chosen (click or Enter on it)
//   onSubmit(q)    -> called on Enter without a chosen title
// Returns the wrapper element to insert into the page.
export function titleSearchBox(input, { fetchTitles, onPick, onSubmit, showBook = false }) {
  const id = `titles-${++boxSeq}`;
  const list = h('ul', { class: 'title-suggest', role: 'listbox', id, hidden: true });
  const wrap = h('div', { class: 'title-search' }, input, list);
  input.setAttribute('role', 'combobox');
  input.setAttribute('aria-autocomplete', 'list');
  input.setAttribute('aria-controls', id);
  input.setAttribute('aria-expanded', 'false');
  input.autocomplete = 'off';
  let items = [];
  let active = -1;
  let seq = 0;
  // The text last submitted with Enter: suggestions for it are not wanted,
  // even if a request for them was still waiting to be sent.
  let submitted = null;

  function close() {
    list.hidden = true;
    input.setAttribute('aria-expanded', 'false');
    input.removeAttribute('aria-activedescendant');
    active = -1;
  }

  function render(q) {
    clear(list);
    items.forEach((it, i) => {
      const li = h('li', { role: 'option', id: `${id}-${i}`, 'aria-selected': i === active ? 'true' : 'false' },
        h('span', { class: 't' }, highlight(it.title, q)),
        showBook && it.book ? h('span', { class: 'b' }, it.book) : null);
      // mousedown, not click: the input's blur would close the list first.
      li.addEventListener('mousedown', (e) => { e.preventDefault(); pick(i); });
      list.appendChild(li);
    });
    list.hidden = !items.length;
    input.setAttribute('aria-expanded', items.length ? 'true' : 'false');
    if (active >= 0) input.setAttribute('aria-activedescendant', `${id}-${active}`);
    else input.removeAttribute('aria-activedescendant');
  }

  function pick(i) {
    const it = items[i];
    close();
    if (it) onPick(it);
  }

  const update = debounce(async () => {
    const q = input.value.trim();
    const mine = ++seq;
    if (q.length < 2 || q === submitted) { items = []; close(); return; }
    let found = [];
    try { found = await fetchTitles(q); } catch (_) { found = []; }
    if (mine !== seq || document.activeElement !== input) return;
    items = found;
    active = -1;
    render(q);
  }, 150);

  input.addEventListener('input', () => { submitted = null; update(); });
  input.addEventListener('focus', () => { if (items.length && input.value.trim().length >= 2 && input.value.trim() !== submitted) render(input.value.trim()); });
  input.addEventListener('blur', () => setTimeout(close, 100));
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      if (!items.length) return;
      e.preventDefault();
      if (list.hidden) { render(input.value.trim()); return; }
      active = e.key === 'ArrowDown' ? (active + 1) % items.length : (active <= 0 ? items.length - 1 : active - 1);
      render(input.value.trim());
    } else if (e.key === 'Enter') {
      e.preventDefault();
      seq++; // results still on their way must not reopen the list
      submitted = input.value.trim();
      if (active >= 0 && !list.hidden) pick(active);
      else { close(); if (submitted) onSubmit(submitted); }
    } else if (e.key === 'Escape' && !list.hidden) {
      e.preventDefault();
      e.stopPropagation();
      close();
    }
  });
  return wrap;
}

// highlight returns text with every occurrence of the query's words wrapped
// in <mark>, built from text nodes only.
export function highlight(text, q) {
  const words = String(q || '').toLowerCase().split(/\s+/).filter((w) => w.length > 1);
  if (!words.length) return [text];
  const lower = text.toLowerCase();
  const marks = [];
  for (const w of words) {
    let i = lower.indexOf(w);
    while (i >= 0) { marks.push([i, i + w.length]); i = lower.indexOf(w, i + w.length); }
  }
  if (!marks.length) return [text];
  marks.sort((a, b) => a[0] - b[0]);
  const out = [];
  let pos = 0;
  for (const [a, b] of marks) {
    if (a < pos) continue;
    if (a > pos) out.push(text.slice(pos, a));
    out.push(h('mark', null, text.slice(a, b)));
    pos = b;
  }
  if (pos < text.length) out.push(text.slice(pos));
  return out;
}

// snippet renders a server-sanitized search snippet, in which only <b>
// highlighting remains (see kiwix.SanitizeSnippet and docs.highlight).
export function snippet(html) {
  const el = h('div', { class: 'snippet' });
  el.innerHTML = html;
  return el;
}

// setMore puts a "Show more" button in container that calls load(); it is
// left empty when nothing remains.
export function setMore(container, remaining, label, load) {
  if (remaining <= 0) { clear(container); return; }
  const b = h('button', null, label);
  b.addEventListener('click', async () => {
    b.disabled = true;
    b.textContent = 'Loading…';
    try { await load(); } catch (e) { b.disabled = false; b.textContent = label; }
  });
  fill(container, b);
}
