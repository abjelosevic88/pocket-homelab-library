// Upload books: drop several files, pick the library, watch each one go up.
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useAsync, useToast } from './ui'
import { api } from '../lib/api'
import { Select } from './Select'
import { Icon } from './icons'
import { fmtBytes } from '../lib/format'
import type { Library } from '../lib/types'
import '../styles/admin.css'

const ACCEPT = '.epub,.kepub,.pdf,.mobi,.azw,.azw3,.prc,.fb2,.fbz,.txt,.rtf,.doc,.docx,.djvu,.cbz,.cbr,.cb7,.cbt'

type Row = { file: File; progress: number; state: 'waiting' | 'uploading' | 'done' | 'failed' | 'skipped'; message?: string; books?: number[] }

/** onUploaded gets the new book ids. */
export default function UploadBooks({ onUploaded }: { onUploaded?: (ids: number[]) => void }) {
  const { settings } = useApp()
  const { toast } = useToast()
  const { data: libs } = useAsync(() => api.get<Library[]>(`${V1}/libraries`), [])
  const [library, setLibrary] = useState('')
  const [rows, setRows] = useState<Row[]>([])
  const [over, setOver] = useState(false)
  const [busy, setBusy] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const xhr = useRef<XMLHttpRequest | null>(null)
  const cancelled = useRef(false)
  useEffect(() => { if (!library && libs?.length) setLibrary(String((libs.find(l => l.uploads) || libs[0]).id)) }, [libs, library])
  useEffect(() => () => { cancelled.current = true; xhr.current?.abort() }, [])
  useEffect(() => {
    if (!busy) return
    const warn = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = '' }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [busy])

  const add = (files: FileList | File[]) => {
    const list = Array.from(files)
    if (list.length) setRows(r => [...r.filter(x => x.state !== 'done'), ...list.map(file => ({ file, progress: 0, state: 'waiting' as const }))])
  }
  const set = (i: number, patch: Partial<Row>) => setRows(r => r.map((x, j) => j === i ? { ...x, ...patch } : x))

  // one file per request, so each gets its own progress and a failure stays alone
  const sendOne = (row: Row, i: number) => new Promise<number[]>(resolve => {
    const body = new FormData()
    body.append('files', row.file, row.file.name)
    if (library) body.append('library_id', library)
    const req = new XMLHttpRequest()
    xhr.current = req
    req.open('POST', `${V1}/upload`)
    req.withCredentials = true
    req.upload.onprogress = e => { if (e.lengthComputable) set(i, { progress: Math.round(e.loaded / e.total * 100) }) }
    req.onerror = () => { set(i, { state: 'failed', message: 'The connection was interrupted.' }); resolve([]) }
    req.onabort = () => { set(i, { state: 'failed', message: 'Cancelled' }); resolve([]) }
    req.onload = () => {
      let j: { books?: number[]; skipped?: { name: string; reason: string }[]; error?: string } = {}
      try { j = JSON.parse(req.responseText) } catch { /* not JSON */ }
      if (req.status === 401) window.dispatchEvent(new CustomEvent('plb:unauthorized'))
      if (req.status >= 200 && req.status < 300) {
        const books = j.books || []
        if (!books.length && j.skipped?.length) set(i, { state: 'skipped', progress: 100, message: j.skipped[0].reason })
        else set(i, { state: 'done', progress: 100, books })
        resolve(books)
      } else {
        set(i, { state: 'failed', message: req.status === 413 ? 'Too big for this server.' : j.error || `Upload failed (${req.status})` })
        resolve([])
      }
    }
    set(i, { state: 'uploading', progress: 0, message: undefined })
    req.send(body)
  })

  const start = async () => {
    setBusy(true); cancelled.current = false
    const ids: number[] = []
    const snapshot = rows
    for (let i = 0; i < snapshot.length; i++) {
      if (cancelled.current) break
      if (snapshot[i].state !== 'waiting' && snapshot[i].state !== 'failed') continue
      ids.push(...await sendOne(snapshot[i], i))
    }
    xhr.current = null
    setBusy(false)
    if (ids.length) { toast(`${ids.length} ${ids.length === 1 ? 'book' : 'books'} added`); onUploaded?.(ids) }
  }
  const cancel = () => { cancelled.current = true; xhr.current?.abort() }
  const pending = rows.filter(r => r.state === 'waiting' || r.state === 'failed').length
  const lib = libs?.find(l => String(l.id) === library)

  return (
    <div className="stack loose">
      <div className={`up-drop ${over ? 'over' : ''}`} role="button" tabIndex={0}
        onClick={() => input.current?.click()} onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); input.current?.click() } }}
        onDragOver={e => { e.preventDefault(); setOver(true) }} onDragLeave={() => setOver(false)}
        onDrop={e => { e.preventDefault(); setOver(false); add(e.dataTransfer.files) }}>
        <Icon name="upload" size={28} />
        <b>Drop books here, or click to choose</b>
        <span className="muted small">EPUB, PDF, MOBI, AZW3, FB2, comics (CBZ, CBR) and more. Several at once is fine.</span>
        <input ref={input} type="file" multiple accept={ACCEPT} hidden onChange={e => { if (e.target.files) add(e.target.files); e.target.value = '' }} />
      </div>
      {libs && libs.length > 1 && (
        <div className="row wrap">
          <span className="muted small">Put them in</span>
          <div style={{ minWidth: 220 }}><Select ariaLabel="Library" value={library} onChange={setLibrary} options={libs.map(l => ({ value: String(l.id), label: l.name, hint: l.uploads ? 'uploads' : undefined }))} /></div>
        </div>
      )}
      {lib && <p className="muted small">Files are saved in the library's folder as <span className="adm-example">{lib.upload_template}</span>.</p>}
      {rows.length > 0 && (
        <div className="up-list">
          {rows.map((r, i) => (
            <div key={i} className="up-row">
              <span className="ellipsis">{r.file.name} <span className="muted small">· {fmtBytes(r.file.size)}</span></span>
              <span className="row" style={{ gap: 6 }}>
                {r.state === 'done' && r.books?.[0] ? <Link to={`/book/${r.books[0]}`} className="pill ok"><Icon name="check" size={12} />Open</Link>
                  : r.state === 'done' ? <span className="pill ok">Added</span>
                  : r.state === 'failed' ? <span className="pill danger" title={r.message}>Failed</span>
                  : r.state === 'skipped' ? <span className="pill warn" title={r.message}>Skipped</span>
                  : r.state === 'uploading' ? <span className="pill accent">{r.progress}%</span> : <span className="pill">Waiting</span>}
                {!busy && r.state !== 'done' && <button type="button" className="btn ghost icon-only sm" aria-label={`Remove ${r.file.name}`} onClick={() => setRows(x => x.filter((_, j) => j !== i))}><Icon name="x" size={14} /></button>}
              </span>
              {r.state === 'uploading' && <progress max={100} value={r.progress} />}
              {r.message && r.state !== 'uploading' && <span className={`small ${r.state === 'failed' ? 'danger-text' : 'muted'}`} style={{ gridColumn: '1 / -1' }}>{r.message}</span>}
            </div>
          ))}
        </div>
      )}
      <div className="row wrap">
        {busy ? <button type="button" className="btn" onClick={cancel}>Cancel</button>
          : <button type="button" className="btn primary" disabled={!pending || !library} onClick={start}><Icon name="upload" size={15} />{pending ? `Upload ${pending} ${pending === 1 ? 'file' : 'files'}` : 'Upload'}</button>}
        {!busy && rows.some(r => r.state === 'done') && <button type="button" className="btn ghost" onClick={() => setRows(r => r.filter(x => x.state !== 'done'))}>Clear finished</button>}
      </div>
      {settings?.ingest_dir && (
        <p className="row top muted small" style={{ gap: 6 }}>
          <Icon name="inbox" size={13} /><span>{settings.ingest_enabled ? <>You can also drop files into <code>{settings.ingest_dir}</code> on the server: they appear here within a minute.</> : <>The drop folder <code>{settings.ingest_dir}</code> is switched off (Settings → E-readers & files).</>}</span>
        </p>
      )}
    </div>
  )
}
