// Your shelves: hand-picked ones (a reading list when ordered) and smart ones (a saved filter).
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApi } from '../lib/use'
import type { Shelf } from '../lib/types'
import { Icon } from '../components/icons'
import { Cover, ShelfTile, SkeletonGrid } from '../components/cards'
import { DragList } from '../components/DragList'
import { ShelfEditor } from '../components/ShelfEditor'
import { Empty, PageHeader, errMsg, useToast } from '../components/ui'
import { sidebarChanged } from '../components/Sidebar'

export default function Shelves() {
  const { toast } = useToast()
  const navigate = useNavigate()
  const { data, setData, error, reload } = useApi<Shelf[]>(`${V1}/shelves`)
  const [creating, setCreating] = useState(false)
  const [ordering, setOrdering] = useState(false)
  const reorder = async (list: Shelf[]) => {
    setData(list)
    try { await api.put(`${V1}/shelves/order`, { ids: list.map(s => s.id) }); sidebarChanged() } catch (e) { toast(errMsg(e), 'err'); reload() }
  }
  return (
    <div>
      <PageHeader title="Shelves" sub="Yours alone. Hand-picked shelves can be reading lists in your order; smart shelves fill themselves from a filter."
        actions={<div className="row">{data && data.length > 1 && <button type="button" className={`btn ${ordering ? 'on' : ''}`} onClick={() => setOrdering(o => !o)}><Icon name="move" size={15} />{ordering ? 'Done' : 'Reorder'}</button>}
          <button type="button" className="btn primary" onClick={() => setCreating(true)}><Icon name="plus" size={15} />New shelf</button></div>} />
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!data ? <SkeletonGrid n={6} /> : data.length === 0 ? (
        <Empty title="No shelves yet" icon="library">Make a shelf for a mood, a project or a reading plan — or a smart shelf like “unread Serbian books added this year”. On a book, “Shelves” puts it on one.</Empty>
      ) : ordering ? (
        <DragList items={data} keyOf={s => s.id} onReorder={reorder} className="shelf-rows"
          render={(s, handle) => (
            <div className="shelf-row-item">
              {handle}
              <span className="covers">{s.covers.slice(0, 3).map((c, i) => <Cover key={i} b={{ title: s.name, cover: c, cover_color: '', authors: [] }} w={60} />)}</span>
              <span className="grow"><b>{s.name}</b><span className="muted small"> · {s.kind === 'smart' ? 'smart' : `${s.books} books`}</span></span>
            </div>
          )} />
      ) : (
        <div className="grid-books">{data.map(s => <ShelfTile key={s.id} s={s} />)}</div>
      )}
      {creating && <ShelfEditor onClose={() => setCreating(false)} onSaved={s => { setCreating(false); sidebarChanged(); navigate(`/shelf/${s.id}`) }} />}
    </div>
  )
}
