// README screenshots and a browser smoke test, against the demo library (scripts/demo/make.sh: public-domain
// books from Project Gutenberg; scripts/demo/seed.py: a reader with history, highlights, shelves and devices):
//
//   scripts/demo/make.sh
//   D=~/.cache/pocket-library-demo; DATA_DIR=$D/data ADMIN_EMAIL=ana@example.com ADMIN_PASSWORD=demo-password \
//     ADMIN_NAME="Ana Petrović" LIBRARIES="Books=$D/books" PORT=18310 ./pocket-library
//   scripts/demo/seed.py http://127.0.0.1:18310 $D/data/pocket-library.db
//   CDP=http://127.0.0.1:19261 BASE=http://127.0.0.1:18310 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                  # checks only
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18310'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'ana@example.com', password: 'demo-password' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
async function go(page, path, wait = 1800) {
  await page.goto(base + path, { waitUntil: 'networkidle' })
  // headless Chrome freezes CSS fade-ins: show everything at once
  await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; }' })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }
const api = (page, path) => page.evaluate(p => fetch('/api/v1' + p).then(r => r.json()), path)
async function bookId(page, title) {
  const r = await api(page, `/search?q=${encodeURIComponent(title)}`)
  const b = r.books.find(x => x.title.startsWith(title))
  assert.ok(b, `${title} is in the demo library`)
  return b.id
}

// ---- desktop ----
{
  const { ctx, page } = await session()
  await go(page, '/')
  assert.ok((await page.content()).includes('Wuthering Heights'), 'Continue reading on Home')
  await shot(page, 'desktop-home')

  await go(page, '/library?sort=title')
  await shot(page, 'desktop-library')

  const wh = await bookId(page, 'Wuthering Heights')
  await go(page, `/book/${wh}`)
  await shot(page, 'desktop-book')

  await go(page, `/read/${wh}`, 5000)
  assert.ok(await page.locator('foliate-view').count() === 1, 'the EPUB reader opened')
  await shot(page, 'desktop-reader')

  await go(page, '/shelves')
  await shot(page, 'desktop-shelves')

  await go(page, '/highlights')
  await shot(page, 'desktop-highlights')

  await go(page, '/stats?range=90d')
  await shot(page, 'desktop-stats')

  await go(page, '/devices')
  await shot(page, 'desktop-devices')

  await go(page, '/admin/libraries')
  await shot(page, 'desktop-admin')
  await ctx.close()
}

// ---- dark ----
{
  const { ctx, page } = await session({ dark: true })
  const dorian = await bookId(page, 'The Picture of Dorian Gray')
  await go(page, `/book/${dorian}`)
  await shot(page, 'desktop-book-dark')
  await ctx.close()
}

// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/')
  await shot(page, 'phone-home')
  const wh = await bookId(page, 'Wuthering Heights')
  await go(page, `/read/${wh}`, 5000)
  await shot(page, 'phone-reader')
  await ctx.close()
}
{
  const { ctx, page } = await session({ phone: true, dark: true })
  const fr = await bookId(page, 'Frankenstein')
  await go(page, `/book/${fr}`)
  await shot(page, 'phone-book-dark')
  await ctx.close()
}

await browser.close()
if (errors.length) {
  console.error('browser errors:\n' + errors.join('\n'))
  process.exit(1)
}
console.log('ok')
