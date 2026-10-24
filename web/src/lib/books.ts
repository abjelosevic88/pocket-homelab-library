// Small helpers for books: covers, authors, formats, statuses and reading time.
import type { Book, ReadStatus } from './types'
export { V1 } from './api'

/** A cover (or author photo) URL at a width; the server keeps resized copies. */
export function coverURL(url: string | undefined, w: number): string {
  if (!url) return ''
  const px = [120, 200, 300, 400, 600, 800].find(x => x >= w * Math.min(2, window.devicePixelRatio || 1)) || 800
  return url + (url.includes('?') ? '&' : '?') + 'w=' + px
}

export function authorsText(b: Pick<Book, 'authors'>, max = 3): string {
  const names = (b.authors || []).map(a => a.name)
  if (names.length <= max) return names.join(', ')
  return names.slice(0, max).join(', ') + ` +${names.length - max}`
}

const FORMAT_LABELS: Record<string, string> = {
  epub: 'EPUB', kepub: 'KEPUB', pdf: 'PDF', mobi: 'MOBI', azw: 'AZW', azw3: 'AZW3', prc: 'PRC', fb2: 'FB2', fbz: 'FB2 (zip)',
  txt: 'Text', rtf: 'RTF', doc: 'Word (doc)', docx: 'Word', djvu: 'DjVu', cbz: 'CBZ', cbr: 'CBR', cb7: 'CB7', cbt: 'CBT',
}
export function formatLabel(f: string): string { return FORMAT_LABELS[f] || f.toUpperCase() }

/** Formats the web reader opens. */
export const READABLE = new Set(['epub', 'kepub', 'mobi', 'azw', 'azw3', 'prc', 'fb2', 'fbz', 'pdf', 'cbz', 'cbr', 'cb7', 'cbt'])

export const STATUS_LABELS: Record<string, string> = { want: 'Want to read', reading: 'Reading', read: 'Read', dnf: 'Didn’t finish', '': 'Not started' }
export function statusLabel(s: ReadStatus | undefined): string { return STATUS_LABELS[s || ''] }

/** 0.4213 → 42. */
export function pct(p: number | undefined): number { return Math.round(Math.max(0, Math.min(1, p || 0)) * 100) }

/** A stable hue (0–359) for a string: placeholders and avatars. */
export function hue(s: string): number { let h = 0; for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0; return Math.abs(h) % 360 }

/** Reading time from words (or pages): "7 h 20 min". */
export function readingTime(b: Pick<Book, 'words' | 'pages'>): string {
  const words = b.words || (b.pages ? b.pages * 275 : 0)
  if (!words) return ''
  const min = Math.round(words / 250)
  return hm(min)
}

export function hm(min: number): string {
  min = Math.max(0, Math.round(min))
  const h = Math.floor(min / 60), m = min % 60
  if (h && m) return `${h} h ${m} min`
  if (h) return `${h} h`
  return `${m} min`
}

/** "#3" / "#3.5" for a series index. */
export function seriesIndex(n: number): string {
  if (!n) return ''
  return '#' + (Number.isInteger(n) ? String(n) : String(Math.round(n * 100) / 100))
}

export function seriesLine(b: Pick<Book, 'series' | 'series_index'>): string {
  if (!b.series) return ''
  return b.series_index ? `${b.series} ${seriesIndex(b.series_index)}` : b.series
}

/** The book's state for the person, never undefined. */
export function stateOf(b: Book) {
  return b.state || { status: '' as ReadStatus, rating: 0, favorite: false, hidden: false, percentage: 0, locator: '', file_id: 0, last_read_at: '', last_device: '', started_at: '', finished_at: '', times_read: 0, updated_at: '' }
}

/** Is it in progress (started, not finished)? */
export function inProgress(b: Book): boolean {
  const s = stateOf(b)
  return s.status === 'reading' || (s.percentage > 0 && s.percentage < 1 && s.status !== 'read' && s.status !== 'dnf')
}

/** A local yyyy-mm-dd for today. */
export function todayISO(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/** Turns an object of query values into a query string (skips empty ones). */
export function qs(o: Record<string, string | number | boolean | undefined | null>): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(o)) if (v !== undefined && v !== null && v !== '' && v !== false) p.set(k, String(v === true ? 1 : v))
  const s = p.toString()
  return s ? '?' + s : ''
}

/** Links for well-known identifiers. */
export function identifierLink(k: string, v: string): string {
  switch (k) {
    case 'isbn': return `https://openlibrary.org/isbn/${encodeURIComponent(v)}`
    case 'goodreads': return `https://www.goodreads.com/book/show/${encodeURIComponent(v)}`
    case 'amazon': case 'asin': case 'mobi-asin': return /^[A-Z0-9]{10}$/.test(v) ? `https://www.amazon.com/dp/${v}` : ''
    case 'google': return `https://books.google.com/books?id=${encodeURIComponent(v)}`
    case 'openlibrary': return v.startsWith('/') ? `https://openlibrary.org${v}` : `https://openlibrary.org/works/${encodeURIComponent(v)}`
    case 'hardcover': return `https://hardcover.app/books/${encodeURIComponent(v)}`
  }
  return ''
}

/** "Chrome on macOS" — names this browser. */
export function deviceName(): string {
  const ua = navigator.userAgent
  const b = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? `${b} on ${os}` : b
}
