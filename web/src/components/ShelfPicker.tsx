// Put a book (or several) on shelves: checkboxes for the hand-picked shelves, and a new one.
import { useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'
import type { Shelf } from '../lib/types'
import { Icon } from './icons'
import { type Anchor, Popover, errMsg, rectOf, useToast } from './ui'
import { sidebarChanged } from './Sidebar'

export function ShelfPicker({ bookIds, on = [], onChange, className = 'btn lg', label = 'Shelves', iconOnly }: { bookIds: number[]; on?: number[]; onChange?: (ids: number[]) => void; className?: string; label?: string; iconOnly?: boolean }) {
  const [anchor, setAnchor] = useState<Anchor | null>(null)
  return (
    <>
      <button type="button" className={className} aria-label={iconOnly ? label : undefined} title={label} onClick={e => setAnchor(anchor ? null : rectOf(e.currentTarget))}>
        <Icon name="library" size={16} />{!iconOnly && (on.length ? `On ${on.length} ${on.length === 1 ? 'shelf' : 'shelves'}` : label)}
      </button>
      {anchor && <Popover anchor={anchor} onClose={() => setAnchor(null)} prefer="below" width={290} label="Shelves"><ShelfPickerBody bookIds={bookIds} on={on} onChange={onChange} /></Popover>}
    </>
  )
}

function ShelfPickerBody({ bookIds, on, onChange }: { bookIds: number[]; on: number[]; onChange?: (ids: number[]) => void }) {
  const { toast } = useToast()
  const [shelves, setShelves] = useState<Shelf[] | null>(null)
  const [cur, setCur] = useState<Set<number>>(new Set(on))
  const [name, setName] = useState('')
  useEffect(() => { api.get<Shelf[]>(`${V1}/shelves`).then(s => setShelves(s.filter(x => x.kind === 'manual'))).catch(e => toast(errMsg(e), 'err')) }, [toast])
  const single = bookIds.length === 1
  const flip = async (s: Shelf) => {
    const has = cur.has(s.id)
    try {
      if (has && single) await api.post(`${V1}/shelves/${s.id}/books/remove`, { book_ids: bookIds })
      else await api.post(`${V1}/shelves/${s.id}/books`, { book_ids: bookIds })
      const n = new Set(cur)
      if (has && single) n.delete(s.id); else n.add(s.id)
      setCur(n)
      onChange?.([...n])
      sidebarChanged()
      if (!single) toast(`${bookIds.length} books put on ${s.name}`)
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    try {
      const s = await api.post<Shelf>(`${V1}/shelves`, { name: name.trim(), kind: 'manual', opds: true })
      await api.post(`${V1}/shelves/${s.id}/books`, { book_ids: bookIds })
      setShelves(l => [...(l || []), s])
      const n = new Set(cur); n.add(s.id); setCur(n); onChange?.([...n])
      setName('')
      sidebarChanged()
      toast(`Put on ${s.name}`)
    } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div className="pop-pad">
      <h4>{single ? 'Shelves' : `Put ${bookIds.length} books on…`}</h4>
      <div className="shelf-picker">
        {shelves === null && <span className="muted small">Loading…</span>}
        {shelves?.length === 0 && <span className="muted small">No shelves yet — make one below.</span>}
        {shelves?.map(s => (
          <label key={s.id}><input type="checkbox" checked={cur.has(s.id)} onChange={() => flip(s)} /><span className="grow ellipsis">{s.name}</span>{s.ordered && <Icon name="list-ordered" size={13} className="muted" />}</label>
        ))}
      </div>
      <form className="row" onSubmit={create}><input className="input-sm" value={name} onChange={e => setName(e.target.value)} placeholder="New shelf" /><button className="btn sm" disabled={!name.trim()}><Icon name="plus" size={14} />Add</button></form>
    </div>
  )
}
