// Review: books that look wrong, for editors to fix (on the book's page),
// delete there, or keep off e-readers' catalogues from here, one or many.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useApi } from '../lib/use'
import type { Library, ReviewFlag, ReviewPage } from '../lib/types'
import { Icon } from '../components/icons'
import { Confirm, Empty, Loading, PageHeader, Segmented, errMsg, useToast } from '../components/ui'
import { Select } from '../components/Select'
import '../styles/review.css'

const FLAG: Record<ReviewFlag, { label: string; help: string }> = {
  junk_title: { label: 'Junk title', help: 'A file name, a printer job or a scan as the title' },
  title_is_author: { label: 'Title is a name', help: "The title is an author's name: title and author swapped" },
  uploader_author: { label: 'Uploader as author', help: "A nickname such as “mladen” or “t” instead of the author" },
  no_author: { label: 'No author', help: 'Nobody is credited' },
  duplicate: { label: 'Duplicate', help: 'The same title by the same author twice in one library' },
  no_cover: { label: 'No cover', help: 'No cover image' },
}
const PAGE = 100

export default function Review() {
  const { settings, user } = useApp()
  const { toast } = useToast()
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const [flag, setFlag] = useState<ReviewFlag | ''>('')
  const [lib, setLib] = useState(0)
  const [off, setOff] = useState<'hide' | 'all' | 'only'>('hide')
  const [offset, setOffset] = useState(0)
  const [picked, setPicked] = useState<Set<number>>(new Set())
  const libs = useApi<Library[]>(`${V1}/libraries`)
  const { data, error, reload } = useApi<ReviewPage>(editor ? `${V1}/review?flag=${flag}&library=${lib || ''}&off=${off === 'all' ? '' : off}&offset=${offset}&limit=${PAGE}` : null)
  useEffect(() => { setOffset(0); setPicked(new Set()) }, [flag, lib, off])

  if (!editor) return <Empty icon="alert" title="Only editors review books" />
  const items = data?.items || []
  const toggle = (id: number) => setPicked(p => { const n = new Set(p); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const setOffFor = async (ids: number[], v: boolean) => {
    try {
      const r = await api.put<{ updated: number }>(`${V1}/books/ereader`, { ids, off: v })
      toast(v ? `${r.updated.toLocaleString()} kept off e-readers` : `${r.updated.toLocaleString()} back on e-readers`)
      setPicked(new Set()); reload()
    } catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <div className="review-page">
      <PageHeader title="Review" sub={data ? `${data.total.toLocaleString()} books look wrong${flag ? ` · ${FLAG[flag].label.toLowerCase()}` : ''}` : undefined} />
      <p className="muted small review-help">
        Fix a book on its page (<b>Edit details</b>), delete it there, or keep it off e-readers: it stays in the library and the web reader, but no device's catalogue lists it.
        A device can also show only one library or shelf: Devices → the device → Edit.
      </p>
      {user?.role === 'admin' && <Cleanup onDone={() => { reload(); libs.reload() }} />}
      <div className="chips review-flags">
        <button type="button" className={`chip ${flag === '' ? 'active' : ''}`} onClick={() => setFlag('')}>All</button>
        {(data?.flags || (Object.keys(FLAG) as ReviewFlag[])).map(f => (
          <button key={f} type="button" className={`chip ${flag === f ? 'active' : ''}`} title={FLAG[f].help} onClick={() => setFlag(flag === f ? '' : f)}>
            {FLAG[f].label} <span className="n">{(data?.counts?.[f] || 0).toLocaleString()}</span>
          </button>
        ))}
      </div>
      <div className="review-bar">
        <div className="review-lib"><Select ariaLabel="Library" value={String(lib)} onChange={v => setLib(Number(v) || 0)} options={[
          { value: '0', label: 'Every library' },
          ...(libs.data || []).filter(l => l.kind === 'books').map(l => ({ value: String(l.id), label: l.name })),
        ]} /></div>
        <Segmented size="sm" value={off} onChange={setOff} label="Kept off e-readers" options={[{ value: 'hide', label: 'To do' }, { value: 'only', label: 'Kept off' }, { value: 'all', label: 'All' }]} />
        <span className="grow" />
        {picked.size > 0 && <>
          <span className="small muted">{picked.size} picked</span>
          {off !== 'only' && <button type="button" className="btn sm" onClick={() => setOffFor([...picked], true)}><Icon name="eye-off" size={14} />Keep off e-readers</button>}
          {off !== 'hide' && <button type="button" className="btn sm" onClick={() => setOffFor([...picked], false)}><Icon name="eye" size={14} />Back on e-readers</button>}
        </>}
        {items.length > 0 && <button type="button" className="btn sm ghost" onClick={() => setPicked(picked.size === items.length ? new Set() : new Set(items.map(i => i.id)))}>{picked.size === items.length ? 'Pick none' : 'Pick all on this page'}</button>}
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!data && !error && <Loading />}
      {data && items.length === 0 && <Empty icon="check-circle" title={flag || lib || off !== 'hide' ? 'Nothing here' : 'Nothing looks wrong'} />}
      {items.length > 0 && (
        <ul className="review-list">
          {items.map(it => (
            <li key={it.id} className={picked.has(it.id) ? 'picked' : ''}>
              <input type="checkbox" checked={picked.has(it.id)} onChange={() => toggle(it.id)} aria-label={`Pick ${it.title}`} />
              <Link to={`/book/${it.id}`} className="review-cover">{it.cover ? <img src={`${it.cover}&w=200`} alt="" loading="lazy" /> : <Icon name="book" size={18} />}</Link>
              <div className="review-main">
                <Link to={`/book/${it.id}`} className="review-title">{it.title || '(no title)'}</Link>
                <div className="small muted ellipsis">{it.authors.join(', ') || 'No author'} · {it.library}{it.file ? ` · ${it.file}` : ''}</div>
                <div className="review-tags">
                  {it.flags.map(f => <span key={f} className="review-flag" title={FLAG[f].help}>{FLAG[f].label}</span>)}
                  {it.dupes?.map(d => <Link key={d} className="small" to={`/book/${d}`}>the other #{d}</Link>)}
                  {it.ereader_off && <span className="review-flag off"><Icon name="eye-off" size={11} />Kept off e-readers</span>}
                </div>
              </div>
              <button type="button" className="btn sm ghost" onClick={() => setOffFor([it.id], !it.ereader_off)} title={it.ereader_off ? 'Back on e-readers' : 'Keep off e-readers'}>
                <Icon name={it.ereader_off ? 'eye' : 'eye-off'} size={14} /><span className="review-act">{it.ereader_off ? 'Back on' : 'Keep off'}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {data && data.total > PAGE && (
        <div className="review-pager">
          <button type="button" className="btn sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}>Previous</button>
          <span className="small muted">{offset + 1}–{Math.min(offset + PAGE, data.total)} of {data.total.toLocaleString()}</span>
          <button type="button" className="btn sm" disabled={offset + PAGE >= data.total} onClick={() => setOffset(offset + PAGE)}>Next</button>
        </div>
      )}
    </div>
  )
}

interface CleanupMove { to_library_id: number; to: string; from: string; count: number; reasons: Record<string, number>; samples: string[] }
interface CleanupRun { running: boolean; total: number; done: number; moved: number; errors: string[]; started_at?: string; finished_at?: string }
interface CleanupInfo { plan?: { main: Library; review: Library; tech?: Library; rename_review_to?: string; moves: CleanupMove[]; protected: number }; run: CleanupRun; error?: string }
const REASON: Record<string, string> = { duplicate: 'extra copies', junk_title: 'junk titles', title_is_author: 'title and author swapped', uploader_author: 'uploader as author', no_author: 'no author', looks_good: 'look fine', tech: 'programming and languages' }

/** Suggested clean-up: what would move where, and Apply (admins). */
function Cleanup({ onDone }: { onDone: () => void }) {
  const { toast } = useToast()
  const [info, setInfo] = useState<CleanupInfo | null>(null)
  const [asking, setAsking] = useState(false)
  const [open, setOpen] = useState(false)
  const load = () => api.get<CleanupInfo>(`${V1}/review/cleanup`).then(setInfo).catch(() => {})
  useEffect(() => { load() }, [])
  const running = !!info?.run.running
  useEffect(() => {
    if (!running) return
    const t = setInterval(() => {
      api.get<CleanupInfo>(`${V1}/review/cleanup`).then(i => { setInfo(i); if (!i.run.running) { onDone(); toast(`Clean-up done: ${i.run.moved.toLocaleString()} books moved`) } }).catch(() => {})
    }, 2000)
    return () => clearInterval(t)
  }, [running]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!info || info.error && !running) return info?.error ? <p className="muted small review-help">{info.error}</p> : null
  const plan = info.plan
  const total = plan?.moves.reduce((n, m) => n + m.count, 0) || 0
  const run = info.run
  if (!running && total === 0 && !run.finished_at) return null
  const apply = async () => {
    setAsking(false)
    try { const r = await api.post<{ run: CleanupRun }>(`${V1}/review/cleanup`); setInfo(i => i && { ...i, run: r.run }) } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <section className="card review-cleanup">
      <div className="row" style={{ gap: 10, alignItems: 'center' }}>
        <Icon name="wand" size={16} />
        <h3 className="grow" style={{ margin: 0 }}>Suggested clean-up</h3>
        {!running && total > 0 && <button type="button" className="btn sm primary" onClick={() => setAsking(true)}>Apply</button>}
      </div>
      {running ? (
        <div className="mt-sm">
          <div className="small">Moving books… {run.done.toLocaleString()} of {run.total.toLocaleString()}</div>
          <div className="review-progress"><span style={{ width: `${run.total ? Math.round(run.done / run.total * 100) : 0}%` }} /></div>
        </div>
      ) : (
        <>
          {total > 0 && plan && <>
            <p className="small muted" style={{ margin: '6px 0 8px' }}>
              {plan.main.name} keeps good books only; the rest go to {plan.rename_review_to ? `${plan.review.name} (renamed ${plan.rename_review_to})` : plan.review.name}. Books keep their reading history, highlights and links; files move on the disk.
              {plan.protected > 0 && ` ${plan.protected} flagged books stay because someone is reading them.`}
            </p>
            <ul className="review-moves">
              {plan.moves.map(m => (
                <li key={m.to_library_id}>
                  <b>{m.count.toLocaleString()}</b> from {m.from} → <b>{m.to}</b>
                  <span className="muted small"> · {Object.entries(m.reasons).sort((a, b) => b[1] - a[1]).map(([k, n]) => `${n.toLocaleString()} ${REASON[k] || k}`).join(' · ')}</span>
                  {open && <div className="xsmall muted">e.g. {m.samples.join('; ')}</div>}
                </li>
              ))}
            </ul>
            <button type="button" className="link-btn small" onClick={() => setOpen(o => !o)}>{open ? 'Hide examples' : 'Show examples'}</button>
          </>}
          {run.finished_at && <p className="small" style={{ margin: '8px 0 0' }}>Last clean-up: {run.moved.toLocaleString()} of {run.total.toLocaleString()} books moved{run.errors.length ? `, ${run.errors.length} failed (${run.errors.slice(0, 3).join('; ')})` : ''}.</p>}
        </>
      )}
      {asking && plan && <Confirm title="Apply the clean-up?" confirmLabel={`Move ${total.toLocaleString()} books`} danger={false} onCancel={() => setAsking(false)} onConfirm={apply}
        message={`${total.toLocaleString()} books move between libraries and their files move on the disk. Every move is logged (moves.jsonl in the data folder) so it can be put back.`} />}
    </section>
  )
}
