// Translate: English books into Serbian with Pocket AI. Drop a file here or
// use "Translate to Serbian" on a book's page; the finished book is filed
// into the library. Jobs run one after the other on the server.
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Translation } from '../lib/types'
import { fmtBytes, fmtDateTime } from '../lib/format'
import { Card, Confirm, Empty, Loading, Modal, PageHeader, errMsg, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Progress } from '../components/cards'
import { AIState, TranslateFields, type TranslateChoice, saveChoice, savedChoice, usePocketAI } from '../components/TranslateOptions'
import '../styles/admin.css'

const ACCEPT = '.epub,.kepub,.pdf,.mobi,.azw,.azw3,.prc,.fb2,.fbz,.docx,.rtf,.txt'

const STATUS: Record<Translation['status'], [string, string]> = {
  queued: ['', 'Waiting'], running: ['accent', 'Translating'], done: ['ok', 'Done'], failed: ['danger', 'Failed'], cancelled: ['', 'Cancelled'],
}
const STEP: Record<string, string> = {
  start: 'Starting', convert: 'Converting to EPUB', analyze: 'Reading the book', translate: 'Translating', assemble: 'Putting the book together', deliver: 'Filing into the library',
}

function mins(ms: number): string {
  const m = Math.round(ms / 60000)
  return m < 1 ? 'under a minute' : m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${m % 60} min`
}

export default function Translate() {
  const { toast } = useToast()
  const { settings, user } = useApp()
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const { status, models } = usePocketAI()
  const { data: jobs, reload, setData } = useAsync(() => api.get<Translation[]>(`${V1}/translations`), [])
  const [file, setFile] = useState<File | null>(null)
  const [over, setOver] = useState(false)
  const [choice, setChoice] = useState<TranslateChoice>(savedChoice)
  const [progress, setProgress] = useState<number | null>(null)
  const [log, setLog] = useState<Translation | null>(null)
  const [del, setDel] = useState<Translation | null>(null)
  const input = useRef<HTMLInputElement>(null)

  // follow running jobs
  const active = !!jobs?.some(j => j.status === 'queued' || j.status === 'running')
  useEffect(() => {
    if (!active) return
    const t = window.setInterval(() => { api.get<Translation[]>(`${V1}/translations`).then(setData).catch(() => {}) }, 2500)
    return () => clearInterval(t)
  }, [active, setData])

  const start = () => {
    if (!file) return
    saveChoice(choice)
    const body = new FormData()
    body.append('file', file, file.name)
    body.append('script', choice.script)
    body.append('model', choice.model)
    body.append('notes', choice.notes)
    const req = new XMLHttpRequest()
    req.open('POST', `${V1}/translations`)
    req.withCredentials = true
    req.upload.onprogress = e => { if (e.lengthComputable) setProgress(Math.round(e.loaded / e.total * 100)) }
    req.onerror = () => { setProgress(null); toast('The connection was interrupted.', 'err') }
    req.onload = () => {
      setProgress(null)
      let j: { error?: string } = {}
      try { j = JSON.parse(req.responseText) } catch { /* not JSON */ }
      if (req.status >= 200 && req.status < 300) {
        setFile(null); setChoice(c => ({ ...c, notes: '' })); reload(); window.dispatchEvent(new CustomEvent('plb:translations'))
        toast('Translation started — you can leave this page, it carries on')
      } else toast(req.status === 413 ? 'Too big for this server.' : j.error || `Upload failed (${req.status})`, 'err')
    }
    setProgress(0)
    req.send(body)
  }

  const act = async (j: Translation, what: 'cancel' | 'retry') => {
    try { await api.post(`${V1}/translations/${j.id}/${what}`); reload(); toast(what === 'retry' ? 'Trying again — finished parts are kept' : 'Stopping…') }
    catch (e) { toast(errMsg(e), 'err') }
  }
  const remove = async (j: Translation) => {
    try { await api.del(`${V1}/translations/${j.id}`); setDel(null); reload(); toast(j.result_book_id ? 'Removed from the list; the book stays in the library' : 'Removed') }
    catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <div className="tr-page">
      <PageHeader title={<><Icon name="languages" size={22} /> Translate</>} sub="English books into Serbian with Pocket AI, part by part, keeping the book's layout. The Serbian edition is filed into the library." />
      <div className="stack loose">
        <AIState status={status} models={models} />
        {editor && (
          <Card title="New translation" sub="Or open any book in the library and choose More → Translate to Serbian.">
            <div className="stack">
              <div className={`up-drop ${over ? 'over' : ''}`} role="button" tabIndex={0}
                onClick={() => input.current?.click()} onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); input.current?.click() } }}
                onDragOver={e => { e.preventDefault(); setOver(true) }} onDragLeave={() => setOver(false)}
                onDrop={e => { e.preventDefault(); setOver(false); if (e.dataTransfer.files[0]) setFile(e.dataTransfer.files[0]) }}>
                <Icon name={file ? 'book' : 'upload'} size={28} />
                {file ? <><b className="ellipsis" style={{ maxWidth: '100%' }}>{file.name}</b><span className="muted small">{fmtBytes(file.size)} · click to choose another</span></>
                  : <><b>Drop a book here, or click to choose</b><span className="muted small">EPUB works best; PDF, MOBI, AZW3, FB2, DOCX, RTF and TXT are converted first.</span></>}
                <input ref={input} type="file" accept={ACCEPT} hidden onChange={e => { if (e.target.files?.[0]) setFile(e.target.files[0]); e.target.value = '' }} />
              </div>
              <TranslateFields value={choice} onChange={setChoice} models={models} />
              <div className="row wrap">
                <button type="button" className="btn primary" disabled={!file || progress !== null || !status?.ready} onClick={start}>
                  <Icon name="languages" size={15} />{progress !== null ? `Uploading ${progress}%` : 'Translate'}
                </button>
                {file && progress === null && <button type="button" className="btn ghost" onClick={() => setFile(null)}>Clear</button>}
              </div>
            </div>
          </Card>
        )}

        {!jobs ? <Loading /> : jobs.length === 0 ? (
          <Empty icon="languages" title="Nothing translated yet">Translations you start show up here with their progress.</Empty>
        ) : (
          <div className="stack">
            {jobs.map(j => <JobCard key={j.id} j={j} onLog={() => setLog(j)} onCancel={() => act(j, 'cancel')} onRetry={() => act(j, 'retry')} onDelete={() => setDel(j)} />)}
          </div>
        )}
      </div>
      {log && <LogModal j={log} onClose={() => setLog(null)} />}
      {del && <Confirm title="Remove this translation?" confirmLabel="Remove"
        message={del.status === 'running' ? 'It stops and its parts are thrown away.' : del.result_book_id ? 'Only the entry and its working files go; the Serbian book stays in the library.' : 'Its working files go too.'}
        onConfirm={() => remove(del)} onCancel={() => setDel(null)} />}
    </div>
  )
}

function clock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60
  return h ? `${h}:${String(m).padStart(2, '0')}:${String(r).padStart(2, '0')}` : `${m}:${String(r).padStart(2, '0')}`
}

/** Re-render every second while on. */
function useNow(on: boolean) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    if (!on) return
    const t = window.setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [on])
  return now
}

/** The running job: percent, a bar with the parts at Pocket AI pulsing ahead of the done ones, a live clock. */
function LiveProgress({ j }: { j: Translation }) {
  const now = useNow(true)
  const total = j.batches
  const done = total ? j.done_batches / total : 0
  const flying = total ? Math.min(j.active, total - j.done_batches) / total : 0
  const pctDone = Math.round(done * 100)
  const spent = j.started_at ? now - new Date(j.started_at).getTime() : 0
  let eta = ''
  const fresh = j.done_batches - j.cached_batches
  if (j.step === 'translate' && fresh > 0) eta = `about ${mins((spent / fresh) * (total - j.done_batches))} left`
  else if (j.step === 'translate' && total) eta = 'working out how long…'
  const indeterminate = !total || j.step !== 'translate'
  return (
    <div className="tr-live">
      <div className="tr-live-head">
        <span className="tr-pct tnum">{indeterminate && j.step !== 'assemble' && j.step !== 'deliver' ? <span className="spinner" /> : `${j.step === 'translate' ? pctDone : 100}%`}</span>
        <span className="grow">
          <b>{STEP[j.step] || 'Starting'}</b>
          {total > 0 && j.step === 'translate' && <span className="muted"> · {j.done_batches} of {total} parts done{j.cached_batches ? ` (${j.cached_batches} kept from before)` : ''}</span>}
        </span>
        <span className="muted small tnum" title="Time since it started"><Icon name="clock" size={13} /> {clock(spent)}</span>
      </div>
      <div className={`tr-bar ${indeterminate ? 'indeterminate' : ''}`} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={indeterminate ? undefined : pctDone}>
        {!indeterminate && <><span className="done" style={{ width: `${done * 100}%` }} /><span className="flying" style={{ width: `${flying * 100}%` }} /></>}
      </div>
      <div className="small muted row" style={{ gap: 8 }}>
        {j.active > 0 && <span className="tr-dots" aria-hidden="true"><i /><i /><i /></span>}
        <span className="grow">{j.active > 0 ? `${j.active} ${j.active === 1 ? 'part is' : 'parts are'} being translated by Pocket AI right now` : j.step === 'translate' ? 'Waiting for Pocket AI…' : ''}</span>
        {eta && <span className="tnum">{eta}</span>}
      </div>
    </div>
  )
}

function JobCard({ j, onLog, onCancel, onRetry, onDelete }: { j: Translation; onLog: () => void; onCancel: () => void; onRetry: () => void; onDelete: () => void }) {
  const [cls, label] = STATUS[j.status]
  const [showWarn, setShowWarn] = useState(false)
  const frac = j.batches ? j.done_batches / j.batches : 0
  const running = j.status === 'running'
  const took = j.started_at && j.finished_at ? mins(new Date(j.finished_at).getTime() - new Date(j.started_at).getTime()) : ''
  return (
    <section className="card tr-job">
      <div className="card-body stack">
        <div className="row top">
          <div className="grow" style={{ minWidth: 0 }}>
            <b className="tr-title">{j.title || j.source_name}</b>{j.author && <span className="muted"> — {j.author}</span>}
            {j.title_translated && j.title_translated !== j.title && <div className="small tr-sr"><Icon name="arrow-right" size={13} /><span>{j.title_translated}</span></div>}
          </div>
          <span className={`pill ${cls}`}>{running && <span className="spinner" style={{ width: 10, height: 10 }} />}{label}</span>
        </div>
        {running ? <LiveProgress j={j} /> : (j.status === 'done' || frac > 0) && <Progress value={j.status === 'done' ? 1 : frac} />}
        {!running && <div className="small muted">
          {j.status === 'queued' ? 'Waiting for the translation before it'
            : j.status === 'done' ? <>Done {fmtDateTime(j.finished_at)}{took ? ` · took ${took}` : ''}</>
            : j.status === 'failed' ? <span className="danger-text">{j.error}</span>
            : j.batches ? `Stopped at ${j.done_batches} of ${j.batches} parts` : 'Stopped'}
        </div>}
        <div className="xsmall muted">
          {j.source_book_id ? <Link to={`/book/${j.source_book_id}`}>{j.source_name}</Link> : j.source_name}
          {j.source_size ? ` · ${fmtBytes(j.source_size)}` : ''} · {j.script === 'cyrillic' ? 'ćirilica' : 'latinica'}{j.model ? ` · ${j.model}` : ''}
          {j.units ? ` · ${j.units.toLocaleString()} paragraphs` : ''}{j.notes ? ` · notes: “${j.notes.length > 60 ? j.notes.slice(0, 60) + '…' : j.notes}”` : ''}
        </div>
        {j.warnings.length > 0 && (
          <div className="callout warn small"><Icon name="alert" size={14} />
            <div className="grow">
              <button type="button" className="link-btn" onClick={() => setShowWarn(s => !s)}>{j.warnings.length} {j.warnings.length === 1 ? 'part' : 'parts'} stayed in English</button>
              {showWarn && <ul className="tr-warn">{j.warnings.slice(0, 50).map((w, i) => <li key={i}>{w}</li>)}</ul>}
            </div>
          </div>
        )}
        <div className="row wrap">
          {j.status === 'done' && j.result_book_id > 0 && <Link to={`/book/${j.result_book_id}`} className="btn primary sm"><Icon name="book-open" size={14} />Open the book</Link>}
          {j.status === 'done' && <a className="btn sm" href={`${V1}/translations/${j.id}/download`}><Icon name="download" size={14} />EPUB</a>}
          {(running || j.status === 'queued') && <button type="button" className="btn sm" onClick={onCancel}>Stop</button>}
          {(j.status === 'failed' || j.status === 'cancelled') && <button type="button" className="btn primary sm" onClick={onRetry}><Icon name="refresh" size={14} />Try again</button>}
          <button type="button" className="btn ghost sm" onClick={onLog}><Icon name="list" size={14} />Log</button>
          <span style={{ flex: 1 }} />
          <button type="button" className="btn ghost sm danger-text" onClick={onDelete}><Icon name="trash" size={14} />Remove</button>
        </div>
      </div>
    </section>
  )
}

function LogModal({ j, onClose }: { j: Translation; onClose: () => void }) {
  const { data, error } = useAsync(() => api.getText(`${V1}/translations/${j.id}/log`), [j.id])
  return (
    <Modal title={`Log · ${j.title || j.source_name}`} onClose={onClose} size="lg">
      {error ? <p className="danger-text">{error}</p> : data === null ? <Loading /> : <pre className="tr-log">{data || 'Nothing logged yet.'}</pre>}
    </Modal>
  )
}
