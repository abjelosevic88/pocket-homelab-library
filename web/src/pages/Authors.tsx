// Every author with books you can open: photos, A–Z, search.
import { useMemo, useState } from 'react'
import { V1 } from '../lib/api'
import { usePaged } from '../lib/use'
import type { Author } from '../lib/types'
import { Icon } from '../components/icons'
import { AuthorTile } from '../components/cards'
import { Empty, PageHeader, useDebounce } from '../components/ui'

const LETTERS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'.split('')

export default function Authors() {
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 250)
  const paged = usePaged<Author>(off => `${V1}/authors?q=${encodeURIComponent(dq)}&offset=${off}&limit=200`, dq, undefined, 200)
  const letter = (a: Author) => { const c = (a.sort_name || a.name).trim().charAt(0).toUpperCase(); return LETTERS.includes(c) ? c : '#' }
  const groups = useMemo(() => {
    const m = new Map<string, Author[]>()
    for (const a of paged.items) { const l = letter(a); m.set(l, [...(m.get(l) || []), a]) }
    return m
  }, [paged.items])
  const jump = (l: string) => document.getElementById(`az-${l}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  return (
    <div>
      <PageHeader title="Authors" sub={paged.total >= 0 ? `${paged.total.toLocaleString()} authors` : undefined} />
      <label className="search-box big"><Icon name="search" size={17} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Find an author" aria-label="Find an author" /></label>
      {!dq && paged.items.length > 60 && (
        <nav className="az" aria-label="Jump to a letter">
          {[...LETTERS, '#'].map(l => <button key={l} type="button" disabled={!groups.has(l)} onClick={() => jump(l)}>{l}</button>)}
        </nav>
      )}
      {paged.error && <div className="callout danger"><Icon name="alert" size={15} />{paged.error}</div>}
      {!paged.busy && paged.total === 0 && <Empty title={dq ? 'No author by that name' : 'No authors yet'} icon="feather" />}
      {dq || paged.items.length <= 60 ? (
        <div className="grid-people">{paged.items.map(a => <AuthorTile key={a.id} a={a} />)}</div>
      ) : [...groups.entries()].map(([l, list]) => (
        <section key={l} id={`az-${l}`} className="section" style={{ scrollMarginTop: 70 }}>
          <h2 className="section-h">{l}</h2>
          <div className="grid-people">{list.map(a => <AuthorTile key={a.id} a={a} />)}</div>
        </section>
      ))}
      {paged.more && <div ref={paged.sentinel} className="more-sentinel">{paged.busy && <span className="spinner" />}</div>}
    </div>
  )
}
