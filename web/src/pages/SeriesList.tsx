// Every series: a fan of covers and how far you are.
import { useState } from 'react'
import { V1 } from '../lib/api'
import { usePaged } from '../lib/use'
import type { Series } from '../lib/types'
import { Icon } from '../components/icons'
import { SeriesTile, SkeletonGrid } from '../components/cards'
import { Empty, PageHeader, Segmented, useDebounce } from '../components/ui'

export default function SeriesList() {
  const [q, setQ] = useState('')
  const [show, setShow] = useState<'all' | 'started' | 'done'>('all')
  const dq = useDebounce(q, 250)
  const paged = usePaged<Series>(off => `${V1}/series?q=${encodeURIComponent(dq)}&offset=${off}&limit=120`, dq, undefined, 120)
  const items = paged.items.filter(s => show === 'all' || (show === 'started' ? s.read > 0 && s.read < s.books : s.books > 0 && s.read >= s.books))
  return (
    <div>
      <PageHeader title="Series" sub={paged.total >= 0 ? `${paged.total.toLocaleString()} series` : undefined}
        actions={<Segmented size="sm" value={show} onChange={setShow} label="Show" options={[{ value: 'all', label: 'All' }, { value: 'started', label: 'In progress' }, { value: 'done', label: 'Finished' }]} />} />
      <label className="search-box big"><Icon name="search" size={17} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Find a series" aria-label="Find a series" /></label>
      {paged.error && <div className="callout danger"><Icon name="alert" size={15} />{paged.error}</div>}
      {paged.busy && !paged.items.length ? <SkeletonGrid /> : items.length === 0 ? <Empty title={dq ? 'No series by that name' : show !== 'all' ? 'None here yet' : 'No series yet'} icon="layers">{!dq && show === 'all' ? 'Books join a series from their files or when you set one in Edit.' : null}</Empty> : (
        <div className="grid-books">{items.map(s => <SeriesTile key={s.id} s={s} />)}</div>
      )}
      {paged.more && <div ref={paged.sentinel} className="more-sentinel">{paged.busy && <span className="spinner" />}</div>}
    </div>
  )
}
