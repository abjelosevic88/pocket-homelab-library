// The library: every book you can open, searched, filtered and sorted — the
// filters live in the URL, so a view can be shared and the back button works.
import { useMemo } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { usePaged } from '../lib/use'
import type { Book, Settings } from '../lib/types'
import { Icon } from '../components/icons'
import { BookGrid, useSelection } from '../components/BookGrid'
import { BulkBar } from '../components/BulkBar'
import { FILTER_KEYS, FilterBar, type Filters, filtersToQuery } from '../components/FilterBar'
import { Empty, PageHeader } from '../components/ui'

/** A browsable, filterable, selectable list of books; Browse, Reading and shelves use it. */
export function BookBrowser({ filters, setFilters, fixed = [], emptyTitle = 'No books here', emptyText, shelfId, numbered, reloadKey, defaultSort, extraSorts }: {
  filters: Filters; setFilters: (f: Filters) => void; fixed?: string[]; emptyTitle?: string; emptyText?: React.ReactNode; shelfId?: number; numbered?: boolean; reloadKey?: string; defaultSort?: string; extraSorts?: { value: string; label: string }[]
}) {
  const { settings, setSettings } = useApp()
  const view = (settings?.library_view || 'grid') as 'grid' | 'list'
  const size = (settings?.card_size || 'm') as 's' | 'm' | 'l'
  const save = (patch: Partial<Settings>) => {
    if (settings) setSettings({ ...settings, ...patch })
    api.put<Settings>(`${V1}/settings`, patch).catch(() => {})
  }
  const q = filtersToQuery(filters)
  const base = shelfId ? `${V1}/shelves/${shelfId}` : `${V1}/books`
  // a shelf answers {shelf, books: Page}; the books list answers the page itself
  const paged = usePaged<Book>(off => `${base}?${q}${q ? '&' : ''}offset=${off}&limit=60`, base + '?' + q + (reloadKey || ''),
    shelfId ? r => (r as { books: { items: Book[]; total: number } }).books : undefined)
  const { sel, toggle, clear } = useSelection(paged.items)
  return (
    <>
      <FilterBar value={filters} onChange={setFilters} fixed={fixed} total={paged.total} defaultSort={defaultSort} extraSorts={extraSorts} view={view} onView={v => save({ library_view: v })} size={size} onSize={s => save({ card_size: s })} />
      {paged.error && <div className="callout danger"><Icon name="alert" size={15} />{paged.error}</div>}
      {!paged.busy && paged.total === 0 ? (
        <Empty title={emptyTitle} icon="book">{emptyText || (Object.keys(filters).some(k => filters[k] && !fixed.includes(k) && k !== 'sort' && k !== 'order') ? 'Nothing matches these filters.' : null)}</Empty>
      ) : (
        <BookGrid books={paged.items} view={view} size={size} loading={paged.busy} sel={sel} onToggle={toggle} showSeries={filters.sort === 'series' || !!filters.series} numbered={numbered} />
      )}
      {paged.more && <div ref={paged.sentinel} className="more-sentinel">{paged.busy && <span className="spinner" />}</div>}
      <BulkBar ids={[...sel]} onClear={clear} onDone={() => { clear(); paged.reload() }} shelfId={shelfId} />
    </>
  )
}

export default function Browse() {
  const [sp, setSp] = useSearchParams()
  const filters = useMemo(() => { const f: Filters = {}; for (const k of FILTER_KEYS) { const v = sp.get(k); if (v) f[k] = v } return f }, [sp])
  const setFilters = (f: Filters) => setSp(new URLSearchParams(filtersToQuery(f)), { replace: true })
  const { settings, user } = useApp()
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  return (
    <div>
      <PageHeader title="Books" actions={editor ? <Link to="/admin/upload" className="btn"><Icon name="upload" size={15} />Add books</Link> : undefined} />
      <BookBrowser filters={filters} setFilters={setFilters} emptyText={filters.q ? <>Nothing matches “{filters.q}”.</> : undefined} />
    </div>
  )
}
