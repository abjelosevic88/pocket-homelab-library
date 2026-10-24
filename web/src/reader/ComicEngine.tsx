// Comics and manga page by page (or as one long strip): pages come from the
// server (CBZ, CBR, CB7, CBT alike), scaled to the screen, the next few
// fetched ahead. Manga read right to left.
import { useEffect, useMemo, useRef, useState } from 'react'
import type { Annotation } from '../lib/types'
import { delegate, type EngineApi, type EngineProps } from './engine'
import { parseLocator } from './locator'

interface PageInfo { n: number; name: string; size: number }

/** The screen width a page is fetched for, in device pixels (the server snaps it). */
function fetchWidth(perScreen: number) {
  const px = Math.round((window.innerWidth * Math.min(window.devicePixelRatio || 1, 2)) / perScreen)
  return Math.min(px, 1600)
}

export default function ComicEngine(p: EngineProps) {
  const props = useRef(p)
  props.current = p
  const [pages, setPages] = useState<PageInfo[] | null>(null)
  const [index, setIndex] = useState(0) // 0-based first page shown
  const strip = useRef<HTMLDivElement>(null)
  const apiRef = useRef<EngineApi>(null!)
  const ready = useRef(false)
  const wide = useRef<Set<number>>(new Set()) // landscape pages (spreads) stand alone
  const [wideVer, setWideVer] = useState(0)
  const { mode, fit } = p.comicPrefs
  const rtl = p.comicPrefs.direction === 'rtl' || (p.comicPrefs.direction === 'auto' && (p.data.book.reading_direction === 'rtl' || p.data.book.kind === 'manga'))
  const count = pages?.length ?? 0

  useEffect(() => {
    let cancelled = false
    fetch(`/api/v1/files/${p.file.id}/pages`, { credentials: 'same-origin' })
      .then(r => r.ok ? r.json() : Promise.reject(new Error(`Could not open the comic (${r.status}).`)))
      .then((j: { pages: PageInfo[] }) => {
        if (cancelled) return
        const { data } = props.current
        const l = parseLocator(data.locator)
        const sameFile = !data.state?.file_id || data.state.file_id === p.file.id
        let start = 0
        if (sameFile && typeof l.page === 'number' && l.page > 0) start = l.page - 1
        else if (data.percentage > 0 && data.percentage < 1) start = Math.floor(data.percentage * j.pages.length)
        setPages(j.pages)
        setIndex(Math.max(0, Math.min(j.pages.length - 1, start)))
      })
      .catch(e => { if (!cancelled) props.current.onError((e as Error).message) })
    return () => { cancelled = true }
  }, [p.file.id])

  const pageURL = (n: number, per = 1) => `/api/v1/files/${p.file.id}/pages/${n}?w=${fetchWidth(per)}`

  // spreads: the cover alone, then pairs; a wide page always alone
  const spread = (i: number): number[] => {
    if (mode !== 'double' || i === 0 || wide.current.has(i) || i + 1 >= count || wide.current.has(i + 1)) return [i]
    return [i, i + 1]
  }
  const shown = useMemo(() => (pages ? spread(index) : []), [pages, index, mode, wideVer]) // eslint-disable-line react-hooks/exhaustive-deps

  const report = (i: number, turned: boolean) => {
    if (!count) return
    const last = Math.max(...(mode === 'double' ? spread(i) : [i]))
    props.current.onRelocate({
      percentage: (last + 1) / count, locator: { type: 'comic', page: i + 1 }, chapter: '',
      page: mode === 'double' && last !== i ? `${i + 1}–${last + 1} / ${count}` : `${i + 1} / ${count}`, atEnd: last >= count - 1, turned,
    })
  }

  useEffect(() => {
    if (!pages || mode === 'scroll') return
    report(index, ready.current)
    // fetch the next pages ahead (and one behind)
    for (const d of [1, 2, 3, 4, -1]) {
      const n = index + d + 1
      if (n >= 1 && n <= pages.length) { const img = new Image(); img.src = pageURL(n, mode === 'double' ? 2 : 1) }
    }
    if (!ready.current) { ready.current = true; props.current.onReady(delegate(apiRef)) }
  }, [pages, index, mode]) // eslint-disable-line react-hooks/exhaustive-deps

  // scroll mode: jump to the page and follow which one is in view
  useEffect(() => {
    if (!pages || mode !== 'scroll') return
    const el = strip.current
    if (!el) return
    el.querySelector<HTMLElement>(`[data-n="${index + 1}"]`)?.scrollIntoView({ block: 'start' })
    if (!ready.current) { ready.current = true; props.current.onReady(delegate(apiRef)) }
    report(index, false)
    let t = 0
    const onScroll = () => {
      props.current.onActivity()
      window.clearTimeout(t)
      t = window.setTimeout(() => {
        const mid = el.scrollTop + el.clientHeight * 0.3
        let n = 1
        for (const img of el.querySelectorAll<HTMLElement>('[data-n]')) { if (img.offsetTop <= mid) n = Number(img.dataset.n); else break }
        report(n - 1, true)
      }, 150)
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [pages, mode]) // eslint-disable-line react-hooks/exhaustive-deps

  const step = (dir: 1 | -1) => {
    if (!count) return
    if (mode === 'scroll') { strip.current?.scrollBy({ top: dir * (strip.current.clientHeight * 0.85), behavior: 'smooth' }); return }
    setIndex(i => {
      if (dir > 0) { const s = spread(i); return Math.min(count - 1, s[s.length - 1] + 1) }
      if (i <= 0) return 0
      // back to the start of the previous spread
      const prev = i - 1
      if (mode === 'double' && prev > 0 && !wide.current.has(prev) && !wide.current.has(prev - 1) && spread(prev - 1).length === 2) return prev - 1
      return prev
    })
  }

  const go = (n: number) => {
    const i = Math.max(0, Math.min(count - 1, n - 1))
    setIndex(i)
    if (mode === 'scroll') strip.current?.querySelector<HTMLElement>(`[data-n="${i + 1}"]`)?.scrollIntoView({ block: 'start' })
  }

  apiRef.current = {
    next: () => step(1), prev: () => step(-1),
    left: () => step(rtl ? 1 : -1), right: () => step(rtl ? -1 : 1),
    goToFraction: f => go(Math.round(f * count) || 1),
    goTo: t => go(typeof t === 'number' ? t : Number(t)),
    goToAnnotation: (a: Annotation) => { const l = parseLocator(a.locator); go(typeof l.page === 'number' ? l.page : a.page || 1) },
    toc: () => [],
    sectionFractions: () => [],
    bookmarkHere: list => list.find(a => a.kind === 'bookmark' && shown.includes((parseLocator(a.locator).page ?? a.page) - 1)),
  }

  const onTap = (e: React.MouseEvent) => {
    const w = window.innerWidth, x = e.clientX
    const zone = x < w * 0.3 ? 'left' : x > w * 0.7 ? 'right' : 'center'
    props.current.onActivity()
    props.current.onTap(zone)
  }

  // swipes turn pages (single and double)
  const touch = useRef<{ x: number; y: number; t: number } | null>(null)
  const onPointerDown = (e: React.PointerEvent) => { touch.current = { x: e.clientX, y: e.clientY, t: Date.now() } }
  const swiped = useRef(false)
  const onPointerUp = (e: React.PointerEvent) => {
    const s = touch.current
    touch.current = null
    swiped.current = false
    if (!s || mode === 'scroll') return
    const dx = e.clientX - s.x, dy = e.clientY - s.y
    if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5 && Date.now() - s.t < 800) {
      swiped.current = true
      // swiping left shows what is to the right
      if (dx < 0) apiRef.current.right(); else apiRef.current.left()
    }
  }

  if (!pages) return <div className="rd-comic" />
  if (mode === 'scroll') {
    return (
      <div className="rd-comic rd-comic-scroll" ref={strip} onClick={onTap}>
        {pages.map(pg => <img key={pg.n} data-n={pg.n} src={pageURL(pg.n)} alt={`Page ${pg.n}`} loading="lazy" decoding="async" className={`rd-comic-strip fit-${fit === 'height' ? 'width' : fit}`} />)}
      </div>
    )
  }
  const order = rtl ? [...shown].reverse() : shown
  return (
    <div className={`rd-comic rd-comic-${mode} fit-${fit}`} onClick={e => { if (!swiped.current) onTap(e) }} onPointerDown={onPointerDown} onPointerUp={onPointerUp}>
      <div className={`rd-comic-spread ${shown.length === 2 ? 'two' : 'one'}`}>
        {order.map(i => (
          <img key={i} src={pageURL(i + 1, shown.length)} alt={`Page ${i + 1}`} draggable={false}
            onLoad={e => {
              const im = e.currentTarget
              if (im.naturalWidth > im.naturalHeight * 1.1 && !wide.current.has(i)) { wide.current.add(i); setWideVer(v => v + 1) }
            }} />
        ))}
      </div>
    </div>
  )
}
