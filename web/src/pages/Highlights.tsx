// Every highlight, note and bookmark across your books: search, filter, edit, jump back in.
import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApi } from '../lib/use'
import type { Annotation, BookDetail } from '../lib/types'
import { fmtDate } from '../lib/format'
import { Icon } from '../components/icons'
import { Cover } from '../components/cards'
import { Confirm, Empty, Loading, PageHeader, Segmented, errMsg, useDebounce, useToast } from '../components/ui'

const COLORS = ['yellow', 'green', 'blue', 'pink', 'purple', 'underline']
const SOURCE: Record<string, string> = { web: 'Web reader', koreader: 'KOReader', kindle: 'Kindle', crosspoint: 'CrossPoint', import: 'Imported' }

export default function Highlights() {
  const [sp, setSp] = useSearchParams()
  const book = sp.get('book') || ''
  const [q, setQ] = useState(sp.get('q') || '')
  const dq = useDebounce(q, 250)
  const [kind, setKind] = useState<'' | 'highlight' | 'note' | 'bookmark'>('')
  const [color, setColor] = useState('')
  const url = `${V1}/annotations?limit=1000${book ? `&book=${book}` : ''}${kind ? `&kind=${kind}` : ''}${dq ? `&q=${encodeURIComponent(dq)}` : ''}`
  const { data, setData, error } = useApi<Annotation[]>(url)
  const [bookInfo, setBookInfo] = useState<BookDetail | null>(null)
  useEffect(() => { if (book) api.get<BookDetail>(`${V1}/books/${book}`).then(setBookInfo).catch(() => setBookInfo(null)); else setBookInfo(null) }, [book])
  const list = (data || []).filter(a => !color || a.color === color)
  const groups = useMemo(() => {
    const m = new Map<number, Annotation[]>()
    for (const a of list) m.set(a.book_id, [...(m.get(a.book_id) || []), a])
    return [...m.entries()]
  }, [list])
  const update = (a: Annotation) => setData((data || []).map(x => x.id === a.id ? a : x))
  const remove = (id: number) => setData((data || []).filter(x => x.id !== id))
  return (
    <div>
      <PageHeader title={bookInfo ? <>Highlights · {bookInfo.title}</> : 'Highlights'} sub={data ? `${list.length.toLocaleString()} ${list.length === 1 ? 'note' : 'notes and highlights'}` : undefined}
        actions={<div className="row wrap">
          {book && <button type="button" className="btn ghost" onClick={() => { const n = new URLSearchParams(sp); n.delete('book'); setSp(n) }}><Icon name="x" size={14} />All books</button>}
          {book && <a className="btn" href={`${V1}/annotations/export?book=${book}&format=md`} download><Icon name="download" size={15} />Export</a>}
        </div>} />
      <div className="filter-row" style={{ marginBottom: 18 }}>
        <label className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search your highlights and notes" aria-label="Search highlights" /></label>
        <Segmented size="sm" value={kind} onChange={setKind} label="Kind" options={[{ value: '', label: 'All' }, { value: 'highlight', label: 'Highlights' }, { value: 'note', label: 'Notes' }, { value: 'bookmark', label: 'Bookmarks' }]} />
        <span className="color-dots" role="radiogroup" aria-label="Colour">
          {COLORS.map(c => <button key={c} type="button" className={`color-dot hl-${c} ${color === c ? 'on' : ''}`} style={{ background: c === 'underline' ? 'transparent' : 'var(--hl)', borderBottom: c === 'underline' ? '2px solid var(--text-2)' : undefined }} aria-label={c} title={c} onClick={() => setColor(color === c ? '' : c)} />)}
        </span>
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!data ? <Loading /> : list.length === 0 ? (
        <Empty title={dq || kind || color ? 'Nothing matches' : 'No highlights yet'} icon="note">
          {dq || kind || color ? null : 'Select text in the reader to highlight it or add a note. Highlights from KOReader (the Pocket Library plugin), CrossPoint and your Kindle’s My Clippings.txt (Settings → Import) land here too.'}
        </Empty>
      ) : groups.map(([bid, items]) => (
        <section key={bid} className="hl-group">
          {!book && (
            <div className="hl-head">
              <Link to={`/book/${bid}`}><Cover b={{ title: items[0].book_title || '', cover: items[0].book_cover || '', cover_color: '', authors: [] }} w={60} /></Link>
              <div className="grow"><Link to={`/book/${bid}`}>{items[0].book_title}</Link><div className="muted small">{items.length} {items.length === 1 ? 'note' : 'notes'}</div></div>
              <a className="btn ghost sm" href={`${V1}/annotations/export?book=${bid}&format=md`} download title="Export as Markdown"><Icon name="download" size={14} /></a>
            </div>
          )}
          <ul className="hl-list">{items.map(a => <Item key={a.id} a={a} onChange={update} onDelete={() => remove(a.id)} />)}</ul>
        </section>
      ))}
    </div>
  )
}

function Item({ a, onChange, onDelete }: { a: Annotation; onChange: (a: Annotation) => void; onDelete: () => void }) {
  const { toast } = useToast()
  const [editing, setEditing] = useState(false)
  const [note, setNote] = useState(a.note)
  const [del, setDel] = useState(false)
  const save = async (patch: Partial<Annotation>) => {
    try { const r = await api.put<Annotation>(`${V1}/annotations/${a.id}`, patch); onChange({ ...a, ...(r && r.id ? r : patch) }) } catch (e) { toast(errMsg(e), 'err') }
  }
  const where = [a.chapter, a.page ? `p. ${a.page}` : a.percentage ? `${Math.round(a.percentage * 100)}%` : ''].filter(Boolean).join(' · ')
  return (
    <li className={`hl hl-${a.color || 'yellow'}`}>
      {a.text ? <p className="hl-text">{a.text}</p> : <p className="hl-text muted"><Icon name="bookmark" size={14} /> Bookmark{a.chapter ? ` · ${a.chapter}` : ''}</p>}
      {editing ? (
        <form className="stack mt-sm" onSubmit={e => { e.preventDefault(); save({ note }); setEditing(false) }}>
          <textarea rows={3} value={note} onChange={e => setNote(e.target.value)} autoFocus placeholder="Your note" />
          <div className="row"><button type="button" className="btn sm" onClick={() => { setEditing(false); setNote(a.note) }}>Cancel</button><button className="btn primary sm">Save note</button></div>
        </form>
      ) : a.note && <p className="hl-note"><Icon name="note" size={14} />{a.note}</p>}
      <div className="hl-meta">
        <span>{where}</span>
        <span>{fmtDate(a.created_at.slice(0, 10))}</span>
        <span>{SOURCE[a.source] || a.source}</span>
        <span className="spacer" />
        {a.kind !== 'bookmark' && <span className="color-dots">{COLORS.slice(0, 5).map(c => <button key={c} type="button" className={`color-dot hl-${c} ${a.color === c ? 'on' : ''}`} style={{ background: 'var(--hl)' }} aria-label={`Colour ${c}`} onClick={() => save({ color: c })} />)}</span>}
        <button type="button" className="btn ghost sm" onClick={() => setEditing(true)}><Icon name="edit" size={12} />{a.note ? 'Edit note' : 'Add note'}</button>
        <Link className="btn ghost sm" to={`/read/${a.book_id}?ann=${a.id}`}><Icon name="book-open" size={12} />Open</Link>
        <button type="button" className="btn ghost icon-only sm danger-text" aria-label="Delete" onClick={() => setDel(true)}><Icon name="trash" size={12} /></button>
      </div>
      {del && <Confirm title="Delete this highlight?" message={a.source === 'koreader' ? 'If KOReader still has it, the next sync from the plugin brings it back — delete it there too.' : 'It is gone for good.'} confirmLabel="Delete" onCancel={() => setDel(false)}
        onConfirm={async () => { setDel(false); try { await api.del(`${V1}/annotations/${a.id}`); onDelete() } catch (e) { toast(errMsg(e), 'err') } }} />}
    </li>
  )
}
