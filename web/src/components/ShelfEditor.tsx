// Make or change a shelf: name, what it is (hand-picked, reading list or smart) and whether e-readers see it.
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import type { Shelf } from '../lib/types'
import { FilterBar, type Filters } from './FilterBar'
import { Modal, Segmented, SettingRow, Spinner, Switch, errMsg, useToast } from './ui'

const NUM = new Set(['library', 'author', 'series', 'rating_min'])

/** A smart shelf's filter is a JSON BookQuery with typed values. */
export function filterToJSON(f: Filters): string {
  const o: Record<string, string | number | boolean> = {}
  for (const [k, v] of Object.entries(f)) {
    if (!v || k === 'seed') continue
    o[k] = NUM.has(k) ? Number(v) : k === 'favorite' ? true : v
  }
  return JSON.stringify(o)
}
export function filterFromJSON(s: string): Filters {
  try { const o = JSON.parse(s || '{}') as Record<string, unknown>; const f: Filters = {}; for (const [k, v] of Object.entries(o)) if (v !== null && v !== undefined && v !== '' && v !== false && v !== 0) f[k] = v === true ? '1' : String(v); return f } catch { return {} }
}

export function ShelfEditor({ shelf, onClose, onSaved }: { shelf?: Shelf; onClose: () => void; onSaved: (s: Shelf) => void }) {
  const { toast } = useToast()
  const [name, setName] = useState(shelf?.name || '')
  const [desc, setDesc] = useState(shelf?.description || '')
  const [kind, setKind] = useState<'manual' | 'list' | 'smart'>(shelf ? (shelf.kind === 'smart' ? 'smart' : shelf.ordered ? 'list' : 'manual') : 'manual')
  const [opds, setOpds] = useState(shelf?.opds ?? true)
  const [filter, setFilter] = useState<Filters>(() => filterFromJSON(shelf?.filter || '{}'))
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    const body = { name: name.trim(), description: desc.trim(), icon: shelf?.icon || '', kind: kind === 'smart' ? 'smart' : 'manual', ordered: kind === 'list', opds, filter: kind === 'smart' ? filterToJSON(filter) : '{}' }
    try {
      const s = shelf ? await api.put<Shelf>(`${V1}/shelves/${shelf.id}`, body) : await api.post<Shelf>(`${V1}/shelves`, body)
      toast(shelf ? 'Saved' : 'Shelf made')
      onSaved(s && s.id ? s : { ...(shelf as Shelf), ...body } as Shelf)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={shelf ? 'Edit shelf' : 'New shelf'} onClose={onClose} size={kind === 'smart' ? 'xl' : ''}>
      <form onSubmit={save} className="stack">
        <label className="field"><span className="label">Name</span><input value={name} onChange={e => setName(e.target.value)} autoFocus required maxLength={80} placeholder="e.g. Summer 2026, Classics, For Ana" /></label>
        <label className="field"><span className="label">Description</span><input value={desc} onChange={e => setDesc(e.target.value)} placeholder="Optional" /></label>
        <div className="field"><span className="label">Kind</span>
          <Segmented value={kind} onChange={setKind} options={[
            { value: 'manual', label: 'Hand-picked', title: 'You put books on it' },
            { value: 'list', label: 'Reading list', title: 'Hand-picked, in your order' },
            { value: 'smart', label: 'Smart', title: 'Books matching a filter, always up to date', disabled: !!shelf && shelf.kind === 'manual' && shelf.books > 0 },
          ]} />
          <span className="help">{kind === 'smart' ? 'Fills itself: every book that matches the filter below, now and later.' : kind === 'list' ? 'Numbered, in the order you drag them into.' : 'Newest additions first.'}</span>
        </div>
        {kind === 'smart' && <div className="card" style={{ padding: 12 }}><FilterBar value={filter} onChange={setFilter} /></div>}
        <SettingRow title="Show on e-readers" help="In the OPDS catalogue, so KOReader and CrossPoint can browse and download from it."><Switch checked={opds} onChange={setOpds} label="Show on e-readers" /></SettingRow>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy || !name.trim()}>{busy && <Spinner />}{shelf ? 'Save' : 'Make shelf'}</button></div>
      </form>
    </Modal>
  )
}
