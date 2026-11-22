// Loads every view of the Arcana interface in headless Chromium and fails if
// the page requests anything from a host other than the Arcana server, or
// logs a JavaScript error. Used by the offline acceptance test.
//   node ui-tripwire.js http://127.0.0.1:8470/
const { chromium } = require('playwright');

(async () => {
  const base = new URL(process.argv[2]);
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const external = [];
  const errors = [];
  page.on('request', (r) => {
    const u = new URL(r.url());
    if (u.hostname !== base.hostname && u.protocol !== 'data:') external.push(r.url());
  });
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  const views = ['library', 'discover', 'maps', 'downloads', 'updates', 'server', 'settings', 'library?q=morse', 'read/arcana.sample'];
  for (const v of views) {
    await page.goto(`${base.href}#/${v}`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(400);
  }
  await browser.close();
  if (external.length || errors.length) {
    console.error(JSON.stringify({ external, errors }, null, 2));
    process.exit(1);
  }
  console.log(`checked ${views.length} views: no external requests, no script errors`);
})().catch((e) => { console.error(e.message); process.exit(1); });
