// One series in order: what you read, what is next, and everything at once.
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useApi } from '../lib/use'
import type { Book, Series } from '../lib/types'
import { authorsText, seriesIndex, stateOf } from '../lib/books'
import { Icon } from '../components/icons'
import { BookGrid, useSelection } from '../components/BookGrid'
import { BulkBar } from '../components/BulkBar'
import { Cover, Progress } from '../components/cards'
import { useCrumb } from '../components/Layout'
import { ShelfPicker } from '../components/ShelfPicker'
import { SendButton } from '../components/SendButton'
import { Confirm, Loading, Modal, Spinner, errMsg, useToast } from '../components/ui'
import { sidebarChanged } from '../components/Sidebar'

export default function SeriesPage() {
  const { id = '' } = useParams()
  const { toast } = useToast()
  const { settings, user } = useApp()
  const { data, error, reload } = useApi<{ series: Series; books: Book[] }>(`${V1}/series/${id}`)
  const [allRead, setAllRead] = useState(false)
  const [edit, setEdit] = useState(false)
  const sel = useSelection(data?.books || [])
  useCrumb(data?.series.name)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const { series: se, books } = data
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const reading = books.find(b => stateOf(b).status === 'reading')
  const next = reading || books.find(b => { const s = stateOf(b); return s.status !== 'read' && s.status !== 'dnf' })
  const authors = [...new Map(books.flatMap(b => b.authors).map(a => [a.id, a])).values()].slice(0, 3)
  const markAll = async () => {
    try { await api.post(`${V1}/books/bulk`, { ids: books.filter(b => stateOf(b).status !== 'read').map(b => b.id), status: 'read' }); toast('All marked as read'); sidebarChanged(); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div>
      <section className="series-hero">
        <div className="series-covers book">{books.slice(0, 3).map((b, i) => <Cover key={b.id} b={b} w={220} className={`sc${i}`} />)}</div>
        <div className="grow" style={{ minWidth: 0 }}>
          <p className="eyebrow">Series</p>
          <h1 className="book-title">{se.name}</h1>
          <p className="muted">{authors.map((a, i) => <span key={a.id}>{i > 0 && ', '}<Link to={`/author/${a.id}`}>{a.name}</Link></span>)}{authors.length ? ' · ' : ''}{se.books} {se.books === 1 ? 'book' : 'books'}{se.read ? ` · ${se.read} read` : ''}</p>
          {se.read > 0 && <div className="book-progress"><Progress value={se.read / Math.max(1, se.books)} /><span className="small muted tnum">{se.read} / {se.books}</span></div>}
          {se.description && <p className="author-bio">{se.description}</p>}
          <div className="row wrap mt-sm">
            {next && <Link to={`/read/${next.id}`} className="btn primary"><Icon name="book-open" size={15} />{reading ? 'Continue' : se.read ? 'Read next' : 'Start'} · {next.series_index ? `Book ${seriesIndex(next.series_index).slice(1)}` : next.title}</Link>}
            {next && <SendButton bookId={next.id} className="btn" label="Send next" />}
            <ShelfPicker bookIds={books.map(b => b.id)} className="btn" label="All on a shelf" />
            {se.read < books.length && <button type="button" className="btn ghost" onClick={() => setAllRead(true)}><Icon name="check-circle" size={15} />Mark all read</button>}
            {editor && <button type="button" className="btn ghost" onClick={() => setEdit(true)}><Icon name="edit" size={14} />Edit</button>}
          </div>
        </div>
      </section>
      <BookGrid books={books} view={(settings?.library_view || 'grid') as 'grid'} size={(settings?.card_size || 'm') as 'm'} sel={sel.sel} onToggle={sel.toggle} showSeries
        sub={b => [b.series_index ? `Book ${seriesIndex(b.series_index).slice(1)}` : '', authorsText(b, 1)].filter(Boolean).join(' · ')} />
      <BulkBar ids={[...sel.sel]} onClear={sel.clear} onDone={() => { sel.clear(); reload() }} />
      {allRead && <Confirm title="Mark the whole series read?" message={`${books.filter(b => stateOf(b).status !== 'read').length} books count as read today in your stats.`} confirmLabel="Mark read" danger={false} onCancel={() => setAllRead(false)} onConfirm={() => { setAllRead(false); markAll() }} />}
      {edit && <EditSeries se={se} onClose={() => setEdit(false)} onSaved={() => { setEdit(false); reload() }} />}
    </div>
  )
}

function EditSeries({ se, onClose, onSaved }: { se: Series; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [name, setName] = useState(se.name)
  const [desc, setDesc] = useState(se.description || '')
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { await api.put(`${V1}/series/${se.id}`, { name: name.trim(), description: desc }); toast('Saved'); onSaved() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Edit series" onClose={onClose}>
      <form onSubmit={save} className="stack">
        <label className="field"><span className="label">Name</span><input value={name} onChange={e => setName(e.target.value)} autoFocus /></label>
        <label className="field"><span className="label">Description</span><textarea rows={5} value={desc} onChange={e => setDesc(e.target.value)} /></label>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy || !name.trim()}>{busy && <Spinner />}Save</button></div>
      </form>
    </Modal>
  )
}
