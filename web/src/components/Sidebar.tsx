// Left column — the Pocket Calendar sidebar. (placeholder; the library UI work fills it)
import { NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { Icon } from './icons'

export function Sidebar({ onNavigate }: { onNavigate: () => void }) {
  const { status, settings } = useApp()
  const title = settings?.title || status?.title || 'Pocket Library'
  return (
    <aside className="sidebar" aria-label="Library">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Library' ? title : <>Pocket <b>Library</b></>}</span>
      </div>
      <nav className="sb-nav" aria-label="Sections">
        <NavLink to="/" end onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}><Icon name="home" size={16} />Home</NavLink>
      </nav>
    </aside>
  )
}
