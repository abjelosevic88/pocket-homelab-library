import { AskButton } from '../components/AskButton'
import React, { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, Device, Library, Settings as SettingsT, User } from '../lib/types'
import { DATE_FORMATS, TIME_FORMATS, fmtBytes, fmtDateTime, type DateFormat, type TimeFormat } from '../lib/format'
import { Avatar, Confirm, CopyField, Field, Loading, Modal, Segmented, SettingRow, Spinner, Switch, Tabs, errMsg, useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { ShortcutsSheet } from '../components/ShortcutsSheet'

type TabId = 'reading' | 'notifications' | 'mail' | 'ereaders' | 'profile' | 'people' | 'tokens' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string; admin?: boolean }[] = [
  { id: 'reading', label: 'Reading', icon: 'book-open', sub: 'Goals, where Send goes first, and how the library looks for you.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'A push when you finish a book, when a send fails, new books, and a weekly recap.' },
  { id: 'mail', label: 'Mail & lookups', icon: 'mail', sub: 'The e-mail account that sends books to Kindles, and book details lookups.', admin: true },
  { id: 'ereaders', label: 'E-readers & files', icon: 'smartphone', sub: 'The catalogue and progress sync for KOReader and the Xteink X4, and the drop folder.' },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email and password.' },
  { id: 'people', label: 'People', icon: 'users', sub: 'Everyone who has an account on this box.' },
  { id: 'tokens', label: 'Apps & API', icon: 'key', sub: 'Personal tokens for Homepage widgets, scripts and e-readers.' },
  { id: 'data', label: 'Backup & data', icon: 'database', sub: 'Download the database, export your reading or import it.' },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, numbers and the server.' },
]

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const { user } = useApp()
  const tabs = TABS.filter(t => !t.admin || user?.role === 'admin')
  const tab: TabId = tabs.find(t => t.id === raw)?.id || 'reading'
  const current = tabs.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .sel-pop')) navigate('/') }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])
  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back to Pocket Library" title="Back (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={tabs.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {tabs.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title"><h2>{current.label}</h2><p>{current.sub}</p></div>
            {tab === 'reading' && <Reading />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'mail' && <Mail />}
            {tab === 'ereaders' && <EReaders />}
            {tab === 'profile' && <Profile />}
            {tab === 'people' && <Users />}
            {tab === 'tokens' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}

function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico" style={{ background: 'var(--accent-soft)', color: 'var(--accent)' }}><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<SettingsT | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async (patch?: Partial<SettingsT>) => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...patch }); setSettings(s); setF(s); toast('Saved') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  // a switch saves at once
  const saveNow = async (patch: Partial<SettingsT>) => {
    try { const s = await api.put<SettingsT>(`${V1}/settings`, patch); setSettings(s); setF(x => x ? { ...x, ...patch } : s); toast('Saved') } catch (e) { toast(errMsg(e), 'err') }
  }
  return { f, setF, save, saveNow, busy, isAdmin: user?.role === 'admin' }
}

function useTester() {
  const [test, setTest] = useState<Record<string, string>>({})
  const run = async (which: string, body: Record<string, string>) => {
    setTest(t => ({ ...t, [which]: 'testing…' }))
    try { const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/integrations/test`, { which, ...body }); setTest(t => ({ ...t, [which]: r.ok ? `✓ ${r.message}` : `✕ ${r.error}` })) } catch (e) { setTest(t => ({ ...t, [which]: `✕ ${errMsg(e)}` })) }
  }
  const status = (which: string) => <span className={`small ${test[which]?.startsWith('✓') ? 'ok-text' : test[which]?.startsWith('✕') ? 'danger-text' : 'muted'}`}>{test[which]}</span>
  return { run, status }
}

// ---------- reading ----------

function Reading() {
  const { f, setF, save, busy } = useSettingsForm()
  const { data: devices } = useAsync(() => api.get<Device[]>(`${V1}/devices`), [])
  const { data: libs } = useAsync(() => api.get<Library[]>(`${V1}/libraries`), [])
  if (!f) return <Loading />
  const hidden = new Set((f.hidden_libraries || '').split(',').filter(Boolean))
  const toggleLib = (id: number, show: boolean) => {
    const h = new Set(hidden)
    if (show) h.delete(String(id)); else h.add(String(id))
    setF({ ...f, hidden_libraries: [...h].join(',') })
  }
  return (
    <div className="stack loose">
      <Section title="Goals" icon="zap" sub="Home shows how far you are; streaks count days that reach the daily goal.">
        <SettingRow title="Every day"><Segmented size="sm" value={f.daily_goal} onChange={v => setF({ ...f, daily_goal: v })} options={[{ value: 0, label: 'None' }, { value: 10, label: '10 min' }, { value: 20, label: '20 min' }, { value: 30, label: '30 min' }, { value: 60, label: '1 h' }]} /></SettingRow>
        <SettingRow title="Books this year" help="0 = no goal.">
          <input type="number" min={0} max={1000} value={f.yearly_goal} onChange={e => setF({ ...f, yearly_goal: Math.max(0, Number(e.target.value) || 0) })} style={{ width: 90 }} aria-label="Books this year" />
        </SettingRow>
      </Section>
      <Section title="Sending" icon="smartphone">
        <SettingRow title="Send goes to" help={<>The device a book's Send button uses first. <Link to="/devices">Manage devices</Link></>}>
          {devices && devices.length > 0
            ? <div style={{ minWidth: 200 }}><Select ariaLabel="Default device" value={String(devices.find(d => d.id === f.default_device)?.id || devices.find(d => d.is_default)?.id || '')} onChange={v => setF({ ...f, default_device: Number(v) })} options={devices.map(d => ({ value: String(d.id), label: d.name }))} /></div>
            : <Link className="btn sm" to="/devices"><Icon name="plus" size={14} />Add a device</Link>}
        </SettingRow>
      </Section>
      <Section title="Library" icon="library">
        <SettingRow title="Show books as"><Segmented size="sm" value={f.library_view} onChange={v => setF({ ...f, library_view: v })} options={[{ value: 'grid', label: 'Covers' }, { value: 'list', label: 'List' }]} /></SettingRow>
        <SettingRow title="Cover size"><Segmented size="sm" value={f.card_size} onChange={v => setF({ ...f, card_size: v })} options={[{ value: 's', label: 'Small' }, { value: 'm', label: 'Medium' }, { value: 'l', label: 'Large' }]} /></SettingRow>
      </Section>
      {libs && libs.length > 1 && (
        <Section title="Libraries on Home" icon="layers" sub="Hide the ones you don't read from; you can still open them from the sidebar.">
          {libs.map(l => <SettingRow key={l.id} title={l.name} help={`${l.books.toLocaleString()} ${l.kind === 'books' ? 'books' : l.kind}`}><Switch checked={!hidden.has(String(l.id))} onChange={v => toggleLib(l.id, v)} label={`Show ${l.name}`} /></SettingRow>)}
        </Section>
      )}
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

// ---------- notifications ----------

function Notifications() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Push" icon="bell" actions={<span className={`badge ${f.notify_configured ? 'ok' : ''}`}>{f.notify_configured ? 'on' : 'not set'}</span>}
        sub={<>Apprise gateway (<code>…/notify/&lt;key&gt;</code>), an ntfy topic URL, or any webhook that takes JSON.</>}>
        <SettingRow title="URL" stack><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="http://apprise:8000/notify/library" /></SettingRow>
        <SettingRow title="Format"><Segmented value={f.notify_format} onChange={v => setF({ ...f, notify_format: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'json', label: 'JSON' }]} size="sm" /></SettingRow>
        <SettingRow title="A book is finished" help="In the web reader or on a synced e-reader."><Switch checked={f.notify_finished} onChange={v => setF({ ...f, notify_finished: v })} label="Finished books" /></SettingRow>
        <SettingRow title="A send didn't work" help="When a book couldn't be mailed to your Kindle or pushed to your X4."><Switch checked={f.notify_sent} onChange={v => setF({ ...f, notify_sent: v })} label="Failed sends" /></SettingRow>
        <SettingRow title="New books" help="Once a day, what was added to the libraries you can open."><Switch checked={f.notify_added} onChange={v => setF({ ...f, notify_added: v })} label="New books" /></SettingRow>
        <SettingRow title="Weekly recap" help="Monday morning: how much you read and what you finished."><Switch checked={f.notify_weekly} onChange={v => setF({ ...f, notify_weekly: v })} label="Weekly recap" /></SettingRow>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        <button type="button" className="btn" onClick={() => run('notify', { url: f.notify_url, format: f.notify_format })} disabled={!f.notify_url}><Icon name="mail" size={15} />Send a test</button>
        {status('notify')}
      </div>
      <Section title="Date & time">
        <SettingRow title="Date format"><Select value={f.date_format} onChange={v => setF({ ...f, date_format: v as DateFormat })} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        <SettingRow title="Time format"><Select value={f.time_format} onChange={v => setF({ ...f, time_format: v as TimeFormat })} options={TIME_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

// ---------- mail (admin) ----------

function Mail() {
  const { f, setF, save, busy } = useSettingsForm()
  const { user } = useApp()
  const { run, status } = useTester()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="E-mail account" icon="mail" actions={<span className={`badge ${f.smtp_configured ? 'ok' : ''}`}>{f.smtp_configured ? 'set up' : 'not set'}</span>}
        sub="Books for Kindles and other e-mail devices are sent from this account. Amazon only accepts mail from addresses each person approved, so tell everyone the From address below.">
        <div className="stack">
          <div className="two-col">
            <Field label="Server" help="e.g. smtp.gmail.com"><input value={f.smtp_host} onChange={e => setF({ ...f, smtp_host: e.target.value })} placeholder="smtp.example.com" /></Field>
            <Field label="Port"><input type="number" value={f.smtp_port} onChange={e => setF({ ...f, smtp_port: Number(e.target.value) || 587 })} /></Field>
          </div>
          <Field group label="Security"><Segmented size="sm" value={f.smtp_tls} onChange={v => setF({ ...f, smtp_tls: v, smtp_port: v === 'tls' && f.smtp_port === 587 ? 465 : v === 'starttls' && f.smtp_port === 465 ? 587 : f.smtp_port })} options={[{ value: 'starttls', label: 'STARTTLS' }, { value: 'tls', label: 'SSL/TLS' }, { value: 'none', label: 'None' }]} /></Field>
          <div className="two-col">
            <Field label="User name"><input value={f.smtp_user} onChange={e => setF({ ...f, smtp_user: e.target.value })} autoComplete="off" /></Field>
            <Field label="Password" help={f.smtp_password ? 'Saved; type a new one to replace it.' : 'An app password for Gmail and most providers.'}>
              <input type="password" value={f.smtp_password} onChange={e => setF({ ...f, smtp_password: e.target.value })} autoComplete="new-password" />
            </Field>
          </div>
          <div className="two-col">
            <Field label="From address" help="The address Amazon must have on its approved list."><input type="email" value={f.smtp_from} onChange={e => setF({ ...f, smtp_from: e.target.value })} placeholder="books@example.com" /></Field>
            <Field label="From name"><input value={f.smtp_from_name} onChange={e => setF({ ...f, smtp_from_name: e.target.value })} placeholder="Pocket Library" /></Field>
          </div>
          <div className="row wrap">
            <button type="button" className="btn primary" disabled={busy} onClick={() => save()}>Save changes</button>
            <button type="button" className="btn" disabled={!f.smtp_host} onClick={() => run('smtp', { to: user?.email || '' })}><Icon name="mail" size={15} />Send a test to {user?.email}</button>
            {status('smtp')}
          </div>
        </div>
      </Section>
      <Section title="Before a book goes out" icon="wand">
        <SettingRow title="Fix EPUBs for Kindle" help="Amazon rejects some EPUBs (a missing language, odd encodings, broken links). This mends them on the way out; the file in the library stays as it is.">
          <Switch checked={f.kindle_fix_epub} onChange={v => setF({ ...f, kindle_fix_epub: v })} label="Fix EPUBs for Kindle" />
        </SettingRow>
        <SettingRow title="Write the details into EPUBs" help="Downloads and sends carry the title, authors, series and cover as edited here, so devices show them right. The original file is never changed.">
          <Switch checked={f.embed_metadata} onChange={v => setF({ ...f, embed_metadata: v })} label="Write the details into EPUBs" />
        </SettingRow>
      </Section>
      <Section title="Book details lookups" icon="search" sub="Edit → Find details and Library admin → Metadata ask Open Library (no key needed) and Google Books (needs a free key from the Google Cloud console, Books API).">
        <SettingRow title="Google Books key" stack>
          <input value={f.google_books_key} onChange={e => setF({ ...f, google_books_key: e.target.value })} placeholder="AIza…" autoComplete="off" />
        </SettingRow>
      </Section>
      <div className="row"><button type="button" className="btn primary" disabled={busy} onClick={() => save()}>Save changes</button></div>
    </div>
  )
}

// ---------- e-readers ----------

function EReaders() {
  const { f, saveNow, isAdmin } = useSettingsForm()
  const { user } = useApp()
  if (!f) return <Loading />
  const base = f.base_url || window.location.origin
  return (
    <div className="stack loose">
      <Section title="Your addresses" icon="smartphone" sub="Type these into KOReader or the Xteink X4. The password is the app password you get when you add the device under Devices.">
        <dl className="set-dl">
          <div><dt>Catalogue (OPDS)</dt><dd><CopyField value={`${base}/opds`} label="OPDS address" /></dd></div>
          <div><dt>Progress sync</dt><dd><CopyField value={`${base}/kosync`} label="KOReader sync address" /></dd></div>
          <div><dt>User name</dt><dd><code>{user?.email}</code></dd></div>
          <div><dt>Password</dt><dd><Link to="/devices">Add the device</Link> to get its own app password. An e-reader that isn't listed can use a personal token from Apps & API.</dd></div>
        </dl>
        <div className="row mt"><Link className="btn" to="/devices"><Icon name="smartphone" size={15} />Go to Devices</Link><a className="btn ghost" href={`${base}/koreader/plugin.zip`} download><Icon name="download" size={15} />KOReader plugin</a></div>
      </Section>
      {isAdmin && (
        <Section title="For the whole box" icon="server">
          <SettingRow title="Catalogue (OPDS)" help="E-readers browse and download books. Off hides /opds for everyone."><Switch checked={f.opds_enabled} onChange={v => saveNow({ opds_enabled: v })} label="OPDS catalogue" /></SettingRow>
          <SettingRow title="KOReader progress sync" help="KOReader and CrossPoint keep everyone's place in step with the web reader."><Switch checked={f.kosync_enabled} onChange={v => saveNow({ kosync_enabled: v })} label="KOReader sync" /></SettingRow>
          <SettingRow title="Drop folder" help={f.ingest_dir ? <>Files dropped into <code>{f.ingest_dir}</code> on the server are filed into the uploads library within a minute, then removed from the folder.</> : <>Not set up: the server needs <code>INGEST_DIR</code> (see docs/configuration.md).</>}>
            <Switch checked={f.ingest_enabled && !!f.ingest_dir} disabled={!f.ingest_dir} onChange={v => saveNow({ ingest_enabled: v })} label="Drop folder" />
          </SettingRow>
          <SettingRow title="Converting formats" help={f.convert_available ? 'Calibre is installed: MOBI and AZW3 books can be sent to Kindles and the X4 as EPUB, and downloaded in other formats.' : 'Calibre is not installed on this server, so only EPUB to KEPUB conversion is available. Books without an EPUB can\'t go to a Kindle by e-mail.'}>
            <span className={`badge ${f.convert_available ? 'ok' : ''}`}>{f.convert_available ? 'available' : 'not available'}</span>
          </SettingRow>
        </Section>
      )}
    </div>
  )
}

// ---------- people, profile, tokens, data, about ----------

function Users() {
  const { user, users, reloadMeta, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  const setCanEdit = async (u: User, v: boolean) => {
    try { await api.put(`${V1}/users/${u.id}`, { can_edit: v }); await reloadMeta(); toast(v ? `${u.name || u.email} can now edit the library` : 'Saved') } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <Section title="People on this box" actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member', can_edit: false })}><Icon name="plus" size={14} />Add person</button>}
      sub="Everyone signs in with their own account: their own place in every book, highlights, shelves and devices. Admins manage accounts and libraries; they don't see anyone else's reading.">
      <div className="set-people">
        {users.map(u => (
          <div key={u.id} className="set-person">
            <Avatar name={u.name} email={u.email} size={34} />
            <div className="grow">
              <div className="set-person-name">{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge">you</span>}{u.role === 'admin' && <span className="badge accent">admin</span>}{u.role !== 'admin' && u.can_edit && <span className="badge ok">editor</span>}</div>
              <div className="muted small ellipsis">{u.email}</div>
            </div>
            {isAdmin && u.role !== 'admin' && <span className="row" title="May edit details, upload and delete books"><span className="muted small">Can edit</span><Switch size="sm" checked={u.can_edit} onChange={v => setCanEdit(u, v)} label={`${u.name || u.email} can edit the library`} /></span>}
            {isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}
          </div>
        ))}
      </div>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: people are added automatically on their first visit.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadMeta() }} />}
      {del && <Confirm confirmLabel="Remove" title={`Remove ${del.name || del.email}?`} message="Their account is deleted here with their reading progress, highlights, shelves and devices. The books stay in the library." onCancel={() => setDel(null)}
        onConfirm={async () => { const d = del; setDel(null); try { await api.del(`${V1}/users/${d.id}`); await reloadMeta(); toast('Removed') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </Section>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member', can_edit: !!user.can_edit })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f); toast(user.id ? 'Saved' : 'Added'); onSaved() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit person' : 'Add a person'} onClose={onClose} size="sm">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in, and as the user name on e-readers."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        {f.role === 'member' && <div className="row" style={{ gap: 10 }}><Switch checked={f.can_edit} onChange={v => setF({ ...f, can_edit: v })} label="Can edit the library" /><span className="small">Can edit book details, upload and delete books</span></div>}
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add person'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setUser(await api.put(`${V1}/auth/me`, f)); setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <form onSubmit={save} className="stack loose">
      <div className="set-profile">
        <Avatar name={user?.name} email={user?.email} size={56} />
        <div className="grow"><h3>{user?.name || user?.email}</h3><p className="muted small">{user?.email} · {user?.role}{user?.role !== 'admin' && user?.can_edit ? ' · editor' : ''}</p></div>
      </div>
      <Section title="You"><div className="stack">
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
        <Field label="Email" help="Also your user name on e-readers."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
      </div></Section>
      {status?.auth_mode === 'local' && <Section title="Password" sub="Leave empty to keep the current one."><div className="stack">
        <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
        <Field label="New password" help="At least 8 characters."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
      </div></Section>}
      <div className="row"><button className="btn primary" disabled={busy}>Save changes</button></div>
    </form>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { settings } = useApp()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token', kind: 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  const base = settings?.base_url || window.location.origin
  const today = new Date().toISOString().slice(0, 10)
  return (
    <div className="stack loose">
      <Section title="Personal tokens" icon="key" sub={<>Use as <code>Authorization: Bearer &lt;token&gt;</code>, or as the password on an e-reader with your e-mail as the user name. A token acts as you. Device passwords from Devices are listed here too.</>}>
        <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. Homepage widget)" /><button className="btn primary"><Icon name="plus" size={15} />Create</button></form>
        {fresh && <div className="set-fresh mb"><div className="row"><Icon name="check-circle" size={16} /><b>Copy it now — it is shown once.</b></div><pre className="mono">{fresh}</pre></div>}
        {data && data.length > 0 ? (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Kind</th><th>Last used</th><th>Created</th><th /></tr></thead>
            <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="muted">{t.kind === 'device' ? 'device' : 'token'}</td><td className="muted">{t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'}</td><td className="muted">{fmtDateTime(t.created_at)}</td><td className="right"><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
          </table>
        ) : <p className="muted">No tokens yet.</p>}
        {del && <Confirm confirmLabel="Revoke" title={`Revoke “${del.name}”?`} message={del.kind === 'device' ? 'The device using this password can no longer sign in.' : 'Anything using this token stops working immediately.'} onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
      </Section>
      <Section title="Handy endpoints" icon="code" sub={<>At <code>{base}/api/v1</code> with a token.</>}>
        <dl className="set-dl">
          <div><dt><code>GET /dashboard</code></dt><dd className="muted">What you're reading, time read today and this week, books finished this year — for a Homepage widget.</dd></div>
          <div><dt><code>GET /day/{today}</code></dt><dd className="muted">One day's reading in a sentence and a list (Pocket Day uses it).</dd></div>
          <div><dt><code>GET /stats?range=30d</code> · <code>GET /sessions</code> · <code>GET /annotations</code></dt><dd className="muted">For dashboards, journals and scripts.</dd></div>
          <div><dt><code>{base}/opds</code></dt><dd className="muted">The OPDS catalogue (e-mail + token as user name and password).</dd></div>
          <div><dt><code>{base}/kosync</code></dt><dd className="muted">KOReader progress sync.</dd></div>
        </dl>
      </Section>
    </div>
  )
}

function Data() {
  const { toast } = useToast()
  const { user } = useApp()
  const [busy, setBusy] = useState(false)
  const importJSON = async (file: File) => {
    setBusy(true)
    try { const r = await api.post<Record<string, number>>(`${V1}/import`, JSON.parse(await file.text())); toast('Imported: ' + (Object.entries(r).map(([k, v]) => `${v} ${k.replace(/_/g, ' ')}`).join(', ') || 'nothing new')) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <div className="stack loose">
      {user?.role === 'admin' && <Section title="Database backup" icon="database" sub="The whole box, every account — admins only.">
        <p className="muted">A consistent copy of the SQLite database: the catalogue, everyone's progress, highlights, shelves and devices. The books themselves are the files in your library folders, and covers live in the data folder. Restore by stopping the app and replacing <code>pocket-library.db</code>.</p>
        <a className="btn primary" href={`${V1}/backup`}><Icon name="download" size={15} />Download database</a>
      </Section>}
      <Section title="Your reading" icon="archive" sub="Where you are in each book, finished books, highlights, shelves and reading time as JSON, to keep or to move to another box.">
        <div className="row wrap">
          <a className="btn" href={`${V1}/export.json`}><Icon name="download" size={15} />Export JSON</a>
          <label className="btn">{busy ? <Spinner /> : <Icon name="upload" size={15} />}{busy ? 'Importing…' : 'Import JSON…'}<input type="file" accept="application/json,.json" hidden onChange={e => { const f = e.target.files?.[0]; e.target.value = ''; if (f) importJSON(f) }} /></label>
        </div>
        <p className="muted small mt">Books are matched by ISBN, else by title and author.</p>
      </Section>
    </div>
  )
}

type SystemInfo = {
  version: string; go: string; os: string; arch: string; auth_mode: string; db_bytes: number; uptime: string; timezone: string; convert: boolean; base_url: string
  core?: { configured: boolean; registered: boolean; url: string; error: string }
  counts?: Record<string, number>
  data_dir?: string; db_path?: string; ingest_dir?: string; convert_path?: string
}

function About() {
  const { data, error } = useAsync(() => api.get<SystemInfo>(`${V1}/settings/system`), [])
  const [keys, setKeys] = useState(false)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  const c = data.counts || {}
  const tiles: [string, number | undefined, string][] = [
    ['Books', c.books, 'book'], ['Files', c.files, 'archive'], ['Authors', c.authors, 'feather'], ['Series', c.series, 'layers'],
    ['Libraries', c.libraries, 'library'], ['Your highlights', c.annotations, 'bookmark'], ['Your reading sessions', c.reading_sessions, 'history'], ['Your shelves', c.shelves, 'list'],
  ]
  const core = data.core
  const rows: [string, React.ReactNode][] = [
    ['Version', <code key="v">{data.version}</code>],
    ['Uptime', data.uptime],
    ['Runtime', `${data.go} · ${data.os}/${data.arch}`],
    ['Time zone', data.timezone],
    ['Sign-in', data.auth_mode],
    ['Converting formats', data.convert ? <span key="cv" className="ok-text">calibre available{data.convert_path ? <> (<code>{data.convert_path}</code>)</> : null}</span> : 'not available (EPUB → KEPUB only)'],
    ['Pocket Core', core?.configured ? (core.registered ? <span key="c">registered at <code>{core.url}</code></span> : <span key="c" className="danger-text">not registered: {core.error}</span>) : 'not configured'],
    ['Database', <span key="db">{data.db_path ? <><code>{data.db_path}</code> · </> : null}{fmtBytes(data.db_bytes)}</span>],
    ...(data.ingest_dir ? [['Drop folder', <code key="i">{data.ingest_dir}</code>] as [string, React.ReactNode]] : []),
    ['People', `${c.users ?? 0} accounts on this box`],
  ]
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <h3>Pocket Library</h3>
          <p className="muted small">Every ebook, comic and manga in one place. It files and tidies your books, reads EPUB, PDF, MOBI, comics and manga in the browser, keeps your place in step with KOReader and the Xteink X4, and sends books to your Kindle and other e-readers. One binary, one SQLite file.</p>
        </div>
      </div>
      <div className="set-stats">
        {tiles.map(([label, n, icon]) => <div key={label} className="set-stat"><Icon name={icon} size={16} /><span className="set-stat-n tnum">{(n ?? 0).toLocaleString()}</span><span className="set-stat-l">{label}</span></div>)}
      </div>
      <Section title="This server"><dl className="set-dl">{rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl></Section>
      <div className="row wrap">
        <button type="button" className="btn" onClick={() => setKeys(true)}><Icon name="keyboard" size={15} />Keyboard shortcuts</button>
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-library" target="_blank" rel="noreferrer"><Icon name="external" size={15} />Project on GitHub</a>
      </div>
      <p className="muted small">Open source (MIT). Part of the Pocket apps: Pocket Core connects them — Bookshelf, Day, Audiobooks and more.</p>
      {keys && <ShortcutsSheet onClose={() => setKeys(false)} />}
    </div>
  )
}
