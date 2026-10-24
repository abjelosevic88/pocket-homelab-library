// What to do with the selected books: shelves, status, favourite, hide, send — and for editors tags, series, language.
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { Icon } from './icons'
import { MenuButton, type MenuItem, Modal, errMsg, useToast } from './ui'
import { ShelfPicker } from './ShelfPicker'
import { SendButton } from './SendButton'
import { sidebarChanged } from './Sidebar'

type Edit = 'add_tags' | 'remove_tags' | 'series' | 'language'

export function BulkBar({ ids, onClear, onDone, shelfId }: { ids: number[]; onClear: () => void; onDone: () => void; shelfId?: number }) {
  const { settings, user } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Edit | null>(null)
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  if (!ids.length) return null
  const bulk = async (body: Record<string, unknown>, done: string) => {
    try {
      const r = await api.post<{ updated: number }>(`${V1}/books/bulk`, { ids, ...body })
      toast(`${done} · ${r.updated} ${r.updated === 1 ? 'book' : 'books'}`)
      sidebarChanged()
      onDone()
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const statusItems = (): MenuItem[] => [
    { label: 'Want to read', icon: 'bookmark-plus', onSelect: () => bulk({ status: 'want' }, 'Want to read') },
    { label: 'Reading', icon: 'book-open', onSelect: () => bulk({ status: 'reading' }, 'Reading') },
    { label: 'Read', icon: 'check-circle', onSelect: () => bulk({ status: 'read' }, 'Marked read') },
    { label: 'Didn’t finish', icon: 'x-circle', onSelect: () => bulk({ status: 'dnf' }, 'Didn’t finish') },
    'sep',
    { label: 'Clear status', icon: 'undo', onSelect: () => bulk({ status: '' }, 'Status cleared') },
  ]
  const more = (): MenuItem[] => [
    { label: 'Favourite', icon: 'heart', onSelect: () => bulk({ favorite: true }, 'Favourites') },
    { label: 'Not favourite', icon: 'heart', onSelect: () => bulk({ favorite: false }, 'Unfavourited') },
    { label: 'Hide from my library', icon: 'eye-off', onSelect: () => bulk({ hidden: true }, 'Hidden') },
    { label: 'Show again', icon: 'eye', onSelect: () => bulk({ hidden: false }, 'Shown') },
    ...(shelfId ? ['sep' as const, { label: 'Take off this shelf', icon: 'x', onSelect: async () => { try { await api.post(`${V1}/shelves/${shelfId}/books/remove`, { book_ids: ids }); toast('Taken off the shelf'); sidebarChanged(); onDone() } catch (e) { toast(errMsg(e), 'err') } } }] : []),
    ...(editor ? ['sep' as const, { heading: 'Edit (everyone sees it)' },
      { label: 'Add tags…', icon: 'tag', onSelect: () => setEdit('add_tags') },
      { label: 'Remove tags…', icon: 'tag', onSelect: () => setEdit('remove_tags') },
      { label: 'Set series…', icon: 'layers', onSelect: () => setEdit('series') },
      { label: 'Set language…', icon: 'globe', onSelect: () => setEdit('language') }] : []),
  ]
  return (
    <div className="bulk-bar" role="toolbar" aria-label="Selected books">
      <button type="button" className="btn ghost icon-only sm" aria-label="Stop selecting" title="Stop selecting (esc)" onClick={onClear}><Icon name="x" size={16} /></button>
      <span className="count">{ids.length} selected</span>
      <ShelfPicker bookIds={ids} className="btn sm" label="Shelf" onChange={() => onDone()} />
      <MenuButton label="Status" className="btn sm" items={statusItems}><Icon name="bookmark" size={15} />Status</MenuButton>
      <SendButton bookIds={ids} className="btn sm" label="Send" onSent={onClear} />
      <MenuButton label="More" className="btn sm" items={more} align="right"><Icon name="more" size={15} /></MenuButton>
      {edit && <BulkEdit kind={edit} n={ids.length} onClose={() => setEdit(null)} onApply={v => { setEdit(null); bulk(v, 'Saved') }} />}
    </div>
  )
}

function BulkEdit({ kind, n, onClose, onApply }: { kind: Edit; n: number; onClose: () => void; onApply: (v: Record<string, unknown>) => void }) {
  const [v, setV] = useState('')
  const title = kind === 'add_tags' ? 'Add tags' : kind === 'remove_tags' ? 'Remove tags' : kind === 'series' ? 'Set the series' : 'Set the language'
  const help = kind.endsWith('tags') ? 'Separate several with commas.' : kind === 'series' ? 'Leave empty to take them out of any series. The order inside the series stays as it is.' : 'A two-letter code like en, sr, de.'
  const apply = (e: React.FormEvent) => {
    e.preventDefault()
    const list = v.split(',').map(x => x.trim()).filter(Boolean)
    onApply(kind === 'add_tags' ? { add_tags: list } : kind === 'remove_tags' ? { remove_tags: list } : kind === 'series' ? { series: v.trim() } : { language: v.trim().toLowerCase() })
  }
  return (
    <Modal title={`${title} · ${n} ${n === 1 ? 'book' : 'books'}`} onClose={onClose} size="sm">
      <form onSubmit={apply} className="stack">
        <label className="field"><span className="label">{title}</span><input autoFocus value={v} onChange={e => setV(e.target.value)} /><span className="help">{help}</span></label>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={kind.endsWith('tags') && !v.trim()}>Apply</button></div>
      </form>
    </Modal>
  )
}
