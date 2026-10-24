// Sends the reading position to the server: shortly after it moves, every
// half minute while someone is reading, and with sendBeacon when the page
// goes away. Only active time counts — the tab visible and a page turned or
// something touched in the last two minutes.

import { V1 } from '../lib/api'

const IDLE_AFTER = 120_000
const EVERY = 30_000
const DEBOUNCE = 2_000

export interface Position { percentage: number; locator: object }

/** "Safari on iPhone", "Chrome on Mac" — how this browser shows up in history. */
export function deviceName(): string {
  const ua = navigator.userAgent
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1) ? 'iPad'
    : /Android/.test(ua) ? 'Android' : /Mac OS X|Macintosh/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  const br = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /CriOS|Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  return os ? `${br} on ${os}` : br
}

export class ProgressReporter {
  private pos: Position | null = null
  private sentKey = ''
  private seconds = 0
  private pages = 0
  private lastActive = Date.now()
  private timer = 0
  private ticker = 0
  private clock = 0
  private stopped = false
  private readonly device = deviceName()

  constructor(private bookId: number, private fileId: number, private onSaved?: (stale: boolean) => void) {
    this.clock = window.setInterval(() => {
      if (document.visibilityState === 'visible' && Date.now() - this.lastActive < IDLE_AFTER) this.seconds++
    }, 1000)
    this.ticker = window.setInterval(() => { if (this.seconds > 0 || this.dirty()) void this.send() }, EVERY)
    document.addEventListener('visibilitychange', this.onHide)
    window.addEventListener('pagehide', this.onHide)
  }

  /** Someone did something (a key, a tap, a scroll). */
  active() { this.lastActive = Date.now() }

  /** The position moved; turned says a page was turned to get there. */
  move(p: Position, turned = true) {
    this.pos = p
    this.active()
    if (turned) this.pages++
    window.clearTimeout(this.timer)
    this.timer = window.setTimeout(() => void this.send(), DEBOUNCE)
  }

  private dirty() { return !!this.pos && JSON.stringify(this.pos) !== this.sentKey }

  private body() {
    const body = { file_id: this.fileId, percentage: this.pos?.percentage ?? 0, locator: this.pos?.locator ?? null, seconds: Math.min(this.seconds, 120), pages: this.pages, device: this.device }
    return body
  }

  async send(): Promise<void> {
    if (!this.pos || this.stopped) return
    const body = this.body()
    const key = JSON.stringify(this.pos)
    this.seconds = 0
    this.pages = 0
    this.sentKey = key
    try {
      const res = await fetch(`${V1}/books/${this.bookId}/progress`, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
      if (res.ok) {
        const j = await res.json().catch(() => ({}))
        this.onSaved?.(!!j.stale)
      }
    } catch { this.sentKey = '' }
  }

  private onHide = (e: Event) => {
    if (e.type === 'visibilitychange' && document.visibilityState === 'visible') return
    this.beacon()
  }

  /** Last word when the page closes or hides. */
  beacon() {
    if (!this.pos || (!this.dirty() && this.seconds === 0)) return
    const body = JSON.stringify(this.body())
    this.sentKey = JSON.stringify(this.pos)
    this.seconds = 0
    this.pages = 0
    const blob = new Blob([body], { type: 'text/plain;charset=UTF-8' })
    if (!navigator.sendBeacon?.(`${V1}/books/${this.bookId}/progress`, blob)) {
      void fetch(`${V1}/books/${this.bookId}/progress`, { method: 'POST', credentials: 'same-origin', keepalive: true, body })
    }
  }

  stop() {
    this.beacon()
    this.stopped = true
    window.clearTimeout(this.timer)
    window.clearInterval(this.ticker)
    window.clearInterval(this.clock)
    document.removeEventListener('visibilitychange', this.onHide)
    window.removeEventListener('pagehide', this.onHide)
  }
}
