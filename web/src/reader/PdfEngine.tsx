// PDFs through pdf.js: pages in one scrolling column, drawn as they come
// near the screen and let go when far away, fit to width by default.
import { useEffect, useRef, useState } from 'react'
// the legacy build: the modern one needs URL.parse (iOS 18+, Chrome 126+)
import * as pdfjs from 'pdfjs-dist/legacy/build/pdf.mjs'
import type { PDFDocumentProxy, PDFPageProxy } from 'pdfjs-dist'
import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?url'
import type { Annotation } from '../lib/types'
import { delegate, type EngineApi, type EngineProps, type TocItem } from './engine'
import { parseLocator } from './locator'

pdfjs.GlobalWorkerOptions.workerSrc = workerUrl

export type PdfZoom = 'width' | 'page' | number

interface PageSize { w: number; h: number }

export default function PdfEngine(p: EngineProps & { zoom: PdfZoom }) {
  const scroller = useRef<HTMLDivElement>(null)
  const props = useRef(p)
  props.current = p
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null)
  const [sizes, setSizes] = useState<PageSize[]>([])
  const [width, setWidth] = useState(0)
  const [height, setHeight] = useState(0)
  const current = useRef(1)
  const toc = useRef<TocItem[]>([])
  const first = useRef(true)
  const apiRef = useRef<EngineApi>(null!)

  useEffect(() => {
    const el = scroller.current!
    const ro = new ResizeObserver(() => { setWidth(el.clientWidth); setHeight(el.clientHeight) })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  useEffect(() => {
    let cancelled = false
    const task = pdfjs.getDocument({ url: `/api/v1/files/${p.file.id}/content`, withCredentials: true, disableAutoFetch: true, disableStream: false })
    task.promise.then(async d => {
      if (cancelled) return
      // page sizes: the first page's for all, then each page's own as it loads
      const one = await d.getPage(1)
      const v = one.getViewport({ scale: 1 })
      setSizes(Array.from({ length: d.numPages }, () => ({ w: v.width, h: v.height })))
      setDoc(d)
      toc.current = await outline(d).catch(() => [])
    }).catch(e => { if (!cancelled) props.current.onError(e?.message || 'Could not open the PDF.') })
    return () => { cancelled = true; void task.destroy() }
  }, [p.file.id])

  const scaleFor = (s: PageSize) => {
    const avail = Math.max(200, width - 24)
    // fit to width, but not wider than a comfortable page on a big screen
    if (p.zoom === 'width') return Math.min(Math.min(avail, 1100) / s.w, 3)
    if (p.zoom === 'page') return Math.min(avail / s.w, (height - 24) / s.h)
    return p.zoom
  }

  // the first page shown: where the person was
  useEffect(() => {
    if (!doc || !width || !sizes.length) return
    if (!first.current) return
    const { data } = props.current
    const l = parseLocator(data.locator)
    const sameFile = !data.state?.file_id || data.state.file_id === p.file.id
    let page = 1
    if (sameFile && typeof l.page === 'number' && l.page > 0) page = l.page
    else if (data.percentage > 0) page = Math.max(1, Math.round(data.percentage * doc.numPages))
    requestAnimationFrame(() => {
      goPage(Math.min(page, doc.numPages), false)
      first.current = false
      props.current.onReady(delegate(apiRef))
      report(false)
    })
  }, [doc, width, sizes.length]) // eslint-disable-line react-hooks/exhaustive-deps

  const pageTop = (n: number) => {
    const el = scroller.current?.querySelector<HTMLElement>(`[data-page="${n}"]`)
    return el ? el.offsetTop - 8 : 0
  }
  const goPage = (n: number, smooth = true) => {
    const el = scroller.current
    if (!el || !doc) return
    n = Math.max(1, Math.min(doc.numPages, n))
    el.scrollTo({ top: pageTop(n), behavior: smooth ? 'smooth' : 'auto' })
  }

  const report = (turned: boolean) => {
    if (!doc) return
    const n = current.current
    props.current.onRelocate({
      percentage: doc.numPages ? n / doc.numPages : 0,
      locator: { type: 'pdf', page: n },
      chapter: chapterOf(toc.current, n), page: `${n} / ${doc.numPages}`,
      atEnd: n >= doc.numPages, turned,
    })
  }

  // which page is in view
  useEffect(() => {
    const el = scroller.current
    if (!el || !doc) return
    let t = 0
    const onScroll = () => {
      props.current.onActivity()
      window.clearTimeout(t)
      t = window.setTimeout(() => {
        const mid = el.scrollTop + el.clientHeight * 0.35
        let n = 1
        for (const pg of el.querySelectorAll<HTMLElement>('[data-page]')) {
          if (pg.offsetTop <= mid) n = Number(pg.dataset.page)
          else break
        }
        if (n !== current.current) { current.current = n; report(true) }
      }, 120)
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [doc]) // eslint-disable-line react-hooks/exhaustive-deps

  const pageStep = (dir: 1 | -1) => {
    const el = scroller.current
    if (!el) return
    // a screen at a time, or to the next page when it fits
    const fits = sizes[0] && sizes[0].h * scaleFor(sizes[0]) <= el.clientHeight
    if (fits) goPage(current.current + dir)
    else el.scrollBy({ top: dir * el.clientHeight * 0.9, behavior: 'smooth' })
  }

  const api: EngineApi = {
    next: () => pageStep(1), prev: () => pageStep(-1), left: () => pageStep(-1), right: () => pageStep(1),
    goToFraction: f => goPage(Math.round(f * (doc?.numPages ?? 1)) || 1),
    goTo: t => goPage(typeof t === 'number' ? t : Number(t)),
    goToAnnotation: (a: Annotation) => { const l = parseLocator(a.locator); goPage(typeof l.page === 'number' ? l.page : a.page || 1) },
    toc: () => toc.current,
    sectionFractions: () => [],
    search: (q, onHit, onDone) => {
      let stop = false
      ;(async () => {
        const needle = q.toLocaleLowerCase()
        for (let i = 1; doc && i <= doc.numPages && !stop; i++) {
          const pg = await doc.getPage(i)
          const tc = await pg.getTextContent()
          const text = tc.items.map(it => ('str' in it ? it.str : '')).join(' ')
          const low = text.toLocaleLowerCase()
          let at = low.indexOf(needle)
          let hits = 0
          while (at >= 0 && hits < 20) {
            onHit({ label: `Page ${i}`, pre: text.slice(Math.max(0, at - 40), at), match: text.slice(at, at + q.length), post: text.slice(at + q.length, at + q.length + 60), target: i })
            at = low.indexOf(needle, at + needle.length)
            hits++
          }
        }
      })().catch(() => {}).finally(() => { if (!stop) onDone() })
      return () => { stop = true }
    },
    bookmarkHere: list => list.find(a => a.kind === 'bookmark' && (parseLocator(a.locator).page === current.current || a.page === current.current)),
  }
  apiRef.current = api

  return (
    <div className="rd-pdf" ref={scroller} onClick={e => {
      const w = window.innerWidth, x = e.clientX
      props.current.onTap(x < w * 0.2 ? 'left' : x > w * 0.8 ? 'right' : 'center')
    }}>
      {doc && sizes.map((s, i) => <PdfPage key={i} doc={doc} n={i + 1} size={s} scale={scaleFor(s)} root={scroller} onSize={sz => setSizes(list => { if (list[i]?.w === sz.w && list[i]?.h === sz.h) return list; const c = [...list]; c[i] = sz; return c })} />)}
    </div>
  )
}

/** One page: a placeholder of the right size, drawn when near the screen. */
function PdfPage({ doc, n, size, scale, root, onSize }: { doc: PDFDocumentProxy; n: number; size: PageSize; scale: number; root: React.RefObject<HTMLDivElement | null>; onSize: (s: PageSize) => void }) {
  const box = useRef<HTMLDivElement>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const [near, setNear] = useState(false)
  useEffect(() => {
    const io = new IntersectionObserver(([e]) => setNear(e.isIntersecting), { root: root.current, rootMargin: '150% 0px' })
    if (box.current) io.observe(box.current)
    return () => io.disconnect()
  }, [root])
  useEffect(() => {
    if (!near) return
    let cancelled = false
    let task: ReturnType<PDFPageProxy['render']> | null = null
    doc.getPage(n).then(page => {
      if (cancelled) return
      const base = page.getViewport({ scale: 1 })
      if (base.width !== size.w || base.height !== size.h) onSize({ w: base.width, h: base.height })
      const ratio = Math.min(window.devicePixelRatio || 1, 2.5)
      const vp = page.getViewport({ scale: scale * ratio })
      const c = canvas.current
      if (!c) return
      c.width = Math.floor(vp.width)
      c.height = Math.floor(vp.height)
      task = page.render({ canvas: c, canvasContext: c.getContext('2d')!, viewport: vp })
      task.promise.catch(() => {})
    }).catch(() => {})
    return () => { cancelled = true; task?.cancel() }
  }, [near, scale, n, doc]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div ref={box} className="rd-pdf-page" data-page={n} style={{ width: size.w * scale, height: size.h * scale }}>
      {near ? <canvas ref={canvas} style={{ width: '100%', height: '100%' }} /> : <span className="rd-pdf-n">{n}</span>}
    </div>
  )
}

async function outline(d: PDFDocumentProxy): Promise<TocItem[]> {
  const items = await d.getOutline()
  if (!items) return []
  const conv = async (list: typeof items): Promise<TocItem[]> => {
    const out: TocItem[] = []
    for (const it of list) {
      let page = 0
      try {
        const dest = typeof it.dest === 'string' ? await d.getDestination(it.dest) : it.dest
        if (dest && dest[0]) page = (await d.getPageIndex(dest[0] as Parameters<PDFDocumentProxy['getPageIndex']>[0])) + 1
      } catch { /* outline entry without a page */ }
      out.push({ label: it.title || 'Untitled', href: page || 1, subitems: it.items?.length ? await conv(it.items) : undefined })
    }
    return out
  }
  return conv(items)
}

function chapterOf(toc: TocItem[], page: number): string {
  let best = ''
  const walk = (list: TocItem[]) => {
    for (const t of list) {
      if (typeof t.href === 'number' && t.href <= page) best = t.label
      if (t.subitems) walk(t.subitems)
    }
  }
  walk(toc)
  return best
}
