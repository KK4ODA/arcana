// Loads every view of the Arcana interface in headless Chromium and fails if
// the page requests anything from a host other than the Arcana server, logs
// a JavaScript error, or is wider than a phone's screen (390 px), which makes
// the whole page scroll sideways. Used by the offline acceptance test.
//   node ui-tripwire.js http://127.0.0.1:8470/
const { chromium } = require('playwright');

(async () => {
  const base = new URL(process.argv[2]);
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const external = [];
  const errors = [];
  const wide = [];
  page.on('request', (r) => {
    const u = new URL(r.url());
    if (u.hostname !== base.hostname && u.protocol !== 'data:') external.push(r.url());
  });
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });

  // A view has settled when no request but an event stream has been open
  // for 500 ms. Playwright's 'networkidle' counts event streams too, and the
  // GPS stream stays open while a receiver is connected (the acceptance test
  // runs a simulated one), so 'networkidle' would never come.
  const inflight = new Set();
  page.on('request', (r) => { if (r.resourceType() !== 'eventsource') inflight.add(r); });
  page.on('requestfinished', (r) => inflight.delete(r));
  page.on('requestfailed', (r) => inflight.delete(r));
  const open = async (v) => {
    await page.goto(`${base.href}#/${v}`);
    const deadline = Date.now() + 30000;
    let quiet = Date.now();
    while (Date.now() - quiet < 500) {
      if (Date.now() > deadline) throw new Error(`#/${v} kept loading: ${[...inflight].map((r) => r.url()).join(', ')}`);
      if (inflight.size) quiet = Date.now();
      await page.waitForTimeout(50);
    }
    await page.waitForTimeout(400);
  };

  const views = ['library', 'discover', 'maps', 'downloads', 'updates', 'server', 'settings', 'classroom', 'library?q=morse', 'read/arcana.sample',
    'entertainment', 'play/chess', 'play/checkers', 'play/backgammon', 'play/reversi', 'play/fourinarow', 'play/mancala', 'play/go'];
  for (const v of views) await open(v);
  // On a phone: each view loaded at that width, with its folded sections
  // opened, must fit it.
  await page.setViewportSize({ width: 390, height: 844 });
  for (const v of views) {
    await open(v);
    const over = await page.evaluate(() => {
      for (const d of document.querySelectorAll('details')) d.open = true;
      const w = document.documentElement.clientWidth;
      if (document.documentElement.scrollWidth <= w) return null;
      const el = [...document.querySelectorAll('body *')].find((e) => e.getBoundingClientRect().right > w + 1 && getComputedStyle(e).position !== 'fixed');
      return { width: document.documentElement.scrollWidth, widest: el ? `${el.tagName.toLowerCase()} "${(el.textContent || '').trim().slice(0, 60)}"` : '' };
    });
    if (over) wide.push({ view: v, ...over });
  }
  await browser.close();
  if (external.length || errors.length || wide.length) {
    console.error(JSON.stringify({ external, errors, wide }, null, 2));
    process.exit(1);
  }
  console.log(`checked ${views.length} views: no external requests, no script errors, all fit a 390 px screen`);
})().catch((e) => { console.error(e.message); process.exit(1); });
