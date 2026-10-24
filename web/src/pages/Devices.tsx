// Your e-readers: Kindle by e-mail, the Xteink X4 with CrossPoint, KOReader
// (a jailbroken Kindle, a Kobo) and any e-mail device, plus what was sent.
import React, { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Device, DeviceCreated, DeviceKind, DeviceSetup as Setup, Send } from '../lib/types'
import { ago, fmtBytes, fmtDateTime } from '../lib/format'
import { Confirm, Empty, Field, Loading, MenuButton, Modal, PageHeader, Spinner, Switch, errMsg, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { DevIcon, DeviceSetup, KIND_LABEL } from '../components/DeviceSetup'
import { MiniCover } from '../components/DevCover'
import '../styles/admin.css'

const KINDS: { kind: DeviceKind; title: string; text: string }[] = [
  { kind: 'kindle', title: 'Kindle', text: 'Books arrive by e-mail at your Kindle address. Works with any Kindle, no setup on the device.' },
  { kind: 'crosspoint', title: 'Xteink X4 (CrossPoint)', text: 'Browse the library on the X4, keep your place in sync, and get books sent straight to it.' },
  { kind: 'koreader', title: 'KOReader', text: 'A jailbroken Kindle, a Kobo or a phone with KOReader: catalogue, progress sync, highlights and sent books.' },
  { kind: 'email', title: 'Another e-mail device', text: 'PocketBook, Kobo and others that take books sent to an e-mail address.' },
]

const STATUS: Record<Send['status'], [string, string]> = {
  queued: ['warn', 'Waiting'], sending: ['accent', 'Sending'], sent: ['ok', 'Sent'], failed: ['danger', 'Failed'], cancelled: ['', 'Cancelled'],
}

function deviceLine(d: Device): string {
  if (d.kind === 'kindle' || d.kind === 'email') return d.email
  if (d.kind === 'crosspoint') return d.address ? `${d.address}${d.settings?.push ? ' · pushes when reachable' : ''}` : 'Fetches from the catalogue'
  return 'Fetches from the catalogue and syncs progress'
}

export default function Devices() {
  const { toast } = useToast()
  const { settings, setSettings } = useApp()
  const { data: devices, reload, error } = useAsync(() => api.get<Device[]>(`${V1}/devices`), [])
  const { data: sends, reload: reloadSends } = useAsync(() => api.get<Send[]>(`${V1}/sends?limit=50`), [])
  const [adding, setAdding] = useState(false)
  const [edit, setEdit] = useState<Device | null>(null)
  const [setupOf, setSetupOf] = useState<{ device: Device; password?: string; username?: string } | null>(null)
  const [del, setDel] = useState<Device | null>(null)
  const [rotate, setRotate] = useState<Device | null>(null)
  const [busy, setBusy] = useState<number | null>(null)

  // while something is on its way, look again every few seconds
  const active = (sends || []).some(s => (s.status === 'queued' && s.method !== 'pull') || s.status === 'sending')
  useEffect(() => {
    if (!active) return
    const t = setInterval(() => { reloadSends(); reload() }, 5000)
    return () => clearInterval(t)
  }, [active, reloadSends, reload])

  const run = async (d: Device, what: 'test' | 'default' | 'provision') => {
    setBusy(d.id)
    try {
      if (what === 'default') {
        await api.post(`${V1}/devices/${d.id}/default`)
        if (settings) setSettings({ ...settings, default_device: d.id })
        toast(`${d.name} is now the default`)
        reload()
        return
      }
      const r = await api.post<{ ok: boolean; message?: string; error?: string; password?: string }>(`${V1}/devices/${d.id}/${what}`)
      toast(r.ok ? r.message || 'Done' : r.error || 'That did not work', r.ok ? 'ok' : 'err')
      if (r.ok && r.password) setSetupOf({ device: d, password: r.password })
      if (what === 'test') reloadSends()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(null) }
  }

  const sendAction = async (s: Send, what: 'retry' | 'cancel') => {
    try {
      if (what === 'retry') await api.post(`${V1}/sends/${s.id}/retry`)
      else await api.del(`${V1}/sends/${s.id}`)
      reloadSends()
    } catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <div className="stack loose">
      <PageHeader title="Devices" sub="Where your books go: Send on any book puts it on one of these."
        actions={<button type="button" className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" size={15} />Add a device</button>} />
      {error && <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>}
      {!devices ? (!error && <Loading />) : devices.length === 0 ? (
        <Empty title="No devices yet" icon="smartphone">
          Add your Kindle, Xteink X4 or KOReader, then send books to it from any book's page.
          <div className="mt"><button type="button" className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" size={15} />Add a device</button></div>
        </Empty>
      ) : (
        <div className="dev-grid">
          {devices.map(d => (
            <div key={d.id} className={`dev-card ${d.is_default ? 'default' : ''}`}>
              <div className="dev-head">
                <DevIcon kind={d.kind} />
                <div className="grow" style={{ minWidth: 0 }}>
                  <div className="dev-name">{d.name}{d.is_default && <span className="badge accent">default</span>}{d.pending > 0 && <span className="pill warn">{d.pending} waiting</span>}</div>
                  <div className="dev-meta">{KIND_LABEL[d.kind] || d.kind} · {deviceLine(d)}</div>
                  <div className="dev-meta">{d.kind === 'kindle' || d.kind === 'email' ? `Added ${fmtDateTime(d.created_at)}` : d.last_seen_at ? `Last seen ${ago(d.last_seen_at)}` : 'Not seen yet'}</div>
                </div>
                <MenuButton label={`More for ${d.name}`} align="right" items={() => [
                  { label: 'Show setup', icon: 'info', onSelect: () => setSetupOf({ device: d }) },
                  { label: 'Edit', icon: 'edit', onSelect: () => setEdit(d) },
                  ...(d.kind === 'crosspoint' || d.kind === 'koreader' ? [{ label: 'New app password', icon: 'key', onSelect: () => setRotate(d) }] : []),
                  ...(d.kind === 'crosspoint' && d.address ? [{ label: 'Put the catalogue on the X4', icon: 'download', onSelect: () => run(d, 'provision') }] : []),
                  'sep' as const,
                  { label: 'Remove', icon: 'trash', danger: true, onSelect: () => setDel(d) },
                ]}><Icon name="more" size={17} /></MenuButton>
              </div>
              <div className="dev-actions">
                <button type="button" className="btn sm" disabled={busy === d.id} onClick={() => run(d, 'test')}>{busy === d.id ? <Spinner /> : <Icon name="zap" size={14} />}{d.kind === 'kindle' || d.kind === 'email' ? 'Send a test' : 'Test'}</button>
                {!d.is_default && <button type="button" className="btn sm ghost" onClick={() => run(d, 'default')}>Make default</button>}
                <button type="button" className="btn sm ghost" onClick={() => setSetupOf({ device: d })}>Setup</button>
              </div>
            </div>
          ))}
        </div>
      )}

      <section className="card">
        <div className="card-head"><div className="grow"><h2>Sent to your devices</h2>
          <p className="muted small">Kindles get books by e-mail. The X4 and KOReader fetch theirs: open the catalogue on the device → Pocket Library → To send.</p></div>
          <button type="button" className="btn ghost sm" onClick={() => reloadSends()}><Icon name="refresh" size={14} />Refresh</button>
        </div>
        <div className="card-body">
          {!sends ? <Loading /> : sends.length === 0 ? <p className="muted">Nothing sent yet. Open a book and press Send.</p> : (
            <div className="dev-sends">
              {sends.map(s => {
                const [tone, label] = STATUS[s.status] || ['', s.status]
                const waiting = s.status === 'queued' && s.method === 'pull'
                return (
                  <div key={s.id} className="dev-send">
                    <Link to={`/book/${s.book_id}`}><MiniCover src={s.cover} title={s.title} width={34} /></Link>
                    <div className="grow">
                      <Link to={`/book/${s.book_id}`} className="ellipsis" style={{ display: 'block', fontWeight: 600, color: 'var(--text)' }}>{s.title || 'A book'}</Link>
                      <div className="muted small ellipsis">{s.device_name} · {s.format.toUpperCase()}{s.bytes ? ` · ${fmtBytes(s.bytes)}` : ''} · {ago(s.sent_at || s.created_at)}</div>
                      {waiting && <div className="muted small">Waiting on the device: it picks it up next time it opens the catalogue.</div>}
                      {s.error && <div className={`small ${s.status === 'failed' ? 'danger-text' : 'muted'}`}>{s.error}</div>}
                    </div>
                    <span className={`pill ${tone}`}>{waiting ? 'On the device list' : label}</span>
                    {s.status === 'failed' && <button type="button" className="btn ghost sm" onClick={() => sendAction(s, 'retry')}>Retry</button>}
                    {s.status === 'queued' && <button type="button" className="btn ghost sm" onClick={() => sendAction(s, 'cancel')}>Cancel</button>}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </section>

      {adding && <AddDevice onClose={() => setAdding(false)} onDone={c => { setAdding(false); reload(); setSetupOf({ device: c.device, password: c.password, username: c.username }) }} />}
      {edit && <EditDevice device={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload() }} />}
      {setupOf && <SetupModal {...setupOf} onClose={() => setSetupOf(null)} />}
      {rotate && <Confirm title={`New app password for ${rotate.name}?`} confirmLabel="Make a new one" danger={false}
        message="The old password stops working right away; type the new one into the device." onCancel={() => setRotate(null)}
        onConfirm={async () => { const d = rotate; setRotate(null); try { const r = await api.post<{ password: string; username: string }>(`${V1}/devices/${d.id}/password`); setSetupOf({ device: d, password: r.password, username: r.username }) } catch (e) { toast(errMsg(e), 'err') } }} />}
      {del && <Confirm title={`Remove ${del.name}?`} confirmLabel="Remove" message={del.kind === 'crosspoint' || del.kind === 'koreader' ? 'Its app password stops working. Books already on it stay there.' : 'Books already on it stay there.'}
        onCancel={() => setDel(null)} onConfirm={async () => { const d = del; setDel(null); try { await api.del(`${V1}/devices/${d.id}`); reload(); toast('Removed') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </div>
  )
}

function AddDevice({ onClose, onDone }: { onClose: () => void; onDone: (c: DeviceCreated) => void }) {
  const { toast } = useToast()
  const { settings } = useApp()
  const [kind, setKind] = useState<DeviceKind | null>(null)
  const [f, setF] = useState({ name: '', email: '', address: '', folder: '/Books', push: true })
  const [busy, setBusy] = useState(false)
  const pick = (k: DeviceKind) => { setKind(k); setF(x => ({ ...x, name: k === 'kindle' ? 'Kindle' : k === 'crosspoint' ? 'Xteink X4' : k === 'koreader' ? 'KOReader' : 'E-reader' })) }
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!kind) return
    setBusy(true)
    try {
      const body: Record<string, unknown> = { kind, name: f.name.trim() }
      if (kind === 'kindle' || kind === 'email') body.email = f.email.trim()
      if (kind === 'crosspoint') { body.address = f.address.trim(); body.settings = { folder: f.folder.trim() || '/Books', push: f.push && !!f.address.trim() } }
      onDone(await api.post<DeviceCreated>(`${V1}/devices`, body))
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={kind ? `Add ${KINDS.find(k => k.kind === kind)?.title}` : 'Add a device'} onClose={onClose} size={kind ? 'sm' : ''}>
      {!kind ? (
        <div className="dev-kinds">
          {KINDS.map(k => (
            <button key={k.kind} type="button" className="dev-kind" onClick={() => pick(k.kind)}>
              <DevIcon kind={k.kind} />
              <span><b>{k.title}</b><span className="muted">{k.text}</span></span>
            </button>
          ))}
        </div>
      ) : (
        <form onSubmit={save} className="stack">
          <Field label="Name" help="How it shows up when you send a book."><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} required maxLength={60} autoFocus /></Field>
          {(kind === 'kindle' || kind === 'email') && (
            <Field label={kind === 'kindle' ? 'Kindle e-mail address' : 'The device\'s e-mail address'}
              help={kind === 'kindle' ? 'Find it on Amazon under Manage Your Content and Devices → Devices (it ends in @kindle.com).' : undefined}>
              <input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required placeholder={kind === 'kindle' ? 'name_123@kindle.com' : 'reader@example.com'} />
            </Field>
          )}
          {(kind === 'kindle' || kind === 'email') && settings && !settings.smtp_configured && (
            <div className="callout"><Icon name="alert" size={15} /><span>Mail isn't set up on this server yet, so nothing can be sent until an admin fills in Settings → Mail.</span></div>
          )}
          {kind === 'crosspoint' && <>
            <Field label="The X4's address (optional)" help="Shown on the X4's File Transfer screen, e.g. http://192.168.1.50. Leave it empty if the server can't reach the X4: it will fetch books from the catalogue instead.">
              <input value={f.address} onChange={e => setF({ ...f, address: e.target.value })} placeholder="http://crosspoint.local" />
            </Field>
            <Field label="Folder on the X4"><input value={f.folder} onChange={e => setF({ ...f, folder: e.target.value })} placeholder="/Books" /></Field>
            <div className="row" style={{ gap: 10 }}><Switch checked={f.push && !!f.address.trim()} disabled={!f.address.trim()} onChange={v => setF({ ...f, push: v })} label="Push straight to the X4" /><span className="small">Push books straight to the X4 when it's on the same network (its File Transfer screen must be open)</span></div>
          </>}
          {kind === 'koreader' && <p className="muted small">You'll get a password for this device next, with the addresses to type into KOReader and a plugin that fetches sent books and syncs your highlights.</p>}
          <div className="form-actions">
            <button type="button" className="btn" onClick={() => setKind(null)}>Back</button>
            <button className="btn primary" disabled={busy}>{busy ? <Spinner /> : null}Add device</button>
          </div>
        </form>
      )}
    </Modal>
  )
}

function EditDevice({ device, onClose, onSaved }: { device: Device; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const s = device.settings || {}
  const [f, setF] = useState({ name: device.name, email: device.email, address: device.address, folder: String(s.folder || '/Books'), push: !!s.push })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try {
      await api.put(`${V1}/devices/${device.id}`, { name: f.name.trim(), email: f.email.trim(), address: f.address.trim(), settings: { ...s, folder: f.folder.trim() || '/Books', push: f.push && !!f.address.trim() } })
      toast('Saved'); onSaved()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={`Edit ${device.name}`} onClose={onClose} size="sm">
      <form onSubmit={save} className="stack">
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} required maxLength={60} /></Field>
        {(device.kind === 'kindle' || device.kind === 'email') && <Field label="E-mail address"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>}
        {device.kind === 'crosspoint' && <>
          <Field label="The X4's address" help="Leave it empty to only fetch from the catalogue."><input value={f.address} onChange={e => setF({ ...f, address: e.target.value })} placeholder="http://crosspoint.local" /></Field>
          <Field label="Folder on the X4"><input value={f.folder} onChange={e => setF({ ...f, folder: e.target.value })} /></Field>
          <div className="row" style={{ gap: 10 }}><Switch checked={f.push && !!f.address.trim()} disabled={!f.address.trim()} onChange={v => setF({ ...f, push: v })} label="Push straight to the X4" /><span className="small">Push books straight to the X4 when it's reachable</span></div>
        </>}
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>Save</button></div>
      </form>
    </Modal>
  )
}

function SetupModal({ device, password, username, onClose }: { device: Device; password?: string; username?: string; onClose: () => void }) {
  const [setup, setSetup] = useState<Setup | null>(null)
  const [err, setErr] = useState('')
  useEffect(() => { api.get<Setup>(`${V1}/devices/${device.id}/setup`).then(setSetup).catch(e => setErr(errMsg(e))) }, [device.id])
  return (
    <Modal title={`Set up ${device.name}`} onClose={onClose} footer={<button type="button" className="btn primary" onClick={onClose}>Done</button>}>
      {err && <div className="callout danger mb"><Icon name="alert" size={15} /><span>{err}</span></div>}
      {!setup && !password && !err ? <Loading /> : <DeviceSetup device={device} setup={setup} password={password} username={username} />}
    </Modal>
  )
}
