// Edit a book's details (editors). Every field changed is locked, so the next
// scan keeps it; the lock beside a field shows that and can let go of it.
import { useMemo, useState } from 'react'
import { api, upload, V1 } from '../lib/api'
import type { BookDetail } from '../lib/types'
import { Icon } from './icons'
import { Cover } from './cards'
import { MetadataSearch } from './MetadataSearch'
import { Segmented, Spinner, Tabs, errMsg, useToast, Modal } from './ui'

type Form = {
  title: string; sort_title: string; subtitle: string; authors: string[]; description: string; language: string; publisher: string; published: string
  isbn: string; identifiers: Record<string, string>; series: string; series_index: string; tags: string[]; pages: string; kind: string; reading_direction: string; age_rating: string
}

function formOf(b: BookDetail): Form {
  return {
    title: b.title, sort_title: b.sort_title, subtitle: b.subtitle, authors: b.authors.map(a => a.name), description: b.description || '', language: b.language,
    publisher: b.publisher, published: b.published, isbn: b.isbn, identifiers: { ...b.identifiers }, series: b.series, series_index: b.series_index ? String(b.series_index) : '',
    tags: [...b.tags], pages: b.pages ? String(b.pages) : '', kind: b.kind, reading_direction: b.reading_direction, age_rating: b.age_rating,
  }
}

/** What each form field sends to the API. */
function value(f: Form, k: keyof Form): unknown {
  if (k === 'series_index') return Number(f.series_index) || 0
  if (k === 'pages') return Number(f.pages) || 0
  return f[k]
}

export function EditBookModal({ book, onClose, onSaved }: { book: BookDetail; onClose: () => void; onSaved: (b: BookDetail) => void }) {
  const { toast } = useToast()
  const [tab, setTab] = useState<'details' | 'find' | 'cover'>('details')
  const start = useMemo(() => formOf(book), [book])
  const [f, setF] = useState<Form>(start)
  const [locked, setLocked] = useState<Set<string>>(new Set(book.locked))
  const [busy, setBusy] = useState(false)
  const [b, setB] = useState(book)
  const changed = (Object.keys(f) as (keyof Form)[]).filter(k => JSON.stringify(f[k]) !== JSON.stringify(start[k]))
  const lockChanged = [...locked].sort().join() !== [...book.locked].sort().join()
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setF(x => ({ ...x, [k]: v }))
  const save = async () => {
    if (!f.title.trim()) { toast('A book needs a title', 'err'); return }
    setBusy(true)
    try {
      const body: Record<string, unknown> = {}
      for (const k of changed) body[k] = value(f, k)
      body.locked = [...new Set([...locked, ...changed])]
      const nb = await api.put<BookDetail>(`${V1}/books/${book.id}`, body)
      toast('Saved')
      onSaved(nb)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const Lock = ({ k }: { k: string }) => {
    const on = locked.has(k) || changed.includes(k as keyof Form)
    return <button type="button" className={`lock-btn ${on ? 'on' : ''}`} title={on ? 'Kept when the files are scanned again — click to let the scan update it' : 'Updated from the files on each scan — edit it (or click) to keep yours'}
      aria-label={on ? `Unlock ${k}` : `Lock ${k}`} onClick={() => setLocked(s => { const n = new Set(s); if (n.has(k)) n.delete(k); else n.add(k); return n })}><Icon name="lock" size={12} /></button>
  }
  const L = ({ k, label }: { k: string; label: string }) => <span className="label field-label-row">{label}<Lock k={k} /></span>
  return (
    <Modal title="Edit book" onClose={onClose} size="xl" footer={tab === 'details' ? <>
      <span className="muted small grow">{changed.length ? `${changed.length} ${changed.length === 1 ? 'change' : 'changes'} — kept on future scans` : 'Everyone on this box sees these details.'}</span>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" disabled={busy || (!changed.length && !lockChanged)} onClick={save}>{busy && <Spinner />}Save</button>
    </> : undefined}>
      <Tabs tabs={[{ id: 'details', label: 'Details', icon: 'edit' }, { id: 'find', label: 'Find online', icon: 'search' }, { id: 'cover', label: 'Cover', icon: 'book' }]} value={tab} onChange={setTab} />
      <div className="mt">
        {tab === 'details' && (
          <div className="edit-fields">
            <label className="field full"><L k="title" label="Title" /><input value={f.title} onChange={e => set('title', e.target.value)} autoFocus /></label>
            <label className="field"><L k="subtitle" label="Subtitle" /><input value={f.subtitle} onChange={e => set('subtitle', e.target.value)} /></label>
            <label className="field"><L k="sort_title" label="Sort as" /><input value={f.sort_title} onChange={e => set('sort_title', e.target.value)} placeholder="e.g. Hobbit, The" /></label>
            <div className="field full"><L k="authors" label="Authors" /><ChipsInput value={f.authors} onChange={v => set('authors', v)} placeholder="Add an author, then Enter" /></div>
            <label className="field"><L k="series" label="Series" /><input value={f.series} onChange={e => set('series', e.target.value)} /></label>
            <label className="field"><L k="series_index" label="Number in the series" /><input inputMode="decimal" value={f.series_index} onChange={e => set('series_index', e.target.value.replace(',', '.'))} placeholder="1, 2, 2.5…" /></label>
            <label className="field full"><L k="description" label="Description" /><textarea rows={6} value={f.description} onChange={e => set('description', e.target.value)} /></label>
            <div className="field full"><L k="tags" label="Tags" /><ChipsInput value={f.tags} onChange={v => set('tags', v)} placeholder="Add a tag, then Enter" /></div>
            <label className="field"><L k="publisher" label="Publisher" /><input value={f.publisher} onChange={e => set('publisher', e.target.value)} /></label>
            <label className="field"><L k="published" label="Published" /><input value={f.published} onChange={e => set('published', e.target.value)} placeholder="YYYY or YYYY-MM-DD" /></label>
            <label className="field"><L k="language" label="Language" /><input value={f.language} onChange={e => set('language', e.target.value)} placeholder="en, sr, de…" maxLength={8} /></label>
            <label className="field"><L k="isbn" label="ISBN" /><input value={f.isbn} onChange={e => set('isbn', e.target.value)} inputMode="numeric" /></label>
            <label className="field"><L k="pages" label="Pages" /><input value={f.pages} onChange={e => set('pages', e.target.value.replace(/\D/g, ''))} inputMode="numeric" /></label>
            <label className="field"><L k="age_rating" label="Age rating" /><input value={f.age_rating} onChange={e => set('age_rating', e.target.value)} /></label>
            <div className="field"><L k="kind" label="Kind" /><Segmented size="sm" value={f.kind} onChange={v => set('kind', v)} options={[{ value: 'book', label: 'Book' }, { value: 'comic', label: 'Comic' }, { value: 'manga', label: 'Manga' }]} /></div>
            <div className="field"><L k="reading_direction" label="Pages turn" /><Segmented size="sm" value={f.reading_direction} onChange={v => set('reading_direction', v)} options={[{ value: 'ltr', label: 'Left to right' }, { value: 'rtl', label: 'Right to left' }]} /></div>
            <div className="field full"><L k="identifiers" label="Identifiers" /><Identifiers value={f.identifiers} onChange={v => set('identifiers', v)} /></div>
          </div>
        )}
        {tab === 'find' && <MetadataSearch book={b} onApplied={nb => { setB(nb); setF(formOf(nb)); setLocked(new Set(nb.locked)); onSaved(nb) }} />}
        {tab === 'cover' && <CoverTab book={b} onChanged={nb => { setB(nb); onSaved(nb) }} />}
      </div>
    </Modal>
  )
}

/** A list of names as chips: Enter or comma adds, Backspace on empty removes the last. */
export function ChipsInput({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const [t, setT] = useState('')
  const add = (s: string) => { const v = s.trim(); if (v && !value.some(x => x.toLowerCase() === v.toLowerCase())) onChange([...value, v]); setT('') }
  return (
    <div className="chips-input">
      {value.map((v, i) => <span key={v + i} className="chip">{v}<button type="button" aria-label={`Remove ${v}`} onClick={() => onChange(value.filter((_, j) => j !== i))}><Icon name="x" size={11} /></button></span>)}
      <input value={t} placeholder={value.length ? '' : placeholder} onChange={e => { const v = e.target.value; if (v.includes(',')) { v.split(',').slice(0, -1).forEach(add); setT(v.split(',').pop() || '') } else setT(v) }}
        onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); add(t) } else if (e.key === 'Backspace' && !t && value.length) onChange(value.slice(0, -1)) }} onBlur={() => t && add(t)} />
    </div>
  )
}

function Identifiers({ value, onChange }: { value: Record<string, string>; onChange: (v: Record<string, string>) => void }) {
  const entries = Object.entries(value)
  const [k, setK] = useState('')
  const [v, setV] = useState('')
  return (
    <div className="stack">
      {entries.map(([key, val]) => (
        <div key={key} className="row"><span className="fmt" style={{ minWidth: 80, justifyContent: 'center' }}>{key}</span>
          <input className="input-sm" value={val} onChange={e => onChange({ ...value, [key]: e.target.value })} />
          <button type="button" className="btn ghost icon-only sm" aria-label={`Remove ${key}`} onClick={() => { const n = { ...value }; delete n[key]; onChange(n) }}><Icon name="x" size={13} /></button>
        </div>
      ))}
      <div className="row"><input className="input-sm" style={{ maxWidth: 140 }} value={k} onChange={e => setK(e.target.value.toLowerCase().replace(/\s/g, ''))} placeholder="goodreads, asin…" />
        <input className="input-sm" value={v} onChange={e => setV(e.target.value)} placeholder="value" />
        <button type="button" className="btn sm" disabled={!k || !v} onClick={() => { onChange({ ...value, [k]: v.trim() }); setK(''); setV('') }}><Icon name="plus" size={13} />Add</button></div>
    </div>
  )
}

function CoverTab({ book, onChanged }: { book: BookDetail; onChanged: (b: BookDetail) => void }) {
  const { toast } = useToast()
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const reload = async () => onChanged(await api.get<BookDetail>(`${V1}/books/${book.id}`))
  const run = async (fn: () => Promise<unknown>, msg: string) => {
    setBusy(true)
    try { await fn(); await reload(); toast(msg) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <div className="edit-grid">
      <div className="edit-cover"><div className="cover-box"><Cover key={book.cover} b={book} w={340} /></div></div>
      <div className="stack">
        <p className="muted small">A cover set here is kept when the files are scanned again. Downloads and books sent to devices carry it too.</p>
        <label className="btn" style={{ alignSelf: 'flex-start' }}><Icon name="upload" size={15} />{busy ? 'Working…' : 'Upload an image…'}
          <input type="file" accept="image/*" hidden onChange={e => { const file = e.target.files?.[0]; if (file) run(() => upload(`${V1}/books/${book.id}/cover`, 'file', file), 'Cover saved') }} /></label>
        <form className="row" onSubmit={e => { e.preventDefault(); if (url.trim()) run(() => api.post(`${V1}/books/${book.id}/cover`, { url: url.trim() }), 'Cover saved').then(() => setUrl('')) }}>
          <input value={url} onChange={e => setUrl(e.target.value)} placeholder="https://… image address" />
          <button className="btn" disabled={busy || !url.trim()}>Use</button>
        </form>
        {book.cover && <button type="button" className="btn ghost danger-text" style={{ alignSelf: 'flex-start' }} disabled={busy} onClick={() => run(() => api.del(`${V1}/books/${book.id}/cover`), 'Cover removed')}><Icon name="trash" size={14} />Remove the cover</button>}
        <p className="muted small">Looking for one? “Find online” brings covers from Open Library and Google Books.</p>
      </div>
    </div>
  )
}
