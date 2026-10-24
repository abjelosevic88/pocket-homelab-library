// Left column: continue reading, the library, yours — the Pocket Calendar sidebar.
import { type ReactNode, useEffect, useState } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Book, Library, Page, ScanStatus, Shelf } from '../lib/types'
import { pct } from '../lib/books'
import { Icon } from './icons'
import { Cover } from './cards'

type NavItem = { to: string; label: string; icon: string; end?: boolean }

const LIBRARY: NavItem[] = [
  { to: '/library', label: 'Books', icon: 'book', end: true },
  { to: '/series', label: 'Series', icon: 'layers' },
  { to: '/authors', label: 'Authors', icon: 'feather' },
  { to: '/tags', label: 'Tags', icon: 'tag' },
]
export const KIND_ICON: Record<string, string> = { books: 'library', comics: 'layers2', manga: 'book-open' }

/** Something changed that the sidebar shows (shelves, what is being read). */
export function sidebarChanged() { window.dispatchEvent(new CustomEvent('plb:sidebar')) }

export function Sidebar({ onNavigate }: { onNavigate: () => void }) {
  const { status, settings, user } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const title = settings?.title || status?.title || 'Pocket Library'
  const [libs, setLibs] = useState<Library[]>([])
  const [shelves, setShelves] = useState<Shelf[]>([])
  const [reading, setReading] = useState<Book | null>(null)
  const [scan, setScan] = useState<ScanStatus | null>(null)
  const editor = !!(settings?.can_edit || user?.role === 'admin')

  useEffect(() => {
    const load = () => {
      api.get<Library[]>(`${V1}/libraries`).then(setLibs).catch(() => {})
      api.get<Shelf[]>(`${V1}/shelves`).then(setShelves).catch(() => {})
      api.get<Page<Book>>(`${V1}/books?status=reading&sort=last_read&order=desc&limit=1`).then(p => setReading(p.items[0] || null)).catch(() => {})
    }
    load()
    window.addEventListener('plb:sidebar', load)
    return () => window.removeEventListener('plb:sidebar', load)
  }, [])
  // the reading position moves in the reader: refresh when coming home
  useEffect(() => { if (loc.pathname === '/') sidebarChanged() }, [loc.pathname])

  useEffect(() => {
    let t = 0
    let live = true
    const poll = () => {
      api.get<{ status: ScanStatus }>(`${V1}/scan`).then(r => {
        if (!live) return
        setScan(r.status)
        t = window.setTimeout(poll, r.status?.running ? 4000 : 60000)
      }).catch(() => { if (live) t = window.setTimeout(poll, 120000) })
    }
    poll()
    const kick = () => { clearTimeout(t); poll() }
    window.addEventListener('plb:scan', kick)
    return () => { live = false; clearTimeout(t); window.removeEventListener('plb:scan', kick) }
  }, [])

  const [shelvesOpen, setShelvesOpen] = useState(() => localStorage.getItem('plb-sb-shelves') !== '0')
  const toggleShelves = () => setShelvesOpen(o => { localStorage.setItem('plb-sb-shelves', o ? '0' : '1'); return !o })
  const libParam = new URLSearchParams(loc.search).get('library')

  return (
    <aside className="sidebar" aria-label="Library">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Library' ? title : <>Pocket <b>Library</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^\d/.test(status.version) ? 'v' + status.version : status.version}</span>}
      </div>
      {reading ? (
        <Link to={`/read/${reading.id}`} className="sb-cont" onClick={onNavigate} title={`Continue reading ${reading.title}`}>
          <Cover b={reading} w={60} />
          <span className="grow"><b className="ellipsis">{reading.title}</b><span className="ellipsis">Continue reading · {pct(reading.state?.percentage)}%</span></span>
          <span className="go"><Icon name="book-open" size={14} /></span>
        </Link>
      ) : (
        <button type="button" className="btn primary create-btn" onClick={() => { onNavigate(); navigate('/library?status=unread&sort=random') }}>
          <Icon name="book-open" size={17} />Find something to read
        </button>
      )}

      <nav className="sb-nav" aria-label="Sections">
        <NavLink to="/" end onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}><Icon name="home" size={16} />Home</NavLink>
      </nav>
      <Group title="Library">
        <nav className="sb-nav tight">
          {LIBRARY.map(n => (
            <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive && !(n.to === '/library' && libParam) ? 'active' : ''}`}>
              <Icon name={n.icon as never} size={16} />{n.to === '/library' && libs.length > 1 ? 'All books' : n.label}
            </NavLink>
          ))}
          {libs.length > 1 && libs.map(l => (
            <Link key={l.id} to={`/library?library=${l.id}`} onClick={onNavigate} className={`sb-nav-item ${loc.pathname === '/library' && libParam === String(l.id) ? 'active' : ''}`}>
              <Icon name={(KIND_ICON[l.kind] || 'library') as never} size={16} /><span className="ellipsis">{l.name}</span><span className="sb-count">{l.books ? l.books.toLocaleString() : ''}</span>
            </Link>
          ))}
        </nav>
      </Group>
      <Group title="Yours">
        <nav className="sb-nav tight">
          <NavLink to="/reading" onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}><Icon name="bookmark" size={16} />Reading</NavLink>
          <NavLink to="/shelves" end onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name="library" size={16} />Shelves
            {shelves.length > 0 && <button type="button" className="sb-count sb-toggle" aria-label={shelvesOpen ? 'Hide shelves' : 'Show shelves'} onClick={e => { e.preventDefault(); e.stopPropagation(); toggleShelves() }}><Icon name={shelvesOpen ? 'chevron-down' : 'chevron-right'} size={13} /></button>}
          </NavLink>
          {shelvesOpen && shelves.length > 0 && (
            <div className="sb-shelves">
              {shelves.map(s => (
                <NavLink key={s.id} to={`/shelf/${s.id}`} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
                  <span className="ellipsis">{s.name}</span>{s.kind === 'manual' && <span className="sb-count">{s.books || ''}</span>}
                </NavLink>
              ))}
            </div>
          )}
          <NavLink to="/highlights" onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}><Icon name="note" size={16} />Highlights</NavLink>
          <NavLink to="/stats" onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}><Icon name="bar" size={16} />Stats</NavLink>
          <NavLink to="/devices" onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}><Icon name="smartphone" size={16} />Devices</NavLink>
        </nav>
      </Group>

      {scan?.running && (
        <div className="sb-scan" role="status"><span className="spinner" />
          <span className="grow ellipsis">Scanning {scan.library || 'libraries'}{scan.total ? ` · ${Math.round((scan.done / scan.total) * 100)}%` : '…'}</span>
        </div>
      )}

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        {editor && <Link to="/admin" className="sb-foot-link" onClick={onNavigate}><Icon name="wrench" size={15} />Admin</Link>}
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('plb:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  const key = `plb-sb-${title}`
  const [open, setOpen] = useState(() => localStorage.getItem(key) !== '0')
  const toggle = () => setOpen(o => { localStorage.setItem(key, o ? '0' : '1'); return !o })
  return (
    <section className="sb-sec">
      <h4 className="sb-h">
        <button type="button" className="sb-h-btn" aria-expanded={open} onClick={toggle}><Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} />{title}</button>
      </h4>
      {open && children}
    </section>
  )
}
