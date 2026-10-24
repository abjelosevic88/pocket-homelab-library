// One author: photo, bio, links, series and every book.
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useApi } from '../lib/use'
import type { Author, Book, Series } from '../lib/types'
import { Icon } from '../components/icons'
import { Img, SeriesTile } from '../components/cards'
import { BookGrid, useSelection } from '../components/BookGrid'
import { BulkBar } from '../components/BulkBar'
import { useCrumb } from '../components/Layout'
import { Loading, MenuButton, Modal, Spinner, errMsg, useToast } from '../components/ui'

type R = { author: Author; books: Book[]; series: Series[] }

export default function AuthorPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const { settings, user } = useApp()
  const { data, error, reload } = useApi<R>(`${V1}/authors/${id}`)
  const [more, setMore] = useState(false)
  const [edit, setEdit] = useState(false)
  const sel = useSelection(data?.books || [])
  useCrumb(data?.author.name)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const a = data.author
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const read = data.books.filter(b => b.state?.status === 'read').length
  const view = (settings?.library_view || 'grid') as 'grid' | 'list'
  return (
    <div>
      <section className="person-hero">
        <div className="person-photo"><Img src={a.photo} w={280} alt={a.name} round /></div>
        <div className="grow" style={{ minWidth: 0 }}>
          <p className="eyebrow">Author</p>
          <h1 className="book-title">{a.name}</h1>
          <p className="muted">{a.books} {a.books === 1 ? 'book' : 'books'}{read ? ` · you read ${read}` : ''}{data.series.length ? ` · ${data.series.length} ${data.series.length === 1 ? 'series' : 'series'}` : ''}</p>
          {a.bio && <><p className={`author-bio ${more ? '' : 'clamp'}`}>{a.bio}</p>{a.bio.length > 400 && <button type="button" className="link-btn small" onClick={() => setMore(m => !m)}>{more ? 'Show less' : 'Show more'}</button>}</>}
          <div className="row wrap mt-sm">
            <Link className="btn" to={`/library?author=${a.id}`}><Icon name="sliders" size={14} />Filter the library</Link>
            {Object.entries(a.links || {}).map(([k, v]) => <a key={k} className="btn ghost sm" href={v} target="_blank" rel="noreferrer"><Icon name="external" size={13} />{k[0].toUpperCase() + k.slice(1)}</a>)}
            {editor && <MenuButton label="Edit" className="btn ghost sm" items={[
              { label: 'Rename or merge…', icon: 'edit', onSelect: () => setEdit(true) },
              { label: 'Look up photo and bio again', icon: 'refresh', onSelect: async () => { try { await api.post(`${V1}/authors/${a.id}/enrich`); toast('Looking it up — back in a moment'); setTimeout(reload, 4000) } catch (e) { toast(errMsg(e), 'err') } } },
            ]}><Icon name="edit" size={13} />Edit</MenuButton>}
          </div>
        </div>
      </section>
      {data.series.length > 0 && (
        <section className="section">
          <h2 className="section-h">Series</h2>
          <div className="grid-books">{data.series.map(s => <SeriesTile key={s.id} s={s} />)}</div>
        </section>
      )}
      <section className="section">
        <h2 className="section-h">Books</h2>
        <BookGrid books={data.books} view={view} size={(settings?.card_size || 'm') as 's'} sel={sel.sel} onToggle={sel.toggle} showSeries />
      </section>
      <BulkBar ids={[...sel.sel]} onClear={sel.clear} onDone={() => { sel.clear(); reload() }} />
      {edit && <RenameAuthor a={a} onClose={() => setEdit(false)} onSaved={nid => { setEdit(false); if (nid && nid !== a.id) navigate(`/author/${nid}`, { replace: true }); else reload() }} />}
    </div>
  )
}

function RenameAuthor({ a, onClose, onSaved }: { a: Author; onClose: () => void; onSaved: (id?: number) => void }) {
  const { toast } = useToast()
  const [name, setName] = useState(a.name)
  const [bio, setBio] = useState(a.bio || '')
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { const r = await api.put<Author | { author: Author }>(`${V1}/authors/${a.id}`, { name: name.trim(), bio }); const na = 'author' in r ? r.author : r; toast('Saved'); onSaved(na?.id) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Edit author" onClose={onClose}>
      <form onSubmit={save} className="stack">
        <label className="field"><span className="label">Name</span><input value={name} onChange={e => setName(e.target.value)} autoFocus /><span className="help">Use the name of another author to merge the two: their books come together under that name.</span></label>
        <label className="field"><span className="label">Bio</span><textarea rows={6} value={bio} onChange={e => setBio(e.target.value)} /></label>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy || !name.trim()}>{busy && <Spinner />}Save</button></div>
      </form>
    </Modal>
  )
}
