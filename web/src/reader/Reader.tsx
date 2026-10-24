// The reader: a full-screen page for one book. Reflowable books open in
// foliate-js, PDFs in pdf.js, comics and manga in the page reader; the shell
// around them is the same — contents, search, highlights and notes,
// bookmarks, the Aa sheet, and the position kept in step with the server.
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Annotation, Settings } from '../lib/types'
import { Icon } from '../components/icons'
import { Spinner, copyText, errMsg, useToast } from '../components/ui'
import { useAskAvailable } from '../components/AskButton'
import type { EngineApi, ReaderData, RelocateInfo, SearchHit, SelectionInfo, TocItem } from './engine'
import { DEFAULT_COMIC, DEFAULT_PREFS, FONTS, HIGHLIGHT_COLORS, THEMES, colorOf, parsePrefs, resolveTheme, type ComicPrefs, type ReaderPrefs, type ReaderTheme } from './prefs'
import { ProgressReporter } from './progress'
import type { PdfZoom } from './PdfEngine'

// each engine is its own chunk: an EPUB never loads pdf.js
const FoliateEngine = React.lazy(() => import('./FoliateEngine'))
const PdfEngine = React.lazy(() => import('./PdfEngine'))
const ComicEngine = React.lazy(() => import('./ComicEngine'))
import './reader.css'

type Panel = null | 'toc' | 'search' | 'notes' | 'settings' | 'files'

const READABLE = new Set(['epub', 'kepub', 'mobi', 'azw', 'azw3', 'prc', 'fb2', 'fbz', 'pdf', 'cbz', 'cbr', 'cb7', 'cbt'])
const isTouch = () => window.matchMedia('(pointer: coarse)').matches

export default function Reader() {
  const { id } = useParams()
  const bookId = Number(id)
  const [params, setParams] = useSearchParams()
  const fileParam = params.get('file')
  const navigate = useNavigate()
  const ask = useAskAvailable()
  const { settings, setSettings } = useApp()
  const { toast } = useToast()

  const [data, setData] = useState<ReaderData | null>(null)
  const [error, setError] = useState('')
  const [engine, setEngine] = useState<EngineApi | null>(null)
  const [info, setInfo] = useState<RelocateInfo | null>(null)
  const [chrome, setChrome] = useState(true)
  const [panel, setPanel] = useState<Panel>(null)
  const [anns, setAnns] = useState<Annotation[]>([])
  const [sel, setSel] = useState<SelectionInfo | null>(null)
  const [editing, setEditing] = useState<{ a: Annotation; rect: DOMRect } | null>(null)
  const [noteFor, setNoteFor] = useState<SelectionInfo | null>(null)
  const [prefs, setPrefsRaw] = useState<ReaderPrefs>(() => parsePrefs(settings?.reader_prefs, DEFAULT_PREFS))
  const [comicPrefs, setComicRaw] = useState<ComicPrefs>(() => parsePrefs(settings?.comic_prefs, DEFAULT_COMIC))
  const [zoom, setZoom] = useState<PdfZoom>('width')
  const [slider, setSlider] = useState<number | null>(null)
  const reporter = useRef<ProgressReporter | null>(null)
  const dragging = useRef(false)
  const engineRef = useRef<EngineApi | null>(null)
  engineRef.current = engine

  // ---- load ----
  useEffect(() => {
    let cancelled = false
    setData(null); setEngine(null); setInfo(null); setError('')
    api.get<ReaderData>(`${V1}/books/${bookId}/reader${fileParam ? `?file=${fileParam}` : ''}`)
      .then(d => {
        if (cancelled) return
        setData(d)
        setAnns(d.annotations || [])
        document.title = d.book.title
        // picked up from somewhere else since this browser last read it?
        const seen = localStorage.getItem(`plb-read-${bookId}`) || ''
        const st = d.state
        if (st?.last_device && !st.last_device.startsWith('Web reader') && st.last_read_at > seen && st.percentage > 0) {
          toast(`Picked up where you left off on ${st.last_device} (${Math.round(st.percentage * 100)}%)`)
        }
      })
      .catch(e => { if (!cancelled) setError(errMsg(e)) })
    return () => { cancelled = true }
  }, [bookId, fileParam]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!data?.file) return
    const r = new ProgressReporter(data.book.id, data.file.id)
    reporter.current = r
    return () => { r.stop(); reporter.current = null }
  }, [data?.book.id, data?.file?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  // ---- prefs, saved a moment after the last change ----
  const saveTimer = useRef(0)
  const persist = useCallback((patch: Partial<Settings>) => {
    window.clearTimeout(saveTimer.current)
    saveTimer.current = window.setTimeout(() => {
      api.put<Settings>(`${V1}/settings`, patch).then(s => setSettings(s)).catch(() => {})
    }, 800)
  }, [setSettings])
  const setPrefs = (p: Partial<ReaderPrefs>) => setPrefsRaw(cur => { const n = { ...cur, ...p }; persist({ reader_prefs: JSON.stringify(n) }); return n })
  const setComic = (p: Partial<ComicPrefs>) => setComicRaw(cur => { const n = { ...cur, ...p }; persist({ comic_prefs: JSON.stringify(n) }); return n })

  const kind = data?.engine ?? 'none'
  const themeName = resolveTheme(kind === 'comic' ? comicPrefs.theme : prefs.theme)
  const theme = THEMES[themeName]

  // ---- engine callbacks ----
  const onRelocate = useCallback((i: RelocateInfo) => {
    setInfo(i)
    if (!dragging.current) setSlider(null)
    if (i.turned) {
      reporter.current?.move({ percentage: i.percentage, locator: i.locator })
      localStorage.setItem(`plb-read-${bookId}`, new Date().toISOString())
      if (isTouch()) setChrome(false)
    }
  }, [bookId])

  const panelRef = useRef<Panel>(panel)
  panelRef.current = panel
  const onTap = useCallback((zone: 'left' | 'right' | 'center') => {
    reporter.current?.active()
    if (zone === 'center') { setChrome(c => !c); setSel(null); return }
    if (panelRef.current) { setPanel(null); return }
    if (zone === 'left') engineRef.current?.left(); else engineRef.current?.right()
  }, [])

  // ---- keyboard ----
  const onKey = (e: KeyboardEvent) => {
    const t = e.target as HTMLElement | null
    if (t?.closest?.('input, textarea, select, [contenteditable="true"]')) return
    if (e.metaKey || e.ctrlKey || e.altKey) return
    const eng = engineRef.current
    reporter.current?.active()
    switch (e.key) {
      case 'ArrowLeft': e.preventDefault(); eng?.left(); break
      case 'ArrowRight': e.preventDefault(); eng?.right(); break
      case 'PageDown': case ' ': e.preventDefault(); if (e.shiftKey) eng?.prev(); else eng?.next(); break
      case 'PageUp': e.preventDefault(); eng?.prev(); break
      case 'ArrowDown': if (kind !== 'pdf' && !(kind === 'foliate' && prefs.flow === 'scrolled') && comicPrefs.mode !== 'scroll') { e.preventDefault(); eng?.next() } break
      case 'ArrowUp': if (kind !== 'pdf' && !(kind === 'foliate' && prefs.flow === 'scrolled') && comicPrefs.mode !== 'scroll') { e.preventDefault(); eng?.prev() } break
      case 't': if (kind !== 'comic') setPanel(p => p === 'toc' ? null : 'toc'); break
      case 'f': toggleFullscreen(); break
      case 'b': toggleBookmark(); break
      case '/': if (kind !== 'comic') { e.preventDefault(); setPanel('search'); setChrome(true) } break
      case 'n': setPanel(p => p === 'notes' ? null : 'notes'); break
      case 'Escape':
        if (sel || editing || noteFor) { setSel(null); setEditing(null); setNoteFor(null); eng?.clearSelection?.() }
        else if (panelRef.current) { if (panelRef.current === 'search') eng?.clearSearch?.(); setPanel(null) }
        else navigate(`/book/${bookId}`)
        break
    }
  }
  const onKeyRef = useRef(onKey)
  onKeyRef.current = onKey
  useEffect(() => {
    const h = (e: KeyboardEvent) => onKeyRef.current(e)
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [])
  const forwardKey = useCallback((e: KeyboardEvent) => onKeyRef.current(e), [])

  // ---- annotations ----
  const saveAnnotation = async (s: SelectionInfo, kindA: 'highlight' | 'note', color: string, note = '') => {
    try {
      const a = await api.post<Annotation>(`${V1}/books/${bookId}/annotations`, {
        kind: kindA, text: s.text, note, color, locator: s.locator, chapter: s.chapter, percentage: s.percentage, page: s.page, file_id: data?.file?.id,
      })
      setAnns(list => [...list, a])
      engineRef.current?.drawAnnotation?.(a)
      engineRef.current?.clearSelection?.()
      setSel(null)
    } catch (e) { toast(errMsg(e), 'err') }
  }

  const updateAnnotation = async (a: Annotation, patch: Partial<Pick<Annotation, 'note' | 'color'>>) => {
    try {
      const n = await api.put<Annotation>(`${V1}/annotations/${a.id}`, patch)
      setAnns(list => list.map(x => x.id === n.id ? n : x))
      if (patch.color) { engineRef.current?.removeAnnotation?.(a); engineRef.current?.drawAnnotation?.(n) }
      setEditing(e => e && e.a.id === n.id ? { ...e, a: n } : e)
    } catch (e) { toast(errMsg(e), 'err') }
  }

  const deleteAnnotation = async (a: Annotation) => {
    try {
      await api.del(`${V1}/annotations/${a.id}`)
      setAnns(list => list.filter(x => x.id !== a.id))
      engineRef.current?.removeAnnotation?.(a)
      setEditing(null)
      toast(a.kind === 'bookmark' ? 'Bookmark removed' : 'Highlight removed')
    } catch (e) { toast(errMsg(e), 'err') }
  }

  const bookmark = engine && info ? engine.bookmarkHere(anns) : undefined
  function toggleBookmark() {
    const eng = engineRef.current
    if (!eng || !info) return
    const here = eng.bookmarkHere(anns)
    if (here) { void deleteAnnotation(here); return }
    const loc = info.locator as Record<string, unknown>
    api.post<Annotation>(`${V1}/books/${bookId}/annotations`, {
      kind: 'bookmark', text: '', color: 'yellow', locator: loc, chapter: info.chapter, percentage: info.percentage,
      page: typeof loc.page === 'number' ? loc.page : 0, file_id: data?.file?.id,
    }).then(a => { setAnns(list => [...list, a]); toast('Bookmarked') }).catch(e => toast(errMsg(e), 'err'))
  }

  function toggleFullscreen() {
    if (document.fullscreenElement) void document.exitFullscreen()
    else void document.documentElement.requestFullscreen?.().catch(() => {})
  }

  const markFinished = async () => {
    if (!data?.file) return
    try {
      await api.post(`${V1}/books/${bookId}/progress`, { file_id: data.file.id, percentage: 1, locator: info?.locator ?? null, seconds: 0 })
      setData(d => d && d.state ? { ...d, state: { ...d.state, status: 'read' } } : d)
      toast('Marked as finished')
    } catch (e) { toast(errMsg(e), 'err') }
  }

  // ---- render ----
  if (error) {
    return (
      <div className="rd rd-msg" data-rtheme="app">
        <div className="rd-msg-box">
          <Icon name="alert" size={22} />
          <h2>This book didn’t open</h2>
          <p className="muted">{error}</p>
          <div className="row"><button type="button" className="btn" onClick={() => navigate(`/book/${bookId}`)}>Back to the book</button></div>
        </div>
      </div>
    )
  }
  if (!data) return <div className="rd rd-msg" data-rtheme="app"><Spinner size={20} /></div>
  const file = data.file
  const readable = data.book.files.filter(f => READABLE.has(f.format) && !f.missing)
  if (!file || kind === 'none') {
    return (
      <div className="rd rd-msg" data-rtheme="app">
        <div className="rd-msg-box">
          <Icon name="book-open" size={22} />
          <h2>{data.book.title}</h2>
          <p className="muted">{file ? `The web reader can’t open ${file.format.toUpperCase()} files.` : 'This book has no file to read.'} Download it, or send it to one of your devices.</p>
          <div className="row">
            {file && <a className="btn primary" href={`${V1}/books/${bookId}/download?file=${file.id}`}><Icon name="download" size={15} />Download</a>}
            <button type="button" className="btn" onClick={() => navigate(`/book/${bookId}`)}>Back to the book</button>
          </div>
        </div>
      </div>
    )
  }

  const engineProps = {
    data, file, prefs, comicPrefs, annotations: anns,
    onReady: (a: EngineApi) => setEngine(a),
    onRelocate,
    onSelect: (s: SelectionInfo | null) => { setSel(s); if (s) setEditing(null) },
    onAnnotationClick: (a: Annotation, rect: DOMRect) => { setSel(null); setEditing({ a, rect }) },
    onTap,
    onActivity: () => reporter.current?.active(),
    onKey: forwardKey,
    onError: (m: string) => setError(m),
  }
  const pct = slider ?? info?.percentage ?? data.percentage ?? 0
  const finished = data.state?.status === 'read'
  const showChrome = chrome || !!panel || !engine
  const ticks = engine ? engine.sectionFractions() : []

  return (
    <div className={`rd rd-k-${kind} ${showChrome ? 'chrome' : 'bare'}`} data-rtheme={themeName} style={{ '--rd-bg': theme.bg, '--rd-fg': theme.fg } as React.CSSProperties}>
      <header className="rd-top">
        <button type="button" className="rd-btn" onClick={() => navigate(`/book/${bookId}`)} aria-label="Back to the book" title="Back (esc)"><Icon name="chevron-left" size={20} /></button>
        <div className="rd-title">
          <b className="ellipsis">{data.book.title}</b>
          <span className="ellipsis">{info?.chapter || data.book.authors.map(a => a.name).join(', ')}</span>
        </div>
        <div className="rd-actions">
          {kind !== 'comic' && <button type="button" className={`rd-btn ${panel === 'toc' ? 'on' : ''}`} onClick={() => setPanel(p => p === 'toc' ? null : 'toc')} aria-label="Contents" title="Contents (t)"><Icon name="list" size={18} /></button>}
          {kind !== 'comic' && <button type="button" className={`rd-btn ${panel === 'search' ? 'on' : ''}`} onClick={() => setPanel(p => p === 'search' ? null : 'search')} aria-label="Search in the book" title="Search (/)"><Icon name="search" size={18} /></button>}
          <button type="button" className={`rd-btn ${panel === 'notes' ? 'on' : ''}`} onClick={() => setPanel(p => p === 'notes' ? null : 'notes')} aria-label="Highlights and bookmarks" title="Highlights (n)"><Icon name="note" size={18} />{anns.length > 0 && <span className="rd-count">{anns.length}</span>}</button>
          <button type="button" className={`rd-btn ${bookmark ? 'on mark' : ''}`} onClick={toggleBookmark} aria-label={bookmark ? 'Remove the bookmark' : 'Bookmark this page'} title="Bookmark (b)"><Icon name={bookmark ? 'bookmark' : 'bookmark-plus'} size={18} /></button>
          <button type="button" className={`rd-btn rd-aa ${panel === 'settings' ? 'on' : ''}`} onClick={() => setPanel(p => p === 'settings' ? null : 'settings')} aria-label="Reading settings" title="Reading settings">Aa</button>
          {readable.length > 1 && <button type="button" className={`rd-btn ${panel === 'files' ? 'on' : ''}`} onClick={() => setPanel(p => p === 'files' ? null : 'files')} aria-label="Formats" title="Formats"><Icon name="layers" size={18} /></button>}
          {ask && <button type="button" className="rd-btn" data-pocket-ask-trigger onClick={() => window.dispatchEvent(new CustomEvent('pocket-ask'))} aria-label="Ask AI about this book" title="Ask AI about this book" aria-haspopup="dialog">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M9.9 4.6 11 8l3.4 1.1L11 10.2 9.9 13.6 8.8 10.2 5.4 9.1 8.8 8Z" /><path d="M17 3v3M15.5 4.5h3M17.5 14.5v4M15.5 16.5h4" /></svg>
          </button>}
          <button type="button" className="rd-btn rd-hide-s" onClick={toggleFullscreen} aria-label="Full screen" title="Full screen (f)"><Icon name={document.fullscreenElement ? 'fullscreen-exit' : 'fullscreen'} size={18} /></button>
        </div>
      </header>

      <main className="rd-stage">
        <React.Suspense fallback={null}>
          {kind === 'foliate' && <FoliateEngine key={file.id} {...engineProps} />}
          {kind === 'pdf' && <PdfEngine key={file.id} {...engineProps} zoom={zoom} />}
          {kind === 'comic' && <ComicEngine key={file.id} {...engineProps} />}
        </React.Suspense>
        {!engine && <div className="rd-loading"><Spinner size={20} /><span>Opening {data.book.title}…</span></div>}
      </main>

      {info?.atEnd && !finished && (
        <div className="rd-end">
          <span>You reached the end.</span>
          <button type="button" className="btn primary sm" onClick={markFinished}><Icon name="check" size={14} />Mark as finished</button>
        </div>
      )}

      <footer className="rd-bottom">
        <div className="rd-progress">
          <input type="range" min={0} max={1000} step={1} value={Math.round(pct * 1000)} aria-label="Position in the book"
            onPointerDown={() => { dragging.current = true }}
            onChange={e => setSlider(Number(e.target.value) / 1000)}
            onPointerUp={e => { dragging.current = false; engine?.goToFraction(Number((e.target as HTMLInputElement).value) / 1000) }}
            onKeyUp={e => engine?.goToFraction(Number((e.target as HTMLInputElement).value) / 1000)}
            list={ticks.length > 1 && ticks.length < 120 ? 'rd-ticks' : undefined} />
          {ticks.length > 1 && ticks.length < 120 && <datalist id="rd-ticks">{ticks.map((f, i) => <option key={i} value={Math.round(f * 1000)} />)}</datalist>}
        </div>
        <div className="rd-meta">
          <span className="tnum">{Math.round(pct * 100)}%</span>
          {info?.page && <span className="tnum">{info.page}</span>}
          {typeof info?.minutesLeft === 'number' && info.minutesLeft > 0 && <span className="rd-hide-s">{minutesLeft(info.minutesLeft)} left in chapter</span>}
          {kind === 'pdf' && <PdfZoomPicker zoom={zoom} setZoom={setZoom} />}
        </div>
      </footer>

      {panel && <div className="rd-scrim" onClick={() => setPanel(null)} />}
      {panel === 'toc' && engine && <TocPanel toc={engine.toc()} current={info?.chapter} onGo={t => { engine.goTo(t); setPanel(null) }} onClose={() => setPanel(null)} />}
      {panel === 'search' && engine && <SearchPanel engine={engine} onClose={() => setPanel(null)} />}
      {panel === 'notes' && <NotesPanel anns={anns} bookId={bookId} onGo={a => { engine?.goToAnnotation(a); setPanel(null) }} onDelete={deleteAnnotation} onEdit={a => setEditing({ a, rect: new DOMRect(window.innerWidth / 2, 120, 0, 0) })} onClose={() => setPanel(null)} />}
      {panel === 'settings' && (kind === 'comic'
        ? <ComicSettings p={comicPrefs} set={setComic} onClose={() => setPanel(null)} rtlDefault={data.book.reading_direction === 'rtl' || data.book.kind === 'manga'} />
        : <TextSettings p={prefs} set={setPrefs} onClose={() => setPanel(null)} pdf={kind === 'pdf'} />)}
      {panel === 'files' && (
        <aside className="rd-panel" aria-label="Formats">
          <PanelHead title="Formats" onClose={() => setPanel(null)} />
          <div className="rd-panel-body">
            {readable.map(f => (
              <button key={f.id} type="button" className={`rd-row ${f.id === file.id ? 'on' : ''}`} onClick={() => { setPanel(null); if (f.id !== file.id) setParams({ file: String(f.id) }) }}>
                <span className="rd-fmt">{f.format.toUpperCase()}</span><span className="grow ellipsis">{f.name}</span>{f.id === file.id && <Icon name="check" size={15} />}
              </button>
            ))}
            <p className="rd-hint">Your place moves with you between formats as near as it can: exactly within the same file, by position in the book otherwise.</p>
          </div>
        </aside>
      )}

      {sel && !noteFor && <SelectionPopup sel={sel} lang={data.book.language}
        onColor={c => saveAnnotation(sel, 'highlight', c)}
        onNote={() => setNoteFor(sel)}
        onCopy={async () => { toast(await copyText(sel.text) ? 'Copied' : 'Could not copy'); engine?.clearSelection?.(); setSel(null) }}
        onSearch={() => { searchSeed = sel.text.slice(0, 80); setPanel('search'); setSel(null) }}
      />}
      {noteFor && <NoteEditor title="Add a note" text={noteFor.text} initial="" onCancel={() => setNoteFor(null)} onSave={note => { const s = noteFor; setNoteFor(null); void saveAnnotation(s, 'note', 'yellow', note) }} />}
      {editing && <AnnotationPopover a={editing.a} rect={editing.rect} onClose={() => setEditing(null)} onColor={c => updateAnnotation(editing.a, { color: c })} onNote={note => updateAnnotation(editing.a, { note })} onDelete={() => deleteAnnotation(editing.a)} onCopy={async () => { toast(await copyText(editing.a.text) ? 'Copied' : 'Could not copy') }} />}
    </div>
  )
}

// a selection's "Find" pre-fills the search panel
let searchSeed = ''

function minutesLeft(m: number) {
  if (m < 1) return 'under a minute'
  if (m < 60) return `${Math.round(m)} min`
  return `${Math.floor(m / 60)} h ${Math.round(m % 60)} min`
}

function PanelHead({ title, onClose, children }: { title: string; onClose: () => void; children?: React.ReactNode }) {
  return (
    <div className="rd-panel-head">
      <h2>{title}</h2>
      <span className="grow" />
      {children}
      <button type="button" className="rd-btn" onClick={onClose} aria-label="Close"><Icon name="x" size={18} /></button>
    </div>
  )
}

function TocPanel({ toc, current, onGo, onClose }: { toc: TocItem[]; current?: string; onGo: (t: string | number) => void; onClose: () => void }) {
  const cur = useRef<HTMLButtonElement>(null)
  useEffect(() => { cur.current?.scrollIntoView({ block: 'center' }) }, [])
  const render = (items: TocItem[], depth: number, path: string): React.ReactNode => items.map((t, i) => (
    <React.Fragment key={`${path}-${i}`}>
      <button type="button" ref={t.label === current ? cur : undefined} className={`rd-row toc d${Math.min(depth, 4)} ${t.label === current ? 'on' : ''}`} onClick={() => onGo(t.href)}>
        <span className="grow">{t.label}</span>
      </button>
      {t.subitems && render(t.subitems, depth + 1, `${path}-${i}`)}
    </React.Fragment>
  ))
  return (
    <aside className="rd-panel left" aria-label="Contents">
      <PanelHead title="Contents" onClose={onClose} />
      <div className="rd-panel-body">{toc.length ? render(toc, 0, 't') : <p className="rd-hint">This book has no table of contents.</p>}</div>
    </aside>
  )
}

function SearchPanel({ engine, onClose }: { engine: EngineApi; onClose: () => void }) {
  const [q, setQ] = useState(searchSeed)
  const [hits, setHits] = useState<SearchHit[]>([])
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const stop = useRef<() => void>(() => {})
  const run = (term: string) => {
    stop.current()
    engine.clearSearch?.()
    setHits([]); setDone(false)
    if (term.trim().length < 2 || !engine.search) return
    setBusy(true)
    const found: SearchHit[] = []
    let flush = 0
    stop.current = engine.search(term.trim(), h => {
      found.push(h)
      window.clearTimeout(flush)
      flush = window.setTimeout(() => setHits([...found]), 60)
    }, () => { window.clearTimeout(flush); setHits([...found]); setBusy(false); setDone(true) })
  }
  useEffect(() => { if (searchSeed) { run(searchSeed); searchSeed = '' } return () => stop.current() }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const close = () => { stop.current(); engine.clearSearch?.(); onClose() }
  return (
    <aside className="rd-panel" aria-label="Search">
      <PanelHead title="Search" onClose={close} />
      <form className="rd-search" onSubmit={e => { e.preventDefault(); run(q) }}>
        <Icon name="search" size={15} />
        <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Words in this book" enterKeyHint="search" />
        {busy && <Spinner size={14} />}
      </form>
      <div className="rd-panel-body">
        {hits.map((h, i) => (
          <button key={i} type="button" className="rd-row hit" onClick={() => { engine.goTo(h.target); if (window.innerWidth <= 640) onClose() }}>
            {h.label && <span className="rd-hit-where">{h.label}</span>}
            <span className="rd-hit-text">…{h.pre}<mark>{h.match}</mark>{h.post}…</span>
          </button>
        ))}
        {done && !hits.length && <p className="rd-hint">Nothing found.</p>}
        {done && hits.length > 0 && <p className="rd-hint">{hits.length} {hits.length === 1 ? 'place' : 'places'}.</p>}
      </div>
    </aside>
  )
}

const SOURCES: Record<string, string> = { koreader: 'KOReader', crosspoint: 'CrossPoint', kindle: 'Kindle', import: 'Imported' }

function NotesPanel({ anns, bookId, onGo, onDelete, onEdit, onClose }: { anns: Annotation[]; bookId: number; onGo: (a: Annotation) => void; onDelete: (a: Annotation) => void; onEdit: (a: Annotation) => void; onClose: () => void }) {
  const [tab, setTab] = useState<'all' | 'highlight' | 'note' | 'bookmark'>('all')
  const list = useMemo(() => [...anns].filter(a => tab === 'all' || a.kind === tab).sort((a, b) => a.percentage - b.percentage || a.id - b.id), [anns, tab])
  return (
    <aside className="rd-panel" aria-label="Highlights and bookmarks">
      <PanelHead title="Highlights" onClose={onClose}>
        <a className="rd-btn" href={`${V1}/annotations/export?book=${bookId}&format=md`} title="Export as Markdown" aria-label="Export as Markdown"><Icon name="download" size={17} /></a>
      </PanelHead>
      <div className="rd-tabs">
        {(['all', 'highlight', 'note', 'bookmark'] as const).map(t => <button key={t} type="button" className={tab === t ? 'on' : ''} onClick={() => setTab(t)}>{t === 'all' ? 'All' : t === 'highlight' ? 'Highlights' : t === 'note' ? 'Notes' : 'Bookmarks'}</button>)}
      </div>
      <div className="rd-panel-body">
        {list.length === 0 && <p className="rd-hint">Select text to highlight it or add a note; the bookmark button marks a page. Highlights from KOReader and CrossPoint show up here too.</p>}
        {list.map(a => (
          <div key={a.id} className="rd-ann">
            <button type="button" className="rd-ann-main" onClick={() => onGo(a)}>
              <span className="rd-ann-where">
                {a.kind === 'bookmark' ? <Icon name="bookmark" size={12} /> : <span className="rd-dot" style={{ background: colorOf(a.color) }} />}
                <span className="ellipsis">{a.chapter || (a.page ? `Page ${a.page}` : `${Math.round(a.percentage * 100)}%`)}</span>
                {a.source !== 'web' && <span className="rd-src">{SOURCES[a.source] || a.source}</span>}
              </span>
              {a.text && <span className={`rd-ann-text ${a.color === 'underline' ? 'u' : ''}`}>{a.text}</span>}
              {a.note && <span className="rd-ann-note">{a.note}</span>}
              {a.kind === 'bookmark' && !a.text && <span className="rd-ann-text muted">Bookmark</span>}
            </button>
            <div className="rd-ann-actions">
              {a.kind !== 'bookmark' && <button type="button" className="rd-btn sm" onClick={() => onEdit(a)} aria-label="Edit"><Icon name="edit" size={14} /></button>}
              <button type="button" className="rd-btn sm" onClick={() => onDelete(a)} aria-label="Delete"><Icon name="trash" size={14} /></button>
            </div>
          </div>
        ))}
      </div>
    </aside>
  )
}

function Seg<T extends string | number>({ value, options, onChange }: { value: T; options: { value: T; label: React.ReactNode; title?: string }[]; onChange: (v: T) => void }) {
  return (
    <div className="rd-seg" role="radiogroup">
      {options.map(o => <button key={String(o.value)} type="button" role="radio" aria-checked={o.value === value} title={o.title} className={o.value === value ? 'on' : ''} onClick={() => onChange(o.value)}>{o.label}</button>)}
    </div>
  )
}

function Stepper({ value, min, max, step, format, onChange, label }: { value: number; min: number; max: number; step: number; format: (v: number) => string; onChange: (v: number) => void; label: string }) {
  const round = (v: number) => Math.round(v * 100) / 100
  return (
    <div className="rd-stepper" aria-label={label}>
      <button type="button" disabled={value <= min} onClick={() => onChange(round(Math.max(min, value - step)))} aria-label={`Less ${label}`}><Icon name="minus" size={15} /></button>
      <span className="tnum">{format(value)}</span>
      <button type="button" disabled={value >= max} onClick={() => onChange(round(Math.min(max, value + step)))} aria-label={`More ${label}`}><Icon name="plus" size={15} /></button>
    </div>
  )
}

const THEME_OPTIONS: { value: ReaderTheme; label: string }[] = [
  { value: 'app', label: 'Auto' },
  ...(Object.keys(THEMES) as Exclude<ReaderTheme, 'app'>[]).map(k => ({ value: k as ReaderTheme, label: THEMES[k].label })),
]

function ThemeSwatches({ value, onChange }: { value: ReaderTheme; onChange: (t: ReaderTheme) => void }) {
  return (
    <div className="rd-swatches">
      {THEME_OPTIONS.map(t => {
        const c = t.value === 'app' ? null : THEMES[t.value as Exclude<ReaderTheme, 'app'>]
        return (
          <button key={t.value} type="button" className={`rd-swatch ${value === t.value ? 'on' : ''} ${c ? '' : 'auto'}`} onClick={() => onChange(t.value)} aria-pressed={value === t.value}
            style={c ? { background: c.bg, color: c.fg } : undefined}>
            <span>Aa</span><small>{t.label}</small>
          </button>
        )
      })}
    </div>
  )
}

function TextSettings({ p, set, onClose, pdf }: { p: ReaderPrefs; set: (p: Partial<ReaderPrefs>) => void; onClose: () => void; pdf: boolean }) {
  return (
    <aside className="rd-panel rd-sheet" aria-label="Reading settings">
      <PanelHead title="Reading" onClose={onClose}>
        <button type="button" className="rd-link" onClick={() => set(DEFAULT_PREFS)}>Reset</button>
      </PanelHead>
      <div className="rd-panel-body rd-set">
        <div className="rd-set-label">Theme</div>
        <ThemeSwatches value={p.theme} onChange={theme => set({ theme })} />
        {pdf ? <p className="rd-hint">PDF pages keep their own look; the theme colours the reader around them. Zoom is at the bottom right.</p> : <>
          <div className="rd-set-label">Font</div>
          <div className="rd-fonts">
            {(Object.keys(FONTS) as (keyof typeof FONTS)[]).map(f => <button key={f} type="button" className={p.font === f ? 'on' : ''} onClick={() => set({ font: f })} style={{ fontFamily: FONTS[f].stack || undefined }}>{FONTS[f].label}</button>)}
          </div>
          <div className="rd-set-row"><span>Size</span><Stepper label="text size" value={p.size} min={12} max={34} step={1} format={v => `${v}`} onChange={size => set({ size })} /></div>
          <div className="rd-set-row"><span>Line spacing</span><Stepper label="line spacing" value={p.lineHeight} min={1.1} max={2.2} step={0.1} format={v => v.toFixed(1)} onChange={lineHeight => set({ lineHeight })} /></div>
          <div className="rd-set-row"><span>Margins</span><Stepper label="margins" value={p.margin} min={0} max={20} step={2} format={v => `${v}%`} onChange={margin => set({ margin })} /></div>
          <div className="rd-set-row"><span>Line length</span><Stepper label="line length" value={p.maxWidth} min={480} max={1200} step={60} format={v => `${v}`} onChange={maxWidth => set({ maxWidth })} /></div>
          <div className="rd-set-row"><span>Layout</span><Seg value={p.flow} onChange={flow => set({ flow })} options={[{ value: 'paginated', label: 'Pages' }, { value: 'scrolled', label: 'Scroll' }]} /></div>
          {p.flow === 'paginated' && <div className="rd-set-row"><span>Columns</span><Seg value={p.columns} onChange={columns => set({ columns })} options={[{ value: 'auto', label: 'Auto', title: 'Two on wide screens' }, { value: 1, label: 'One' }, { value: 2, label: 'Two' }]} /></div>}
          <div className="rd-set-row"><span>Alignment</span><Seg value={p.justify ? 1 : 0} onChange={v => set({ justify: v === 1 })} options={[{ value: 0, label: 'Left' }, { value: 1, label: 'Justified' }]} /></div>
          <div className="rd-set-row"><span>Hyphenation</span><Seg value={p.hyphenate ? 1 : 0} onChange={v => set({ hyphenate: v === 1 })} options={[{ value: 0, label: 'Off' }, { value: 1, label: 'On' }]} /></div>
          <div className="rd-set-row"><span>Book’s colours</span><Seg value={p.publisherColors ? 1 : 0} onChange={v => set({ publisherColors: v === 1 })} options={[{ value: 0, label: 'Plain' }, { value: 1, label: 'Keep' }]} /></div>
        </>}
      </div>
    </aside>
  )
}

function ComicSettings({ p, set, onClose, rtlDefault }: { p: ComicPrefs; set: (p: Partial<ComicPrefs>) => void; onClose: () => void; rtlDefault: boolean }) {
  return (
    <aside className="rd-panel rd-sheet" aria-label="Reading settings">
      <PanelHead title="Reading" onClose={onClose} />
      <div className="rd-panel-body rd-set">
        <div className="rd-set-row"><span>Pages</span><Seg value={p.mode} onChange={mode => set({ mode })} options={[{ value: 'single', label: 'Single' }, { value: 'double', label: 'Spread' }, { value: 'scroll', label: 'Strip', title: 'One long strip, for webtoons' }]} /></div>
        <div className="rd-set-row"><span>Fit</span><Seg value={p.fit} onChange={fit => set({ fit })} options={[{ value: 'height', label: 'Screen' }, { value: 'width', label: 'Width' }, { value: 'original', label: 'Original' }]} /></div>
        <div className="rd-set-row"><span>Direction</span><Seg value={p.direction} onChange={direction => set({ direction })} options={[{ value: 'auto', label: rtlDefault ? 'Auto ←' : 'Auto →' }, { value: 'ltr', label: '→' }, { value: 'rtl', label: '←' }]} /></div>
        <div className="rd-set-label">Background</div>
        <ThemeSwatches value={p.theme} onChange={theme => set({ theme })} />
      </div>
    </aside>
  )
}

function PdfZoomPicker({ zoom, setZoom }: { zoom: PdfZoom; setZoom: (z: PdfZoom) => void }) {
  const steps = [0.5, 0.75, 1, 1.25, 1.5, 2, 3]
  return (
    <span className="rd-zoom">
      <button type="button" className={zoom === 'width' ? 'on' : ''} onClick={() => setZoom('width')}>Width</button>
      <button type="button" className={zoom === 'page' ? 'on' : ''} onClick={() => setZoom('page')}>Page</button>
      <button type="button" aria-label="Zoom out" onClick={() => setZoom(typeof zoom === 'number' ? steps.filter(s => s < zoom).pop() ?? zoom : 1)}><Icon name="minus" size={13} /></button>
      <span className="tnum">{typeof zoom === 'number' ? `${Math.round(zoom * 100)}%` : ''}</span>
      <button type="button" aria-label="Zoom in" onClick={() => setZoom(typeof zoom === 'number' ? steps.find(s => s > zoom) ?? zoom : 1.25)}><Icon name="plus" size={13} /></button>
    </span>
  )
}

/** Above (or below) a rectangle on the page, kept on screen. */
function place(rect: DOMRect, w: number, h: number) {
  const m = 10
  let left = rect.left + rect.width / 2 - w / 2
  left = Math.max(m, Math.min(window.innerWidth - w - m, left))
  let top = rect.top - h - 12
  if (top < 60) top = rect.bottom + 12
  top = Math.max(m, Math.min(window.innerHeight - h - m, top))
  return { left, top }
}

function SelectionPopup({ sel, lang, onColor, onNote, onCopy, onSearch }: { sel: SelectionInfo; lang: string; onColor: (c: string) => void; onNote: () => void; onCopy: () => void; onSearch: () => void }) {
  const word = sel.text.split(/\s+/).length <= 3 ? sel.text.trim().replace(/[.,;:!?"“”‘’()«»]/g, '') : ''
  const sr = /^(sr|hr|bs|sh)/.test(lang || '')
  const wiki = `https://${sr ? 'sr' : 'en'}.wiktionary.org/wiki/${encodeURIComponent(word.toLowerCase())}`
  return (
    <div className="rd-pop" style={place(sel.rect, 300, 92)} role="toolbar" aria-label="Selected text" onPointerDown={e => e.preventDefault()}>
      <div className="rd-pop-colors">
        {HIGHLIGHT_COLORS.map(c => (
          <button key={c.id} type="button" className={`rd-color ${c.id === 'underline' ? 'u' : ''}`} style={{ '--c': c.color } as React.CSSProperties} onClick={() => onColor(c.id)} aria-label={`Highlight ${c.label.toLowerCase()}`} title={c.label} />
        ))}
      </div>
      <div className="rd-pop-actions">
        <button type="button" onClick={onNote}><Icon name="note" size={14} />Note</button>
        <button type="button" onClick={onCopy}><Icon name="copy" size={14} />Copy</button>
        <button type="button" onClick={onSearch}><Icon name="search" size={14} />Find</button>
        {word && <a href={wiki} target="_blank" rel="noreferrer"><Icon name="globe" size={14} />Define</a>}
      </div>
    </div>
  )
}

function NoteEditor({ title, text, initial, onSave, onCancel }: { title: string; text: string; initial: string; onSave: (note: string) => void; onCancel: () => void }) {
  const [note, setNote] = useState(initial)
  return (
    <div className="rd-modal-back" onMouseDown={e => { if (e.target === e.currentTarget) onCancel() }}>
      <form className="rd-modal" onSubmit={e => { e.preventDefault(); onSave(note.trim()) }}>
        <h2>{title}</h2>
        {text && <blockquote className="rd-quote">{text.length > 400 ? text.slice(0, 400) + '…' : text}</blockquote>}
        <textarea autoFocus rows={5} value={note} onChange={e => setNote(e.target.value)} placeholder="Your thoughts…" onKeyDown={e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) onSave(note.trim()) }} />
        <div className="rd-modal-foot">
          <button type="button" className="btn" onClick={onCancel}>Cancel</button>
          <button className="btn primary">Save</button>
        </div>
      </form>
    </div>
  )
}

function AnnotationPopover({ a, rect, onClose, onColor, onNote, onDelete, onCopy }: { a: Annotation; rect: DOMRect; onClose: () => void; onColor: (c: string) => void; onNote: (n: string) => void; onDelete: () => void; onCopy: () => void }) {
  const [edit, setEdit] = useState(false)
  if (edit) return <NoteEditor title={a.note ? 'Edit the note' : 'Add a note'} text={a.text} initial={a.note} onCancel={() => setEdit(false)} onSave={n => { setEdit(false); onNote(n) }} />
  return (
    <>
      <div className="rd-pop-back" onClick={onClose} />
      <div className="rd-pop" style={place(rect, 300, a.note ? 150 : 92)} role="dialog" aria-label="Highlight">
        <div className="rd-pop-colors">
          {HIGHLIGHT_COLORS.map(c => (
            <button key={c.id} type="button" className={`rd-color ${c.id === 'underline' ? 'u' : ''} ${a.color === c.id ? 'on' : ''}`} style={{ '--c': c.color } as React.CSSProperties} onClick={() => onColor(c.id)} aria-label={c.label} title={c.label} />
          ))}
        </div>
        {a.note && <p className="rd-pop-note">{a.note}</p>}
        <div className="rd-pop-actions">
          <button type="button" onClick={() => setEdit(true)}><Icon name="note" size={14} />{a.note ? 'Edit note' : 'Note'}</button>
          <button type="button" onClick={onCopy}><Icon name="copy" size={14} />Copy</button>
          <button type="button" className="danger" onClick={onDelete}><Icon name="trash" size={14} />Delete</button>
        </div>
      </div>
    </>
  )
}
