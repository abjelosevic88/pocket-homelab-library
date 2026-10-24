// Library admin: libraries and scanning, uploads, metadata health,
// duplicates and imports from Calibre-Web, Kavita and Kindle clippings.
import React, { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, uploadForm, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Book, DuplicateGroup, Library, LibraryKind, Page, ScanStatus, User } from '../lib/types'
import { ago, fmtDateTime, fmtSeconds } from '../lib/format'
import { Card, Confirm, Empty, Field, Loading, Modal, PageHeader, Segmented, Spinner, Switch, Tabs, errMsg, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { MiniCover } from '../components/DevCover'
import UploadBooks from '../components/UploadBooks'
import '../styles/admin.css'

type TabId = 'libraries' | 'upload' | 'health' | 'duplicates' | 'import'

type ScanInfo = {
  status: ScanStatus
  libraries: { id: number; scanning: boolean; last_scan_at: string; last_scan_ms: number; last_scan_error: string }[]
  hashing?: { pending: number }
  enrich?: { pending: number }
  metadata?: { running: boolean; done: number; total: number; updated: number }
}

const KIND_LABEL: Record<LibraryKind, string> = { books: 'Books', comics: 'Comics', manga: 'Manga' }
const KIND_ICON: Record<LibraryKind, string> = { books: 'book', comics: 'layers', manga: 'book-open' }

/** Polls /scan while anything runs (every 2 s), else every 30 s. */
function useScan() {
  const [scan, setScan] = useState<ScanInfo | null>(null)
  const load = useCallback(() => api.get<ScanInfo>(`${V1}/scan`).then(setScan).catch(() => {}), [])
  const running = !!scan?.status?.running || !!scan?.metadata?.running || !!scan?.libraries?.some(l => l.scanning)
  useEffect(() => {
    load()
    const t = setInterval(load, running ? 2000 : 30000)
    return () => clearInterval(t)
  }, [load, running])
  return { scan, reload: load, running }
}

export default function Admin() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const { user } = useApp()
  const admin = user?.role === 'admin'
  const editor = admin || !!user?.can_edit
  const tabs: { id: TabId; label: string; icon: string }[] = [
    ...(admin ? [{ id: 'libraries' as const, label: 'Libraries', icon: 'library' }] : []),
    ...(editor ? [{ id: 'upload' as const, label: 'Upload', icon: 'upload' }, { id: 'health' as const, label: 'Metadata', icon: 'wand' }, { id: 'duplicates' as const, label: 'Duplicates', icon: 'copy' }] : []),
    { id: 'import', label: 'Import', icon: 'inbox' },
  ]
  const tab: TabId = tabs.find(t => t.id === raw)?.id || tabs[0].id
  const { scan, reload, running } = useScan()
  return (
    <div className="stack loose">
      <PageHeader title="Library admin" sub={admin ? 'Folders, scanning, uploads and keeping the catalogue tidy.' : 'Uploads, metadata and imports.'} />
      <Tabs tabs={tabs} value={tab} onChange={t => navigate(`/admin/${t}`, { replace: true })} />
      {tab === 'libraries' && admin && <Libraries scan={scan} reloadScan={reload} running={running} />}
      {tab === 'upload' && editor && <Card><UploadBooks /></Card>}
      {tab === 'health' && editor && <Health scan={scan} reloadScan={reload} admin={admin} />}
      {tab === 'duplicates' && editor && <Duplicates />}
      {tab === 'import' && <Imports admin={admin} />}
    </div>
  )
}

// ---------- libraries ----------

function Libraries({ scan, reloadScan, running }: { scan: ScanInfo | null; reloadScan: () => void; running: boolean }) {
  const { toast } = useToast()
  const { data: libs, reload, error } = useAsync(() => api.get<Library[]>(`${V1}/libraries`), [])
  const { data: users } = useAsync(() => api.get<User[]>(`${V1}/users`), [])
  const [edit, setEdit] = useState<Partial<Library> | null>(null)
  const [del, setDel] = useState<Library | null>(null)
  // a scan that just ended changes the counts
  const [wasRunning, setWasRunning] = useState(false)
  useEffect(() => { if (wasRunning && !running) reload(); setWasRunning(running) }, [running]) // eslint-disable-line react-hooks/exhaustive-deps

  const startScan = async (id: number | 0, full: boolean) => {
    try {
      await api.post(id ? `${V1}/libraries/${id}/scan${full ? '?full=1' : ''}` : `${V1}/scan${full ? '?full=1' : ''}`)
      toast(full ? 'Reading every file again' : 'Looking for new and changed books')
      reloadScan()
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const st = scan?.status
  return (
    <div className="stack loose">
      {st?.running && (
        <Card>
          <div className="stack tight">
            <div className="row"><Spinner /><b className="grow">Scanning {st.library || 'the libraries'}</b><span className="muted small">{st.phase}{st.total ? ` · ${st.done.toLocaleString()} of ${st.total.toLocaleString()}` : ''}</span></div>
            <div className={`adm-progress ${st.total ? '' : 'indeterminate'}`}><span style={st.total ? { width: `${Math.min(100, (st.done / st.total) * 100)}%` } : undefined} /></div>
            <span className="muted small">{st.added} added · {st.updated} updated · {st.removed} gone{st.errors?.length ? ` · ${st.errors.length} problems` : ''}</span>
          </div>
        </Card>
      )}
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => setEdit({ kind: 'books', paths: [''], access: 'all', people: [], uploads: false, upload_template: '{author}/{author} - {title}', scan_every: 60 })}><Icon name="plus" size={15} />Add a library</button>
        {libs && libs.length > 0 && <button type="button" className="btn" disabled={running} onClick={() => startScan(0, false)}><Icon name="refresh" size={15} />Scan all now</button>}
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>}
      {!libs ? (!error && <Loading />) : libs.length === 0 ? (
        <Empty title="No libraries yet" icon="library">A library is a folder of books, comics or manga on this server. Add one and the books in it show up within minutes.</Empty>
      ) : (
        <div className="adm-libs">
          {libs.map(l => {
            const s = scan?.libraries?.find(x => x.id === l.id)
            const at = s?.last_scan_at || l.last_scan_at
            const err = s?.last_scan_error ?? l.last_scan_error
            return (
              <div key={l.id} className="adm-lib">
                <div className="adm-lib-head">
                  <span className="set-app-ico" style={{ background: 'var(--accent)' }}><Icon name={KIND_ICON[l.kind]} size={17} /></span>
                  <div className="grow">
                    <div className="row wrap" style={{ gap: 6 }}><b>{l.name}</b><span className="badge">{KIND_LABEL[l.kind]}</span>{l.uploads && <span className="badge accent">uploads</span>}{l.access === 'people' && <span className="badge warn">{l.people.length} {l.people.length === 1 ? 'person' : 'people'}</span>}</div>
                    <div className="muted small">{l.books.toLocaleString()} {l.books === 1 ? 'book' : 'books'} · {s?.scanning ? 'scanning now…' : at ? `scanned ${ago(at)}${l.last_scan_ms ? ` in ${fmtSeconds(Math.round(l.last_scan_ms / 1000))}` : ''}` : 'not scanned yet'}{l.scan_every ? ` · every ${l.scan_every >= 60 ? `${l.scan_every / 60} h` : `${l.scan_every} min`}` : ' · by hand only'}</div>
                  </div>
                  <Link className="btn ghost sm" to={`/library?library=${l.id}`}>Open</Link>
                </div>
                <div className="adm-paths">{l.paths.map(p => <span key={p}>{p}</span>)}</div>
                {err && <div className="callout danger small"><Icon name="alert" size={14} /><span>{err}</span></div>}
                <div className="row wrap">
                  <button type="button" className="btn sm" disabled={running} onClick={() => startScan(l.id, false)}><Icon name="refresh" size={14} />Scan now</button>
                  <button type="button" className="btn sm ghost" disabled={running} onClick={() => startScan(l.id, true)} title="Read the details of every file again (slow)">Full rescan</button>
                  <span className="spacer" />
                  <button type="button" className="btn sm ghost" onClick={() => setEdit(l)}><Icon name="edit" size={14} />Edit</button>
                  <button type="button" className="btn sm ghost danger-text" onClick={() => setDel(l)}>Remove</button>
                </div>
              </div>
            )
          })}
        </div>
      )}
      {edit && <LibraryModal lib={edit} users={users || []} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload(); reloadScan() }} />}
      {del && <Confirm title={`Remove ${del.name}?`} confirmLabel="Remove"
        message={<>The library leaves Pocket Library with everyone's progress and highlights in its books. <b>The files stay on disk</b>; add the folder again to bring the books back.</>}
        onCancel={() => setDel(null)} onConfirm={async () => { const l = del; setDel(null); try { await api.del(`${V1}/libraries/${l.id}`); reload(); toast('Removed') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </div>
  )
}

function exampleOf(template: string) {
  const name = (template || '{author}/{author} - {title}')
    .replace(/\{author\}/g, 'Ivo Andrić').replace(/\{title\}/g, 'Na Drini ćuprija').replace(/\{series\}/g, 'Bosanska trilogija').replace(/\{series_index\}/g, '1')
    .replace(/\{year\}/g, '1945').replace(/\{language\}/g, 'sr')
  return `${name}.epub`
}

function LibraryModal({ lib, users, onClose, onSaved }: { lib: Partial<Library>; users: User[]; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({
    name: lib.name || '', kind: (lib.kind || 'books') as LibraryKind, paths: lib.paths?.length ? [...lib.paths] : [''], access: lib.access || 'all',
    people: lib.people || [], uploads: !!lib.uploads, upload_template: lib.upload_template || '{author}/{author} - {title}', scan_every: lib.scan_every ?? 60,
  })
  const [busy, setBusy] = useState(false)
  const setPath = (i: number, v: string) => setF({ ...f, paths: f.paths.map((p, j) => j === i ? v : p) })
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const body = { ...f, name: f.name.trim(), paths: f.paths.map(p => p.trim()).filter(Boolean) }
    if (!body.paths.length) { toast('Add at least one folder', 'err'); return }
    setBusy(true)
    try {
      if (lib.id) await api.put(`${V1}/libraries/${lib.id}`, body)
      else await api.post(`${V1}/libraries`, body)
      toast(lib.id ? 'Saved' : 'Library added — scanning starts in a moment')
      onSaved()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const members = users.filter(u => u.role !== 'admin')
  return (
    <Modal title={lib.id ? `Edit ${lib.name}` : 'Add a library'} onClose={onClose}>
      <form onSubmit={save} className="stack">
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} required maxLength={60} autoFocus placeholder="Books" /></Field>
        <Field group label="What's in it" help={f.kind === 'books' ? 'Ebooks and PDFs. Files with the same name in one folder (an EPUB and a MOBI) become one book with two formats.' : f.kind === 'comics' ? 'Comic archives (CBZ, CBR). Each file is an issue; the folder is the series.' : 'Manga chapters or volumes. Each file is one; the folder is the series, read right to left.'}>
          <Segmented value={f.kind} onChange={v => setF({ ...f, kind: v })} options={[{ value: 'books', label: 'Books' }, { value: 'comics', label: 'Comics' }, { value: 'manga', label: 'Manga' }]} />
        </Field>
        <Field group label="Folders" help="As the server sees them, e.g. /books or /media/ebooks/biblioteka. Every folder below them is included.">
          <div className="stack tight">
            {f.paths.map((p, i) => (
              <div key={i} className="adm-path-row">
                <input value={p} onChange={e => setPath(i, e.target.value)} placeholder="/books" aria-label={`Folder ${i + 1}`} />
                {f.paths.length > 1 && <button type="button" className="btn ghost icon-only sm" aria-label="Remove folder" onClick={() => setF({ ...f, paths: f.paths.filter((_, j) => j !== i) })}><Icon name="x" size={14} /></button>}
              </div>
            ))}
            <div><button type="button" className="btn ghost sm" onClick={() => setF({ ...f, paths: [...f.paths, ''] })}><Icon name="plus" size={14} />Add a folder</button></div>
          </div>
        </Field>
        <Field group label="Who can open it" help="Admins always can.">
          <Segmented value={f.access} onChange={v => setF({ ...f, access: v })} options={[{ value: 'all', label: 'Everyone' }, { value: 'people', label: 'Only some people' }]} />
        </Field>
        {f.access === 'people' && (
          <div className="stack tight">
            {members.length === 0 ? <p className="muted small">There is nobody else on this box yet.</p> : members.map(u => (
              <div key={u.id} className="row"><Switch size="sm" checked={f.people.includes(u.id)} onChange={v => setF({ ...f, people: v ? [...f.people, u.id] : f.people.filter(x => x !== u.id) })} label={u.name || u.email} /><span className="small">{u.name || u.email} <span className="muted">{u.name ? u.email : ''}</span></span></div>
            ))}
          </div>
        )}
        <div className="row" style={{ gap: 10 }}><Switch checked={f.uploads} onChange={v => setF({ ...f, uploads: v })} label="Uploads go here" /><span className="small">Uploads and the drop folder go into this library (only one library does)</span></div>
        {f.uploads && (
          <Field label="File names for uploads" help={<>Use {'{author}'}, {'{title}'}, {'{series}'}, {'{series_index}'}, {'{year}'}, {'{language}'}. For example: <span className="adm-example">{exampleOf(f.upload_template)}</span></>}>
            <input value={f.upload_template} onChange={e => setF({ ...f, upload_template: e.target.value })} className="mono" />
          </Field>
        )}
        <Field group label="Look for new books">
          <Segmented size="sm" value={f.scan_every} onChange={v => setF({ ...f, scan_every: v })} options={[{ value: 15, label: '15 min' }, { value: 60, label: 'Hourly' }, { value: 360, label: '6 h' }, { value: 1440, label: 'Daily' }, { value: 0, label: 'By hand' }]} />
        </Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{busy ? <Spinner /> : null}{lib.id ? 'Save' : 'Add library'}</button></div>
      </form>
    </Modal>
  )
}

// ---------- metadata health ----------

const MISSING: { key: string; label: string; icon: string }[] = [
  { key: 'cover', label: 'No cover', icon: 'eye-off' },
  { key: 'description', label: 'No description', icon: 'text' },
  { key: 'isbn', label: 'No ISBN', icon: 'tag' },
  { key: 'series', label: 'Not in a series', icon: 'layers' },
  { key: 'files', label: 'Files gone', icon: 'alert' },
]

function Health({ scan, reloadScan, admin }: { scan: ScanInfo | null; reloadScan: () => void; admin: boolean }) {
  const { toast } = useToast()
  const { settings } = useApp()
  const [counts, setCounts] = useState<Record<string, number | null>>({})
  const { data: libs } = useAsync(() => api.get<Library[]>(`${V1}/libraries`), [])
  useEffect(() => {
    for (const m of MISSING) {
      api.get<Page<Book>>(`${V1}/books?missing=${m.key}&limit=1&status=any`).then(p => setCounts(c => ({ ...c, [m.key]: p.total }))).catch(() => setCounts(c => ({ ...c, [m.key]: null })))
    }
  }, [])
  const total = libs?.reduce((a, l) => a + l.books, 0) || 0
  const job = scan?.metadata
  const fetchMissing = async () => {
    try { await api.post(`${V1}/metadata/fetch-missing`, {}); toast('Looking up missing details in the background'); reloadScan() } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div className="stack loose">
      <Card title="Books missing details" sub={total ? `Out of ${total.toLocaleString()} books. Open a list to fix them one by one with Edit → Find details.` : undefined}>
        <div className="adm-health">
          {MISSING.map(m => (
            <Link key={m.key} to={`/library?missing=${m.key}`}>
              <span className="n">{counts[m.key] === undefined ? <Spinner /> : counts[m.key] === null ? '—' : counts[m.key]!.toLocaleString()}</span>
              <span className="l"><Icon name={m.icon} size={13} /> {m.label}</span>
            </Link>
          ))}
        </div>
      </Card>
      {admin && (
        <Card title="Fill in what's missing" sub="Looks each book without a description or cover up on Open Library and Google Books, and only fills empty fields when the title and an author match. About one book a second.">
          <div className="stack tight">
            {job?.running ? <>
              <div className="row"><Spinner /><span className="grow">{job.done.toLocaleString()} of {job.total.toLocaleString()} looked up · {job.updated.toLocaleString()} filled in</span></div>
              <div className="adm-progress"><span style={{ width: `${job.total ? Math.min(100, (job.done / job.total) * 100) : 0}%` }} /></div>
            </> : <div className="row wrap"><button type="button" className="btn primary" onClick={fetchMissing}><Icon name="wand" size={15} />Fill in missing details</button>{job && job.total > 0 && <span className="muted small">Last run: {job.updated} of {job.total} filled in.</span>}</div>}
            {settings && !settings.google_books_key && <p className="muted small">Google Books needs a key for lookups (Settings → Mail & lookups); without one only Open Library is asked.</p>}
            {!!scan?.enrich?.pending && <p className="muted small">{scan.enrich.pending.toLocaleString()} authors are still waiting for a photo and a short bio.</p>}
            {!!scan?.hashing?.pending && <p className="muted small">{scan.hashing.pending.toLocaleString()} files are still being fingerprinted to find duplicates.</p>}
          </div>
        </Card>
      )}
    </div>
  )
}

// ---------- duplicates ----------

function Duplicates() {
  const { toast } = useToast()
  const { data, reload, error } = useAsync(() => api.get<DuplicateGroup[]>(`${V1}/duplicates`), [])
  const [del, setDel] = useState<Book | null>(null)
  const [typed, setTyped] = useState('')
  const remove = async () => {
    const b = del
    if (!b) return
    setDel(null); setTyped('')
    try { await api.del(`${V1}/books/${b.id}?files=1`); toast('Deleted with its files'); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div className="stack loose">
      <p className="muted">Books that look like the same book twice: the very same file, the same ISBN, or the same title by the same author in one library. Formats of one book (an EPUB next to its MOBI) are already one book and don't show up here.</p>
      {error && <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>}
      {!data ? (!error && <Loading label="Comparing books…" />) : data.length === 0 ? <Empty title="No duplicates found" icon="check-circle">Files are still being fingerprinted after a big scan, so check again later.</Empty> : (
        <div className="adm-dupes">
          <p className="muted small">{data.length} {data.length === 1 ? 'group' : 'groups'}.</p>
          {data.map((g, i) => (
            <div key={i} className="adm-dupe">
              <span className={`pill ${g.reason === 'same file' ? 'danger' : g.reason === 'same isbn' ? 'warn' : ''}`} style={{ justifySelf: 'start' }}>{g.reason === 'same file' ? 'The same file' : g.reason === 'same isbn' ? 'The same ISBN' : 'The same title and author'}</span>
              <div className="adm-dupe-books">
                {g.books.map(b => (
                  <div key={b.id} className="adm-dupe-book">
                    <Link to={`/book/${b.id}`}><MiniCover src={b.cover} title={b.title} width={44} /></Link>
                    <div className="grow">
                      <Link to={`/book/${b.id}`} style={{ fontWeight: 600, color: 'var(--text)' }} className="ellipsis">{b.title}</Link>
                      <div className="muted small ellipsis">{b.authors.map(a => a.name).join(', ') || 'Unknown author'}</div>
                      <div className="muted xsmall">{b.formats.map(x => x.toUpperCase()).join(' · ')}{b.isbn ? ` · ${b.isbn}` : ''} · added {fmtDateTime(b.added_at)}</div>
                      <button type="button" className="link-btn small danger-text" onClick={() => { setDel(b); setTyped('') }}>Delete with its files</button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
      {del && (
        <Modal title="Delete this book and its files?" onClose={() => setDel(null)} size="sm" footer={<>
          <button type="button" className="btn" onClick={() => setDel(null)}>Cancel</button>
          <button type="button" className="btn danger" disabled={typed.trim().toLowerCase() !== del.title.trim().toLowerCase()} onClick={remove}>Delete for good</button>
        </>}>
          <div className="stack">
            <p>The files of <b>{del.title}</b> are deleted from disk, with everyone's progress and highlights in it. This can't be undone.</p>
            <Field label="Type the title to confirm"><input value={typed} onChange={e => setTyped(e.target.value)} placeholder={del.title} autoFocus /></Field>
          </div>
        </Modal>
      )}
    </div>
  )
}

// ---------- imports ----------

type Source = { configured: boolean; path: string; ok: boolean; error?: string }
type Sources = { calibre_web?: Source; kavita?: Source; calibre?: Source }

function Summary({ data }: { data: Record<string, unknown> }) {
  const rows = Object.entries(data).filter(([, v]) => typeof v === 'number' || typeof v === 'string' || typeof v === 'boolean')
  const lists = Object.entries(data).filter(([, v]) => Array.isArray(v) && (v as unknown[]).length > 0) as [string, unknown[]][]
  return (
    <div className="stack tight">
      {rows.length > 0 && <dl className="set-dl">{rows.map(([k, v]) => <div key={k}><dt>{k.replace(/_/g, ' ')}</dt><dd className="tnum">{String(v)}</dd></div>)}</dl>}
      {lists.map(([k, v]) => (
        <details key={k}><summary className="small">{k.replace(/_/g, ' ')} ({v.length})</summary>
          <ul className="small muted">{v.slice(0, 100).map((x, i) => <li key={i}>{typeof x === 'string' ? x : JSON.stringify(x)}</li>)}</ul>
        </details>
      ))}
    </div>
  )
}

function Imports({ admin }: { admin: boolean }) {
  const { toast } = useToast()
  const { data: sources } = useAsync(() => admin ? api.get<Sources>(`${V1}/import/sources`) : Promise.resolve({} as Sources), [admin])
  const [cw, setCw] = useState({ mail: true, kindle: true, shelves: true, read_status: true, force: false })
  const [cwResult, setCwResult] = useState<Record<string, unknown> | null>(null)
  const [kavita, setKavita] = useState<{ dry: Record<string, unknown> | null; done: Record<string, unknown> | null }>({ dry: null, done: null })
  const [busy, setBusy] = useState('')
  const [clip, setClip] = useState<{ created: number; updated: number; unmatched: string[] } | null>(null)

  const runCw = async () => {
    setBusy('cw')
    try { setCwResult(await api.post<Record<string, unknown>>(`${V1}/import/calibre-web`, cw)); toast('Imported from Calibre-Web') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy('') }
  }
  const runKavita = async (dry: boolean) => {
    setBusy(dry ? 'kdry' : 'k')
    try {
      const r = await api.post<Record<string, unknown>>(`${V1}/import/kavita${dry ? '?dry=1' : ''}`, {})
      setKavita(k => dry ? { dry: r, done: null } : { ...k, done: r })
      if (!dry) toast('Imported from Kavita')
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy('') }
  }
  const runClippings = async (file: File) => {
    setBusy('clip')
    try { const r = await uploadForm<{ created: number; updated: number; unmatched: string[] }>(`${V1}/import/kindle-clippings`, file); setClip(r); toast(`${r.created} new highlights`) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy('') }
  }
  const sourceLine = (s?: Source, name = '') => !s?.configured
    ? <span className="muted small">Not set up: the server needs the path to {name}'s database (see docs/configuration.md).</span>
    : s.ok ? <span className="row small ok-text" style={{ gap: 5 }}><Icon name="check" size={13} />Found <code>{s.path}</code></span>
      : <span className="small danger-text">Can't read <code>{s.path}</code>{s.error ? `: ${s.error}` : ''}</span>

  return (
    <div className="stack loose">
      <Card title="Highlights from your Kindle" sub="Plug your Kindle into a computer and pick documents/My Clippings.txt. Your highlights and notes land on the matching books; importing again only adds what's new.">
        <div className="stack tight">
          <div className="row wrap">
            <label className="btn primary">{busy === 'clip' ? <Spinner /> : <Icon name="upload" size={15} />}Choose My Clippings.txt<input type="file" accept=".txt,text/plain" hidden onChange={e => { const f = e.target.files?.[0]; e.target.value = ''; if (f) runClippings(f) }} /></label>
          </div>
          {clip && <div className="callout"><Icon name="check-circle" size={15} /><div><b>{clip.created}</b> new, <b>{clip.updated}</b> updated.{clip.unmatched.length > 0 && <> These books aren't in your library: {clip.unmatched.slice(0, 12).join(', ')}{clip.unmatched.length > 12 ? ` and ${clip.unmatched.length - 12} more` : ''}.</>}</div></div>}
        </div>
      </Card>
      {admin && <>
        <Card title="Calibre-Web" sub="Moving over from Calibre-Web (or Calibre-Web Automated): bring its Kindle addresses, mail account, shelves and read marks along. People are matched by e-mail; books by their Calibre id.">
          <div className="stack">
            {sourceLine(sources?.calibre_web, 'Calibre-Web')}
            <div className="stack tight">
              {([['kindle', 'Kindle addresses → devices'], ['mail', 'The mail account → Settings → Mail'], ['shelves', 'Shelves'], ['read_status', 'Read and reading marks']] as const).map(([k, label]) => (
                <div key={k} className="row"><Switch size="sm" checked={cw[k]} onChange={v => setCw({ ...cw, [k]: v })} label={label} /><span className="small">{label}</span></div>
              ))}
              {cw.mail && <div className="row"><Switch size="sm" checked={cw.force} onChange={v => setCw({ ...cw, force: v })} label="Replace the mail account here" /><span className="small muted">Replace the mail account set here (otherwise only filled when empty)</span></div>}
            </div>
            <div className="row"><button type="button" className="btn primary" disabled={!sources?.calibre_web?.ok || busy === 'cw'} onClick={runCw}>{busy === 'cw' ? <Spinner /> : <Icon name="inbox" size={15} />}Import from Calibre-Web</button></div>
            {cwResult && <Summary data={cwResult} />}
          </div>
        </Card>
        <Card title="Kavita" sub="Brings each person's place in their comics and manga, want-to-read, ratings, collections and reading lists. Look at what it would do first.">
          <div className="stack">
            {sourceLine(sources?.kavita, 'Kavita')}
            <div className="row wrap">
              <button type="button" className="btn" disabled={!sources?.kavita?.ok || !!busy} onClick={() => runKavita(true)}>{busy === 'kdry' ? <Spinner /> : <Icon name="eye" size={15} />}See what it would import</button>
              {kavita.dry && <button type="button" className="btn primary" disabled={!!busy} onClick={() => runKavita(false)}>{busy === 'k' ? <Spinner /> : <Icon name="inbox" size={15} />}Import it</button>}
            </div>
            {kavita.dry && !kavita.done && <><p className="small muted">Would import:</p><Summary data={kavita.dry} /></>}
            {kavita.done && <><p className="small ok-text">Imported:</p><Summary data={kavita.done} /></>}
          </div>
        </Card>
      </>}
    </div>
  )
}
