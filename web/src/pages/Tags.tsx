// Tags: the most used first, the one-book strays tucked away, and for
// editors a tidy-up mode to remove junk or merge spellings for good.
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useApi } from '../lib/use'
import type { Tag } from '../lib/types'
import { Icon } from '../components/icons'
import { Confirm, Empty, Loading, Modal, PageHeader, Segmented, Switch, errMsg, useDebounce, useToast } from '../components/ui'

interface TagRules { blocked: string[]; renames: Record<string, string> }

export default function Tags() {
  const { settings, user } = useApp()
  const { toast } = useToast()
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const [q, setQ] = useState('')
  const [sort, setSort] = useState<'count' | 'name'>('count')
  const [rare, setRare] = useState(false)
  const [tidy, setTidy] = useState(false)
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [merging, setMerging] = useState(false)
  const [removing, setRemoving] = useState(false)
  const dq = useDebounce(q, 200)
  const { data, error, reload } = useApi<Tag[]>(`${V1}/tags?q=${encodeURIComponent(dq)}&limit=2000`)
  const rules = useApi<TagRules>(editor ? `${V1}/tags/rules` : null)

  const all = useMemo(() => data || [], [data])
  const strays = all.filter(t => t.books === 1).length
  // a search shows every match; otherwise one-book tags wait behind the switch
  const shown = useMemo(() => {
    const list = all.filter(t => rare || dq || t.books > 1)
    return [...list].sort((a, b) => sort === 'name' ? a.name.localeCompare(b.name) : b.books - a.books || a.name.localeCompare(b.name))
  }, [all, rare, dq, sort])
  const max = Math.max(1, ...all.map(t => t.books))
  const books = (names: Set<string>) => all.filter(t => names.has(t.name)).reduce((n, t) => n + t.books, 0)

  const toggle = (name: string) => setPicked(p => { const n = new Set(p); if (n.has(name)) n.delete(name); else n.add(name); return n })
  const done = (msg: string) => { toast(msg); setPicked(new Set()); reload(); rules.reload() }
  const remove = async () => {
    setRemoving(false)
    try { const r = await api.post<{ books: number }>(`${V1}/tags/remove`, { names: [...picked] }); done(`Removed from ${r.books.toLocaleString()} books`) } catch (e) { toast(errMsg(e), 'err') }
  }
  const allow = async (name: string) => {
    try { await api.post(`${V1}/tags/allow`, { names: [name] }); toast(`“${name}” is allowed again: books get it back on their next scan`); rules.reload() } catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <div className="tags-page">
      <PageHeader title="Tags" sub={data ? `${all.length.toLocaleString()} tags` : undefined}
        actions={editor && <button type="button" className={`btn sm ${tidy ? 'primary' : ''}`} onClick={() => { setTidy(t => !t); setPicked(new Set()) }}>
          <Icon name={tidy ? 'check' : 'wand'} size={14} />{tidy ? 'Done' : 'Tidy up'}</button>} />
      <label className="search-box big"><Icon name="search" size={17} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Find a tag" aria-label="Find a tag" /></label>
      <div className="tags-bar">
        <Segmented size="sm" value={sort} onChange={setSort} label="Order" options={[{ value: 'count', label: 'Most books' }, { value: 'name', label: 'A–Z' }]} />
        {strays > 0 && !dq && <label className="tags-rare"><Switch size="sm" checked={rare} onChange={setRare} label="Show tags on one book" /><span>Tags on one book <span className="muted">{strays.toLocaleString()}</span></span></label>}
      </div>
      {tidy && <p className="muted small tags-help">Pick tags to take off every book (for good: a scan won’t bring them back) or to merge into one name. Uploader names, converter notes and a collection’s name on every book are the usual suspects.</p>}
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!data ? <Loading /> : shown.length === 0 ? <Empty title={dq ? 'No tag like that' : 'No tags'} icon="tag">Tags come from the books’ subjects; editors can add their own.</Empty> : (
        <ul className="tag-list">
          {shown.map(t => {
            const on = picked.has(t.name)
            const body = <>
              {tidy && <span className={`tag-check ${on ? 'on' : ''}`} aria-hidden="true">{on && <Icon name="check" size={13} />}</span>}
              <span className="tag-name">{t.name}</span>
              <span className="tag-bar" aria-hidden="true"><span style={{ width: `${Math.max(4, Math.round(Math.sqrt(t.books / max) * 100))}%` }} /></span>
              <span className="tag-n tnum">{t.books.toLocaleString()}</span>
            </>
            return (
              <li key={t.name}>
                {tidy
                  ? <button type="button" className={`tag-row ${on ? 'picked' : ''}`} onClick={() => toggle(t.name)} aria-pressed={on}>{body}</button>
                  : <Link className="tag-row" to={`/library?tag=${encodeURIComponent(t.name)}`}>{body}<Icon name="chevron-right" size={15} className="tag-go" /></Link>}
              </li>
            )
          })}
        </ul>
      )}

      {editor && rules.data && (rules.data.blocked.length > 0 || Object.keys(rules.data.renames).length > 0) && (
        <details className="tag-rules">
          <summary>Removed and merged tags <span className="muted">{rules.data.blocked.length + Object.keys(rules.data.renames).length}</span></summary>
          <ul>
            {rules.data.blocked.map(b => <li key={'b' + b}><span className="grow"><s>{b}</s> <span className="muted small">removed</span></span><button type="button" className="btn ghost sm" onClick={() => allow(b)}>Allow again</button></li>)}
            {Object.entries(rules.data.renames).map(([from, to]) => <li key={'r' + from}><span className="grow">{from} <span className="muted">→</span> {to}</span><button type="button" className="btn ghost sm" onClick={() => allow(from)}>Undo</button></li>)}
          </ul>
        </details>
      )}

      {tidy && picked.size > 0 && (
        <div className="tags-actions">
          <span className="grow"><b>{picked.size}</b> {picked.size === 1 ? 'tag' : 'tags'} · {books(picked).toLocaleString()} books</span>
          <button type="button" className="btn sm" onClick={() => setMerging(true)}><Icon name="layers" size={14} />{picked.size === 1 ? 'Rename' : 'Merge'}</button>
          <button type="button" className="btn sm danger" onClick={() => setRemoving(true)}><Icon name="trash" size={14} />Remove</button>
        </div>
      )}
      {merging && <MergeTags names={[...picked]} tags={all} onClose={() => setMerging(false)} onDone={n => { setMerging(false); done(`Done on ${n.toLocaleString()} books`) }} />}
      {removing && <Confirm title={picked.size === 1 ? `Remove “${[...picked][0]}”?` : `Remove ${picked.size} tags?`} confirmLabel="Remove"
        message={<>They come off {books(picked).toLocaleString()} books, and a scan won’t bring them back. You can allow them again under “Removed and merged tags”.</>}
        onCancel={() => setRemoving(false)} onConfirm={remove} />}
    </div>
  )
}

function MergeTags({ names, tags, onClose, onDone }: { names: string[]; tags: Tag[]; onClose: () => void; onDone: (books: number) => void }) {
  const { toast } = useToast()
  // the most used of the picked tags is the likely keeper
  const [into, setInto] = useState(() => [...tags].filter(t => names.includes(t.name)).sort((a, b) => b.books - a.books)[0]?.name || names[0])
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try { const r = await api.post<{ books: number }>(`${V1}/tags/merge`, { names, into: into.trim() }); onDone(r.books) } catch (err) { toast(errMsg(err), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={names.length === 1 ? 'Rename the tag' : `Merge ${names.length} tags`} onClose={onClose} size="sm">
      <form onSubmit={save} className="stack">
        <p className="muted small">{names.map(n => `“${n}”`).join(', ')} {names.length === 1 ? 'gets' : 'become one tag with'} the new name on every book, and books added later get it too.</p>
        <label className="field"><span className="label">{names.length === 1 ? 'New name' : 'Into'}</span>
          <input value={into} onChange={e => setInto(e.target.value)} list="tag-names" autoFocus required maxLength={80} />
          <datalist id="tag-names">{tags.slice(0, 300).map(t => <option key={t.name} value={t.name} />)}</datalist>
        </label>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy || !into.trim()}>{names.length === 1 ? 'Rename' : 'Merge'}</button></div>
      </form>
    </Modal>
  )
}
