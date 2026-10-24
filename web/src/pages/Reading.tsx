// Your reading: what you're in, what you want to read, what you finished (by
// year), what you gave up on, your favourites.
import { useMemo } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useApi } from '../lib/use'
import type { Book, Page } from '../lib/types'
import { authorsText, stateOf } from '../lib/books'
import { fmtDate } from '../lib/format'
import { Icon } from '../components/icons'
import { BookGrid, useSelection } from '../components/BookGrid'
import { BulkBar } from '../components/BulkBar'
import { FILTER_KEYS, type Filters, filtersToQuery } from '../components/FilterBar'
import { Empty, Loading, PageHeader, Tabs } from '../components/ui'
import { BookBrowser } from './Browse'

type Tab = 'reading' | 'want' | 'read' | 'dnf' | 'favorites'
const TABS: { id: Tab; label: string; icon: string }[] = [
  { id: 'reading', label: 'Reading now', icon: 'book-open' }, { id: 'want', label: 'Want to read', icon: 'bookmark-plus' },
  { id: 'read', label: 'Finished', icon: 'check-circle' }, { id: 'dnf', label: 'Didn’t finish', icon: 'x-circle' }, { id: 'favorites', label: 'Favourites', icon: 'heart' },
]
const FIXED: Record<Exclude<Tab, 'read'>, Filters> = {
  reading: { status: 'reading', sort: 'last_read', order: 'desc' },
  want: { status: 'want', sort: 'added', order: 'desc' },
  dnf: { status: 'dnf', sort: 'last_read', order: 'desc' },
  favorites: { favorite: '1', sort: 'rating', order: 'desc' },
}
const EMPTY: Record<Tab, [string, string]> = {
  reading: ['Nothing on the go', 'Open a book in the reader, or read on your Kindle, KOReader or CrossPoint — it shows up here.'],
  want: ['Nothing on your list yet', 'On a book, press “Want to read” to keep it for later.'],
  read: ['No finished books yet', 'Books you finish (here or on an e-reader) are kept here by year.'],
  dnf: ['Nothing given up on', 'Books you stop reading can be marked “Didn’t finish”; they stay out of your finished count.'],
  favorites: ['No favourites yet', 'The heart on a book makes it a favourite.'],
}

export default function Reading() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const tab: Tab = TABS.some(t => t.id === raw) ? raw as Tab : 'reading'
  const [sp, setSp] = useSearchParams()
  const fixed = tab === 'read' ? {} : FIXED[tab]
  const filters = useMemo(() => { const f: Filters = { ...fixed }; for (const k of FILTER_KEYS) { const v = sp.get(k); if (v) f[k] = v } return f }, [sp, tab]) // eslint-disable-line react-hooks/exhaustive-deps
  const setFilters = (f: Filters) => { const n: Filters = {}; for (const [k, v] of Object.entries(f)) if (v && (fixed as Filters)[k] !== v) n[k] = v; setSp(new URLSearchParams(filtersToQuery(n)), { replace: true }) }
  return (
    <div>
      <PageHeader title="Reading" />
      <Tabs tabs={TABS} value={tab} onChange={t => navigate(`/reading/${t === 'reading' ? '' : t}`, { replace: true })} />
      <div className="mt">
        {tab === 'read' ? <Finished /> : (
          <BookBrowser key={tab} filters={filters} setFilters={setFilters} fixed={tab === 'favorites' ? ['favorite'] : ['status']} emptyTitle={EMPTY[tab][0]} emptyText={EMPTY[tab][1]} />
        )}
      </div>
    </div>
  )
}

/** Finished books by the year you finished them, newest first. */
function Finished() {
  const { settings } = useApp()
  const { data, error, reload } = useApi<Page<Book>>(`${V1}/books?status=read&sort=last_read&order=desc&limit=500`)
  const books = data?.items
  const years = useMemo(() => {
    const m = new Map<string, Book[]>()
    for (const b of [...(books || [])].sort((a, z) => (stateOf(z).finished_at || '').localeCompare(stateOf(a).finished_at || ''))) {
      const y = (stateOf(b).finished_at || '').slice(0, 4) || 'Some time'
      m.set(y, [...(m.get(y) || []), b])
    }
    return [...m.entries()]
  }, [books])
  const sel = useSelection(books || [])
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  if (!data.items.length) return <Empty title={EMPTY.read[0]} icon="check-circle">{EMPTY.read[1]}</Empty>
  return (
    <>
      {years.map(([y, list]) => (
        <section key={y} className="section">
          <h2 className="year-h">{y}<span className="muted small">{list.length} {list.length === 1 ? 'book' : 'books'}</span></h2>
          <BookGrid books={list} view={(settings?.library_view || 'grid') as 'grid'} size={(settings?.card_size || 'm') as 'm'} sel={sel.sel} onToggle={sel.toggle}
            sub={b => { const f = stateOf(b).finished_at; return f ? `${fmtDate(f.slice(0, 10), false)}${stateOf(b).rating ? ' · ' + '★'.repeat(Math.round(stateOf(b).rating / 2)) : ''}` : authorsText(b) }} />
        </section>
      ))}
      {data.total > data.items.length && <p className="muted small">Showing your latest {data.items.length} of {data.total}.</p>}
      <BulkBar ids={[...sel.sel]} onClear={sel.clear} onDone={() => { sel.clear(); reload() }} />
    </>
  )
}
