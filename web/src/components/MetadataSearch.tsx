// Find a book's details online (Open Library, Google Books) and take the fields you want.
import { useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'
import type { BookDetail, MetadataCandidate } from '../lib/types'
import { Icon } from './icons'
import { Spinner, errMsg, useToast } from './ui'

const FIELDS: { k: string; label: string; from: (c: MetadataCandidate) => string; cur: (b: BookDetail) => string }[] = [
  { k: 'title', label: 'Title', from: c => c.title, cur: b => b.title },
  { k: 'subtitle', label: 'Subtitle', from: c => c.subtitle, cur: b => b.subtitle },
  { k: 'authors', label: 'Authors', from: c => c.authors.join(', '), cur: b => b.authors.map(a => a.name).join(', ') },
  { k: 'description', label: 'Description', from: c => c.description, cur: b => b.description || '' },
  { k: 'cover', label: 'Cover', from: c => c.cover ? 'new cover' : '', cur: b => b.cover ? 'has one' : '' },
  { k: 'series', label: 'Series', from: c => c.series ? `${c.series}${c.series_index ? ' #' + c.series_index : ''}` : '', cur: b => b.series ? `${b.series}${b.series_index ? ' #' + b.series_index : ''}` : '' },
  { k: 'publisher', label: 'Publisher', from: c => c.publisher, cur: b => b.publisher },
  { k: 'published', label: 'Published', from: c => c.published, cur: b => b.published },
  { k: 'isbn', label: 'ISBN', from: c => c.isbn, cur: b => b.isbn },
  { k: 'language', label: 'Language', from: c => c.language, cur: b => b.language },
  { k: 'pages', label: 'Pages', from: c => c.pages ? String(c.pages) : '', cur: b => b.pages ? String(b.pages) : '' },
  { k: 'tags', label: 'Tags', from: c => c.tags.join(', '), cur: b => b.tags.join(', ') },
  { k: 'identifiers', label: 'Identifiers', from: c => Object.entries(c.identifiers || {}).map(([k, v]) => `${k}:${v}`).join(' '), cur: b => Object.keys(b.identifiers).join(', ') },
]

export function MetadataSearch({ book, onApplied }: { book: BookDetail; onApplied: (b: BookDetail) => void }) {
  const { toast } = useToast()
  const [q, setQ] = useState(`${book.title.replace(/^\d+[.)]\s*/, '')}${book.authors[0] ? ' ' + book.authors[0].name : ''}`)
  const [isbn, setIsbn] = useState(book.isbn)
  const [list, setList] = useState<MetadataCandidate[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [pick, setPick] = useState<MetadataCandidate | null>(null)
  const [fields, setFields] = useState<Set<string>>(new Set())
  const search = async () => {
    setBusy(true); setPick(null)
    try { setList(await api.get<MetadataCandidate[]>(`${V1}/books/${book.id}/metadata/search?q=${encodeURIComponent(q)}${isbn ? `&isbn=${encodeURIComponent(isbn)}` : ''}`)) } catch (e) { toast(errMsg(e), 'err'); setList([]) } finally { setBusy(false) }
  }
  useEffect(() => { search() }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const choose = (c: MetadataCandidate) => {
    setPick(c)
    // take what is new or missing; keep what was edited by hand unless it is empty
    setFields(new Set(FIELDS.filter(f => f.from(c) && (!f.cur(book) || (!book.locked.includes(f.k) && ['description', 'cover', 'isbn', 'publisher', 'published', 'pages', 'identifiers'].includes(f.k)))).map(f => f.k)))
  }
  const apply = async () => {
    if (!pick || !fields.size) return
    setBusy(true)
    try {
      await api.post(`${V1}/books/${book.id}/metadata/apply`, { candidate: pick, fields: [...fields] })
      const nb = await api.get<BookDetail>(`${V1}/books/${book.id}`)
      toast(`Took ${fields.size} ${fields.size === 1 ? 'field' : 'fields'} from ${pick.source === 'google' ? 'Google Books' : 'Open Library'}`)
      onApplied(nb)
      setPick(null)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  if (pick) {
    return (
      <div className="stack">
        <div className="row"><button type="button" className="btn ghost sm" onClick={() => setPick(null)}><Icon name="chevron-left" size={14} />Back to results</button><span className="muted small">Tick what to take. Taken fields are kept on future scans.</span></div>
        <div className="row" style={{ alignItems: 'flex-start', gap: 16 }}>
          {pick.cover && <img src={pick.cover} alt="" style={{ width: 110, borderRadius: 6, boxShadow: 'var(--shadow)' }} />}
          <div className="apply-fields grow">
            <span /><b className="muted xsmall">Now</b><b className="muted xsmall">{pick.source === 'google' ? 'Google Books' : 'Open Library'}</b>
            {FIELDS.filter(f => f.from(pick)).map(f => (
              <span key={f.k} style={{ display: 'contents' }}>
                <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={fields.has(f.k)} onChange={() => setFields(s => { const n = new Set(s); if (n.has(f.k)) n.delete(f.k); else n.add(f.k); return n })} />{f.label}</label>
                <span className="cell cur">{f.cur(book) || '—'}</span>
                <span className="cell new">{f.from(pick)}</span>
              </span>
            ))}
          </div>
        </div>
        <div className="row"><span style={{ flex: 1 }} /><button type="button" className="btn primary" disabled={busy || !fields.size} onClick={apply}>{busy && <Spinner />}Take {fields.size} {fields.size === 1 ? 'field' : 'fields'}</button></div>
      </div>
    )
  }
  return (
    <div className="stack">
      <form className="row wrap" onSubmit={e => { e.preventDefault(); search() }}>
        <input value={q} onChange={e => setQ(e.target.value)} placeholder="Title and author" style={{ flex: '2 1 240px' }} />
        <input value={isbn} onChange={e => setIsbn(e.target.value)} placeholder="ISBN" style={{ flex: '1 1 120px' }} />
        <button className="btn primary" disabled={busy}>{busy ? <Spinner /> : <Icon name="search" size={15} />}Search</button>
      </form>
      {list && list.length === 0 && !busy && <p className="muted small">Nothing found. Try fewer words, the original title, or the ISBN.</p>}
      <ul className="cand-list">
        {(list || []).map((c, i) => (
          <li key={c.source + c.id + i}>
            <button type="button" className="cand" onClick={() => choose(c)}>
              <span className="cand-art">{c.cover && <img src={c.cover} alt="" loading="lazy" />}</span>
              <span className="cand-body">
                <b>{c.title}{c.subtitle ? `: ${c.subtitle}` : ''}</b>
                <span className="muted">{c.authors.join(', ')}</span>
                <span className="muted xsmall">{[c.published, c.publisher, c.language, c.pages ? `${c.pages} pages` : '', c.isbn].filter(Boolean).join(' · ')}</span>
                <span className="xsmall"><span className="fmt">{c.source === 'google' ? 'Google Books' : 'Open Library'}</span></span>
              </span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}
