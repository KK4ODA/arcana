package server

// frameScript is injected into archive pages served through the content
// origin. It tells the Arcana reader (the parent frame, on the application
// origin) which page is open, so the reader can offer bookmarks, notes and
// history. It sends only the page path and title, and only to Arcana's own
// application origin (content port − 1 on the same host).
var frameScript = []byte(`(function () {
  if (window.parent === window) return;
  var port = parseInt(location.port || '80', 10);
  var appOrigin = location.protocol + '//' + location.hostname + ':' + (port - 1);
  function send() {
    try {
      window.parent.postMessage({ type: 'arcana:location', path: location.pathname, title: document.title }, appOrigin);
    } catch (e) { /* the reader is optional */ }
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', send); else send();
  window.addEventListener('hashchange', send);

  // Translation: the reader asks for the page's paragraphs, translates
  // them on this device and sends them back. Only messages from the
  // reader (the parent frame on the application origin) are accepted, and
  // translated markup is cleaned before it is inserted.
  var SEL = 'p,li,h1,h2,h3,h4,h5,h6,td,th,dt,dd,figcaption,caption,blockquote,summary';
  var SKIP = 'script,style,code,pre,kbd,samp,math,svg,noscript,textarea';
  var els = [], originals = new Map(), run = 0;
  function blocks() {
    var out = [], all = document.body ? document.body.querySelectorAll(SEL) : [];
    for (var i = 0; i < all.length; i++) {
      var el = all[i];
      if (el.closest(SKIP) || el.querySelector(SEL)) continue;
      var t = (el.textContent || '').trim();
      if (!t || t.length > 6000 || !/[A-Za-z\u00C0-\uFFFF]/.test(t)) continue;
      // Letters, codes and symbols ("I", "QRM", "SOS") are not words to
      // translate.
      if (t.length <= 2 || /^[A-Z0-9][A-Z0-9 .,:;\/+()-]{0,7}$/.test(t)) continue;
      out.push(el);
    }
    return out;
  }
  function clean(html) {
    var t = document.createElement('template');
    t.innerHTML = html;
    t.content.querySelectorAll('script,style,iframe,object,embed,link,meta,base,form').forEach(function (n) { n.remove(); });
    t.content.querySelectorAll('*').forEach(function (n) {
      Array.prototype.slice.call(n.attributes).forEach(function (a) {
        if (/^on/i.test(a.name) || (/^(href|src|xlink:href|action|formaction)$/i.test(a.name) && /^\s*javascript:/i.test(a.value))) n.removeAttribute(a.name);
      });
    });
    return t.content;
  }
  function restore() {
    originals.forEach(function (html, el) { el.innerHTML = html; });
    originals.clear();
  }
  window.addEventListener('message', function (e) {
    if (e.source !== window.parent || e.origin !== appOrigin || !e.data) return;
    var d = e.data;
    if (d.type === 'arcana:translate-collect') {
      restore();
      run = d.run;
      els = blocks();
      window.parent.postMessage({ type: 'arcana:translate-blocks', run: run, lang: (document.documentElement.lang || '').toLowerCase(),
        blocks: els.map(function (el) { return el.innerHTML; }) }, appOrigin);
    } else if (d.type === 'arcana:translate-apply' && d.run === run && Array.isArray(d.items)) {
      d.items.forEach(function (it) {
        var el = els[it[0]];
        if (!el || typeof it[1] !== 'string') return;
        if (!originals.has(el)) originals.set(el, el.innerHTML);
        el.replaceChildren(clean(it[1]));
      });
    } else if (d.type === 'arcana:translate-restore') {
      run = 0;
      restore();
    }
  });
})();
`)
