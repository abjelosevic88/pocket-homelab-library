// Covers, book tiles and rows, rails, series stacks, authors, shelves and stars.
import React, { useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import type { Author, Book, Series, Shelf } from '../lib/types'
import { authorsText, coverURL, hue, pct, seriesIndex, stateOf } from '../lib/books'
import { Icon } from './icons'
import '../styles/library.css'

/** A book cover: fades in over a placeholder in its own colour; books without
 *  one get a generated cover with the title and author set in type. */
export function Cover({ b, w, className = '' }: { b: Pick<Book, 'title' | 'cover' | 'cover_color' | 'authors'>; w: number; className?: string }) {
  const [state, setState] = useState<'loading' | 'ok' | 'err'>(b.cover ? 'loading' : 'err')
  const url = coverURL(b.cover, w)
  const h = hue(b.title || '?')
  const style = { '--h': h, ...(b.cover_color ? { background: b.cover_color } : {}) } as React.CSSProperties
  return (
    <span className={`bcover ${state === 'ok' ? 'loaded' : ''} ${state === 'err' ? 'gen' : ''} ${className}`} style={style}>
      {state === 'err' && (
        <span className="bcover-gen" aria-hidden="true">
          <span className="bcover-rule" />
          <b className="bcover-title">{b.title}</b>
          {b.authors?.length ? <span className="bcover-author">{authorsText(b, 2)}</span> : null}
        </span>
      )}
      {url && state !== 'err' && <img src={url} alt="" loading="lazy" decoding="async" onLoad={() => setState('ok')} onError={() => setState('err')} />}
    </span>
  )
}

/** A picture (author photo, series) that fades in, with initials or an icon until then. */
export function Img({ src, w, alt, className = '', round, icon }: { src?: string; w: number; alt: string; className?: string; round?: boolean; icon?: string }) {
  const [state, setState] = useState<'loading' | 'ok' | 'err'>(src ? 'loading' : 'err')
  const url = coverURL(src, w)
  const initials = alt.split(/\s+/).filter(Boolean).slice(0, 2).map(x => x[0]).join('').toUpperCase()
  return (
    <span className={`art ${state === 'ok' ? 'loaded' : ''} ${round ? 'round' : ''} ${className}`} style={{ '--h': hue(alt) } as React.CSSProperties}>
      {state !== 'ok' && <span className="art-fallback">{icon ? <Icon name={icon as never} size={22} /> : <b>{initials}</b>}</span>}
      {url && state !== 'err' && <img src={url} alt="" loading="lazy" decoding="async" onLoad={() => setState('ok')} onError={() => setState('err')} />}
    </span>
  )
}

export function Progress({ value, className = '' }: { value: number; className?: string }) {
  if (!value || value <= 0) return null
  return <span className={`progress ${className}`}><span style={{ width: `${Math.min(100, Math.max(2, value * 100))}%` }} /></span>
}

/** Half stars: value 1..10 (2 = one star). With onChange it is an input; clicking the current value clears it. */
export function Stars({ value, onChange, size = 18, className = '' }: { value: number; onChange?: (v: number) => void; size?: number; className?: string }) {
  const [hover, setHover] = useState(0)
  const shown = hover || value
  const pick = (e: React.MouseEvent<HTMLSpanElement>, i: number) => {
    const r = e.currentTarget.getBoundingClientRect()
    return i * 2 + (e.clientX - r.left < r.width / 2 ? 1 : 2)
  }
  return (
    <span className={`stars ${onChange ? 'input' : ''} ${className}`} role={onChange ? 'slider' : 'img'} aria-label={value ? `${value / 2} of 5 stars` : 'Not rated'}
      aria-valuemin={onChange ? 0 : undefined} aria-valuemax={onChange ? 10 : undefined} aria-valuenow={onChange ? value : undefined} tabIndex={onChange ? 0 : undefined}
      onMouseLeave={() => setHover(0)}
      onKeyDown={onChange ? e => { if (e.key === 'ArrowRight' || e.key === 'ArrowUp') { e.preventDefault(); onChange(Math.min(10, value + 1)) } else if (e.key === 'ArrowLeft' || e.key === 'ArrowDown') { e.preventDefault(); onChange(Math.max(0, value - 1)) } } : undefined}>
      {[0, 1, 2, 3, 4].map(i => {
        const fill = shown >= i * 2 + 2 ? 'full' : shown === i * 2 + 1 ? 'half' : 'none'
        return (
          <span key={i} className={`star ${fill}`} style={{ width: size, height: size }}
            onMouseMove={onChange ? e => setHover(pick(e, i)) : undefined}
            onClick={onChange ? e => { const v = pick(e, i); onChange(v === value ? 0 : v) } : undefined}>
            <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
              <defs><clipPath id={`sh${i}`}><rect x="0" y="0" width="12" height="24" /></clipPath></defs>
              <path className="star-bg" d="M12 2.6l2.9 5.9 6.5.9-4.7 4.6 1.1 6.5L12 17.4l-5.8 3.1 1.1-6.5L2.6 9.4l6.5-.9z" />
              {fill !== 'none' && <path className="star-fg" clipPath={fill === 'half' ? `url(#sh${i})` : undefined} d="M12 2.6l2.9 5.9 6.5.9-4.7 4.6 1.1 6.5L12 17.4l-5.8 3.1 1.1-6.5L2.6 9.4l6.5-.9z" />}
            </svg>
          </span>
        )
      })}
    </span>
  )
}

/** A status mark in the corner of a cover. */
function StateMark({ b }: { b: Book }) {
  const s = stateOf(b)
  if (s.status === 'read') return <span className="mark done" title="Read"><Icon name="check" size={12} strokeWidth={3} /></span>
  if (s.status === 'dnf') return <span className="mark dnf" title="Didn’t finish"><Icon name="x" size={12} strokeWidth={3} /></span>
  if (s.status === 'want') return <span className="mark want" title="Want to read"><Icon name="bookmark" size={12} strokeWidth={2.4} /></span>
  return null
}

/** A cover linking to the book, with progress along the bottom. In select
 *  mode a click toggles the selection instead. */
export function BookCard({ b, sub, selecting, selected, onSelect, showSeries }: { b: Book; sub?: React.ReactNode; selecting?: boolean; selected?: boolean; onSelect?: (b: Book, e: React.MouseEvent | React.PointerEvent) => void; showSeries?: boolean }) {
  const s = stateOf(b)
  const started = s.percentage > 0 && s.status !== 'read'
  const press = useLongPress(e => onSelect?.(b, e))
  return (
    <Link to={`/book/${b.id}`} className={`tile book-tile ${selected ? 'selected' : ''} ${b.missing ? 'missing' : ''}`} title={b.title}
      onClick={e => { if (press.fired()) { e.preventDefault(); return } if (selecting || e.shiftKey || e.metaKey || e.ctrlKey) { if (onSelect) { e.preventDefault(); onSelect(b, e) } } }}
      {...press.handlers}>
      <span className="tile-art book">
        <Cover b={b} w={220} />
        <StateMark b={b} />
        {s.favorite && <span className="mark fav" title="Favourite"><Icon name="heart" size={11} strokeWidth={2.6} /></span>}
        {showSeries && b.series_index > 0 && <span className="mark seq">{seriesIndex(b.series_index)}</span>}
        {onSelect && <span className={`tile-check ${selected ? 'on' : ''}`} role="checkbox" aria-checked={!!selected} aria-label={`Select ${b.title}`}
          onClick={e => { e.preventDefault(); e.stopPropagation(); onSelect(b, e) }}>{selected && <Icon name="check" size={13} strokeWidth={3} />}</span>}
        <Progress value={started ? s.percentage : 0} className="on-art" />
      </span>
      <span className="tile-title ellipsis2">{b.title}</span>
      <span className="tile-sub ellipsis">{sub ?? (authorsText(b, 2) || '—')}</span>
    </Link>
  )
}

/** Long-press (phones) to start selecting. */
function useLongPress(cb: (e: React.PointerEvent) => void) {
  const t = useRef(0)
  const fired = useRef(false)
  const start = useRef<{ x: number; y: number } | null>(null)
  return {
    fired: () => { const f = fired.current; fired.current = false; return f },
    handlers: {
      onPointerDown: (e: React.PointerEvent) => {
        if (e.pointerType !== 'touch') return
        fired.current = false
        start.current = { x: e.clientX, y: e.clientY }
        const ev = e
        t.current = window.setTimeout(() => { fired.current = true; navigator.vibrate?.(10); cb(ev) }, 480)
      },
      onPointerMove: (e: React.PointerEvent) => { if (start.current && Math.hypot(e.clientX - start.current.x, e.clientY - start.current.y) > 10) clearTimeout(t.current) },
      onPointerUp: () => clearTimeout(t.current),
      onPointerCancel: () => clearTimeout(t.current),
      onContextMenu: (e: React.MouseEvent) => { if (fired.current) e.preventDefault() },
    },
  }
}

/** A wide "continue" card: cover, title, progress, where it was last read, and Read. */
export function ContinueCard({ b }: { b: Book }) {
  const s = stateOf(b)
  return (
    <div className="cont-card">
      <Link to={`/book/${b.id}`} className="cont-art"><Cover b={b} w={120} /></Link>
      <div className="cont-body">
        <Link to={`/book/${b.id}`} className="cont-title ellipsis2">{b.title}</Link>
        <span className="muted small ellipsis">{authorsText(b, 2)}</span>
        <span className="cont-foot">
          <Progress value={s.percentage || 0.001} />
          <span className="muted xsmall tnum ellipsis">{pct(s.percentage)}%{s.last_device ? ` · on ${s.last_device}` : ''}</span>
        </span>
      </div>
      <Link to={`/read/${b.id}`} className="btn primary round cont-play" aria-label={`Continue reading ${b.title}`} title="Continue reading"><Icon name="book-open" size={17} /></Link>
    </div>
  )
}

/** A horizontal row of tiles with a heading and scroll arrows. */
export function Rail({ title, sub, to, children, wide, actions }: { title: React.ReactNode; sub?: React.ReactNode; to?: string; children: React.ReactNode; wide?: boolean; actions?: React.ReactNode }) {
  const row = useRef<HTMLDivElement>(null)
  const scroll = (dir: number) => row.current?.scrollBy({ left: dir * row.current.clientWidth * 0.85, behavior: 'smooth' })
  return (
    <section className="shelf">
      <div className="shelf-head">
        <div className="grow"><h2>{to ? <Link to={to}>{title}<Icon name="chevron-right" size={16} className="shelf-go" /></Link> : title}</h2>{sub && <p className="muted small">{sub}</p>}</div>
        {actions}
        <div className="shelf-arrows">
          <button type="button" className="btn ghost icon-only sm" aria-label="Scroll left" onClick={() => scroll(-1)}><Icon name="chevron-left" size={16} /></button>
          <button type="button" className="btn ghost icon-only sm" aria-label="Scroll right" onClick={() => scroll(1)}><Icon name="chevron-right" size={16} /></button>
        </div>
      </div>
      <div className={`shelf-row books ${wide ? 'wide' : ''}`} ref={row}>{children}</div>
    </section>
  )
}

/** A series as a little fan of its covers. */
export function SeriesTile({ s }: { s: Series }) {
  const covers = (s.covers || []).slice(0, 3)
  const done = s.books > 0 && s.read >= s.books
  return (
    <Link to={`/series/${s.id}`} className="tile" title={s.name}>
      <span className={`tile-art stack book n${Math.max(1, covers.length)}`}>
        {covers.length === 0 && <Cover b={{ title: s.name, cover: '', cover_color: '', authors: [] }} w={200} className="st0" />}
        {covers.map((c, i) => <Cover key={c + i} b={{ title: s.name, cover: c, cover_color: '', authors: [] }} w={200} className={`st${i}`} />)}
        {done && <span className="mark done" title="All read"><Icon name="check" size={12} strokeWidth={3} /></span>}
      </span>
      <span className="tile-title ellipsis">{s.name}</span>
      <span className="tile-sub ellipsis">{s.books} {s.books === 1 ? 'book' : 'books'}{s.read > 0 && !done ? ` · ${s.read} read` : done ? ' · all read' : ''}</span>
      {s.read > 0 && !done && <Progress value={s.read / s.books} className="thin series-prog" />}
    </Link>
  )
}

export function AuthorTile({ a }: { a: Author }) {
  return (
    <Link to={`/author/${a.id}`} className="tile person" title={a.name}>
      <span className="tile-art round"><Img src={a.photo} w={200} alt={a.name} round /></span>
      <span className="tile-title ellipsis center">{a.name}</span>
      <span className="tile-sub ellipsis center">{a.books ? `${a.books} ${a.books === 1 ? 'book' : 'books'}` : ''}</span>
    </Link>
  )
}

export function ShelfTile({ s }: { s: Shelf }) {
  const covers = (s.covers || []).slice(0, 3)
  return (
    <Link to={`/shelf/${s.id}`} className="tile shelf-tile" title={s.name}>
      <span className={`tile-art stack book n${Math.max(1, covers.length)}`}>
        {covers.length === 0 && <span className="shelf-empty-art st0" style={{ '--h': hue(s.name) } as React.CSSProperties}><Icon name={s.kind === 'smart' ? 'sparkles' : s.ordered ? 'list-ordered' : 'library'} size={26} /></span>}
        {covers.map((c, i) => <Cover key={c + i} b={{ title: s.name, cover: c, cover_color: '', authors: [] }} w={200} className={`st${i}`} />)}
      </span>
      <span className="tile-title ellipsis">{s.kind === 'smart' && <Icon name="sparkles" size={12} className="accent-ico" />} {s.name}</span>
      <span className="tile-sub ellipsis">{s.kind === 'smart' ? 'Smart shelf' : `${s.books} ${s.books === 1 ? 'book' : 'books'}`}{s.ordered ? ' · reading list' : ''}</span>
    </Link>
  )
}

/** A row in a list of books (series page, shelf page, list view). */
export function BookRow({ b, n, actions, selecting, selected, onSelect, handle }: { b: Book; n?: React.ReactNode; actions?: React.ReactNode; selecting?: boolean; selected?: boolean; onSelect?: (b: Book, e: React.MouseEvent) => void; handle?: React.ReactNode }) {
  const s = stateOf(b)
  return (
    <li className={`book-row ${s.status === 'read' ? 'done' : ''} ${selected ? 'selected' : ''}`} onClick={e => { if ((selecting || e.shiftKey || e.metaKey) && onSelect) { e.preventDefault(); onSelect(b, e) } }}>
      {handle}
      {onSelect && <span className={`row-check ${selected ? 'on' : ''}`} role="checkbox" aria-checked={!!selected} aria-label={`Select ${b.title}`} onClick={e => { e.preventDefault(); e.stopPropagation(); onSelect(b, e) }}>{selected && <Icon name="check" size={12} strokeWidth={3} />}</span>}
      {n !== undefined && <span className="br-n tnum">{n}</span>}
      <Link to={`/book/${b.id}`} className="br-art book"><Cover b={b} w={80} /></Link>
      <div className="br-body">
        <Link to={`/book/${b.id}`} className="br-title ellipsis">{b.title}</Link>
        <span className="muted small ellipsis">{[authorsText(b, 2), b.series ? `${b.series}${b.series_index ? ' ' + seriesIndex(b.series_index) : ''}` : ''].filter(Boolean).join(' · ')}</span>
        <span className="br-meta muted xsmall">
          {s.status === 'read' ? <span className="badge st-ok">Read</span> : s.status === 'dnf' ? <span className="badge">Didn’t finish</span> : s.percentage > 0 ? <><Progress value={s.percentage} /><span className="tnum">{pct(s.percentage)}%</span></> : s.status === 'want' ? <span className="badge accent">Want to read</span> : null}
          {s.rating > 0 && <Stars value={s.rating} size={12} />}
          <span className="br-formats">{b.formats.slice(0, 3).map(f => <span key={f} className="fmt">{f}</span>)}</span>
        </span>
      </div>
      <div className="br-actions">{actions}</div>
    </li>
  )
}

/** Placeholder tiles while a grid loads. */
export function SkeletonGrid({ n = 12 }: { n?: number }) {
  return <div className="grid-books">{Array.from({ length: n }, (_, i) => <div key={i} className="tile skel"><span className="tile-art book skel-box" /><span className="skel-line" /><span className="skel-line short" /></div>)}</div>
}
