// One book: cover, what it is, where you stand with it, read / send / download,
// its series and author, your highlights and reading history.
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useApi } from '../lib/use'
import type { Annotation, BookDetail, ReadStatus, ReadingSession, UserBook } from '../lib/types'
import { READABLE, formatLabel, hm, identifierLink, pct, readingTime, seriesIndex, stateOf, todayISO } from '../lib/books'
import { fmtBytes, fmtDate, fmtDateTime } from '../lib/format'
import { Icon } from '../components/icons'
import { BookCard, Cover, Progress, Rail, Stars } from '../components/cards'
import { useCrumb } from '../components/Layout'
import { type Anchor, Confirm, Loading, MenuButton, type MenuItem, Modal, Popover, download, errMsg, rectOf, useToast } from '../components/ui'
import { SendButton } from '../components/SendButton'
import { ShelfPicker } from '../components/ShelfPicker'
import { EditBookModal } from '../components/EditBookModal'
import { sidebarChanged } from '../components/Sidebar'
import { langName } from '../components/FilterBar'

const PILLS: { s: ReadStatus; label: string; icon: string }[] = [
  { s: 'want', label: 'Want to read', icon: 'bookmark-plus' },
  { s: 'reading', label: 'Reading', icon: 'book-open' },
  { s: 'read', label: 'Read', icon: 'check' },
  { s: 'dnf', label: 'Didn’t finish', icon: 'x' },
]

export default function BookPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const { settings, user } = useApp()
  const { data, setData, error, reload } = useApi<BookDetail>(`${V1}/books/${encodeURIComponent(id)}`)
  const [more, setMore] = useState(false)
  const [editing, setEditing] = useState(false)
  const [finishAt, setFinishAt] = useState<Anchor | null>(null)
  const [deleting, setDeleting] = useState(false)
  useCrumb(data?.title)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const b = data
  const s = stateOf(b)
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const files = b.files.filter(f => !f.missing)
  const readable = files.filter(f => READABLE.has(f.format))
  const started = s.percentage > 0 && s.status !== 'read'

  const setState = async (patch: Partial<{ status: ReadStatus; rating: number; favorite: boolean; hidden: boolean; day: string }>, msg?: string) => {
    try {
      const ub = await api.put<UserBook>(`${V1}/books/${b.id}/state`, patch)
      setData({ ...b, state: ub })
      if (patch.status !== undefined) { sidebarChanged(); if (patch.status === 'read') reload() }
      if (msg) toast(msg)
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const pill = (p: ReadStatus, e: React.MouseEvent<HTMLButtonElement>) => {
    if (s.status === p) { setState({ status: '' }, 'Status cleared'); return }
    if (p === 'read') { setFinishAt(rectOf(e.currentTarget)); return }
    setState({ status: p }, p === 'reading' ? 'On your Reading list' : p === 'want' ? 'Added to Want to read' : 'Marked as not finished')
  }
  const downloads = (): MenuItem[] => {
    const items: MenuItem[] = files.map(f => ({ label: <span className="dev-item"><b>{formatLabel(f.format)}</b><span className="muted xsmall ellipsis">{fmtBytes(f.size)} · {f.name}</span></span>, icon: 'download', onSelect: () => download(`${V1}/books/${b.id}/download?file=${f.id}`) }))
    const have = new Set(files.map(f => f.format))
    const conv: MenuItem[] = []
    const epub = files.find(f => f.format === 'epub')
    if (epub && !have.has('kepub')) conv.push({ label: 'KEPUB (for Kobo)', icon: 'refresh', onSelect: () => download(`${V1}/books/${b.id}/download?file=${epub.id}&format=kepub`) })
    if (settings?.convert_available && files.length) {
      const src = files.find(f => ['epub', 'azw3', 'mobi', 'azw', 'fb2', 'fbz', 'docx', 'rtf', 'txt'].includes(f.format))
      if (src) for (const t of ['epub', 'azw3', 'mobi', 'pdf']) if (!have.has(t)) conv.push({ label: `${formatLabel(t)} (converted)`, icon: 'refresh', onSelect: () => download(`${V1}/books/${b.id}/download?file=${src.id}&format=${t}`) })
    }
    if (conv.length) items.push('sep', { heading: 'Convert' }, ...conv)
    return items
  }
  const menu = (): MenuItem[] => [
    ...(started || s.status === 'read' ? [{ label: 'Start over', icon: 'rotate-ccw', onSelect: () => setState({ status: 'reading' }, 'Starting again from the beginning') }] : []),
    { label: s.hidden ? 'Show in my library again' : 'Hide from my library', icon: s.hidden ? 'eye' : 'eye-off', onSelect: () => setState({ hidden: !s.hidden }, s.hidden ? 'Shown again' : 'Hidden from Home and browsing') },
    { label: 'Highlights & notes', icon: 'note', onSelect: () => navigate(`/highlights?book=${b.id}`) },
    ...(editor ? ['sep' as const,
      { label: 'Edit details', icon: 'edit', onSelect: () => setEditing(true) },
      { label: 'Read the files again', icon: 'refresh', onSelect: async () => { try { await api.post(`${V1}/books/${b.id}/rescan`); await reload(); toast('Read again from its files') } catch (e) { toast(errMsg(e), 'err') } } },
      { label: 'Delete book and files…', icon: 'trash', danger: true, onSelect: () => setDeleting(true) }] : []),
  ]
  const readLabel = s.status === 'read' ? 'Read again' : started ? `Continue · ${pct(s.percentage)}%` : 'Start reading'
  const glow = b.cover_color || undefined

  return (
    <div className="book-page" style={glow ? { '--glow': glow } as React.CSSProperties : undefined}>
      <div className="glow" />
      <section className="book-hero">
        <div className="book-cover book"><Cover b={b} w={460} /></div>
        <div className="book-text">
          {b.series && <Link className="eyebrow link" to={`/series/${b.series_id}`}><Icon name="layers" size={12} />{b.series}{b.series_index ? ` · Book ${seriesIndex(b.series_index).slice(1)}` : ''}</Link>}
          <h1 className="book-title">{b.title}</h1>
          {b.subtitle && <p className="book-subtitle">{b.subtitle}</p>}
          {b.authors.length > 0 && <p className="book-by">by {b.authors.map((a, i) => <span key={a.id}>{i > 0 && (i === b.authors.length - 1 ? ' and ' : ', ')}<Link to={`/author/${a.id}`}>{a.name}</Link></span>)}</p>}
          <p className="book-meta muted">
            {b.published && <span>{b.published.slice(0, 4)}</span>}
            {b.pages > 0 && <span><Icon name="book" size={13} />{b.pages.toLocaleString()} {b.kind === 'book' ? 'pages' : 'pages'}</span>}
            {readingTime(b) && b.kind === 'book' && <span><Icon name="clock" size={13} />{readingTime(b)}</span>}
            {b.language && <span><Icon name="globe" size={13} />{langName(b.language)}</span>}
            {b.formats.map(f => <span key={f} className="fmt">{f}</span>)}
          </p>
          <div className="book-rating">
            <Stars value={s.rating} onChange={v => setState({ rating: v }, v ? `Rated ${v / 2} ★` : 'Rating cleared')} size={22} />
            {s.rating > 0 && <span className="muted">{s.rating / 2} / 5</span>}
            <button type="button" className={`fav-btn ${s.favorite ? 'on' : ''}`} aria-pressed={s.favorite} aria-label={s.favorite ? 'Favourite' : 'Make a favourite'} title={s.favorite ? 'Favourite' : 'Make a favourite'} onClick={() => setState({ favorite: !s.favorite })}><Icon name="heart" size={15} /></button>
          </div>
          <div className="book-status" role="group" aria-label="Status">
            {PILLS.map(p => (
              <button key={p.s} type="button" className={`status-pill ${s.status === p.s ? `on ${p.s}` : ''}`} aria-pressed={s.status === p.s} onClick={e => pill(p.s, e)}>
                <Icon name={p.icon as never} size={13} />{p.label}{p.s === 'read' && s.status === 'read' && s.finished_at ? ` · ${fmtDate(s.finished_at.slice(0, 10), false)}` : ''}
              </button>
            ))}
          </div>
          {started && (
            <div className="book-progress">
              <Progress value={s.percentage} />
              <span className="small muted tnum">{pct(s.percentage)}%{s.last_device ? ` · last on ${s.last_device}` : ''}{s.last_read_at ? ` · ${fmtDateTime(s.last_read_at)}` : ''}</span>
            </div>
          )}
          <div className="book-actions">
            {readable.length > 0 && <Link to={`/read/${b.id}`} className="btn primary lg"><Icon name="book-open" size={16} />{readLabel}</Link>}
            {readable.length > 1 && <MenuButton label="Read another format" className="btn lg icon-only" items={readable.map(f => ({ label: `${formatLabel(f.format)} · ${fmtBytes(f.size)}`, icon: 'book-open', onSelect: () => navigate(`/read/${b.id}?file=${f.id}`) }))}><Icon name="chevron-down" size={16} /></MenuButton>}
            {b.sendable && <SendButton bookId={b.id} />}
            {files.length > 0 && <MenuButton label="Download" className="btn lg" items={downloads} width={280}><Icon name="download" size={16} />Download</MenuButton>}
            <ShelfPicker bookIds={[b.id]} on={b.shelves} onChange={ids => setData({ ...b, shelves: ids })} />
            <MenuButton label="More" className="btn ghost icon-only lg" items={menu} align="right"><Icon name="more" size={18} /></MenuButton>
          </div>
          {b.missing && <div className="callout warn small"><Icon name="alert" size={14} />Its files are gone from the library folder. Your progress and highlights stay.</div>}
          {b.tags.length > 0 && <p className="book-genres">{b.tags.slice(0, 14).map(t => <Link key={t} className="chip" to={`/library?tag=${encodeURIComponent(t)}`}>{t}</Link>)}</p>}
        </div>
      </section>

      <div className="book-cols">
        <div className="book-main">
          {b.description && (
            <section className="section">
              <h2 className="section-h">About</h2>
              <Description text={b.description} more={more} />
              {b.description.length > 500 && <button type="button" className="link-btn small" onClick={() => setMore(m => !m)}>{more ? 'Show less' : 'Show more'}</button>}
            </section>
          )}
          {!b.description && editor && <section className="section"><p className="muted small">No description yet. <button type="button" className="link-btn" onClick={() => setEditing(true)}>Find it online or write one</button>.</p></section>}
          {b.series_books.length > 1 && (
            <Rail title={b.series} to={`/series/${b.series_id}`} sub={`${b.series_books.length} books`}>
              {b.series_books.map(x => <span key={x.id} className={x.id === b.id ? 'tile-current' : ''}><BookCard b={x} showSeries sub={x.id === b.id ? 'This book' : undefined} /></span>)}
            </Rail>
          )}
          {b.more_by_author.length > 0 && b.authors[0] && <Rail title={`More by ${b.authors[0].name}`} to={`/author/${b.authors[0].id}`}>{b.more_by_author.map(x => <BookCard key={x.id} b={x} />)}</Rail>}
        </div>
        <aside className="book-side">
          <Highlights bookId={b.id} count={b.annotations} />
          <History b={b} onChange={reload} />
          {b.bookshelf && <BookshelfCard bs={b.bookshelf} />}
          <section className="card side-card">
            <h3>Details</h3>
            <dl className="facts">
              {b.publisher && <><dt>Publisher</dt><dd><Link to={`/library?publisher=${encodeURIComponent(b.publisher)}`}>{b.publisher}</Link></dd></>}
              {b.published && <><dt>Published</dt><dd>{b.published.length === 10 ? fmtDate(b.published) : b.published}</dd></>}
              {b.language && <><dt>Language</dt><dd>{langName(b.language)}</dd></>}
              {b.pages > 0 && <><dt>Length</dt><dd>{b.pages.toLocaleString()} pages{b.words ? ` · ${b.words.toLocaleString()} words` : ''}</dd></>}
              {b.isbn && <><dt>ISBN</dt><dd><a href={identifierLink('isbn', b.isbn)} target="_blank" rel="noreferrer">{b.isbn}</a></dd></>}
              {Object.entries(b.identifiers).filter(([k]) => !['isbn', 'uuid', 'calibre'].includes(k)).map(([k, v]) => {
                const href = identifierLink(k, v)
                return <span key={k} style={{ display: 'contents' }}><dt>{idName(k)}</dt><dd>{href ? <a href={href} target="_blank" rel="noreferrer">{v}</a> : <span className="mono small">{v}</span>}</dd></span>
              })}
              {b.age_rating && <><dt>Age rating</dt><dd>{b.age_rating}</dd></>}
              {b.library && <><dt>Library</dt><dd><Link to={`/library?library=${b.library.id}`}>{b.library.name}</Link></dd></>}
              <dt>Added</dt><dd>{fmtDate(b.added_at.slice(0, 10))}</dd>
            </dl>
          </section>
          {b.files.length > 0 && (
            <section className="card side-card">
              <h3>Files</h3>
              <ul className="files-list">
                {b.files.map(f => (
                  <li key={f.id} title={f.name}>
                    <span className="fmt">{f.format}</span>
                    <span className="name">{f.missing ? <s>{f.name}</s> : f.name}</span>
                    <span className="muted xsmall tnum">{fmtBytes(f.size)}</span>
                    {!f.missing && <a className="btn ghost icon-only xs" href={`${V1}/books/${b.id}/download?file=${f.id}`} download aria-label={`Download ${formatLabel(f.format)}`}><Icon name="download" size={13} /></a>}
                  </li>
                ))}
              </ul>
            </section>
          )}
        </aside>
      </div>

      {finishAt && <FinishPopover anchor={finishAt} onClose={() => setFinishAt(null)} onDone={day => { setFinishAt(null); setState({ status: 'read', day }, 'Marked as read') }} />}
      {editing && <EditBookModal book={b} onClose={() => setEditing(false)} onSaved={nb => { setEditing(false); setData(nb); reload() }} />}
      {deleting && <DeleteBook b={b} onClose={() => setDeleting(false)} onDeleted={() => { setDeleting(false); toast('Deleted'); navigate('/library') }} />}
    </div>
  )
}

function idName(k: string): string {
  return ({ goodreads: 'Goodreads', amazon: 'Amazon', asin: 'ASIN', 'mobi-asin': 'ASIN', google: 'Google Books', openlibrary: 'Open Library', hardcover: 'Hardcover', doi: 'DOI' } as Record<string, string>)[k] || k
}

/** Descriptions come as text or light HTML from the book. */
function Description({ text, more }: { text: string; more: boolean }) {
  const html = /<\/?(p|br|div|i|em|b|strong|ul|li)\b/i.test(text)
  if (html) return <div className={`book-desc html desc-html ${more ? '' : 'clamp'}`} dangerouslySetInnerHTML={{ __html: sanitize(text) }} />
  return <div className={`book-desc ${more ? '' : 'clamp'}`}>{text.split(/\n{2,}/).map((p, i) => <p key={i}>{p}</p>)}</div>
}

/** Keeps a few harmless tags of a description, drops everything else. */
function sanitize(html: string): string {
  const doc = new DOMParser().parseFromString(html, 'text/html')
  const ok = new Set(['P', 'BR', 'I', 'EM', 'B', 'STRONG', 'UL', 'OL', 'LI', 'BLOCKQUOTE'])
  const walk = (n: Node): string => {
    if (n.nodeType === Node.TEXT_NODE) return (n.textContent || '').replace(/[&<>]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' } as Record<string, string>)[c])
    if (n.nodeType !== Node.ELEMENT_NODE) return ''
    const el = n as Element
    const inner = [...el.childNodes].map(walk).join('')
    if (!ok.has(el.tagName)) return inner + (el.tagName === 'DIV' ? '<br>' : '')
    const t = el.tagName.toLowerCase()
    return t === 'br' ? '<br>' : `<${t}>${inner}</${t}>`
  }
  return [...doc.body.childNodes].map(walk).join('')
}

function FinishPopover({ anchor, onClose, onDone }: { anchor: Anchor; onClose: () => void; onDone: (day: string) => void }) {
  const [day, setDay] = useState(todayISO())
  return (
    <Popover anchor={anchor} onClose={onClose} prefer="below" width={260} label="Finished on">
      <form className="pop-pad" onSubmit={e => { e.preventDefault(); onDone(day) }}>
        <h4>When did you finish it?</h4>
        <input type="date" value={day} max={todayISO()} onChange={e => setDay(e.target.value)} autoFocus />
        <div className="row"><button type="button" className="btn sm" onClick={() => onDone(todayISO())}>Today</button><span className="spacer" style={{ flex: 1 }} /><button className="btn primary sm">Mark as read</button></div>
      </form>
    </Popover>
  )
}

const COLOR_CLASS = (c: string) => `hl-${c || 'yellow'}`

function Highlights({ bookId, count }: { bookId: number; count: number }) {
  const [list, setList] = useState<Annotation[] | null>(null)
  useEffect(() => { if (count > 0) api.get<Annotation[]>(`${V1}/annotations?book=${bookId}&limit=50`).then(setList).catch(() => setList([])) }, [bookId, count])
  if (!count) return null
  const latest = (list || []).filter(a => a.text || a.note).sort((a, b) => b.created_at.localeCompare(a.created_at)).slice(0, 3)
  return (
    <section className="card side-card">
      <h3>Your highlights <span className="muted small">{count}</span></h3>
      <ul className="ann-mini">
        {latest.map(a => <li key={a.id} className={COLOR_CLASS(a.color)}>{a.text ? <span className="ellipsis2" style={{ display: '-webkit-box' }}>“{a.text}”</span> : <i className="muted">Bookmark{a.chapter ? ` · ${a.chapter}` : ''}</i>}{a.note && <div className="note">{a.note}</div>}</li>)}
      </ul>
      <div className="row mt-sm"><Link to={`/highlights?book=${bookId}`} className="small">See all</Link><span style={{ flex: 1 }} /><a className="small" href={`${V1}/annotations/export?book=${bookId}&format=md`} download>Export</a></div>
    </section>
  )
}

function History({ b, onChange }: { b: BookDetail; onChange: () => void }) {
  const { toast } = useToast()
  const [sessions, setSessions] = useState<ReadingSession[] | null>(null)
  const [del, setDel] = useState<number | null>(null)
  useEffect(() => { api.get<ReadingSession[]>(`${V1}/sessions?book=${b.id}&limit=500`).then(setSessions).catch(() => setSessions([])) }, [b.id])
  const s = stateOf(b)
  const secs = (sessions || []).reduce((a, x) => a + x.seconds, 0)
  const days = new Set((sessions || []).map(x => x.day)).size
  const devices = [...new Set((sessions || []).map(x => x.device).filter(Boolean))]
  if (!b.reads.length && !(sessions && sessions.length) && !s.started_at) return null
  return (
    <section className="card side-card">
      <h3>Your reading</h3>
      <dl className="facts">
        {secs > 0 && <><dt>Time</dt><dd>{hm(secs / 60)} on {days} {days === 1 ? 'day' : 'days'}{(sessions || []).some(x => x.estimated) ? ' (partly estimated)' : ''}</dd></>}
        {s.started_at && s.status === 'reading' && <><dt>Started</dt><dd>{fmtDate(s.started_at.slice(0, 10))}</dd></>}
        {devices.length > 0 && <><dt>On</dt><dd>{devices.join(', ')}</dd></>}
        {s.times_read > 1 && <><dt>Read</dt><dd>{s.times_read} times</dd></>}
      </dl>
      {b.reads.length > 0 && (
        <ul className="reads-list mt-sm">
          {b.reads.map(r => (
            <li key={r.id}><Icon name={r.status === 'read' ? 'check-circle' : 'x-circle'} size={14} className={r.status === 'read' ? 'ok-text' : 'muted'} />
              <span className="grow">{r.status === 'read' ? 'Finished' : 'Stopped'} {fmtDate(r.day)}{r.started_at ? <span className="muted"> · started {fmtDate(r.started_at.slice(0, 10), false)}</span> : null}</span>
              <button type="button" className="btn ghost icon-only xs" aria-label="Remove this read" onClick={() => setDel(r.id)}><Icon name="x" size={12} /></button>
            </li>
          ))}
        </ul>
      )}
      {del !== null && <Confirm title="Remove this read?" message="It leaves your reading history and stats. The book's status stays as it is." confirmLabel="Remove" onCancel={() => setDel(null)}
        onConfirm={async () => { const d = del; setDel(null); try { await api.del(`${V1}/reads/${d}`); onChange(); toast('Removed') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </section>
  )
}

function BookshelfCard({ bs }: { bs: NonNullable<BookDetail['bookshelf']> }) {
  return (
    <section className="card side-card bookshelf-card">
      <h3><Icon name="library" size={14} />Pocket Bookshelf</h3>
      {bs.available ? (
        <>
          <p className="small" style={{ margin: '0 0 8px' }}>{bs.status ? `On your Bookshelf as “${bs.status}”` : 'On your Bookshelf'}{bs.rating ? '' : '.'}</p>
          {bs.rating ? <Stars value={bs.rating} size={14} /> : null}
          {bs.url && <div className="mt-sm"><a className="small" href={bs.url} target="_blank" rel="noreferrer">Open in Pocket Bookshelf <Icon name="external" size={11} /></a></div>}
        </>
      ) : (
        <p className="small muted" style={{ margin: 0 }}>Not on your Bookshelf yet.{bs.url && <> <a href={bs.url} target="_blank" rel="noreferrer">Open Pocket Bookshelf</a></>}</p>
      )}
    </section>
  )
}

function DeleteBook({ b, onClose, onDeleted }: { b: BookDetail; onClose: () => void; onDeleted: () => void }) {
  const { toast } = useToast()
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const go = async () => {
    setBusy(true)
    try { await api.del(`${V1}/books/${b.id}?files=1`); sidebarChanged(); onDeleted() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Delete this book?" onClose={onClose} size="sm" footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn danger" disabled={busy || typed.trim().toLowerCase() !== b.title.trim().toLowerCase()} onClick={go}>Delete for everyone</button></>}>
      <div className="stack">
        <p>Its {b.files.length === 1 ? 'file is' : `${b.files.length} files are`} deleted from the library folder, and it goes away for everyone on this box, with their progress and highlights in it.</p>
        <ul className="small muted" style={{ margin: 0 }}>{b.files.map(f => <li key={f.id}>{f.name}</li>)}</ul>
        <label className="field"><span className="label">Type the title to confirm: <b>{b.title}</b></span><input value={typed} onChange={e => setTyped(e.target.value)} autoFocus /></label>
      </div>
    </Modal>
  )
}
