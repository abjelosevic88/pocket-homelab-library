// Reflowable books (EPUB, KEPUB, MOBI/AZW3, FB2) through foliate-js.
/* eslint-disable @typescript-eslint/no-explicit-any */
import { useEffect, useRef } from 'react'
import './foliate/view.js'
import * as CFI from './foliate/epubcfi.js'
import { Overlayer } from './foliate/overlayer.js'
import { FootnoteHandler } from './foliate/footnotes.js'
import type { Annotation } from '../lib/types'
import type { EngineApi, EngineProps, TocItem } from './engine'
import { fractionAt, parseLocator, rangeAtFraction } from './locator'
import { bookCSS, colorOf } from './prefs'

const MIME: Record<string, string> = {
  epub: 'application/epub+zip', kepub: 'application/epub+zip', mobi: 'application/x-mobipocket-ebook', azw: 'application/vnd.amazon.ebook',
  azw3: 'application/vnd.amazon.mobi8-ebook', prc: 'application/x-mobipocket-ebook', fb2: 'application/x-fictionbook+xml', fbz: 'application/x-zip-compressed-fb2',
}

/** The name foliate recognises a format by. */
function fileName(name: string, format: string): string {
  const ext = format === 'fbz' ? '.fbz' : format === 'kepub' ? '.epub' : '.' + format
  return name.toLowerCase().endsWith(ext) ? name : name + ext
}

function cfiOf(a: Annotation): string {
  const l = parseLocator(a.locator)
  return typeof l.cfi === 'string' ? l.cfi : ''
}

export default function FoliateEngine(p: EngineProps) {
  const host = useRef<HTMLDivElement>(null)
  const viewRef = useRef<any>(null)
  const props = useRef(p)
  props.current = p
  const annsRef = useRef<Annotation[]>(p.annotations)
  annsRef.current = p.annotations
  const hitAt = useRef(0)

  useEffect(() => {
    let cancelled = false
    let view: any = null
    let first = true
    const footnotes = new FootnoteHandler()
    ;(async () => {
      const res = await fetch(`/api/v1/files/${p.file.id}/content`, { credentials: 'same-origin' })
      if (!res.ok) throw new Error(res.status === 404 ? 'The file is not in the library any more.' : `Could not load the book (${res.status}).`)
      const blob = await res.blob()
      if (cancelled) return
      const file = new File([blob], fileName(p.file.name, p.file.format), { type: MIME[p.file.format] || blob.type })
      view = document.createElement('foliate-view')
      viewRef.current = view
      host.current!.append(view)
      await view.open(file)
      if (cancelled) return
      const book = view.book
      book.transformTarget?.addEventListener('data', ({ detail }: any) => {
        detail.data = Promise.resolve(detail.data).catch(() => '')
      })
      applyPrefs(view, props.current.prefs)

      view.addEventListener('load', ({ detail: { doc, index } }: any) => wireSection(doc, index))
      view.addEventListener('relocate', ({ detail }: any) => onRelocate(detail))
      view.addEventListener('create-overlay', ({ detail: { index } }: any) => {
        for (const a of annsRef.current) {
          const cfi = cfiOf(a)
          if (!cfi || a.kind === 'bookmark') continue
          const l = parseLocator(a.locator)
          if (typeof l.spine === 'number' && l.spine !== index) continue
          view.addAnnotation({ value: cfi, color: a.color }).catch(() => {})
        }
      })
      view.addEventListener('draw-annotation', ({ detail: { draw, annotation } }: any) => {
        if (annotation.color === 'underline') draw(Overlayer.underline, { color: colorOf('underline'), width: 2 })
        else draw(Overlayer.highlight, { color: colorOf(annotation.color) })
      })
      view.addEventListener('show-annotation', ({ detail: { value, range } }: any) => {
        hitAt.current = Date.now()
        const a = annsRef.current.find(x => cfiOf(x) === value)
        if (a) props.current.onAnnotationClick(a, pageRect(range))
      })
      view.addEventListener('link', (e: any) => {
        footnotes.handle(view.book, e)?.catch(() => {})
      })
      view.addEventListener('external-link', (e: any) => { e.preventDefault(); window.open(e.detail.a.href, '_blank', 'noopener') })
      footnotes.addEventListener('render', (e: any) => showFootnote(e.detail.view, host.current!))

      // where to start: this file's own position, else the same place in another copy, else the share read
      const { data } = props.current
      const loc = parseLocator(data.locator)
      const sameFile = !data.state?.file_id || data.state.file_id === p.file.id
      const isEPUB = p.file.format === 'epub' || p.file.format === 'kepub'
      if (sameFile && typeof loc.cfi === 'string' && loc.cfi) {
        await view.init({ lastLocation: loc.cfi })
      } else if (typeof loc.spine === 'number' && typeof loc.fraction === 'number' && (sameFile || (isEPUB && loc.type === 'kosync'))) {
        const { spine, fraction } = loc
        await view.renderer.goTo({ index: spine, anchor: (doc: Document) => rangeAtFraction(doc, fraction) ?? 0 })
      } else if (data.percentage > 0 && data.percentage < 1) {
        await view.init({ lastLocation: { fraction: data.percentage } })
      } else {
        await view.init({ showTextStart: true })
      }
      if (cancelled) return
      props.current.onReady(engineApi())
    })().catch(e => { if (!cancelled) props.current.onError((e as Error).message || String(e)) })

    function engineApi(): EngineApi { return api }

    function pageRect(range: Range): DOMRect {
      const frame = range.startContainer.ownerDocument?.defaultView?.frameElement as HTMLElement | null
      const fr = frame?.getBoundingClientRect() ?? new DOMRect()
      const rr = range.getBoundingClientRect()
      return new DOMRect(fr.left + rr.left, fr.top + rr.top, rr.width, rr.height)
    }

    function sectionIndex(doc: Document): number {
      return (view.renderer.getContents() as any[]).find(c => c.doc === doc)?.index ?? 0
    }

    function onRelocate(d: any) {
      const range: Range | undefined = d.range
      const doc = range?.startContainer?.ownerDocument
      const index = doc ? sectionIndex(doc) : 0
      const fraction = doc && range ? fractionAt(doc, range.startContainer, range.startOffset) : 0
      const cfi = d.cfi ? CFI.collapse(d.cfi) : ''
      const page = d.pageItem?.label ? `Page ${d.pageItem.label}` : d.location ? `${(d.location.current ?? 0) + 1} / ${d.location.total}` : undefined
      props.current.onRelocate({
        percentage: d.fraction ?? 0,
        locator: { type: 'epub', cfi, spine: index, fraction: Math.round(fraction * 1e5) / 1e5, chapter: d.tocItem?.label ?? '' },
        chapter: d.tocItem?.label ?? '', chapterHref: d.tocItem?.href, page,
        minutesLeft: typeof d.time?.section === 'number' ? d.time.section : undefined,
        atEnd: !!view.renderer.atEnd && index >= view.book.sections.length - 1,
        turned: !first,
      })
      first = false
    }

    function wireSection(doc: Document, index: number) {
      doc.addEventListener('keydown', e => props.current.onKey(e))
      const activity = () => props.current.onActivity()
      doc.addEventListener('wheel', activity, { passive: true })
      doc.addEventListener('touchstart', activity, { passive: true })
      let selTimer = 0
      const checkSelection = () => {
        window.clearTimeout(selTimer)
        selTimer = window.setTimeout(() => {
          const sel = doc.getSelection()
          if (!sel || sel.isCollapsed || !sel.rangeCount) { props.current.onSelect(null); return }
          const range = sel.getRangeAt(0)
          const text = range.toString().trim()
          if (!text) { props.current.onSelect(null); return }
          const cfi = view.getCFI(index, range)
          const fraction = fractionAt(doc, range.startContainer, range.startOffset)
          const { tocItem } = view.getProgressOf(index, range) ?? {}
          props.current.onSelect({
            text, rect: pageRect(range), cfi, chapter: tocItem?.label ?? '',
            percentage: view.lastLocation?.fraction ?? 0, page: 0,
            locator: { type: 'epub', cfi, spine: index, fraction: Math.round(fraction * 1e5) / 1e5, chapter: tocItem?.label ?? '' },
          })
        }, 350)
      }
      doc.addEventListener('selectionchange', checkSelection)
      doc.addEventListener('pointerup', checkSelection)
      doc.addEventListener('click', e => {
        const sel = doc.getSelection()
        if (sel && !sel.isCollapsed) return
        if ((e.target as Element)?.closest?.('a[href]')) return
        if (Date.now() - hitAt.current < 300) return
        const frame = doc.defaultView?.frameElement as HTMLElement | null
        const x = (frame?.getBoundingClientRect().left ?? 0) + e.clientX
        const w = window.innerWidth
        props.current.onTap(x < w * 0.28 ? 'left' : x > w * 0.72 ? 'right' : 'center')
      })
    }

    const toToc = (items: any[] | undefined): TocItem[] =>
      (items ?? []).map(i => ({ label: (i.label ?? '').trim() || 'Untitled', href: i.href, subitems: i.subitems?.length ? toToc(i.subitems) : undefined }))

    const api: EngineApi = {
      next: () => { view.next() },
      prev: () => { view.prev() },
      left: () => { view.goLeft() },
      right: () => { view.goRight() },
      goToFraction: f => { view.goToFraction(f) },
      goTo: t => { view.goTo(t) },
      goToAnnotation: a => {
        const l = parseLocator(a.locator)
        if (typeof l.cfi === 'string' && l.cfi) view.goTo(l.cfi)
        else if (typeof l.spine === 'number' && typeof l.fraction === 'number') {
          const { spine, fraction } = l
          view.renderer.goTo({ index: spine, anchor: (doc: Document) => rangeAtFraction(doc, fraction) ?? 0 })
        } else if (a.percentage) view.goToFraction(a.percentage)
      },
      toc: () => toToc(view?.book?.toc),
      sectionFractions: () => view?.getSectionFractions?.() ?? [],
      search: (q, onHit, onDone) => {
        let stop = false
        ;(async () => {
          for await (const r of view.search({ query: q })) {
            if (stop) break
            if (r === 'done') break
            if (r.subitems) for (const s of r.subitems) onHit({ label: r.label, pre: s.excerpt?.pre ?? '', match: s.excerpt?.match ?? '', post: s.excerpt?.post ?? '', target: s.cfi })
          }
        })().catch(() => {}).finally(() => { if (!stop) onDone() })
        return () => { stop = true }
      },
      clearSearch: () => view?.clearSearch(),
      drawAnnotation: a => { const cfi = cfiOf(a); if (cfi && a.kind !== 'bookmark') view.addAnnotation({ value: cfi, color: a.color }).catch(() => {}) },
      removeAnnotation: a => { const cfi = cfiOf(a); if (cfi) view.deleteAnnotation({ value: cfi }).catch(() => {}) },
      clearSelection: () => view?.deselect(),
      bookmarkHere: list => {
        const cur = view?.lastLocation?.cfi
        if (!cur) return undefined
        const start = CFI.collapse(cur), end = CFI.collapse(cur, true)
        return list.find(a => {
          if (a.kind !== 'bookmark') return false
          const c = cfiOf(a)
          if (!c) return false
          try { return CFI.compare(start, c) <= 0 && CFI.compare(c, end) <= 0 } catch { return false }
        })
      },
    }

    return () => {
      cancelled = true
      try { view?.close() } catch { /* already gone */ }
      view?.remove()
      viewRef.current = null
    }
  }, [p.file.id]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { if (viewRef.current?.renderer) applyPrefs(viewRef.current, p.prefs) }, [p.prefs])

  return <div className="rd-foliate" ref={host} />
}

function applyPrefs(view: any, prefs: EngineProps['prefs']) {
  const r = view.renderer
  if (!r) return
  r.setAttribute('flow', prefs.flow)
  r.setAttribute('gap', `${prefs.margin}%`)
  r.setAttribute('max-inline-size', `${prefs.maxWidth}px`)
  r.setAttribute('max-column-count', String(prefs.columns === 'auto' ? 2 : prefs.columns))
  r.setAttribute('margin', window.matchMedia('(pointer: coarse)').matches ? '44px' : '64px')
  r.setStyles?.(bookCSS(prefs))
}

/** A footnote in a small sheet over the page. */
function showFootnote(fview: any, host: HTMLElement) {
  host.parentElement?.querySelector('.rd-footnote')?.remove()
  const box = document.createElement('div')
  box.className = 'rd-footnote'
  const close = document.createElement('button')
  close.className = 'rd-footnote-x'
  close.type = 'button'
  close.textContent = '×'
  close.setAttribute('aria-label', 'Close')
  close.onclick = () => box.remove()
  box.append(close, fview)
  fview.renderer?.setAttribute?.('flow', 'scrolled')
  fview.renderer?.setAttribute?.('margin', '0px')
  fview.renderer?.setAttribute?.('gap', '5%')
  host.parentElement?.append(box)
  const dismiss = (e: Event) => { if (!box.contains(e.target as Node)) { box.remove(); document.removeEventListener('pointerdown', dismiss, true) } }
  setTimeout(() => document.addEventListener('pointerdown', dismiss, true), 0)
}
