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
  const views = ['library', 'discover', 'maps', 'downloads', 'updates', 'server', 'settings', 'classroom', 'library?q=morse', 'read/arcana.sample'];
  for (const v of views) {
    await page.goto(`${base.href}#/${v}`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(400);
  }
  // On a phone: each view loaded at that width, with its folded sections
  // opened, must fit it.
  await page.setViewportSize({ width: 390, height: 844 });
  for (const v of views) {
    await page.goto(`${base.href}#/${v}`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(400);
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
