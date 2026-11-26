import { api, contentURL } from '../api.js';
import { h, fill, licenseBadge } from '../util.js';

// The film player: the video from the content origin, with its edition,
// licence, source and any content notes. Where each viewer stopped is kept
// in this browser only (it is a convenience, not library data), so every
// phone on the network resumes its own place.

const KEY = (id) => `arcana-watch:${id}`;

export function savedPosition(id) {
  try { return Number(localStorage.getItem(KEY(id))) || 0; } catch (_) { return 0; }
}

function savePosition(id, t) {
  try {
    if (t > 0) localStorage.setItem(KEY(id), String(Math.floor(t)));
    else localStorage.removeItem(KEY(id));
  } catch (_) { /* storage unavailable: nothing to resume later */ }
}

export function fmtClock(sec) {
  const s = Math.max(0, Math.floor(sec));
  const hh = Math.floor(s / 3600);
  const mm = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, '0');
  return hh ? `${hh}:${String(mm).padStart(2, '0')}:${ss}` : `${mm}:${ss}`;
}

export async function watchView(main, id) {
  const lib = await api.get('/api/library');
  const r = lib.resources.find((x) => x.resourceId === id);
  if (!r || !r.openPath) {
    main.appendChild(h('div', { class: 'banner err' }, 'This film is not on this drive.'),
      h('a', { class: 'button', href: '#/entertainment' }, '← Entertainment'));
    return null;
  }
  document.title = `${r.title} — Arcana`;
  const src = contentURL(r.openPath);
  const video = h('video', { controls: true, preload: 'metadata', playsinline: true, src, 'aria-label': r.title });
  const resume = h('div', { class: 'row resume', hidden: true });
  const problem = h('div', { class: 'banner err', hidden: true, role: 'alert' });

  const at = savedPosition(id);
  video.addEventListener('loadedmetadata', () => {
    if (at > 30 && (!video.duration || at < video.duration - 60)) {
      fill(resume, h('span', { class: 'small' }, `You stopped at ${fmtClock(at)}.`),
        h('button', { class: 'primary', onclick: () => { video.currentTime = at; resume.hidden = true; video.play().catch(() => {}); } }, `Continue from ${fmtClock(at)}`),
        h('button', { onclick: () => { resume.hidden = true; savePosition(id, 0); video.play().catch(() => {}); } }, 'Start from the beginning'));
      resume.hidden = false;
    }
  });
  let lastSaved = 0;
  video.addEventListener('timeupdate', () => {
    if (Math.abs(video.currentTime - lastSaved) >= 5) { lastSaved = video.currentTime; savePosition(id, video.currentTime); }
  });
  video.addEventListener('play', () => { resume.hidden = true; });
  video.addEventListener('ended', () => savePosition(id, 0));
  video.addEventListener('error', () => {
    problem.hidden = false;
    fill(problem, 'This browser cannot play the film. ',
      video.error && video.error.code === 4 ? 'Its format (MP4 with H.264 video) is not supported here; most current browsers play it. ' : 'The file may be missing; run a library check on the computer running Arcana. ',
      h('a', { href: src, target: '_blank', rel: 'noopener' }, 'Open the file directly'));
  });

  const lic = r.license || {};
  main.appendChild(h('section', { class: 'panel watch' },
    h('header', null, h('a', { class: 'button', href: '#/entertainment' }, '← Entertainment'), h('h2', null, r.title),
      licenseBadge(r.license)),
    (r.warnings || []).map((w) => h('div', { class: 'notice' }, w.text)),
    h('div', { class: 'player' }, video),
    resume, problem,
    h('div', { class: 'body' },
      h('p', null, r.description),
      h('p', { class: 'small muted' }, [r.publisher, r.sourceUrl ? `source: ${r.sourceUrl.replace(/^https?:\/\//, '')}` : null].filter(Boolean).join(' · ')),
      lic.notes ? h('p', { class: 'small muted' }, lic.notes.startsWith(lic.name || lic.id) ? lic.notes : `${lic.name || lic.id}. ${lic.notes}`) : null,
      h('p', { class: 'small muted' }, r.verification ? `Download check: ${r.verification}.` : null))));
  return () => { savePosition(id, video.ended ? 0 : video.currentTime || savedPosition(id)); video.pause(); video.removeAttribute('src'); video.load(); };
}
