// One shelf: its books (in your order for a reading list), and what to do with all of them.
import { useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApi } from '../lib/use'
import type { Book, Page, Shelf } from '../lib/types'
import { authorsText } from '../lib/books'
import { Icon } from '../components/icons'
import { BookRow } from '../components/cards'
import { DragList } from '../components/DragList'
import { FILTER_KEYS, type Filters, filtersToQuery } from '../components/FilterBar'
import { useCrumb } from '../components/Layout'
import { SendButton } from '../components/SendButton'
import { ShelfEditor, filterFromJSON } from '../components/ShelfEditor'
import { Confirm, Loading, MenuButton, Switch, errMsg, useToast } from '../components/ui'
import { sidebarChanged } from '../components/Sidebar'
import { BookBrowser } from './Browse'

export default function ShelfPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const [sp, setSp] = useSearchParams()
  const { data, setData, error, reload } = useApi<{ shelf: Shelf; books: Page<Book> }>(`${V1}/shelves/${id}?limit=1`)
  const [edit, setEdit] = useState(false)
  const [del, setDel] = useState(false)
  const [ordering, setOrdering] = useState(false)
  const [rev, setRev] = useState(0)
  const filters = useMemo(() => { const f: Filters = {}; for (const k of ['sort', 'order']) { const v = sp.get(k); if (v) f[k] = v } return f }, [sp])
  useCrumb(data?.shelf.name)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const sh = data.shelf
  const smart = sh.kind === 'smart'
  const allIds = async () => (await api.get<{ books: Page<Book> }>(`${V1}/shelves/${sh.id}?limit=500`)).books.items.map(b => b.id)
  const setOpds = async (v: boolean) => {
    try { const s = await api.put<Shelf>(`${V1}/shelves/${sh.id}`, { ...sh, opds: v }); setData({ ...data, shelf: s && s.id ? s : { ...sh, opds: v } }); toast(v ? 'E-readers see this shelf' : 'Hidden from e-readers') } catch (e) { toast(errMsg(e), 'err') }
  }
  const smartFilters = smart ? filterFromJSON(sh.filter) : {}
  return (
    <div>
      <div className="page-header">
        <div className="grow">
          <p className="eyebrow">{smart ? <><Icon name="sparkles" size={12} />Smart shelf</> : sh.ordered ? <><Icon name="list-ordered" size={12} />Reading list</> : 'Shelf'}</p>
          <h1>{sh.name}</h1>
          {sh.description && <div className="sub">{sh.description}</div>}
          <div className="sub">{data.books.total.toLocaleString()} {data.books.total === 1 ? 'book' : 'books'}</div>
        </div>
        <div className="actions row wrap">
          <label className="row small muted" title="Show this shelf in the OPDS catalogue for KOReader and CrossPoint"><Switch size="sm" checked={sh.opds} onChange={setOpds} label="Show on e-readers" />On e-readers</label>
          {data.books.total > 0 && <SendAll shelfId={sh.id} load={allIds} />}
          {sh.ordered && !smart && data.books.total > 1 && <button type="button" className={`btn ${ordering ? 'on' : ''}`} onClick={() => setOrdering(o => !o)}><Icon name="move" size={15} />{ordering ? 'Done' : 'Reorder'}</button>}
          {smart && <button type="button" className="btn" onClick={() => navigate(`/library?${filtersToQuery(smartFilters)}`)}><Icon name="sliders" size={15} />Open as filter</button>}
          <MenuButton label="Shelf" className="btn ghost icon-only" align="right" items={[
            { label: 'Edit shelf', icon: 'edit', onSelect: () => setEdit(true) },
            { label: 'Delete shelf', icon: 'trash', danger: true, onSelect: () => setDel(true) },
          ]}><Icon name="more" size={17} /></MenuButton>
        </div>
      </div>
      {ordering ? <Reorder shelf={sh} onDone={() => { setOrdering(false); setRev(r => r + 1) }} /> : (
        <BookBrowser key={rev} filters={filters} setFilters={f => setSp(new URLSearchParams(filtersToQuery(f)), { replace: true })} shelfId={sh.id} numbered={sh.ordered && !filters.sort}
          fixed={FILTER_KEYS.filter(k => k !== 'sort' && k !== 'order')}
          defaultSort={smart ? (smartFilters.sort || 'title') : 'shelf'} extraSorts={smart ? [] : [{ value: 'shelf', label: sh.ordered ? 'Your order' : 'Added to the shelf' }]} emptyTitle={smart ? 'Nothing matches this filter yet' : 'Nothing on this shelf yet'}
          emptyText={smart ? 'Books that match will show up here by themselves.' : 'On a book, press “Shelves” to put it here — or select several in the library.'} />
      )}
      {edit && <ShelfEditor shelf={sh} onClose={() => setEdit(false)} onSaved={() => { setEdit(false); sidebarChanged(); reload(); setRev(r => r + 1) }} />}
      {del && <Confirm title={`Delete “${sh.name}”?`} message="The shelf goes away; the books stay in the library." confirmLabel="Delete" onCancel={() => setDel(false)}
        onConfirm={async () => { setDel(false); try { await api.del(`${V1}/shelves/${sh.id}`); sidebarChanged(); toast('Shelf deleted'); navigate('/shelves') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </div>
  )
}


function SendAll({ load }: { shelfId: number; load: () => Promise<number[]> }) {
  const [ids, setIds] = useState<number[] | null>(null)
  if (!ids) return <button type="button" className="btn" onClick={() => load().then(setIds)}><Icon name="share" size={15} />Send all…</button>
  return <SendButton bookIds={ids} className="btn" label={`Send ${ids.length}`} onSent={() => setIds(null)} />
}

function Reorder({ shelf, onDone }: { shelf: Shelf; onDone: () => void }) {
  const { toast } = useToast()
  const { data, setData } = useApi<{ books: Page<Book> }>(`${V1}/shelves/${shelf.id}?limit=500`)
  if (!data) return <Loading />
  const save = async (list: Book[]) => {
    setData({ books: { ...data.books, items: list } })
    try { await api.put(`${V1}/shelves/${shelf.id}/books/order`, { book_ids: list.map(b => b.id) }) } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div className="stack">
      <p className="muted small">Drag the handles to change the order. {data.books.total > 500 ? 'Only the first 500 books are shown.' : ''}</p>
      <DragList items={data.books.items} keyOf={b => b.id} onReorder={save} className="book-list"
        render={(b, handle, i) => <ul className="book-list" style={{ margin: 0 }}><BookRow b={b} n={i + 1} handle={handle} actions={<span className="muted xsmall hide-sm">{authorsText(b, 1)}</span>} /></ul>} />
      <div className="row"><button type="button" className="btn primary" onClick={onDone}>Done</button></div>
    </div>
  )
}
