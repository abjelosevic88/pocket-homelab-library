// Tags, sized by how many books carry them.
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { V1 } from '../lib/api'
import { useApi } from '../lib/use'
import type { Tag } from '../lib/types'
import { Icon } from '../components/icons'
import { Empty, Loading, PageHeader, Segmented, useDebounce } from '../components/ui'

export default function Tags() {
  const [q, setQ] = useState('')
  const [view, setView] = useState<'cloud' | 'list'>('cloud')
  const dq = useDebounce(q, 200)
  const { data, error } = useApi<Tag[]>(`${V1}/tags?q=${encodeURIComponent(dq)}&limit=1000`)
  const max = useMemo(() => Math.max(1, ...(data || []).map(t => t.books)), [data])
  const sorted = useMemo(() => [...(data || [])].sort((a, b) => view === 'cloud' ? a.name.localeCompare(b.name) : b.books - a.books), [data, view])
  return (
    <div>
      <PageHeader title="Tags" sub={data ? `${data.length.toLocaleString()} tags` : undefined}
        actions={<Segmented size="sm" value={view} onChange={setView} label="View" options={[{ value: 'cloud', label: 'Cloud' }, { value: 'list', label: 'Most used' }]} />} />
      <label className="search-box big"><Icon name="search" size={17} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Find a tag" aria-label="Find a tag" /></label>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!data ? <Loading /> : data.length === 0 ? <Empty title="No tags" icon="tag">Tags come from the books’ subjects; editors can add their own.</Empty> : view === 'cloud' ? (
        <div className="tag-cloud">
          {sorted.map(t => <Link key={t.name} to={`/library?tag=${encodeURIComponent(t.name)}`} style={{ fontSize: `${13 + Math.round(Math.sqrt(t.books / max) * 13)}px`, fontWeight: t.books / max > .3 ? 650 : 500 }}>{t.name}<small>{t.books}</small></Link>)}
        </div>
      ) : (
        <table className="table compact">
          <tbody>{sorted.map(t => <tr key={t.name}><td><Link to={`/library?tag=${encodeURIComponent(t.name)}`}>{t.name}</Link></td><td className="right muted tnum">{t.books.toLocaleString()}</td></tr>)}</tbody>
        </table>
      )}
    </div>
  )
}
