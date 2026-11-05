// Review: books that look wrong, for editors to fix (on the book's page),
// delete there, or keep off e-readers' catalogues from here, one or many.
import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
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
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const [sp, setSp] = useSearchParams()
  const tab = sp.get('tab') === 'ai' ? 'ai' : 'wrong'
  const waiting = useApi<SortPage>(editor ? `${V1}/review/imports?status=suggested&limit=1` : null)
  if (!editor) return <Empty icon="alert" title="Only editors review books" />
  const n = waiting.data?.counts?.suggested || 0
  return (
    <div className="review-page">
      <PageHeader title="Review" actions={<Segmented size="sm" value={tab} label="View" onChange={v => setSp(v === 'ai' ? { tab: 'ai' } : {}, { replace: true })}
        options={[{ value: 'wrong', label: 'Looks wrong' }, { value: 'ai', label: n ? `AI sorting · ${n.toLocaleString()}` : 'AI sorting' }]} />} />
      {tab === 'ai' ? <AISorting onChange={waiting.reload} /> : <LooksWrong />}
    </div>
  )
}

function LooksWrong() {
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
    <>
      <p className="muted small review-help">
        {data && <b>{data.total.toLocaleString()} books look wrong{flag ? ` · ${FLAG[flag].label.toLowerCase()}` : ''}. </b>}
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
          <button type="button" className="btn sm" onClick={async () => { try { const r = await api.post<{ queued: number }>(`${V1}/review/imports/ask`, { ids: [...picked] }); toast(`Asked Pocket AI about ${r.queued.toLocaleString()} books: see AI sorting`); setPicked(new Set()) } catch (e) { toast(errMsg(e), 'err') } }}><Icon name="wand" size={14} />Ask Pocket AI</button>
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
    </>
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

interface SortRow { book_id: number; status: string; target: 'books' | 'tech' | 'review'; reason: string; confidence: number; source: string; error?: string; title: string; authors: string[]; cover: string; library: string; to?: string; stays?: boolean; updated_at: string }
interface SortPage { items: SortRow[]; total: number; counts: Record<string, number>; ai: { ready: boolean; detail?: string } }
const SOURCE: Record<string, string> = { upload: 'uploaded', ingest: 'ingest folder', scan: 'found by a scan', asked: 'asked' }

/** Pocket AI's suggestions for new books: where each would end up, to move or keep. */
function AISorting({ onChange }: { onChange: () => void }) {
  const { user } = useApp()
  const { toast } = useToast()
  const admin = user?.role === 'admin'
  const [status, setStatus] = useState<'suggested' | 'pending' | 'done'>('suggested')
  const [offset, setOffset] = useState(0)
  const [picked, setPicked] = useState<Set<number>>(new Set())
  const [busy, setBusy] = useState(false)
  const { data, error, reload } = useApi<SortPage>(`${V1}/review/imports?status=${status}&offset=${offset}&limit=${PAGE}`)
  useEffect(() => { setOffset(0); setPicked(new Set()) }, [status])
  // books waiting for Pocket AI: look again every few seconds
  useEffect(() => {
    if (!(data?.counts?.pending)) return
    const t = setInterval(() => { reload(); onChange() }, 5000)
    return () => clearInterval(t)
  }, [data?.counts?.pending]) // eslint-disable-line react-hooks/exhaustive-deps
  const items = data?.items || []
  const toggle = (id: number) => setPicked(p => { const n = new Set(p); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const act = async (path: string, ids: number[], done: (r: Record<string, unknown>) => string) => {
    setBusy(true)
    try { const r = await api.post<Record<string, unknown>>(`${V1}/review/imports/${path}`, { ids }); toast(done(r)); setPicked(new Set()); reload(); onChange() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const apply = (ids: number[]) => act('apply', ids, r => `${Number(r.moved || 0).toLocaleString()} moved${r.stayed ? `, ${r.stayed} already there` : ''}`)
  const keep = (ids: number[]) => act('keep', ids, r => `${Number(r.kept || 0).toLocaleString()} kept where they are`)
  const c = data?.counts || {}
  return (
    <>
      <p className="muted small review-help">
        Pocket AI looks at every book that comes in (uploads, the ingest folder, new files a scan finds) and says where it belongs: <b>Books</b>, <b>Tech books</b> or <b>Review</b>. Nothing moves until you say so. You can also pick books under <i>Looks wrong</i> and ask it about them.
      </p>
      {data && !data.ai.ready && <div className="callout mb"><Icon name="alert" size={15} /><span>Pocket AI isn't available right now ({data.ai.detail}); books wait in the queue until it is.</span></div>}
      <div className="review-bar">
        <Segmented size="sm" value={status} onChange={setStatus} label="Show" options={[
          { value: 'suggested', label: `To decide${c.suggested ? ` · ${c.suggested}` : ''}` },
          { value: 'pending', label: `Waiting for AI${(c.pending || 0) + (c.failed || 0) ? ` · ${(c.pending || 0) + (c.failed || 0)}` : ''}` },
          { value: 'done', label: 'Done' },
        ]} />
        <span className="grow" />
        {status === 'suggested' && picked.size > 0 && <>
          <span className="small muted">{picked.size} picked</span>
          {admin && <button type="button" className="btn sm primary" disabled={busy} onClick={() => apply([...picked])}>Move picked</button>}
          <button type="button" className="btn sm" disabled={busy} onClick={() => keep([...picked])}>Keep where they are</button>
        </>}
        {status === 'suggested' && items.length > 0 && <button type="button" className="btn sm ghost" onClick={() => setPicked(picked.size === items.length ? new Set() : new Set(items.map(i => i.book_id)))}>{picked.size === items.length ? 'Pick none' : 'Pick all on this page'}</button>}
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!data && !error && <Loading />}
      {data && items.length === 0 && <Empty icon="check-circle" title={status === 'suggested' ? 'Nothing to decide' : status === 'pending' ? 'Nothing waiting for Pocket AI' : 'Nothing sorted yet'} />}
      {items.length > 0 && (
        <ul className="review-list">
          {items.map(it => (
            <li key={it.book_id} className={picked.has(it.book_id) ? 'picked' : ''}>
              {status === 'suggested' && <input type="checkbox" checked={picked.has(it.book_id)} onChange={() => toggle(it.book_id)} aria-label={`Pick ${it.title}`} />}
              <Link to={`/book/${it.book_id}`} className="review-cover">{it.cover ? <img src={`${it.cover}&w=200`} alt="" loading="lazy" /> : <Icon name="book" size={18} />}</Link>
              <div className="review-main">
                <Link to={`/book/${it.book_id}`} className="review-title">{it.title || '(no title)'}</Link>
                <div className="small muted ellipsis">{it.authors.join(', ') || 'No author'} · {SOURCE[it.source] || it.source}</div>
                <div className="review-tags">
                  <span className="review-flag">{it.library}</span>
                  {it.to && <><Icon name="arrow-right" size={12} /><span className={`review-flag to-${it.target}`}>{it.stays ? `stays in ${it.to}` : it.to}</span></>}
                  {it.status === 'pending' && <span className="review-flag">waiting for Pocket AI</span>}
                  {it.status === 'failed' && <span className="review-flag to-review" title={it.error}>Pocket AI failed: {it.error}</span>}
                  {(it.status === 'applied' || it.status === 'kept') && <span className="review-flag off">{it.status === 'applied' ? 'moved' : 'kept'}</span>}
                  {it.reason && <span className="small muted">{it.reason}{it.confidence ? ` · ${Math.round(it.confidence * 100)}% sure` : ''}</span>}
                </div>
              </div>
              {it.status === 'suggested' && !it.stays && admin && <button type="button" className="btn sm" disabled={busy} onClick={() => apply([it.book_id])}>Move</button>}
              {it.status === 'suggested' && <button type="button" className="btn sm ghost" disabled={busy} onClick={() => keep([it.book_id])} title="Leave it where it is">{it.stays ? 'OK' : 'Keep'}</button>}
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
    </>
  )
}
