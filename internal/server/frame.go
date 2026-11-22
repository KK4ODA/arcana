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
})();
`)
